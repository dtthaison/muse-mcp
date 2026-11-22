package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dtthaison/muse-mcp/internal/accounts"
	"github.com/dtthaison/muse-mcp/internal/config"
	"github.com/dtthaison/muse-mcp/internal/muse"
	"github.com/dtthaison/muse-mcp/internal/tools"
)

// /dev/null is a character device but not a terminal: a command run by a script has it as stdin
// and must not be asked to approve anything.
func TestDevNullIsNotATerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Fatal("/dev/null counted as a terminal")
	}
}

func TestPipeIsNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) {
		t.Fatal("a pipe counted as a terminal")
	}
}

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	t.Setenv("MUSE_MCP_HOME", t.TempDir())
	p, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// The pool builds one service per account, only when first asked, and shuts all of them down.
func TestPoolBuildsAccountsLazily(t *testing.T) {
	store := accounts.Open(filepath.Join(t.TempDir(), "accounts.json"))
	if err := store.Save(accounts.File{Default: "sg1", Accounts: map[string]accounts.Account{
		"sg1": {Browser: "cdp:http://127.0.0.1:1"}, "y": {Browser: "cdp:http://127.0.0.1:2"}}}); err != nil {
		t.Fatal(err)
	}
	var built, closed []string
	pl := &pool{store: store, build: func(name string, acc accounts.Account) (tools.Service, func(), error) {
		built = append(built, name+"="+acc.Browser)
		return unavailable{}, func() { closed = append(closed, name) }, nil
	}}
	if len(built) != 0 {
		t.Fatal("built before first use")
	}
	a, err := pl.Service("") // the default account is sg1
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := pl.Service("sg1"); b != a {
		t.Fatal("a second call built a second service")
	}
	if _, err := pl.Service("y"); err != nil {
		t.Fatal(err)
	}
	if _, err := pl.Service("default"); err != nil { // the implicit local account
		t.Fatal(err)
	}
	want := []string{"sg1=cdp:http://127.0.0.1:1", "y=cdp:http://127.0.0.1:2", "default=local"}
	if !reflect.DeepEqual(built, want) {
		t.Fatalf("built %v, want %v", built, want)
	}
	pl.Close()
	if len(closed) != 3 {
		t.Fatalf("closed %v", closed)
	}
}

func TestPoolUnknownAccountListsKnownOnes(t *testing.T) {
	store := accounts.Open(filepath.Join(t.TempDir(), "accounts.json"))
	store.Save(accounts.File{Accounts: map[string]accounts.Account{"sg1": {Browser: "local"}}})
	pl := &pool{store: store, build: func(string, accounts.Account) (tools.Service, func(), error) {
		t.Fatal("built an unknown account")
		return nil, nil, nil
	}}
	_, err := pl.Service("nope")
	if err == nil || !strings.Contains(err.Error(), `unknown muse account "nope"`) || !strings.Contains(err.Error(), "default, sg1") {
		t.Fatalf("error = %v", err)
	}
}

// A new account added by the CLI while the server runs is usable without a restart.
func TestPoolSeesAccountsAddedLater(t *testing.T) {
	store := accounts.Open(filepath.Join(t.TempDir(), "accounts.json"))
	pl := &pool{store: store, build: func(string, accounts.Account) (tools.Service, func(), error) { return unavailable{}, nil, nil }}
	if _, err := pl.Service("late"); err == nil {
		t.Fatal("unknown account accepted")
	}
	store.Save(accounts.File{Accounts: map[string]accounts.Account{"late": {Browser: "local"}}})
	if _, err := pl.Service("late"); err != nil {
		t.Fatal(err)
	}
}

func TestNewMuseUsesPerAccountState(t *testing.T) {
	paths := testPaths(t)
	s, closeFn, err := newMuse(paths, "sg1", accounts.Account{Browser: "cdp:http://127.0.0.1:1"}, true, testLog())
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Account != "sg1" || st.Browser != "cdp:http://127.0.0.1:1" || st.ChromeRunning {
		t.Fatalf("status %+v", st)
	}
	if fi, err := os.Stat(filepath.Join(paths.Root, "accounts", "sg1")); err != nil || !fi.IsDir() {
		t.Fatalf("per-account directory: %v", err)
	}
	// the default account keeps the root paths: no accounts/ directory for it
	d, closeD, err := newMuse(paths, "default", accounts.Account{Browser: "cdp:http://127.0.0.1:2"}, true, testLog())
	if err != nil {
		t.Fatal(err)
	}
	defer closeD()
	if _, err := os.Stat(filepath.Join(paths.Root, "accounts", "default")); err == nil {
		t.Fatal("the default account got a directory under accounts/")
	}
	var _ *muse.Service = d
}

func TestMuseCDPURLOverridesTheAccountsBrowser(t *testing.T) {
	paths := testPaths(t)
	t.Setenv("MUSE_CDP_URL", "http://127.0.0.1:9333")
	s, closeFn, err := newMuse(paths, "sg1", accounts.Account{Browser: "shardx:x"}, true, testLog())
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	st, _ := s.Status(context.Background())
	if st.Browser != "cdp:http://127.0.0.1:9333" || st.Account != "sg1" {
		t.Fatalf("status %+v", st)
	}
	t.Setenv("MUSE_CDP_URL", "not-a-url")
	if _, _, err := newMuse(paths, "sg1", accounts.Account{}, true, testLog()); err == nil || !strings.Contains(err.Error(), "MUSE_CDP_URL") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewMuseReportsABadAccountBrowser(t *testing.T) {
	paths := testPaths(t)
	_, _, err := newMuse(paths, "sg1", accounts.Account{Browser: "cdp:nope"}, true, testLog())
	if err == nil || !strings.Contains(err.Error(), "account sg1") {
		t.Fatalf("error = %v", err)
	}
}

// Pointing an account at another browser in accounts.json (login --browser ...)
// takes effect without a restart: the old service is closed, then a new one is built.
func TestPoolRebuildsWhenTheAccountsBrowserChanges(t *testing.T) {
	store := accounts.Open(filepath.Join(t.TempDir(), "accounts.json"))
	save := func(browser string) {
		t.Helper()
		if err := store.Save(accounts.File{Accounts: map[string]accounts.Account{"sg1": {Browser: browser}}}); err != nil {
			t.Fatal(err)
		}
	}
	var events []string
	pl := &pool{store: store, build: func(name string, acc accounts.Account) (tools.Service, func(), error) {
		events = append(events, "build "+acc.Browser)
		return unavailable{}, func() { events = append(events, "close "+acc.Browser) }, nil
	}}
	save("cdp:http://127.0.0.1:1")
	if _, err := pl.Service("sg1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pl.Service("sg1"); err != nil { // unchanged: no rebuild
		t.Fatal(err)
	}
	save("shardx:muse-sg-1")
	if _, err := pl.Service("sg1"); err != nil {
		t.Fatal(err)
	}
	pl.Close()
	want := []string{"build cdp:http://127.0.0.1:1", "close cdp:http://127.0.0.1:1", "build shardx:muse-sg-1", "close shardx:muse-sg-1"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events %q\nwant   %q", events, want)
	}
}

// An account whose browser can't be set up (provider not installed yet) answers
// with the reason, and works on the next call once it is fixed: failures are not cached.
func TestPoolRetriesAFailedBuild(t *testing.T) {
	store := accounts.Open(filepath.Join(t.TempDir(), "accounts.json"))
	store.Save(accounts.File{Accounts: map[string]accounts.Account{"sg1": {Browser: "shardx:x"}}})
	broken := errors.New(`no browser provider "shardx"`)
	installed := false
	builds := 0
	var degraded []error
	pl := &pool{store: store,
		build: func(name string, acc accounts.Account) (tools.Service, func(), error) {
			builds++
			if !installed {
				return nil, nil, broken
			}
			return &muse.Service{}, func() {}, nil
		},
		fallback: func(name string, err error) tools.Service {
			degraded = append(degraded, err)
			return unavailable{err: err}
		},
	}
	defer pl.Close()
	for i := 0; i < 2; i++ {
		svc, err := pl.Service("sg1")
		if err != nil {
			t.Fatal(err)
		}
		if u, ok := svc.(unavailable); !ok || u.err != broken {
			t.Fatalf("call %d: got %#v", i, svc)
		}
	}
	installed = true
	svc, err := pl.Service("sg1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.(*muse.Service); !ok {
		t.Fatalf("after the fix: got %#v", svc)
	}
	if builds != 3 || len(degraded) != 2 {
		t.Fatalf("builds=%d degraded=%d", builds, len(degraded))
	}
	if again, _ := pl.Service("sg1"); again != svc || builds != 3 {
		t.Fatal("a good service was not cached")
	}
	// Without a fallback the build error itself comes back.
	pl2 := &pool{store: store, build: func(string, accounts.Account) (tools.Service, func(), error) { return nil, nil, broken }}
	if _, err := pl2.Service("sg1"); !errors.Is(err, broken) {
		t.Fatalf("error = %v", err)
	}
}

// Once the pool is closed (the server is shutting down) a request still in
// flight gets an error instead of a freshly built browser nobody will close.
func TestPoolRefusesServicesAfterClose(t *testing.T) {
	store := accounts.Open(filepath.Join(t.TempDir(), "accounts.json"))
	store.Save(accounts.File{Accounts: map[string]accounts.Account{"sg1": {Browser: "local"}}})
	built, closed := 0, 0
	pl := &pool{store: store, build: func(string, accounts.Account) (tools.Service, func(), error) {
		built++
		return unavailable{}, func() { closed++ }, nil
	}}
	if _, err := pl.Service("sg1"); err != nil {
		t.Fatal(err)
	}
	pl.Close()
	pl.Close() // closing twice is harmless
	if _, err := pl.Service("sg1"); !errors.Is(err, errPoolClosed) {
		t.Fatalf("error = %v", err)
	}
	if built != 1 || closed != 1 {
		t.Fatalf("built=%d closed=%d; nothing may be built after Close and everything built must be closed", built, closed)
	}
}
