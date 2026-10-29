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

	"github.com/dtthaison/muse-mcp/internal/browser"
	"github.com/dtthaison/muse-mcp/internal/chats"
	"github.com/dtthaison/muse-mcp/internal/cli"
	"github.com/dtthaison/muse-mcp/internal/config"
	"github.com/dtthaison/muse-mcp/internal/library"
	"github.com/dtthaison/muse-mcp/internal/muse"
	"github.com/dtthaison/muse-mcp/internal/tools"
	"github.com/dtthaison/muse-mcp/internal/voices"
)

const version = "0.7.0"

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
	fi, _ := os.Stdin.Stat()
	return cli.Run(ctx, os.Args[1:], cli.Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Interactive: fi != nil && fi.Mode()&os.ModeCharDevice != 0,
		Library:     lib,
		Voices:      cat,
		Muse:        func() (cli.Service, func(), error) { return startMuse(paths, log) },
		Serve:       func(ctx context.Context) error { return serve(ctx, paths, log, lib, cat) },
		Claude:      runClaude,
		Exe:         exe,
		Home:        home,
		Dir:         dir,
		Version:     version,
	})
}

// startMuse starts Chrome and the muse service for one CLI command.
func startMuse(paths config.Paths, log *slog.Logger) (cli.Service, func(), error) {
	exe, err := browser.FindChrome()
	if err != nil {
		return nil, nil, err
	}
	br, err := browser.New(browser.Options{ExecPath: exe, ProfileDir: paths.Profile, LockPath: paths.Lock})
	if errors.Is(err, browser.ErrLocked) {
		return nil, nil, errors.New("another muse-mcp (probably a Claude Code session) is using your muse login; use the muse tools there, or quit it first")
	}
	if err != nil {
		return nil, nil, err
	}
	s := muse.New(muse.Options{BaseURL: "https://muse.ai", Browser: br, Chats: chats.Load(paths.Chats),
		Timing: muse.DefaultTiming(), Log: log})
	return s, func() { s.Close(); br.Close() }, nil
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

func serve(ctx context.Context, paths config.Paths, log *slog.Logger, lib *library.Store, cat *voices.Catalog) error {
	log.Info("starting", "version", version)
	var svc tools.Service
	exe, findErr := browser.FindChrome()
	br, err := browser.New(browser.Options{ExecPath: exe, ProfileDir: paths.Profile, LockPath: paths.Lock})
	switch {
	case errors.Is(err, browser.ErrLocked):
		log.Warn("profile locked by another muse-mcp")
		svc = unavailable{err}
	case err != nil:
		return err
	case findErr != nil:
		svc = unavailable{findErr}
	default:
		defer br.Close()
		s := muse.New(muse.Options{BaseURL: "https://muse.ai", Browser: br, Chats: chats.Load(paths.Chats),
			Timing: muse.DefaultTiming(), Log: log})
		defer s.Close()
		svc = s
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "muse-mcp", Version: version}, nil)
	tools.Register(server, svc, lib, cat)
	err = server.Run(ctx, &mcp.StdioTransport{})
	log.Info("stopped", "err", err)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// unavailable answers every muse call with the reason muse can't be used
// (another session holds the profile, or Chrome is missing). Saved references
// still work.
type unavailable struct{ err error }

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
