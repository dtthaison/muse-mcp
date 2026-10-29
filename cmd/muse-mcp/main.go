// Command muse-mcp is an MCP server (stdio) that lets Claude Code generate
// images and chat through the owner's muse.ai account.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dtthaison/muse-mcp/internal/browser"
	"github.com/dtthaison/muse-mcp/internal/chats"
	"github.com/dtthaison/muse-mcp/internal/config"
	"github.com/dtthaison/muse-mcp/internal/library"
	"github.com/dtthaison/muse-mcp/internal/muse"
	"github.com/dtthaison/muse-mcp/internal/tools"
)

const version = "0.1.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "muse-mcp:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	paths, err := config.Load()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(paths.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	// stdout belongs to MCP; never log cookies, tokens or page bodies.
	log := slog.New(slog.NewJSONHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))
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
	tools.Register(server, svc, library.Open(paths.Library))
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
