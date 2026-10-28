package muse

import (
	"testing"
	"time"

	"pp/muse-mcp/internal/musepage"
)

var t0 = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func waitTiming() Timing {
	t := DefaultTiming()
	t.ImageSettle = 3 * time.Second
	t.TextOnlyGrace = 10 * time.Second
	return t
}

func img(src string) musepage.Image { return musepage.Image{Src: src, Width: 64, Height: 48} }

// step feeds states one second apart and returns the verdicts.
func steps(w interface {
	Step(musepage.State, time.Time) Verdict
}, states ...musepage.State) []Verdict {
	var out []Verdict
	for i, s := range states {
		out = append(out, w.Step(s, t0.Add(time.Duration(i+1)*time.Second)))
	}
	return out
}

func repeat(s musepage.State, n int) []musepage.State {
	out := make([]musepage.State, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func last(v []Verdict) Verdict { return v[len(v)-1] }

func TestWaitImageDoneAfterSettle(t *testing.T) {
	w := NewImageWait(musepage.State{}, waitTiming(), t0)
	done := musepage.State{AgentTexts: []string{"Here"}, Images: []musepage.Image{img("blob:x")}}
	v := steps(w, append([]musepage.State{{Busy: true}}, repeat(done, 4)...)...)
	// idle from second 2; settle 3s → done at second 5
	if v[3] != Continue || v[4] != Done {
		t.Fatalf("verdicts %v", v)
	}
	if n := w.NewImages(done); len(n) != 1 || n[0].Src != "blob:x" {
		t.Fatalf("new images %v", n)
	}
}

func TestWaitImageNotDoneWhileBusy(t *testing.T) {
	w := NewImageWait(musepage.State{}, waitTiming(), t0)
	busy := musepage.State{Busy: true, Images: []musepage.Image{img("blob:x")}}
	if v := steps(w, repeat(busy, 10)...); last(v) != Continue {
		t.Fatalf("verdicts %v", v)
	}
}

func TestWaitImageIgnoresPreexistingImages(t *testing.T) {
	base := musepage.State{AgentTexts: []string{"old"}, Images: []musepage.Image{img("blob:old")}}
	w := NewImageWait(base, waitTiming(), t0)
	if v := steps(w, repeat(base, 5)...); last(v) != Continue {
		t.Fatalf("verdicts %v", v)
	}
	if n := w.NewImages(base); len(n) != 0 {
		t.Fatalf("new images %v", n)
	}
}

func TestWaitImageIgnoresLoadingImage(t *testing.T) {
	w := NewImageWait(musepage.State{}, waitTiming(), t0)
	loading := musepage.State{AgentTexts: []string{"Here"}, Images: []musepage.Image{img("")}}
	if v := steps(w, repeat(loading, 15)...); last(v) != Continue {
		t.Fatalf("an image still loading must not count as done or text-only: %v", v)
	}
}

func TestWaitImageTextOnlyNudgeThenRefused(t *testing.T) {
	w := NewImageWait(musepage.State{}, waitTiming(), t0)
	text := musepage.State{AgentTexts: []string{"I can't"}}
	v := steps(w, repeat(text, 11)...)
	if v[9] != Continue || v[10] != Nudge {
		t.Fatalf("verdicts %v", v)
	}
	w.Nudged()
	// the old reply alone must not count as the answer to the nudge
	if got := w.Step(text, t0.Add(30*time.Second)); got != Continue {
		t.Fatalf("after nudge, old text gave %v", got)
	}
	again := musepage.State{AgentTexts: []string{"I can't", "Still can't"}}
	var got Verdict
	for i := 0; i < 12; i++ {
		got = w.Step(again, t0.Add(time.Duration(31+i)*time.Second))
	}
	if got != Refused {
		t.Fatalf("got %v", got)
	}
}

func TestWaitImageLateImageWithinGraceIsDone(t *testing.T) {
	w := NewImageWait(musepage.State{}, waitTiming(), t0)
	text := musepage.State{AgentTexts: []string{"Here"}}
	withImg := musepage.State{AgentTexts: []string{"Here"}, Images: []musepage.Image{img("blob:x")}}
	v := steps(w, append(repeat(text, 5), repeat(withImg, 4)...)...)
	if last(v) != Done {
		t.Fatalf("verdicts %v", v)
	}
	for _, x := range v {
		if x == Nudge {
			t.Fatalf("nudged before the grace ran out: %v", v)
		}
	}
}

func TestWaitImageTimeoutNotBusy(t *testing.T) {
	tm := waitTiming()
	w := NewImageWait(musepage.State{}, tm, t0)
	if got := w.Step(musepage.State{}, t0.Add(tm.ImageTimeout)); got != TimedOut {
		t.Fatalf("got %v", got)
	}
}

func TestWaitImageBusyExtendsUntilMax(t *testing.T) {
	tm := waitTiming()
	w := NewImageWait(musepage.State{}, tm, t0)
	if got := w.Step(musepage.State{Busy: true}, t0.Add(tm.ImageTimeout+time.Minute)); got != Continue {
		t.Fatalf("busy past timeout: %v", got)
	}
	if got := w.Step(musepage.State{Busy: true}, t0.Add(tm.ImageMax)); got != TimedOut {
		t.Fatalf("at max: %v", got)
	}
}

func TestWaitApprovalWins(t *testing.T) {
	s := musepage.State{AgentTexts: []string{"x"}, Images: []musepage.Image{img("blob:x")}, Approval: &musepage.Approval{Text: "Upload"}}
	if got := NewImageWait(musepage.State{}, waitTiming(), t0).Step(s, t0.Add(time.Hour)); got != NeedsApproval {
		t.Fatalf("image: %v", got)
	}
	if got := NewChatWait(musepage.State{}, waitTiming(), t0).Step(s, t0.Add(time.Hour)); got != NeedsApproval {
		t.Fatalf("chat: %v", got)
	}
}

func TestWaitChatDoneWhenStable(t *testing.T) {
	tm := waitTiming()
	tm.ChatStable = 3 * time.Second
	w := NewChatWait(musepage.State{}, tm, t0)
	v := steps(w,
		musepage.State{Busy: true, AgentTexts: []string{"Hel"}},
		musepage.State{AgentTexts: []string{"Hello"}},
		musepage.State{AgentTexts: []string{"Hello"}},
		musepage.State{AgentTexts: []string{"Hello"}},
		musepage.State{AgentTexts: []string{"Hello"}},
	)
	if v[3] != Continue || v[4] != Done {
		t.Fatalf("verdicts %v", v)
	}
}

func TestWaitChatForceDoneAfterStableBusyPolls(t *testing.T) {
	tm := waitTiming()
	w := NewChatWait(musepage.State{}, tm, t0)
	stuck := musepage.State{Busy: true, AgentTexts: []string{"Done."}}
	v := steps(w, repeat(stuck, tm.ChatForcePolls+1)...)
	if v[tm.ChatForcePolls-1] != Continue || last(v) != Done {
		t.Fatalf("verdicts %v", v)
	}
}

func TestWaitChatReplyOnlyNewBubblesJoined(t *testing.T) {
	base := musepage.State{AgentTexts: []string{"old"}, Images: []musepage.Image{img("blob:old")}}
	w := NewChatWait(base, waitTiming(), t0)
	s := musepage.State{AgentTexts: []string{"old", "one", "two"}, Images: []musepage.Image{img("blob:old"), img("blob:new")}}
	if got := w.Reply(s); got != "one\n\ntwo" {
		t.Fatalf("reply %q", got)
	}
	if n := w.NewImages(s); len(n) != 1 || n[0].Src != "blob:new" {
		t.Fatalf("images %v", n)
	}
}

func TestWaitChatTimeoutExtendsWhileBusyUntilMax(t *testing.T) {
	tm := waitTiming()
	w := NewChatWait(musepage.State{}, tm, t0)
	if got := w.Step(musepage.State{Busy: true}, t0.Add(tm.ChatTimeout+time.Second)); got != Continue {
		t.Fatalf("busy past timeout: %v", got)
	}
	if got := w.Step(musepage.State{}, t0.Add(tm.ChatTimeout+2*time.Second)); got != TimedOut {
		t.Fatalf("idle past timeout: %v", got)
	}
	w2 := NewChatWait(musepage.State{}, tm, t0)
	if got := w2.Step(musepage.State{Busy: true}, t0.Add(tm.ChatMax)); got != TimedOut {
		t.Fatalf("at max: %v", got)
	}
}

func TestWaitResumeRestartsClock(t *testing.T) {
	tm := waitTiming()
	later := t0.Add(tm.ImageTimeout + time.Minute) // the owner took a while to approve
	iw := NewImageWait(musepage.State{}, tm, t0)
	iw.Resume(later)
	if got := iw.Step(musepage.State{AgentTexts: []string{"Uploaded."}}, later.Add(time.Second)); got != Continue {
		t.Fatalf("image wait after resume: %v", got)
	}
	cw := NewChatWait(musepage.State{}, tm, t0)
	cw.Resume(later)
	if got := cw.Step(musepage.State{}, later.Add(time.Second)); got != Continue {
		t.Fatalf("chat wait after resume: %v", got)
	}
}

func TestDefaultTiming(t *testing.T) {
	d := DefaultTiming()
	want := Timing{Poll: 1500 * time.Millisecond, Ready: 45 * time.Second, ImageTimeout: 4 * time.Minute,
		ImageMax: 10 * time.Minute, ImageSettle: 3 * time.Second, TextOnlyGrace: 10 * time.Second,
		ChatTimeout: 5 * time.Minute, ChatMax: 10 * time.Minute, ChatStable: 3 * time.Second,
		ChatForcePolls: 12, ApprovalTimeout: 10 * time.Minute, Unresponsive: 20 * time.Second}
	if d != want {
		t.Fatalf("got %+v", d)
	}
}
