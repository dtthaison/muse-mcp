// Package browser owns the Chrome process muse-mcp drives: a dedicated
// profile the owner logs in to once, hidden normally and visible for login.
package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"

	"github.com/chromedp/chromedp"
)

var ErrLocked = errors.New("another muse-mcp (another Claude Code session or a muse-mcp command) is using your muse login; use muse there or quit it, then try again")

type Options struct {
	ExecPath   string
	ProfileDir string
	LockPath   string
	// LockLater takes the lock when Chrome first starts instead of in New,
	// so a server that never uses muse doesn't keep others out.
	LockLater bool
}

// Browser starts Chrome lazily and relaunches it when it has died or when the
// hidden/visible mode changes.
type Browser struct {
	opts Options
	lock *os.File

	mu            sync.Mutex
	visible       bool
	gen           uint64
	cancelAlloc   context.CancelFunc
	browserCtx    context.Context
	cancelBrowser context.CancelFunc
}

// New takes the profile lock, unless LockLater is set. It does not start
// Chrome.
func New(opts Options) (*Browser, error) {
	b := &Browser{opts: opts}
	if !opts.LockLater {
		if err := b.lockLocked(); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// lockLocked takes the profile lock if this Browser doesn't hold it yet.
func (b *Browser) lockLocked() error {
	if b.lock != nil {
		return nil
	}
	f, err := os.OpenFile(b.opts.LockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrLocked
		}
		return fmt.Errorf("locking %s: %w", b.opts.LockPath, err)
	}
	b.lock = f
	return nil
}

// NewTab opens a new tab, starting (or restarting) Chrome first if needed.
// The returned cancel closes the tab.
func (b *Browser) NewTab(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.ensureLocked(); err != nil {
		return nil, nil, err
	}
	tab, cancel := chromedp.NewContext(b.browserCtx)
	if err := chromedp.Do(tab); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("opening a Chrome tab: %w", err)
	}
	return tab, cancel, nil
}

// SetVisible switches between a hidden and a visible window, relaunching
// Chrome only when the mode actually changes.
func (b *Browser) SetVisible(visible bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.visible == visible {
		return nil
	}
	b.visible = visible
	if !b.runningLocked() {
		return nil
	}
	b.stopLocked()
	return b.launchLocked()
}

func (b *Browser) Visible() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.visible
}

func (b *Browser) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runningLocked()
}

// Generation counts Chrome launches. Tabs and page state from an earlier
// generation are gone.
func (b *Browser) Generation() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.gen
}

// Close stops Chrome and releases the profile lock.
func (b *Browser) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopLocked()
	if b.lock == nil {
		return nil
	}
	syscall.Flock(int(b.lock.Fd()), syscall.LOCK_UN)
	err := b.lock.Close()
	b.lock = nil
	return err
}

func (b *Browser) runningLocked() bool {
	return b.browserCtx != nil && b.browserCtx.Err() == nil
}

func (b *Browser) ensureLocked() error {
	if b.runningLocked() {
		return nil
	}
	b.stopLocked()
	return b.launchLocked()
}

func (b *Browser) launchLocked() error {
	if err := b.lockLocked(); err != nil {
		return err
	}
	// chromedp talks to Chrome over a pipe; a remote-debugging port or address
	// flag would open a port other programs could attach to.
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(b.opts.ExecPath),
		chromedp.UserDataDir(b.opts.ProfileDir),
	)
	if b.visible {
		opts = append(opts,
			chromedp.Flag("headless", false),
			chromedp.Flag("hide-scrollbars", false),
			chromedp.Flag("mute-audio", false),
			chromedp.WindowSize(1280, 900),
		)
	} else {
		opts = append(opts, chromedp.WindowSize(1440, 2200))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	if err := chromedp.Do(browserCtx); err != nil {
		cancelBrowser()
		cancelAlloc()
		return fmt.Errorf("starting Chrome (%s): %w", b.opts.ExecPath, err)
	}
	b.cancelAlloc, b.browserCtx, b.cancelBrowser = cancelAlloc, browserCtx, cancelBrowser
	b.gen++
	return nil
}

func (b *Browser) stopLocked() {
	if b.browserCtx == nil {
		return
	}
	if b.browserCtx.Err() == nil {
		chromedp.Cancel(b.browserCtx) // closes Chrome gracefully
	}
	b.cancelBrowser()
	b.cancelAlloc()
	b.browserCtx, b.cancelBrowser, b.cancelAlloc = nil, nil, nil
}

// pid is the Chrome process ID (0 when not running); used by tests.
func (b *Browser) pid() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.runningLocked() {
		return 0
	}
	if p := chromedp.FromContext(b.browserCtx).Browser.Process(); p != nil {
		return p.Pid
	}
	return 0
}
