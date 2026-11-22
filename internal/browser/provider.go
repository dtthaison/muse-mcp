package browser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Provider timeouts. The values are part of the provider contract in the README.
const (
	ProviderStartTimeout = 90 * time.Second // wait for the DevTools address
	describeTimeout      = 3 * time.Second
	stderrLines          = 20
)

// providerStopTimeout is how long a provider gets after SIGTERM before SIGKILL
// (a variable so tests can shorten it).
var providerStopTimeout = 5 * time.Second

// providerEOFGrace is how long a provider has to exit on its own after its
// stdin is closed, before it gets SIGTERM.
const providerEOFGrace = 200 * time.Millisecond

// provider is a running external provider: a program that prints a DevTools
// address and then keeps running (it may hold tunnels) until its stdin closes.
type provider struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	done     chan struct{} // closed when the process has been reaped
	errw     *tailWriter
	stopOnce sync.Once
}

// startProvider runs argv and waits up to timeout for the first line of its
// stdout, which must be a DevTools address. A cancelled ctx aborts the wait and
// stops the half-started provider; once it has started, the provider's life is
// not tied to ctx (it runs until stop).
func startProvider(ctx context.Context, argv []string, timeout time.Duration) (string, *provider, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	// Not exec.CommandContext: ctx belongs to one request, the provider outlives it.
	cmd := exec.Command(argv[0], argv[1:]...)
	// Its own process group, so a Ctrl-C meant for muse-mcp doesn't kill the
	// provider before muse-mcp has shut it down in order.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	first := newFirstLine()
	p := &provider{cmd: cmd, done: make(chan struct{}), errw: &tailWriter{}}
	cmd.Stdout = first
	cmd.Stderr = p.errw
	cmd.WaitDelay = 2 * time.Second // a tunnel it left behind must not hang Wait
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", nil, err
	}
	p.stdin = stdin
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("starting %s: %w", argv[0], err)
	}
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(p.done)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var line string
	select {
	case line = <-first.line:
	case <-p.done:
		select { // the line may have arrived just before the exit
		case line = <-first.line:
		default:
			err := p.failure(argv[0], waitErr, "exited before it printed a DevTools address")
			p.stop() // anything it left behind in its group goes too
			return "", nil, err
		}
	case <-timer.C:
		err := p.failure(argv[0], nil, fmt.Sprintf("did not print a DevTools address within %s", timeout.Round(time.Second)))
		p.stop()
		return "", nil, err
	case <-ctx.Done():
		p.stop()
		return "", nil, fmt.Errorf("starting browser provider %s: %w", argv[0], ctx.Err())
	}
	line = strings.TrimSpace(line)
	if err := CheckDevToolsURL(line); err != nil {
		p.stop()
		return "", nil, fmt.Errorf("%s printed %q as its first line, but a DevTools address (http://host:port or ws://…) is expected\n%s",
			argv[0], line, p.errw.report())
	}
	return line, p, nil
}

func (p *provider) failure(name string, waitErr error, what string) error {
	msg := fmt.Sprintf("browser provider %s %s", name, what)
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		msg += fmt.Sprintf(" (exit code %d)", ee.ExitCode())
	} else if waitErr != nil {
		msg += fmt.Sprintf(" (%v)", waitErr)
	}
	return errors.New(msg + "\n" + p.errw.report())
}

// stop closes the provider's stdin, sends SIGTERM to its process group (after
// a short moment for a provider that exits on EOF), waits up to 5 s for the
// leader to exit and the group to empty, then sends SIGKILL to the group. It
// returns once the leader has been reaped and nothing is left in the group, so
// a tunnel the provider started and did not stop goes with it. Calling it
// again is harmless.
func (p *provider) stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(p.stopNow)
	<-p.done
}

func (p *provider) stopNow() {
	p.stdin.Close()
	// A provider that exits on stdin EOF gets a moment to do so before the signal.
	select {
	case <-p.done:
	case <-time.After(providerEOFGrace):
	}
	if p.cmd.Process == nil {
		return
	}
	pgid := p.cmd.Process.Pid // the provider leads its own process group
	if !p.exited() || groupAlive(pgid) {
		if syscall.Kill(-pgid, syscall.SIGTERM) != nil {
			p.cmd.Process.Signal(syscall.SIGTERM)
		}
	}
	deadline := time.NewTimer(providerStopTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !p.exited() || groupAlive(pgid) {
		select {
		case <-deadline.C:
			// The whole group, so a tunnel the provider left behind goes too.
			if syscall.Kill(-pgid, syscall.SIGKILL) != nil {
				p.cmd.Process.Kill()
			}
			<-p.done
			return
		case <-tick.C:
		}
	}
}

// groupAlive reports whether any process is left in the process group pgid.
// Only call it for a group whose leader is this provider: once the group is
// empty its id can belong to another process.
func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (p *provider) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// firstLine records the first line written to it and discards the rest.
type firstLine struct {
	mu   sync.Mutex
	buf  []byte
	sent bool
	line chan string
}

func newFirstLine() *firstLine { return &firstLine{line: make(chan string, 1)} }

func (f *firstLine) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.sent {
		f.buf = append(f.buf, b...)
		if i := bytes.IndexByte(f.buf, '\n'); i >= 0 {
			f.line <- string(f.buf[:i])
			f.sent, f.buf = true, nil
		} else if len(f.buf) > 64<<10 {
			f.line <- string(f.buf) // a runaway line is not an address; report it
			f.sent, f.buf = true, nil
		}
	}
	return len(b), nil
}

// tailWriter keeps the end of what is written to it, for error messages.
type tailWriter struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailWriter) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > 32<<10 {
		t.buf = t.buf[len(t.buf)-16<<10:]
	}
	return len(b), nil
}

// report is the last lines of stderr, ready to append to an error.
func (t *tailWriter) report() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimRight(string(t.buf), "\r\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "(the provider wrote nothing to stderr)"
	}
	if len(lines) > stderrLines {
		lines = lines[len(lines)-stderrLines:]
	}
	return "provider stderr:\n  " + strings.Join(lines, "\n  ")
}

// ProviderInfo describes a muse-browser-* program found on PATH.
type ProviderInfo struct {
	Name        string // "shardx" for muse-browser-shardx
	Path        string
	Description string // from --describe; "(no description)" when that failed
}

// Describe runs "<path> --describe" and returns its first line of output.
func Describe(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), describeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--describe")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("--describe printed nothing")
	}
	return line, nil
}

// Discover lists the providers on PATH (the first one wins when a name
// appears twice), sorted by name, each described by its --describe line.
func Discover() []ProviderInfo {
	seen := map[string]bool{}
	var found []ProviderInfo
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name, ok := strings.CutPrefix(e.Name(), ProviderPrefix)
			if !ok || !providerName.MatchString(name) || seen[name] {
				continue
			}
			full := filepath.Join(dir, e.Name())
			if fi, err := os.Stat(full); err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
				continue
			}
			seen[name] = true
			found = append(found, ProviderInfo{Name: name, Path: full})
		}
	}
	var wg sync.WaitGroup
	for i := range found {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := Describe(found[i].Path)
			if err != nil {
				d = "(no description)"
			}
			found[i].Description = d
		}()
	}
	wg.Wait()
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found
}
