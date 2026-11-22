package browser

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dtthaison/muse-mcp/internal/fakemuse"
)

// fakeProviderScript is a muse-browser-fake that follows the provider
// contract. FAKE_MODE picks how it behaves; FAKE_LOG gets one line per event.
//
//	(default)      print the address in $FAKE_URL_FILE, wait for stdin EOF
//	ignore-stdin   as above but never looks at stdin; only SIGTERM stops it
//	stubborn       ignores stdin and SIGTERM; only SIGKILL stops it
//	exit-early     writes to stderr and exits 3 before printing an address
//	silent         prints nothing and keeps running
//	junk           prints a first line that is not an address
//	leave-child    as default, but leaves a background sleep running when it exits
//	leave-stubborn-child  as above, the leftover ignores SIGTERM
//	exit-leave-child  prints the address, starts a background sleep, exits at once
//	die-on-file    prints the address, then exits as soon as $FAKE_DIE_FILE exists
//
// "muse-browser-fake -- ARG" is how muse-mcp runs a "fake:ARG" spec; the
// script drops the "--" and logs the whole command line as argv=.
const fakeProviderScript = `#!/bin/sh
if [ "$1" = "--describe" ]; then echo "a fake provider"; echo "second line"; exit 0; fi
echo "argv=$*" >> "$FAKE_LOG"
if [ "$1" = "--" ]; then shift; fi
echo "start $$ arg=$1" >> "$FAKE_LOG"
case "$FAKE_MODE" in
exit-early) echo "tunnel failed: no route" >&2; echo "second error line" >&2; exit 3 ;;
silent) while :; do sleep 0.05; done ;;
junk) echo "not an address"; while :; do sleep 0.05; done ;;
esac
case "$FAKE_MODE" in
stubborn) trap '' TERM ;;
*) trap 'echo "term $$" >> "$FAKE_LOG"; exit 0' TERM ;;
esac
cat "$FAKE_URL_FILE"
case "$FAKE_MODE" in
ignore-stdin|stubborn) while :; do sleep 0.05; done ;;
die-on-file) while [ ! -e "$FAKE_DIE_FILE" ]; do sleep 0.05; done; echo "died $$" >> "$FAKE_LOG"; exit 0 ;;
leave-child) sleep 300 & echo "child $!" >> "$FAKE_LOG" ;;
leave-stubborn-child) sh -c 'trap "" TERM; while :; do sleep 0.05; done' & echo "child $!" >> "$FAKE_LOG" ;;
exit-leave-child) sleep 300 </dev/null >/dev/null 2>&1 & echo "child $!" >> "$FAKE_LOG"; exit 0 ;;
esac
while read -r line; do :; done
echo "eof $$" >> "$FAKE_LOG"
`

// installFake puts a muse-browser-fake on a new PATH and returns the log file
// and the file the provider takes its address from.
func installFake(t *testing.T, url string) (logFile, urlFile string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "muse-browser-fake"), []byte(fakeProviderScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// A plain file with the right name but no execute bit is not a provider.
	os.WriteFile(filepath.Join(bin, "muse-browser-noexec"), []byte("#!/bin/sh\n"), 0o644)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	logFile, urlFile = filepath.Join(dir, "log"), filepath.Join(dir, "url")
	if err := os.WriteFile(urlFile, []byte(url+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_LOG", logFile)
	t.Setenv("FAKE_URL_FILE", urlFile)
	t.Setenv("FAKE_MODE", "")
	return logFile, urlFile
}

func readLog(logFile string) string {
	b, _ := os.ReadFile(logFile)
	return string(b)
}

// logPid is the pid on the first "<word> <pid>" line of the fake provider's log.
func logPid(t *testing.T, logFile, word string) int {
	t.Helper()
	for _, l := range strings.Split(readLog(logFile), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == word {
			if pid, err := strconv.Atoi(f[1]); err == nil {
				return pid
			}
		}
	}
	t.Fatalf("no %q line in the provider log:\n%s", word, readLog(logFile))
	return 0
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDiscoverFindsProvidersOnPath(t *testing.T) {
	installFake(t, "http://127.0.0.1:1")
	var got *ProviderInfo
	for _, p := range Discover() {
		if p.Name == "noexec" {
			t.Fatalf("listed a file that is not executable: %+v", p)
		}
		if p.Name == "fake" {
			p := p
			got = &p
		}
	}
	if got == nil {
		t.Fatal("muse-browser-fake not discovered")
	}
	if got.Description != "a fake provider" {
		t.Fatalf("description = %q (only the first line of --describe counts)", got.Description)
	}
}

func TestDiscoverWithoutDescription(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "muse-browser-mute"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "muse-browser-slow"), []byte("#!/bin/sh\nsleep 30\n"), 0o755)
	t.Setenv("PATH", bin)
	start := time.Now()
	found := Discover()
	if len(found) != 2 || found[0].Name != "mute" || found[1].Name != "slow" {
		t.Fatalf("found %+v", found)
	}
	for _, p := range found {
		if p.Description != "(no description)" {
			t.Errorf("%s description = %q", p.Name, p.Description)
		}
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Fatalf("a hanging --describe held discovery for %s (the limit is 3 s)", d)
	}
}

func TestDiscoverFirstOnPathWins(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(a, "muse-browser-x"), []byte("#!/bin/sh\necho first\n"), 0o755)
	os.WriteFile(filepath.Join(b, "muse-browser-x"), []byte("#!/bin/sh\necho second\n"), 0o755)
	t.Setenv("PATH", a+string(os.PathListSeparator)+b)
	found := Discover()
	if len(found) != 1 || found[0].Description != "first" {
		t.Fatalf("found %+v", found)
	}
}

func TestProviderRunsAndStopsOnStdinEOF(t *testing.T) {
	logFile, _ := installFake(t, "http://127.0.0.1:9222")
	spec, _ := ParseSpec("fake:profile-1", nil)
	argv, err := spec.Argv()
	if err != nil {
		t.Fatal(err)
	}
	url, p, err := startProvider(context.Background(), argv, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://127.0.0.1:9222" {
		t.Fatalf("url = %q", url)
	}
	if !strings.Contains(readLog(logFile), "arg=profile-1") {
		t.Fatalf("provider did not get its argument: %q", readLog(logFile))
	}
	// The argument comes after "--", so "setup", "list" or "-x" can't be read as a command or flag.
	if !strings.Contains(readLog(logFile), "argv=-- profile-1\n") {
		t.Fatalf("provider was not run as `muse-browser-fake -- profile-1`: %q", readLog(logFile))
	}
	if p.exited() {
		t.Fatal("provider exited by itself; it must keep running until stdin closes")
	}
	// The provider exits on its own when stdin reaches EOF, without a signal.
	p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("provider still running after stdin EOF")
	}
	if log := readLog(logFile); !strings.Contains(log, "eof ") || strings.Contains(log, "term ") {
		t.Fatalf("expected a clean exit on EOF, log:\n%s", log)
	}
}

func TestProviderStopEscalates(t *testing.T) {
	old := providerStopTimeout
	providerStopTimeout = 300 * time.Millisecond
	defer func() { providerStopTimeout = old }()

	for _, mode := range []string{"ignore-stdin", "stubborn"} {
		t.Run(mode, func(t *testing.T) {
			logFile, _ := installFake(t, "http://127.0.0.1:9222")
			t.Setenv("FAKE_MODE", mode)
			spec, _ := ParseSpec("fake:x", nil)
			argv, _ := spec.Argv()
			_, p, err := startProvider(context.Background(), argv, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			pid := p.cmd.Process.Pid
			start := time.Now()
			p.stop()
			if !p.exited() {
				t.Fatal("stop returned before the provider was reaped")
			}
			if err := syscall.Kill(pid, 0); err == nil {
				t.Fatal("provider process still exists after stop")
			}
			termed := strings.Contains(readLog(logFile), "term ")
			if mode == "ignore-stdin" && (!termed || time.Since(start) > 700*time.Millisecond) {
				t.Fatalf("SIGTERM should have ended it quickly (termed=%v after %s)", termed, time.Since(start))
			}
			if mode == "stubborn" && (termed || time.Since(start) < 450*time.Millisecond) {
				t.Fatalf("SIGKILL should come only after the grace period (termed=%v after %s)", termed, time.Since(start))
			}
			p.stop() // stopping again is harmless
		})
	}
}

func TestProviderFailsBeforeAddress(t *testing.T) {
	installFake(t, "http://127.0.0.1:9222")
	t.Setenv("FAKE_MODE", "exit-early")
	spec, _ := ParseSpec("fake:x", nil)
	argv, _ := spec.Argv()
	_, _, err := startProvider(context.Background(), argv, 5*time.Second)
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"exited before it printed a DevTools address", "exit code 3", "tunnel failed: no route", "second error line"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestProviderTimeoutReportsAndKills(t *testing.T) {
	logFile, _ := installFake(t, "http://127.0.0.1:9222")
	t.Setenv("FAKE_MODE", "silent")
	spec, _ := ParseSpec("fake:x", nil)
	argv, _ := spec.Argv()
	start := time.Now()
	_, _, err := startProvider(context.Background(), argv, 400*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not print a DevTools address within") {
		t.Fatalf("error = %v", err)
	}
	if time.Since(start) > 6*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	// the provider was stopped: its pid (from the log) is gone
	pid := logPid(t, logFile, "start")
	waitFor(t, "provider to be gone", func() bool { return syscall.Kill(pid, 0) != nil })
}

func TestProviderFirstLineMustBeAnAddress(t *testing.T) {
	installFake(t, "http://127.0.0.1:9222")
	t.Setenv("FAKE_MODE", "junk")
	spec, _ := ParseSpec("fake:x", nil)
	argv, _ := spec.Argv()
	_, _, err := startProvider(context.Background(), argv, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), `printed "not an address"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestBrowserWithUnknownProviderFailsOnFirstTab(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	spec, _ := ParseSpec("shardx:muse-sg-1", nil)
	b, err := New(Options{Spec: spec, LockPath: filepath.Join(t.TempDir(), "lock")})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	_, _, err = b.NewTab(context.Background())
	if err == nil || !strings.Contains(err.Error(), `no browser provider "shardx": install muse-browser-shardx`) {
		t.Fatalf("error = %v", err)
	}
	if b.Running() {
		t.Fatal("running")
	}
}

// An argument that looks like a provider command or a flag still reaches the
// provider as an argument: it comes after "--". A command array is run as written.
func TestSpecArgvSeparatesTheArgument(t *testing.T) {
	installFake(t, "http://127.0.0.1:9222")
	for _, arg := range []string{"setup", "list", "-x", "--describe", "--help"} {
		spec, err := ParseSpec("fake:"+arg, nil)
		if err != nil {
			t.Fatal(err)
		}
		argv, err := spec.Argv()
		if err != nil {
			t.Fatal(err)
		}
		if len(argv) != 3 || argv[1] != "--" || argv[2] != arg {
			t.Fatalf("argv for %q = %q", arg, argv)
		}
	}
	spec, _ := ParseSpec("", []string{"muse-browser-fake", "--flag", "x"})
	argv, _ := spec.Argv()
	if len(argv) != 3 || argv[1] != "--flag" || argv[2] != "x" {
		t.Fatalf("a command array must be run as written, got %q", argv)
	}
}

// The provider leaves a process behind (an ssh tunnel it forgot to kill):
// stop takes it down with the provider, in both of its signals.
func TestProviderStopKillsWhatTheProviderLeftBehind(t *testing.T) {
	old := providerStopTimeout
	providerStopTimeout = 400 * time.Millisecond
	defer func() { providerStopTimeout = old }()

	for _, mode := range []string{"leave-child", "leave-stubborn-child"} {
		t.Run(mode, func(t *testing.T) {
			logFile, _ := installFake(t, "http://127.0.0.1:9222")
			t.Setenv("FAKE_MODE", mode)
			spec, _ := ParseSpec("fake:x", nil)
			argv, _ := spec.Argv()
			_, p, err := startProvider(context.Background(), argv, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the child to start", func() bool { return strings.Contains(readLog(logFile), "child ") })
			child := logPid(t, logFile, "child")
			if syscall.Kill(child, 0) != nil {
				t.Fatal("the leftover process is not running before stop")
			}
			start := time.Now()
			p.stop()
			if d := time.Since(start); d > 4*time.Second {
				t.Fatalf("stop took %s", d)
			}
			waitFor(t, "the leftover process to be gone", func() bool { return syscall.Kill(child, 0) != nil })
			if groupAlive(p.cmd.Process.Pid) {
				t.Fatal("the provider's process group is not empty after stop")
			}
		})
	}
}

// The provider exited on its own (its tunnel dropped) but left a process in its
// group: the next stop, which relaunching does, still removes it.
func TestProviderStopAfterTheLeaderExitedKillsTheGroup(t *testing.T) {
	logFile, _ := installFake(t, "http://127.0.0.1:9222")
	t.Setenv("FAKE_MODE", "exit-leave-child")
	spec, _ := ParseSpec("fake:x", nil)
	argv, _ := spec.Argv()
	_, p, err := startProvider(context.Background(), argv, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the provider to exit", p.exited)
	waitFor(t, "the child to start", func() bool { return strings.Contains(readLog(logFile), "child ") })
	child := logPid(t, logFile, "child")
	if syscall.Kill(child, 0) != nil {
		t.Fatal("the leftover process died with the leader; the test proves nothing")
	}
	p.stop()
	waitFor(t, "the leftover process to be gone", func() bool { return syscall.Kill(child, 0) != nil })
}

// A provider that fails before printing an address must not leave its helpers running either.
func TestProviderFailureAlsoCleansItsGroup(t *testing.T) {
	logFile, _ := installFake(t, "http://127.0.0.1:9222")
	bin := t.TempDir()
	script := "#!/bin/sh\nsleep 300 </dev/null >/dev/null 2>&1 &\necho \"child $!\" >> \"$FAKE_LOG\"\necho failed >&2\nexit 4\n"
	if err := os.WriteFile(filepath.Join(bin, "muse-browser-bad"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	spec, _ := ParseSpec("bad:x", nil)
	argv, _ := spec.Argv()
	if _, _, err := startProvider(context.Background(), argv, 5*time.Second); err == nil || !strings.Contains(err.Error(), "exit code 4") {
		t.Fatalf("error = %v", err)
	}
	child := logPid(t, logFile, "child")
	waitFor(t, "the leftover process to be gone", func() bool { return syscall.Kill(child, 0) != nil })
}

// A cancelled ctx ends the wait for the address and stops the half-started provider.
func TestProviderStartIsCancellable(t *testing.T) {
	logFile, _ := installFake(t, "http://127.0.0.1:9222")
	t.Setenv("FAKE_MODE", "silent")
	spec, _ := ParseSpec("fake:x", nil)
	argv, _ := spec.Argv()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	_, _, err := startProvider(ctx, argv, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Fatalf("took %s", d)
	}
	pid := logPid(t, logFile, "start")
	waitFor(t, "provider to be gone", func() bool { return syscall.Kill(pid, 0) != nil })
	// An already cancelled ctx doesn't even start it.
	before := readLog(logFile)
	if _, _, err := startProvider(ctx, argv, time.Minute); !errors.Is(err, context.Canceled) || readLog(logFile) != before {
		t.Fatalf("error = %v, log changed = %v", err, readLog(logFile) != before)
	}
}

// NewTab with a ctx that ends while the provider is starting returns at once,
// leaves nothing running, and doesn't keep the Browser locked.
func TestNewTabCancelledWhileTheProviderStarts(t *testing.T) {
	logFile, _ := installFake(t, "http://127.0.0.1:9222")
	t.Setenv("FAKE_MODE", "silent")
	spec, _ := ParseSpec("fake:x", nil)
	b, err := New(Options{Spec: spec, LockPath: filepath.Join(t.TempDir(), "lock"), ProviderTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err = b.NewTab(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want the ctx's deadline", err)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Fatalf("took %s", d)
	}
	if b.Running() || b.prov != nil {
		t.Fatal("a half-started provider was kept")
	}
	pid := logPid(t, logFile, "start")
	waitFor(t, "provider to be gone", func() bool { return syscall.Kill(pid, 0) != nil })
}

// The provider printed an address, but nothing answers there: a cancelled ctx
// still ends NewTab, and the provider is stopped.
func TestNewTabCancelledWhileAttaching(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // accepts, never answers
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	logFile, _ := installFake(t, "http://"+ln.Addr().String())
	spec, _ := ParseSpec("fake:x", nil)
	b, err := New(Options{Spec: spec, LockPath: filepath.Join(t.TempDir(), "lock"), ProviderTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := b.NewTab(ctx); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want the ctx's deadline", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("NewTab ignored its cancelled ctx while attaching")
	}
	if b.Running() || b.prov != nil {
		t.Fatal("a half-attached browser was kept")
	}
	waitFor(t, "provider to be stopped", func() bool {
		return strings.Contains(readLog(logFile), "eof ") || strings.Contains(readLog(logFile), "term ")
	})
}

// A provider that has exited makes the Browser report "not running" even
// though its websocket has not failed yet.
func TestRunningNoticesAnExitedProvider(t *testing.T) {
	p := &provider{done: make(chan struct{})}
	b := &Browser{browserCtx: context.Background(), prov: p}
	if !b.Running() {
		t.Fatal("not running with a live provider")
	}
	close(p.done)
	if b.Running() {
		t.Fatal("running although the provider exited")
	}
	b.prov = nil // a local or cdp: browser has no provider
	if !b.Running() {
		t.Fatal("a browser without a provider is not running")
	}
}

// startDebugChrome runs a headless Chrome with a debugging port, the way a
// provider's browser would, and returns its DevTools address and a kill func.
func startDebugChrome(t *testing.T) (string, func()) {
	t.Helper()
	exe, err := FindChrome()
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	cmd := exec.Command(exe, "--headless=new", "--remote-debugging-port=0", "--user-data-dir="+dir,
		"--no-first-run", "--no-default-browser-check", "--disable-gpu", "about:blank")
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	kill := func() {
		cmd.Process.Kill()
		<-done
	}
	t.Cleanup(kill)
	var port string
	waitFor(t, "Chrome's DevToolsActivePort", func() bool {
		f, err := os.Open(filepath.Join(dir, "DevToolsActivePort"))
		if err != nil {
			return false
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		if sc.Scan() {
			port = strings.TrimSpace(sc.Text())
		}
		return port != ""
	})
	return "http://127.0.0.1:" + port, kill
}

// The whole provider path with a real browser: the browser attaches through the
// address the provider printed, Close stops the provider but leaves the
// browser running, and a dead connection re-runs the provider (whose address
// may have changed).
func TestBrowserWithProviderLifecycle(t *testing.T) {
	urlA, killA := startDebugChrome(t)
	urlB, killB := startDebugChrome(t)
	defer killB()
	logFile, urlFile := installFake(t, urlA)

	spec, err := ParseSpec("", []string{"muse-browser-fake", "--flag", "arg one"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Options{Spec: spec, LockPath: filepath.Join(t.TempDir(), "lock"), ProviderTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	srv := fakemuse.New()
	defer srv.Close()

	openFake(t, b, srv)
	if !b.Running() || b.Generation() != 1 || b.prov == nil {
		t.Fatalf("running=%v gen=%d prov=%v", b.Running(), b.Generation(), b.prov)
	}
	if !strings.Contains(readLog(logFile), "arg=--flag") {
		t.Fatalf("provider not started with the command's arguments: %q", readLog(logFile))
	}
	// SetVisible is a no-op for an attached browser.
	if err := b.SetVisible(true); err != nil {
		t.Fatal(err)
	}
	if b.Generation() != 1 {
		t.Fatal("SetVisible relaunched an attached browser")
	}
	first := b.prov

	// The browser dies; the next tab re-runs the provider, now pointing at B.
	if err := os.WriteFile(urlFile, []byte(urlB+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	killA()
	waitFor(t, "the dead connection to be noticed", func() bool { return !b.Running() })
	openFake(t, b, srv)
	if b.Generation() != 2 || b.prov == nil || b.prov == first {
		t.Fatalf("gen=%d, provider re-run=%v", b.Generation(), b.prov != nil && b.prov != first)
	}
	if !first.exited() {
		t.Fatal("the first provider was not reaped on relaunch")
	}
	if n := strings.Count(readLog(logFile), "start "); n != 2 {
		t.Fatalf("provider ran %d times, want 2:\n%s", n, readLog(logFile))
	}

	// Close stops the provider (stdin closed first, so it exits on EOF)...
	second := b.prov
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if !second.exited() {
		t.Fatal("provider still running after Close")
	}
	if !strings.Contains(readLog(logFile), "eof ") {
		t.Fatalf("provider did not see stdin EOF:\n%s", readLog(logFile))
	}
	// ...but never the attached browser.
	cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b2, err := New(Options{Spec: Spec{Kind: KindCDP, URL: urlB}, LockPath: filepath.Join(t.TempDir(), "lock")})
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()
	if _, c, err := b2.NewTab(cctx); err != nil {
		t.Fatalf("the attached Chrome was closed by Close: %v", err)
	} else {
		c()
	}
}

// The provider dies while the browser it pointed at is fine: the next tab
// notices (without waiting for a websocket error), tears the connection down
// and runs the provider again. A ctx that ends after NewTab returned changes
// nothing: neither the provider nor the connection is tied to it.
func TestBrowserRelaunchesWhenTheProviderExits(t *testing.T) {
	urlA, _ := startDebugChrome(t)
	logFile, _ := installFake(t, urlA)
	dieFile := filepath.Join(t.TempDir(), "die")
	t.Setenv("FAKE_DIE_FILE", dieFile)
	t.Setenv("FAKE_MODE", "die-on-file")
	spec, _ := ParseSpec("fake:x", nil)
	b, err := New(Options{Spec: spec, LockPath: filepath.Join(t.TempDir(), "lock"), ProviderTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	srv := fakemuse.New()
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	_, closeTab, err := b.NewTab(ctx)
	if err != nil {
		t.Fatal(err)
	}
	closeTab()
	cancel() // the request is over
	time.Sleep(300 * time.Millisecond)
	if !b.Running() || b.Generation() != 1 || b.prov.exited() {
		t.Fatalf("the connection was tied to the request's ctx: running=%v gen=%d", b.Running(), b.Generation())
	}

	first := b.prov
	if err := os.WriteFile(dieFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the provider to exit", first.exited)
	if b.Running() {
		t.Fatal("Running() is true although the provider exited")
	}
	os.Remove(dieFile)
	openFake(t, b, srv)
	if b.Generation() != 2 || b.prov == first || b.prov == nil {
		t.Fatalf("gen=%d, provider re-run=%v", b.Generation(), b.prov != nil && b.prov != first)
	}
	if n := strings.Count(readLog(logFile), "start "); n != 2 {
		t.Fatalf("provider ran %d times, want 2:\n%s", n, readLog(logFile))
	}
}
