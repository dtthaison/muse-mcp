package muse

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pp/muse-mcp/internal/browser"
	"pp/muse-mcp/internal/chats"
	"pp/muse-mcp/internal/config"
	"pp/muse-mcp/internal/prompt"
)

// Live tests run against the real muse.ai with the owner's profile. They use
// quota and add chats to the owner's muse list, so they only run with
// MUSE_LIVE=1. Results go to MUSE_LIVE_OUT (default $TMPDIR/muse-mcp-live).
func TestLive(t *testing.T) {
	if os.Getenv("MUSE_LIVE") != "1" {
		t.Skip("set MUSE_LIVE=1 to run against the real muse.ai")
	}
	paths, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := browser.FindChrome()
	if err != nil {
		t.Fatal(err)
	}
	br, err := browser.New(browser.Options{ExecPath: exe, ProfileDir: paths.Profile, LockPath: paths.Lock})
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()
	svc := New(Options{BaseURL: "https://muse.ai", Browser: br, Chats: chats.Load(paths.Chats),
		Timing: DefaultTiming(), Log: slog.New(slog.NewTextHandler(os.Stderr, nil))})
	defer svc.Close()

	out := os.Getenv("MUSE_LIVE_OUT")
	if out == "" {
		out = filepath.Join(os.TempDir(), "muse-mcp-live")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Minute)
	defer cancel()

	generate := func(t *testing.T, in prompt.Input, file string) string {
		t.Helper()
		built, err := prompt.Build(in)
		if err != nil {
			t.Fatal(err)
		}
		req := ImageRequest{Text: built.Text, Attachments: built.Attachments, OutputPath: filepath.Join(out, file)}
		res, err := svc.GenerateImage(ctx, req)
		if errors.Is(err, ErrLoggedOut) {
			t.Log("not logged in: log in to muse.ai in the Chrome window that opened (waiting up to 10 minutes)")
			waitLogin(t, ctx, svc, 10*time.Minute)
			res, err = svc.GenerateImage(ctx, req)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("status=%s chat=%s reply=%q", res.Status, res.ChatURL, res.Reply)
		if res.Status != "done" || len(res.Images) == 0 {
			t.Fatalf("no image: %+v", res)
		}
		im := res.Images[0]
		if im.Bytes == 0 || im.Width == 0 || im.Height == 0 {
			t.Fatalf("bad image %+v", im)
		}
		t.Logf("saved %s (%dx%d, %d bytes)", im.Path, im.Width, im.Height, im.Bytes)
		return im.Path
	}

	var teapot string
	t.Run("TextToImage", func(t *testing.T) {
		teapot = generate(t, prompt.Input{Prompt: "a small blue teapot on a wooden table"}, "teapot.png")
	})
	t.Run("ReferenceImage", func(t *testing.T) {
		if teapot == "" {
			t.Skip("needs TextToImage")
		}
		generate(t, prompt.Input{Change: "the teapot is now red", References: []string{teapot}}, "teapot-red.png")
	})
}

// waitLogin waits for the owner to log in in the window ErrLoggedOut opened.
func waitLogin(t *testing.T, ctx context.Context, svc *Service, limit time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); {
		if st, err := svc.Status(ctx); err == nil && st.LoggedIn {
			t.Log("login detected")
			return
		}
		if err := sleep(ctx, 3*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("not logged in after waiting")
}
