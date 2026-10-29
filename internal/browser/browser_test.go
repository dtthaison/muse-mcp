package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dtthaison/muse-mcp/internal/fakemuse"
	"github.com/dtthaison/muse-mcp/internal/musepage"
)

func newBrowser(t *testing.T, lock string) *Browser {
	t.Helper()
	exe, err := FindChrome()
	if err != nil {
		t.Skip(err)
	}
	if lock == "" {
		lock = filepath.Join(t.TempDir(), "lock")
	}
	b, err := New(Options{ExecPath: exe, ProfileDir: filepath.Join(t.TempDir(), "profile"), LockPath: lock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func openFake(t *testing.T, b *Browser, srv *fakemuse.Server) {
	t.Helper()
	tab, cancel, err := b.NewTab(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if _, err := musepage.Open(tab, musepage.NewChatURL(srv.URL), 15*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestLockIsExclusive(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "lock")
	exe, err := FindChrome()
	if err != nil {
		t.Skip(err)
	}
	opts := Options{ExecPath: exe, ProfileDir: t.TempDir(), LockPath: lock}
	first, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(opts); !errors.Is(err, ErrLocked) {
		t.Fatalf("second New: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := New(opts)
	if err != nil {
		t.Fatalf("after close: %v", err)
	}
	again.Close()
}

func TestLazyStartAndTab(t *testing.T) {
	b := newBrowser(t, "")
	if b.Running() {
		t.Fatal("running before first tab")
	}
	srv := fakemuse.New()
	defer srv.Close()
	openFake(t, b, srv)
	if !b.Running() || b.Generation() != 1 {
		t.Fatalf("running=%v gen=%d", b.Running(), b.Generation())
	}
}

func TestRelaunchAfterChromeDies(t *testing.T) {
	b := newBrowser(t, "")
	srv := fakemuse.New()
	defer srv.Close()
	openFake(t, b, srv)

	p, err := os.FindProcess(b.pid())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for b.Running() && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if b.Running() {
		t.Fatal("still running after kill")
	}
	openFake(t, b, srv)
	if b.Generation() != 2 {
		t.Fatalf("gen = %d", b.Generation())
	}
}

// Opens real windows on screen, so it only runs when asked for.
func TestSetVisibleRelaunches(t *testing.T) {
	if os.Getenv("MUSE_TEST_VISIBLE") == "" {
		t.Skip("set MUSE_TEST_VISIBLE=1 to run (opens a Chrome window)")
	}
	b := newBrowser(t, "")
	srv := fakemuse.New()
	defer srv.Close()
	openFake(t, b, srv)
	if err := b.SetVisible(true); err != nil {
		t.Fatal(err)
	}
	openFake(t, b, srv)
	pid := b.pid()
	if !b.Visible() || b.Generation() != 2 {
		t.Fatalf("visible=%v gen=%d", b.Visible(), b.Generation())
	}
	if err := b.SetVisible(true); err != nil {
		t.Fatal(err)
	}
	if b.pid() != pid || b.Generation() != 2 {
		t.Fatal("relaunched without a mode change")
	}
	if err := b.SetVisible(false); err != nil {
		t.Fatal(err)
	}
	if b.Visible() {
		t.Fatal("still visible")
	}
}
