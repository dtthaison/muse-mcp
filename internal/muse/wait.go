package muse

import (
	"strings"
	"time"

	"pp/muse-mcp/internal/musepage"
)

// Timing holds every wait and poll duration (design doc §4.4, §4.7, §4.7a).
type Timing struct {
	Poll            time.Duration
	Ready           time.Duration // page load until the chat box is usable
	ImageTimeout    time.Duration // give up unless muse is visibly working
	ImageMax        time.Duration // give up regardless
	ImageSettle     time.Duration // Stop button gone this long with a new image → done
	TextOnlyGrace   time.Duration // idle text-only reply this long → nudge, then refuse
	ChatTimeout     time.Duration
	ChatMax         time.Duration
	ChatStable      time.Duration // reply unchanged and idle this long → done
	ChatForcePolls  int           // reply unchanged for this many polls → done even if busy
	ApprovalTimeout time.Duration // unanswered approval → Deny
}

func DefaultTiming() Timing {
	return Timing{
		Poll:            1500 * time.Millisecond,
		Ready:           45 * time.Second,
		ImageTimeout:    4 * time.Minute,
		ImageMax:        10 * time.Minute,
		ImageSettle:     3 * time.Second,
		TextOnlyGrace:   10 * time.Second,
		ChatTimeout:     5 * time.Minute,
		ChatMax:         10 * time.Minute,
		ChatStable:      3 * time.Second,
		ChatForcePolls:  12,
		ApprovalTimeout: 10 * time.Minute,
	}
}

// Verdict is what a wait decides after looking at one page snapshot.
type Verdict int

const (
	Continue Verdict = iota
	Done
	NeedsApproval
	Nudge   // text but no image: ask once for the image
	Refused // still no image after the nudge
	TimedOut
)

func (v Verdict) String() string {
	return [...]string{"Continue", "Done", "NeedsApproval", "Nudge", "Refused", "TimedOut"}[v]
}

// idleClock tracks how long the Stop button has been gone.
type idleClock struct{ since time.Time }

func (c *idleClock) observe(busy bool, now time.Time) time.Duration {
	if busy {
		c.since = time.Time{}
		return 0
	}
	if c.since.IsZero() {
		c.since = now
	}
	return now.Sub(c.since)
}

// ImageWait decides when an image request is finished.
type ImageWait struct {
	t           Timing
	start       time.Time
	baseImages  int
	startAgents int // agent messages before the request; Reply starts after these
	baseAgents  int // moves past the first reply once nudged
	lastAgents  int
	idle        idleClock
	nudged      bool
}

func NewImageWait(base musepage.State, t Timing, start time.Time) *ImageWait {
	n := len(base.AgentTexts)
	return &ImageWait{t: t, start: start, baseImages: len(base.Images), startAgents: n, baseAgents: n, lastAgents: n}
}

// Reply is everything muse wrote since the request was sent.
func (w *ImageWait) Reply(s musepage.State) string {
	if len(s.AgentTexts) <= w.startAgents {
		return ""
	}
	return strings.Join(s.AgentTexts[w.startAgents:], "\n\n")
}

func (w *ImageWait) Step(s musepage.State, now time.Time) Verdict {
	if s.Approval != nil {
		return NeedsApproval
	}
	w.lastAgents = len(s.AgentTexts)
	idleFor := w.idle.observe(s.Busy, now)
	ready := w.NewImages(s)
	if len(ready) > 0 && !s.Busy && idleFor >= w.t.ImageSettle {
		return Done
	}
	pending := len(s.Images) > w.baseImages // includes images still loading
	if len(s.AgentTexts) > w.baseAgents && !pending && !s.Busy && idleFor >= w.t.TextOnlyGrace {
		if w.nudged {
			return Refused
		}
		return Nudge
	}
	elapsed := now.Sub(w.start)
	if elapsed >= w.t.ImageMax || (elapsed >= w.t.ImageTimeout && !s.Busy) {
		return TimedOut
	}
	return Continue
}

// Resume restarts the timeouts after the wait was parked on an approval.
func (w *ImageWait) Resume(now time.Time) {
	w.start = now
	w.idle = idleClock{}
}

// Nudged marks that the nudge was sent: only a reply after it counts.
func (w *ImageWait) Nudged() {
	w.nudged = true
	w.baseAgents = w.lastAgents
	w.idle = idleClock{}
}

// NewImages are result images posted after the wait began that have loaded.
func (w *ImageWait) NewImages(s musepage.State) []musepage.Image {
	return loaded(s.Images, w.baseImages)
}

// ChatWait decides when a chat reply is finished.
type ChatWait struct {
	t           Timing
	start       time.Time
	baseAgents  int
	baseImages  int
	lastCount   int
	lastText    string
	stableSince time.Time
	stablePolls int
}

func NewChatWait(base musepage.State, t Timing, start time.Time) *ChatWait {
	return &ChatWait{t: t, start: start, baseAgents: len(base.AgentTexts), baseImages: len(base.Images),
		lastCount: len(base.AgentTexts), lastText: lastOf(base.AgentTexts), stableSince: start}
}

func (w *ChatWait) Step(s musepage.State, now time.Time) Verdict {
	if s.Approval != nil {
		return NeedsApproval
	}
	count, text := len(s.AgentTexts), lastOf(s.AgentTexts)
	if count != w.lastCount || text != w.lastText {
		w.lastCount, w.lastText, w.stableSince, w.stablePolls = count, text, now, 0
	} else {
		w.stablePolls++
	}
	if count > w.baseAgents && text != "" {
		if !s.Busy && now.Sub(w.stableSince) >= w.t.ChatStable {
			return Done
		}
		if w.stablePolls >= w.t.ChatForcePolls {
			return Done
		}
	}
	elapsed := now.Sub(w.start)
	if elapsed >= w.t.ChatMax || (elapsed >= w.t.ChatTimeout && !s.Busy) {
		return TimedOut
	}
	return Continue
}

// Resume restarts the timeouts after the wait was parked on an approval.
func (w *ChatWait) Resume(now time.Time) {
	w.start, w.stableSince, w.stablePolls = now, now, 0
}

// Reply is every agent message after the wait began, in order.
func (w *ChatWait) Reply(s musepage.State) string {
	if len(s.AgentTexts) <= w.baseAgents {
		return ""
	}
	return strings.Join(s.AgentTexts[w.baseAgents:], "\n\n")
}

func (w *ChatWait) NewImages(s musepage.State) []musepage.Image {
	return loaded(s.Images, w.baseImages)
}

func loaded(images []musepage.Image, base int) []musepage.Image {
	var out []musepage.Image
	for i := base; i < len(images); i++ {
		if strings.HasPrefix(images[i].Src, "blob:") {
			out = append(out, images[i])
		}
	}
	return out
}

func lastOf(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[len(xs)-1]
}
