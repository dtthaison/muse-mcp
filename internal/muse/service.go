// Package muse is the service behind the MCP tools: it runs one request at a
// time against muse.ai, waits for results, and hands approvals to the owner.
package muse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/dtthaison/muse-mcp/internal/browser"
	"github.com/dtthaison/muse-mcp/internal/chats"
	"github.com/dtthaison/muse-mcp/internal/musepage"
	"github.com/dtthaison/muse-mcp/internal/output"
	"github.com/dtthaison/muse-mcp/internal/prompt"
)

var (
	ErrLoggedOut         = errors.New("Log in to muse.ai in the window that just opened, then try again.")
	ErrUnknownChat       = errors.New("unknown chat_id: only chats started by muse-mcp can be continued; start a new chat")
	ErrAwaitingApproval  = errors.New("this chat is waiting for an approval decision; call muse_decide_approval or wait for it to time out")
	ErrNoSuchApproval    = errors.New("no such pending approval (the server may have restarted)")
	ErrApprovalTimedOut  = errors.New("already denied (timed out)")
	ErrUntrackedApproval = errors.New("this chat has an approval muse-mcp is not tracking (muse-mcp may have restarted); decide it in muse.ai first")
)

// RefusedError means muse answered with text and no image (or video), even
// after the nudge.
type RefusedError struct {
	Reply string
	Video bool
}

func (e RefusedError) Error() string {
	if e.Video {
		return "muse replied without a video: " + e.Reply
	}
	return "muse replied without an image: " + e.Reply
}

const (
	idleTabLimit  = 15 * time.Minute
	loginPoll     = 3 * time.Second
	threadIDLimit = 15 * time.Second
)

type Options struct {
	BaseURL string
	Browser *browser.Browser
	Chats   *chats.Registry
	Timing  Timing
	Log     *slog.Logger
	Now     func() time.Time
}

type ImageRequest struct {
	Text        string   // from prompt.Build
	Attachments []string // from prompt.Build
	OutputPath  string
	Video       bool // a video (experimental) instead of an image
}

type ChatRequest struct {
	Message      string
	ChatID       string
	Attachments  []string
	SaveImagesTo string
}

type PendingApproval struct {
	ChatID    string    `json:"chat_id"`
	ID        string    `json:"approval_id"`
	Text      string    `json:"text"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Result struct {
	Status   string           `json:"status"` // "done" | "needs_approval" | "timeout"
	ChatID   string           `json:"chat_id"`
	ChatURL  string           `json:"chat_url"`
	Reply    string           `json:"reply"`
	Images   []output.Saved   `json:"images,omitempty"`
	Videos   []output.Saved   `json:"videos,omitempty"`
	Approval *PendingApproval `json:"approval,omitempty"`
}

type StatusInfo struct {
	ChromeRunning bool              `json:"chrome_running"`
	Visible       bool              `json:"window_visible"`
	LoggedIn      bool              `json:"logged_in"`
	TrackedChats  int               `json:"tracked_chats"`
	Pending       []PendingApproval `json:"pending_approvals"`
}

// tab is an open chat page.
type tab struct {
	ctx      context.Context
	cancel   context.CancelFunc
	gen      uint64 // browser generation it was opened in
	lastUsed time.Time
	failing  failing
}

// errTabGone: chromedp does not notice a tab closed by hand, so a tab whose
// page can't be read for Timing.Unresponsive counts as gone.
var errTabGone = errors.New("the muse tab was closed or stopped responding; try again")

// failing tracks how long page reads have been failing in a row.
type failing struct{ since time.Time }

// observe records one read and reports whether reads have failed for longer
// than limit.
func (f *failing) observe(err error, limit time.Duration) bool {
	if err == nil {
		f.since = time.Time{}
		return false
	}
	if f.since.IsZero() {
		f.since = time.Now()
	}
	return time.Since(f.since) > limit
}

// pending is a parked request waiting for the owner's approval decision.
type pending struct {
	PendingApproval
	tab   *tab
	image *ImageRequest
	iw    *ImageWait
	chat  *ChatRequest
	cw    *ChatWait
	timer *time.Timer
}

type Service struct {
	o    Options
	call sync.Mutex // one request at a time

	mu       sync.Mutex // guards everything below
	tabs     map[string]*tab
	active   *tab                // the tab the running call is using; sync never drops it
	seq      uint64              // approval IDs are unique for the server's lifetime
	pending  map[string]*pending // by chat ID
	timedOut map[string]bool     // approval IDs denied by the timeout
	watching bool
	loggedIn bool
	closed   bool
}

func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Service{o: o, tabs: map[string]*tab{}, pending: map[string]*pending{}, timedOut: map[string]bool{}}
}

// GenerateImage sends a built image (or video) prompt in a fresh chat and saves
// the result.
func (s *Service) GenerateImage(ctx context.Context, r ImageRequest) (Result, error) {
	s.call.Lock()
	defer s.call.Unlock()
	defer s.setActive(nil)
	s.sync()
	id, t, base, err := s.begin(ctx, "", r.Attachments, r.Text)
	if err != nil {
		return Result{}, err
	}
	w := NewImageWait(base, s.o.Timing, s.o.Now())
	if r.Video {
		w = NewVideoWait(base, s.o.Timing, s.o.Now())
	}
	return s.runImage(ctx, id, t, &r, w)
}

// Chat sends a message in a new chat, or continues one this server created.
func (s *Service) Chat(ctx context.Context, r ChatRequest) (Result, error) {
	s.call.Lock()
	defer s.call.Unlock()
	defer s.setActive(nil)
	s.sync()
	id, t, base, err := s.begin(ctx, r.ChatID, r.Attachments, r.Message)
	if err != nil {
		return Result{}, err
	}
	return s.runChat(ctx, id, t, &r, NewChatWait(base, s.o.Timing, s.o.Now()))
}

// Decide clicks Approve or Deny on a parked approval and resumes the wait.
func (s *Service) Decide(ctx context.Context, chatID, approvalID string, approve bool) (Result, error) {
	s.call.Lock()
	defer s.call.Unlock()
	defer s.setActive(nil)
	s.sync()
	s.mu.Lock()
	timedOut, p := s.timedOut[approvalID], s.pending[chatID]
	s.mu.Unlock()
	if p == nil || p.ID != approvalID {
		if timedOut {
			return Result{}, ErrApprovalTimedOut
		}
		return Result{}, fmt.Errorf("%w; open the chat in muse.ai to decide it there: %s",
			ErrNoSuchApproval, musepage.ThreadURL(s.o.BaseURL, chatID))
	}
	op, release := bind(ctx, p.tab.ctx)
	err := musepage.Decide(op, approve)
	release()
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, err // still pending; the timer keeps running
	}
	s.mu.Lock()
	p.timer.Stop()
	delete(s.pending, chatID)
	p.tab.lastUsed = s.o.Now()
	s.active = p.tab
	s.mu.Unlock()
	s.o.Log.Info("approval decided", "chat", chatID, "approval", approvalID, "approve", approve)
	if p.image != nil {
		p.iw.Resume(s.o.Now())
		return s.runImage(ctx, chatID, p.tab, p.image, p.iw)
	}
	p.cw.Resume(s.o.Now())
	return s.runChat(ctx, chatID, p.tab, p.chat, p.cw)
}

func (s *Service) Status(ctx context.Context) (StatusInfo, error) {
	s.sync()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := StatusInfo{
		ChromeRunning: s.o.Browser.Running(),
		Visible:       s.o.Browser.Visible(),
		LoggedIn:      s.loggedIn,
		TrackedChats:  s.o.Chats.Count(),
		Pending:       []PendingApproval{},
	}
	for _, p := range s.pending {
		st.Pending = append(st.Pending, p.PendingApproval)
	}
	sort.Slice(st.Pending, func(i, j int) bool { return st.Pending[i].ExpiresAt.Before(st.Pending[j].ExpiresAt) })
	return st, nil
}

// Close stops approval timers and closes every tab.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for id, p := range s.pending {
		p.timer.Stop()
		delete(s.pending, id)
	}
	for id, t := range s.tabs {
		t.cancel()
		delete(s.tabs, id)
	}
}

// begin opens (or reuses) the chat page, attaches files and sends text. For a
// new chat it waits for muse to assign the chat ID and records it.
func (s *Service) begin(ctx context.Context, chatID string, files []string, text string) (string, *tab, musepage.State, error) {
	var t *tab
	var base musepage.State
	var err error
	if chatID != "" {
		if !s.o.Chats.Known(chatID) {
			return "", nil, base, ErrUnknownChat
		}
		s.mu.Lock()
		_, waiting := s.pending[chatID]
		t = s.tabs[chatID]
		s.mu.Unlock()
		if waiting {
			return "", nil, base, ErrAwaitingApproval
		}
	}
	fresh := t == nil
	if fresh {
		if t, err = s.newTab(ctx); err != nil {
			return "", nil, base, err
		}
		url := musepage.NewChatURL(s.o.BaseURL)
		if chatID != "" {
			url = musepage.ThreadURL(s.o.BaseURL, chatID)
		}
		base, err = s.open(ctx, t, url)
	} else {
		op, release := bind(ctx, t.ctx)
		base, err = musepage.Snapshot(op)
		release()
	}
	if err == nil && chatID != "" && base.Approval != nil {
		err = fmt.Errorf("%w: %s", ErrUntrackedApproval, musepage.ThreadURL(s.o.BaseURL, chatID))
	}
	if err == nil {
		op, release := bind(ctx, t.ctx)
		if err = musepage.Attach(op, files); err == nil {
			err = musepage.Send(op, text)
		}
		if err == nil && chatID == "" {
			chatID, err = s.awaitChatID(ctx, op)
		}
		release()
	}
	if err != nil {
		t.cancel()
		if chatID != "" {
			s.mu.Lock()
			delete(s.tabs, chatID)
			s.mu.Unlock()
		}
		if ctx.Err() != nil {
			return "", nil, base, ctx.Err()
		}
		return "", nil, base, err
	}
	s.mu.Lock()
	t.lastUsed = s.o.Now()
	s.tabs[chatID] = t
	s.active = t
	s.mu.Unlock()
	return chatID, t, base, nil
}

func (s *Service) open(ctx context.Context, t *tab, url string) (musepage.State, error) {
	op, release := bind(ctx, t.ctx)
	defer release()
	st, err := musepage.Open(op, url, s.o.Timing.Ready)
	s.mu.Lock()
	switch {
	case err == nil:
		s.loggedIn = true
	case errors.Is(err, musepage.ErrLoggedOut):
		s.loggedIn = false
	}
	s.mu.Unlock()
	if errors.Is(err, musepage.ErrLoggedOut) {
		t.cancel()
		return st, s.startLogin()
	}
	return st, err
}

func (s *Service) awaitChatID(ctx, op context.Context) (string, error) {
	deadline := time.Now().Add(threadIDLimit)
	for time.Now().Before(deadline) {
		if st, err := musepage.Snapshot(op); err == nil {
			if id, ok := musepage.ThreadID(st.URL); ok {
				if err := s.o.Chats.Add(id); err != nil {
					s.o.Log.Warn("saving chats.json", "err", err)
				}
				return id, nil
			}
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return "", err
		}
	}
	return "", errors.New("muse did not start the chat (no chat ID appeared)")
}

func (s *Service) runImage(ctx context.Context, id string, t *tab, r *ImageRequest, w *ImageWait) (Result, error) {
	op, release := bind(ctx, t.ctx)
	defer release()
	for {
		snap, err := s.poll(ctx, op, t)
		if err != nil {
			s.dropTab(id)
			return Result{}, err
		}
		if snap == nil {
			continue
		}
		res := Result{ChatID: id, ChatURL: musepage.ThreadURL(s.o.BaseURL, id), Reply: w.Reply(*snap)}
		switch w.Step(*snap, s.o.Now()) {
		case NeedsApproval:
			return s.park(id, t, *snap, res, &pending{image: r, iw: w}), nil
		case Nudge:
			s.o.Log.Info("text-only reply; asking for the result", "chat", id)
			nudge := prompt.Nudge
			if r.Video {
				nudge = prompt.NudgeVideo
			}
			if err := musepage.Send(op, nudge); err != nil {
				s.dropTab(id)
				return Result{}, err
			}
			w.Nudged()
		case Refused:
			s.dropTab(id)
			return Result{}, RefusedError{Reply: res.Reply, Video: r.Video}
		case TimedOut:
			s.dropTab(id)
			res.Status = "timeout"
			return res, nil
		case Done:
			defer s.dropTab(id)
			for _, im := range w.NewMedia(*snap) {
				if r.Video {
					saved, err := s.download(op, im, func(data []byte) (output.Saved, error) {
						return output.SaveVideo(data, r.OutputPath, im.Width, im.Height)
					})
					if err != nil {
						return Result{}, err
					}
					res.Videos = append(res.Videos, saved)
					continue
				}
				saved, err := s.download(op, im, func(data []byte) (output.Saved, error) { return output.Save(data, r.OutputPath) })
				if err != nil {
					return Result{}, err
				}
				res.Images = append(res.Images, saved)
			}
			res.Status = "done"
			return res, nil
		}
	}
}

func (s *Service) runChat(ctx context.Context, id string, t *tab, r *ChatRequest, w *ChatWait) (Result, error) {
	op, release := bind(ctx, t.ctx)
	defer release()
	for {
		snap, err := s.poll(ctx, op, t)
		if err != nil {
			s.dropTab(id)
			return Result{}, err
		}
		if snap == nil {
			continue
		}
		res := Result{ChatID: id, ChatURL: musepage.ThreadURL(s.o.BaseURL, id), Reply: w.Reply(*snap)}
		switch v := w.Step(*snap, s.o.Now()); v {
		case NeedsApproval:
			return s.park(id, t, *snap, res, &pending{chat: r, cw: w}), nil
		case Done, TimedOut:
			res.Status = "done"
			if v == TimedOut {
				res.Status = "timeout"
			}
			if r.SaveImagesTo != "" {
				for _, im := range w.NewImages(*snap) {
					saved, err := s.download(op, im, func(data []byte) (output.Saved, error) {
						return output.SaveInDir(data, r.SaveImagesTo, "muse-reply")
					})
					if err != nil {
						return Result{}, err
					}
					res.Images = append(res.Images, saved)
				}
				for _, v := range w.NewVideos(*snap) {
					saved, err := s.download(op, v, func(data []byte) (output.Saved, error) {
						return output.SaveVideoInDir(data, r.SaveImagesTo, "muse-reply", v.Width, v.Height)
					})
					if err != nil {
						return Result{}, err
					}
					res.Videos = append(res.Videos, saved)
				}
			}
			s.mu.Lock()
			t.lastUsed = s.o.Now()
			s.mu.Unlock()
			return res, nil
		}
	}
}

// poll waits one interval and snapshots the page. A nil state with no error
// means a transient read failure; try again.
func (s *Service) poll(ctx, op context.Context, t *tab) (*musepage.State, error) {
	if err := sleep(ctx, s.o.Timing.Poll); err != nil {
		return nil, err
	}
	snap, err := snapshot(op, s.o.Timing.Unresponsive)
	gone := t.failing.observe(err, s.o.Timing.Unresponsive)
	if err == nil {
		if snap.Stalled {
			s.o.Log.Info("muse page shows a stall message")
		}
		return &snap, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if t.ctx.Err() != nil {
		return nil, errors.New("Chrome closed while waiting for muse; try again")
	}
	if gone {
		return nil, errTabGone
	}
	return nil, nil
}

// snapshot reads the page, giving up after limit (a closed tab can hang).
func snapshot(ctx context.Context, limit time.Duration) (musepage.State, error) {
	c, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	return musepage.Snapshot(c)
}

func (s *Service) download(op context.Context, im musepage.Image, save func([]byte) (output.Saved, error)) (output.Saved, error) {
	data, err := musepage.Download(op, im.Src)
	if err != nil {
		return output.Saved{}, err
	}
	return save(data)
}

// park records an approval the owner must decide and returns needs_approval.
// The tab stays open; the call lock is released when the caller returns.
func (s *Service) park(id string, t *tab, snap musepage.State, res Result, p *pending) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	t.lastUsed = s.o.Now()
	p.tab = t
	p.PendingApproval = PendingApproval{ChatID: id, ID: fmt.Sprintf("%s-%d", id, s.seq),
		Text: snap.Approval.Text, ExpiresAt: s.o.Now().Add(s.o.Timing.ApprovalTimeout)}
	approvalID := p.ID
	p.timer = time.AfterFunc(s.o.Timing.ApprovalTimeout, func() { s.expire(id, approvalID) })
	s.pending[id] = p
	s.o.Log.Info("approval needed", "chat", id, "approval", approvalID)
	res.Status = "needs_approval"
	a := p.PendingApproval
	res.Approval = &a
	return res
}

// expire denies an approval nobody decided in time.
func (s *Service) expire(chatID, approvalID string) {
	s.call.Lock()
	defer s.call.Unlock()
	s.mu.Lock()
	p := s.pending[chatID]
	if s.closed || p == nil || p.ID != approvalID {
		s.mu.Unlock()
		return
	}
	delete(s.pending, chatID)
	s.timedOut[approvalID] = true
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(p.tab.ctx, 15*time.Second)
	err := musepage.Decide(ctx, false)
	cancel()
	s.o.Log.Info("approval timed out; denied", "chat", chatID, "approval", approvalID, "err", err)
	if p.image != nil {
		s.dropTab(chatID)
	}
}

// startLogin shows Chrome on muse.ai so the owner can log in, and watches for
// the login to finish to hide it again.
func (s *Service) startLogin() error {
	if err := s.o.Browser.SetVisible(true); err != nil {
		return err
	}
	s.sync() // the relaunch dropped every tab and pending approval
	s.mu.Lock()
	if s.watching {
		s.mu.Unlock()
		return ErrLoggedOut
	}
	s.watching = true
	s.mu.Unlock()

	tabCtx, cancel, err := s.o.Browser.NewTab(context.Background())
	if err == nil {
		err = chromedp.Do(tabCtx, chromedp.Navigate(s.o.BaseURL))
	}
	if err != nil {
		if cancel != nil {
			cancel()
		}
		s.mu.Lock()
		s.watching = false
		s.mu.Unlock()
		return fmt.Errorf("opening the muse.ai login window: %w", err)
	}
	go s.watchLogin(tabCtx, cancel)
	return ErrLoggedOut
}

func (s *Service) watchLogin(tabCtx context.Context, cancel context.CancelFunc) {
	defer func() {
		cancel()
		s.mu.Lock()
		s.watching = false
		s.mu.Unlock()
	}()
	var f failing
	for {
		if sleep(tabCtx, loginPoll) != nil {
			return // Chrome relaunched
		}
		st, err := snapshot(tabCtx, s.o.Timing.Unresponsive)
		if f.observe(err, s.o.Timing.Unresponsive) {
			s.o.Log.Info("login window closed")
			return // the next muse call opens it again
		}
		if err != nil || !st.Ready || st.LoggedOut {
			continue
		}
		s.o.Log.Info("login detected; hiding Chrome")
		cancel()
		s.call.Lock()
		err = s.o.Browser.SetVisible(false)
		s.call.Unlock()
		if err != nil {
			s.o.Log.Warn("hiding Chrome after login", "err", err)
		}
		s.mu.Lock()
		s.loggedIn = true
		s.mu.Unlock()
		return
	}
}

// sync drops tabs (and their pending approvals) from a previous Chrome launch,
// from a dead Chrome, or idle too long.
func (s *Service) sync() {
	gen, running := s.o.Browser.Generation(), s.o.Browser.Running()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.o.Now()
	for id, t := range s.tabs {
		p := s.pending[id]
		stale := t.gen != gen || !running || t.ctx.Err() != nil
		idle := p == nil && t != s.active && now.Sub(t.lastUsed) > idleTabLimit
		if !stale && !idle {
			continue
		}
		t.cancel()
		delete(s.tabs, id)
		if p != nil {
			p.timer.Stop()
			delete(s.pending, id)
			s.o.Log.Info("dropped pending approval after Chrome restarted", "chat", id, "approval", p.ID)
		}
	}
}

func (s *Service) newTab(ctx context.Context) (*tab, error) {
	c, cancel, err := s.o.Browser.NewTab(ctx)
	if err != nil {
		return nil, err
	}
	return &tab{ctx: c, cancel: cancel, gen: s.o.Browser.Generation(), lastUsed: s.o.Now()}, nil
}

func (s *Service) setActive(t *tab) {
	s.mu.Lock()
	s.active = t
	s.mu.Unlock()
}

func (s *Service) dropTab(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.tabs[id]; t != nil {
		t.cancel()
		delete(s.tabs, id)
	}
}

// bind derives a context for page operations on tab that is also cancelled
// when the request's context is.
func bind(req, tab context.Context) (context.Context, func()) {
	c, cancel := context.WithCancel(tab)
	stop := context.AfterFunc(req, cancel)
	return c, func() { stop(); cancel() }
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
