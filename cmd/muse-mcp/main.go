// Command muse-mcp is an MCP server (stdio) that lets Claude Code generate
// images and chat through the owner's muse.ai account. With a command
// (install, login, image, ...) it is a CLI; see internal/cli.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dtthaison/muse-mcp/internal/accounts"
	"github.com/dtthaison/muse-mcp/internal/browser"
	"github.com/dtthaison/muse-mcp/internal/chats"
	"github.com/dtthaison/muse-mcp/internal/cli"
	"github.com/dtthaison/muse-mcp/internal/config"
	"github.com/dtthaison/muse-mcp/internal/library"
	"github.com/dtthaison/muse-mcp/internal/muse"
	"github.com/dtthaison/muse-mcp/internal/tools"
	"github.com/dtthaison/muse-mcp/internal/voices"
)

const version = "1.1.2"

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	paths, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "muse-mcp:", err)
		return 1
	}
	logFile, err := os.OpenFile(paths.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "muse-mcp:", err)
		return 1
	}
	defer logFile.Close()
	// stdout belongs to MCP; never log cookies, tokens or page bodies.
	log := slog.New(slog.NewJSONHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))
	lib := library.Open(paths.Library)
	cat := voices.Open(paths.Voices)

	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		exe = "muse-mcp"
	}
	home, _ := os.UserHomeDir()
	dir, _ := os.Getwd()
	store := accounts.Open(paths.AccountsFile())
	return cli.Run(ctx, os.Args[1:], cli.Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Interactive: isTerminal(os.Stdin),
		Library:     lib,
		Voices:      cat,
		Accounts:    store,
		Chats:       func(name string) *chats.Registry { return accountChats(paths, name, log) },
		Muse:        func(name string) (cli.Service, func(), error) { return startMuse(paths, store, name, log) },
		Serve:       func(ctx context.Context) error { return serve(ctx, paths, store, log, lib, cat) },
		Claude:      runClaude,
		Exe:         exe,
		Home:        home,
		Dir:         dir,
		Version:     version,
	})
}

// accountChats is the chats.json of an account.
func accountChats(paths config.Paths, name string, log *slog.Logger) *chats.Registry {
	ap, err := paths.Account(name)
	if err != nil {
		log.Warn("account state directory", "account", name, "err", err)
		return chats.Load(filepath.Join(paths.Root, "accounts", name, "chats.json"))
	}
	return chats.Load(ap.Chats)
}

// newMuse builds the browser and muse service of one account. Nothing starts
// until the first muse call. MUSE_CDP_URL, when set, replaces the account's
// browser for this run.
func newMuse(paths config.Paths, name string, acc accounts.Account, lockLater bool, log *slog.Logger) (*muse.Service, func(), error) {
	spec, err := acc.Spec()
	if err != nil {
		return nil, nil, fmt.Errorf("account %s: %w", name, err)
	}
	if u := config.RemoteURL(); u != "" {
		if spec, err = browser.ParseSpec("cdp:"+u, nil); err != nil {
			return nil, nil, fmt.Errorf("MUSE_CDP_URL: %w", err)
		}
	}
	ap, err := paths.Account(name)
	if err != nil {
		return nil, nil, err
	}
	opts := browser.Options{ProfileDir: ap.Profile, LockPath: ap.Lock, LockLater: lockLater, Spec: spec}
	if spec.Kind == browser.KindLocal {
		if opts.ExecPath, err = browser.FindChrome(); err != nil {
			return nil, nil, err
		}
	}
	br, err := browser.New(opts)
	if err != nil {
		return nil, nil, err
	}
	s := muse.New(muse.Options{BaseURL: "https://muse.ai", Browser: br, Chats: chats.Load(ap.Chats),
		Timing: muse.DefaultTiming(), Log: log, Account: name, BrowserSpec: spec.String()})
	return s, func() { s.Close(); br.Close() }, nil
}

// startMuse starts the browser and the muse service of an account for one CLI command.
func startMuse(paths config.Paths, store *accounts.Store, name string, log *slog.Logger) (cli.Service, func(), error) {
	f, err := store.Load()
	if err != nil {
		return nil, nil, err
	}
	name, acc, err := f.Resolve(name)
	if err != nil {
		return nil, nil, err
	}
	s, closeFn, err := newMuse(paths, name, acc, false, log)
	if errors.Is(err, browser.ErrLocked) {
		return nil, nil, fmt.Errorf("another muse-mcp (probably a Claude Code session) is using the muse account %q; use the muse tools there, or quit it first", name)
	}
	if err != nil {
		return nil, nil, err
	}
	return s, closeFn, nil
}

// runClaude runs the claude CLI, for install and uninstall.
func runClaude(ctx context.Context, args ...string) error {
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("the claude command isn't on PATH; run it yourself: claude %v", args)
	}
	// muse-mcp prints its own summary; claude's output only matters when it fails.
	out, err := exec.CommandContext(ctx, "claude", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude %s: %w\n%s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}

func serve(ctx context.Context, paths config.Paths, store *accounts.Store, log *slog.Logger, lib *library.Store, cat *voices.Catalog) error {
	log.Info("starting", "version", version)
	// One browser and muse service per account, built on the first muse call
	// for it. Each takes its lock then, so a session that never uses muse
	// doesn't keep the CLI or other sessions out.
	pl := &pool{store: store, build: func(name string, acc accounts.Account) (tools.Service, func(), error) {
		s, closeFn, err := newMuse(paths, name, acc, true, log)
		if err != nil {
			// Saved references and the list of chats still work.
			return unavailable{err, accountChats(paths, name, log)}, nil, nil
		}
		return s, closeFn, nil
	}}
	defer pl.Close()

	server := mcp.NewServer(&mcp.Implementation{Name: "muse-mcp", Version: version}, nil)
	tools.RegisterAccounts(server, pl, lib, cat)
	err := server.Run(ctx, &mcp.StdioTransport{})
	log.Info("stopped", "err", err)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// unavailable answers every muse call with the reason muse can't be used
// (Chrome is missing, or the account's browser can't be set up). Saved references
// and the list of chats still work.
type unavailable struct {
	err   error
	chats *chats.Registry
}

func (u unavailable) GenerateImage(context.Context, muse.ImageRequest) (muse.Result, error) {
	return muse.Result{}, u.err
}
func (u unavailable) Chat(context.Context, muse.ChatRequest) (muse.Result, error) {
	return muse.Result{}, u.err
}
func (u unavailable) Decide(context.Context, string, string, bool) (muse.Result, error) {
	return muse.Result{}, u.err
}
func (u unavailable) Status(context.Context) (muse.StatusInfo, error) {
	return muse.StatusInfo{}, u.err
}
func (u unavailable) DeleteChat(context.Context, string) error { return u.err }
func (u unavailable) ListChats() []chats.Chat                  { return u.chats.List() }

// isTerminal reports whether f is a terminal a person can answer at. /dev/null is a character
// device too, but a script that runs muse-mcp with it as stdin has nobody to ask.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(fi, null)
}
