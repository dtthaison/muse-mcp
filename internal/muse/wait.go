package muse

import (
	"strings"
	"time"

	"github.com/dtthaison/muse-mcp/internal/musepage"
)

// Timing holds every wait and poll duration (docs/design.md §4.4, §4.7, §4.7a).
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
	Unresponsive    time.Duration // page reads failing this long → the tab is gone
	TitleWait       time.Duration // after a voice clip, wait this long for muse to name the chat
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
		Unresponsive:    20 * time.Second,
		TitleWait:       10 * time.Second,
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

// quietFor is how long muse has not looked busy, counted from start when it
// never has. The "not busy" timeouts wait this long, so a job that finishes
// after them still gets its settle time.
func quietFor(lastBusy, start, now time.Time) time.Duration {
	if lastBusy.Before(start) {
		lastBusy = start
	}
	return now.Sub(lastBusy)
}

// ImageWait decides when an image (or video, or voice) request is finished.
type ImageWait struct {
	t           Timing
	media       func(musepage.State) []musepage.Image // the results it waits for
	start       time.Time
	baseMedia   int
	startAgents int // agent messages before the request; Reply starts after these
	baseAgents  int // moves past the first reply once nudged
	lastAgents  int
	idle        idleClock
	lastBusy    time.Time
	nudged      bool
}

func NewImageWait(base musepage.State, t Timing, start time.Time) *ImageWait {
	return newMediaWait(base, t, start, func(s musepage.State) []musepage.Image { return s.Images })
}

// NewVideoWait waits for a video the same way NewImageWait waits for an image.
func NewVideoWait(base musepage.State, t Timing, start time.Time) *ImageWait {
	return newMediaWait(base, t, start, func(s musepage.State) []musepage.Image { return s.Videos })
}

// NewVoiceWait waits for an audio clip the same way.
func NewVoiceWait(base musepage.State, t Timing, start time.Time) *ImageWait {
	return newMediaWait(base, t, start, func(s musepage.State) []musepage.Image { return s.Audios })
}

func newMediaWait(base musepage.State, t Timing, start time.Time, media func(musepage.State) []musepage.Image) *ImageWait {
	n := len(base.AgentTexts)
	return &ImageWait{t: t, media: media, start: start, baseMedia: len(media(base)), startAgents: n, baseAgents: n, lastAgents: n}
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
	if s.Busy {
		w.lastBusy = now
	}
	idleFor := w.idle.observe(s.Busy, now)
	ready := w.NewMedia(s)
	if len(ready) > 0 && !s.Busy && idleFor >= w.t.ImageSettle {
		return Done
	}
	pending := len(w.media(s)) > w.baseMedia // includes results still loading
	if len(s.AgentTexts) > w.baseAgents && !pending && !s.Busy && idleFor >= w.t.TextOnlyGrace {
		if w.nudged {
			return Refused
		}
		return Nudge
	}
	elapsed := now.Sub(w.start)
	quiet := quietFor(w.lastBusy, w.start, now) >= w.t.TextOnlyGrace+w.t.ImageSettle
	if elapsed >= w.t.ImageMax || (elapsed >= w.t.ImageTimeout && !s.Busy && quiet) {
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

// NewMedia are results posted after the wait began that have loaded.
func (w *ImageWait) NewMedia(s musepage.State) []musepage.Image {
	return loaded(w.media(s), w.baseMedia)
}

// ChatWait decides when a chat reply is finished.
type ChatWait struct {
	t           Timing
	start       time.Time
	baseAgents  int
	baseImages  int
	baseVideos  int
	baseAudios  int
	lastCount   int
	lastText    string
	stableSince time.Time
	stablePolls int
	lastBusy    time.Time
}

func NewChatWait(base musepage.State, t Timing, start time.Time) *ChatWait {
	return &ChatWait{t: t, start: start, baseAgents: len(base.AgentTexts), baseImages: len(base.Images),
		baseVideos: len(base.Videos), baseAudios: len(base.Audios),
		lastCount: len(base.AgentTexts), lastText: lastOf(base.AgentTexts), stableSince: start}
}

func (w *ChatWait) Step(s musepage.State, now time.Time) Verdict {
	if s.Approval != nil {
		return NeedsApproval
	}
	if s.Busy {
		w.lastBusy = now
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
	quiet := quietFor(w.lastBusy, w.start, now) >= 2*w.t.ChatStable
	if elapsed >= w.t.ChatMax || (elapsed >= w.t.ChatTimeout && !s.Busy && quiet) {
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

func (w *ChatWait) NewVideos(s musepage.State) []musepage.Image {
	return loaded(s.Videos, w.baseVideos)
}

func (w *ChatWait) NewAudios(s musepage.State) []musepage.Image {
	return loaded(s.Audios, w.baseAudios)
}

func loaded(images []musepage.Image, base int) []musepage.Image {
	var out []musepage.Image
	for i := base; i < len(images); i++ {
		// A blob: URL means loaded; an audio card is ready once it is shown.
		if src := images[i].Src; strings.HasPrefix(src, "blob:") || strings.HasPrefix(src, "audio:") {
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
