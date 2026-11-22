package browser

import (
	"bufio"
	"context"
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
const fakeProviderScript = `#!/bin/sh
if [ "$1" = "--describe" ]; then echo "a fake provider"; echo "second line"; exit 0; fi
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
	url, p, err := startProvider(argv, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://127.0.0.1:9222" {
		t.Fatalf("url = %q", url)
	}
	if !strings.Contains(readLog(logFile), "arg=profile-1") {
		t.Fatalf("provider did not get its argument: %q", readLog(logFile))
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
			_, p, err := startProvider(argv, 5*time.Second)
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
	_, _, err := startProvider(argv, 5*time.Second)
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
	_, _, err := startProvider(argv, 400*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not print a DevTools address within") {
		t.Fatalf("error = %v", err)
	}
	if time.Since(start) > 6*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	// the provider was stopped: its pid (from the log) is gone
	fields := strings.Fields(readLog(logFile))
	pid, _ := strconv.Atoi(fields[1])
	waitFor(t, "provider to be gone", func() bool { return syscall.Kill(pid, 0) != nil })
}

func TestProviderFirstLineMustBeAnAddress(t *testing.T) {
	installFake(t, "http://127.0.0.1:9222")
	t.Setenv("FAKE_MODE", "junk")
	spec, _ := ParseSpec("fake:x", nil)
	argv, _ := spec.Argv()
	_, _, err := startProvider(argv, 5*time.Second)
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
