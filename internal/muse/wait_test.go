package muse

import (
	"testing"
	"time"

	"github.com/dtthaison/muse-mcp/internal/musepage"
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
	if n := w.NewMedia(done); len(n) != 1 || n[0].Src != "blob:x" {
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
	if n := w.NewMedia(base); len(n) != 0 {
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

// muse can post a short message ("Checking the exact tool parameters…") and
// keep working with it unchanged for a long time; the reply isn't done while
// muse shows its Stop button.
func TestWaitChatKeepsWaitingBehindInterimMessage(t *testing.T) {
	tm := waitTiming()
	w := NewChatWait(musepage.State{}, tm, t0)
	interim := musepage.State{Busy: true, AgentTexts: []string{"Checking the exact tool parameters."}}
	for s := 0; s <= 90; s += 2 {
		if got := w.Step(interim, t0.Add(time.Duration(s)*time.Second)); got != Continue {
			t.Fatalf("busy behind an interim message at %ds: %v", s, got)
		}
	}
	answer := musepage.State{AgentTexts: []string{"Checking the exact tool parameters.", "Here is the list."}}
	w.Step(answer, t0.Add(92*time.Second))
	if got := w.Step(answer, t0.Add(92*time.Second+tm.ChatStable)); got != Done {
		t.Fatalf("after the answer: %v", got)
	}
	if got := w.Reply(answer); got != "Checking the exact tool parameters.\n\nHere is the list." {
		t.Fatalf("reply %q", got)
	}
}

// A Stop button that never goes away doesn't hold the reply until ChatMax.
func TestWaitChatDoneWhenStuckBusy(t *testing.T) {
	tm := waitTiming()
	w := NewChatWait(musepage.State{}, tm, t0)
	stuck := musepage.State{Busy: true, AgentTexts: []string{"Done."}}
	w.Step(stuck, t0)
	if got := w.Step(stuck, t0.Add(tm.ChatStuck-time.Second)); got != Continue {
		t.Fatalf("before ChatStuck: %v", got)
	}
	if got := w.Step(stuck, t0.Add(tm.ChatStuck)); got != Done {
		t.Fatalf("at ChatStuck: %v", got)
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
	if got := w.Step(musepage.State{}, t0.Add(tm.ChatTimeout+2*time.Second)); got != Continue {
		t.Fatalf("just went idle past timeout: %v", got)
	}
	if got := w.Step(musepage.State{}, t0.Add(tm.ChatTimeout+time.Second+2*tm.ChatStable)); got != TimedOut {
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
		ChatStuck: 2 * time.Minute, ApprovalTimeout: 10 * time.Minute, Unresponsive: 20 * time.Second, TitleWait: 10 * time.Second}
	if d != want {
		t.Fatalf("got %+v", d)
	}
}

func TestWaitVideoDoneOnVideoOnly(t *testing.T) {
	base := musepage.State{Videos: []musepage.Image{img("blob:old")}}
	w := NewVideoWait(base, waitTiming(), t0)
	withImage := musepage.State{AgentTexts: []string{"Here"}, Images: []musepage.Image{img("blob:i")}, Videos: base.Videos}
	if v := steps(w, repeat(withImage, 5)...); last(v) == Done {
		t.Fatalf("an image finished a video wait: %v", v)
	}
	w = NewVideoWait(base, waitTiming(), t0)
	done := musepage.State{AgentTexts: []string{"Here"}, Videos: []musepage.Image{img("blob:old"), img("blob:v")}}
	if v := steps(w, repeat(done, 4)...); last(v) != Done {
		t.Fatalf("verdicts %v", v)
	}
	if n := w.NewMedia(done); len(n) != 1 || n[0].Src != "blob:v" {
		t.Fatalf("new videos %v", n)
	}
}

func TestWaitChatNewVideos(t *testing.T) {
	w := NewChatWait(musepage.State{Videos: []musepage.Image{img("blob:old")}}, waitTiming(), t0)
	s := musepage.State{Videos: []musepage.Image{img("blob:old"), img("blob:v")}}
	if n := w.NewVideos(s); len(n) != 1 || n[0].Src != "blob:v" {
		t.Fatalf("new videos %v", n)
	}
}

func TestWaitVoiceDoneOnClip(t *testing.T) {
	w := NewVoiceWait(musepage.State{}, waitTiming(), t0)
	done := musepage.State{AgentTexts: []string{"Here"}, Audios: []musepage.Image{{Src: "audio:0"}}}
	if v := steps(w, repeat(done, 4)...); last(v) != Done {
		t.Fatalf("verdicts %v", v)
	}
	if n := w.NewMedia(done); len(n) != 1 || n[0].Src != "audio:0" {
		t.Fatalf("new clips %v", n)
	}
	cw := NewChatWait(musepage.State{Audios: []musepage.Image{{Src: "audio:0"}}}, waitTiming(), t0)
	if n := cw.NewAudios(musepage.State{Audios: []musepage.Image{{Src: "audio:0"}, {Src: "audio:1"}}}); len(n) != 1 || n[0].Src != "audio:1" {
		t.Fatalf("chat clips %v", n)
	}
}

// A job that runs past ImageTimeout still gets its settle time once muse stops
// (a voice clip that took muse nine minutes timed out the moment it finished).
func TestWaitLongJobSettlesAfterTimeout(t *testing.T) {
	tm := waitTiming()
	late := t0.Add(tm.ImageTimeout + 4*time.Minute)
	w := NewVoiceWait(musepage.State{}, tm, t0)
	w.Step(musepage.State{Busy: true}, late)
	done := musepage.State{AgentTexts: []string{"Your clip is ready"}, Audios: []musepage.Image{{Src: "audio:0"}}}
	if got := w.Step(done, late.Add(time.Second)); got != Continue {
		t.Fatalf("just finished: %v", got)
	}
	if got := w.Step(done, late.Add(time.Second+tm.ImageSettle)); got != Done {
		t.Fatalf("settled: %v", got)
	}

	idle := NewImageWait(musepage.State{}, tm, t0)
	idle.Step(musepage.State{Busy: true}, late)
	if got := idle.Step(musepage.State{}, late.Add(time.Second)); got != Continue {
		t.Fatalf("just went idle: %v", got)
	}
	if got := idle.Step(musepage.State{}, late.Add(tm.TextOnlyGrace+tm.ImageSettle)); got != TimedOut {
		t.Fatalf("idle with nothing: %v", got)
	}

	cw := NewChatWait(musepage.State{}, tm, t0)
	cw.Step(musepage.State{Busy: true, AgentTexts: []string{"Work"}}, t0.Add(tm.ChatTimeout+time.Minute))
	reply := musepage.State{AgentTexts: []string{"Working on it. Done."}}
	at := t0.Add(tm.ChatTimeout + time.Minute + time.Second)
	if got := cw.Step(reply, at); got != Continue {
		t.Fatalf("chat just finished: %v", got)
	}
	if got := cw.Step(reply, at.Add(tm.ChatStable)); got != Done {
		t.Fatalf("chat settled: %v", got)
	}
}
