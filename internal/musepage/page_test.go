package musepage_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/dtthaison/muse-mcp/internal/browser"
	"github.com/dtthaison/muse-mcp/internal/fakemuse"
	. "github.com/dtthaison/muse-mcp/internal/musepage"
)

// newTab starts a headless Chrome on a throwaway profile and returns a tab context.
func newTab(t *testing.T) context.Context {
	t.Helper()
	exe, err := browser.FindChrome()
	if err != nil {
		t.Skip(err)
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(exe), chromedp.UserDataDir(t.TempDir()))
	actx, cancelA := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(actx)
	t.Cleanup(func() { cancel(); cancelA() })
	return ctx
}

func openChat(t *testing.T) (context.Context, *fakemuse.Server) {
	t.Helper()
	ctx := newTab(t)
	srv := fakemuse.New()
	t.Cleanup(srv.Close)
	if _, err := Open(ctx, NewChatURL(srv.URL), 15*time.Second); err != nil {
		t.Fatal(err)
	}
	return ctx, srv
}

// until polls Snapshot until cond holds or 10s pass.
func until(t *testing.T, ctx context.Context, what string, cond func(State) bool) State {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var s State
	var err error
	for time.Now().Before(deadline) {
		s, err = Snapshot(ctx)
		if err == nil && cond(s) {
			return s
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; last state %+v err %v", what, s, err)
	return s
}

func lastText(s State) string {
	if len(s.AgentTexts) == 0 {
		return ""
	}
	return s.AgentTexts[len(s.AgentTexts)-1]
}

func TestThreadID(t *testing.T) {
	id, ok := ThreadID("https://muse.ai/thread/123e4567-e89b-42d3-a456-426614174000")
	if !ok || id != "123e4567-e89b-42d3-a456-426614174000" {
		t.Fatalf("got %q %v", id, ok)
	}
	for _, u := range []string{"https://muse.ai/thread/new", "https://muse.ai/", "not a url"} {
		if _, ok := ThreadID(u); ok {
			t.Errorf("%q accepted", u)
		}
	}
	if got := ThreadURL("https://muse.ai", "abc"); got != "https://muse.ai/thread/abc" {
		t.Fatalf("ThreadURL = %q", got)
	}
}

func TestOpenReady(t *testing.T) {
	ctx := newTab(t)
	srv := fakemuse.New()
	defer srv.Close()
	s, err := Open(ctx, NewChatURL(srv.URL), 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Ready || s.LoggedOut {
		t.Fatalf("state %+v", s)
	}
}

func TestOpenLoggedOut(t *testing.T) {
	ctx := newTab(t)
	srv := fakemuse.New()
	defer srv.Close()
	srv.SetLoggedOut(true)
	if _, err := Open(ctx, NewChatURL(srv.URL), 15*time.Second); !errors.Is(err, ErrLoggedOut) {
		t.Fatalf("err = %v", err)
	}
}

func TestSendImageFlow(t *testing.T) {
	ctx, _ := openChat(t)
	if err := Send(ctx, "[fake:image] a fox"); err != nil {
		t.Fatal(err)
	}
	s := until(t, ctx, "image", func(s State) bool { return len(s.Images) == 1 && !s.Busy })
	if _, ok := ThreadID(s.URL); !ok {
		t.Fatalf("no thread id in %q", s.URL)
	}
	if !strings.HasPrefix(s.Images[0].Src, "blob:") || lastText(s) != "Here is your image." {
		t.Fatalf("state %+v", s)
	}
	data, err := Download(ctx, s.Images[0].Src)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 64 || b.Dy() != 48 {
		t.Fatalf("size %v", b)
	}
}

func TestAttachKeepsOrder(t *testing.T) {
	ctx, _ := openChat(t)
	dir := t.TempDir()
	var files []string
	for _, n := range []string{"zz.png", "aa.png", "mm.png"} {
		p := filepath.Join(dir, n)
		var b bytes.Buffer
		png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
		if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}
	if err := Attach(ctx, files); err != nil {
		t.Fatal(err)
	}
	if err := Send(ctx, "[fake:echo-attachments]"); err != nil {
		t.Fatal(err)
	}
	s := until(t, ctx, "echo", func(s State) bool { return !s.Busy && len(s.AgentTexts) == 1 })
	if lastText(s) != "files: zz.png,aa.png,mm.png" {
		t.Fatalf("reply %q", lastText(s))
	}
}

func TestApprovalSnapshotAndApprove(t *testing.T) {
	ctx, _ := openChat(t)
	if err := Send(ctx, "[fake:approval]"); err != nil {
		t.Fatal(err)
	}
	s := until(t, ctx, "approval card", func(s State) bool { return s.Approval != nil })
	if !strings.Contains(s.Approval.Text, "Upload image to Page album") || !s.Busy {
		t.Fatalf("state %+v", s)
	}
	if err := Decide(ctx, true); err != nil {
		t.Fatal(err)
	}
	s = until(t, ctx, "after approve", func(s State) bool { return !s.Busy })
	if lastText(s) != "Uploaded." || s.Approval != nil {
		t.Fatalf("state %+v", s)
	}
}

func TestDeny(t *testing.T) {
	ctx, _ := openChat(t)
	if err := Send(ctx, "[fake:approval]"); err != nil {
		t.Fatal(err)
	}
	until(t, ctx, "approval card", func(s State) bool { return s.Approval != nil })
	if err := Decide(ctx, false); err != nil {
		t.Fatal(err)
	}
	s := until(t, ctx, "after deny", func(s State) bool { return !s.Busy })
	if lastText(s) != "Okay, not uploading." {
		t.Fatalf("reply %q", lastText(s))
	}
}

func TestOddApproveLabel(t *testing.T) {
	ctx, _ := openChat(t)
	if err := Send(ctx, "[fake:approval-oddlabel]"); err != nil {
		t.Fatal(err)
	}
	until(t, ctx, "approval card", func(s State) bool { return s.Approval != nil })
	if err := Decide(ctx, true); !errors.Is(err, ErrNoButton) {
		t.Fatalf("err = %v", err)
	}
	s, err := Snapshot(ctx)
	if err != nil || s.Approval == nil {
		t.Fatalf("card gone: %+v %v", s, err)
	}
}

// A "A task needs review" card has a button that is neither approve nor deny: Decide reports the
// approval card, whichever way it is asked, and presses nothing.
func TestReviewCardIsReportedAndNeverClicked(t *testing.T) {
	ctx, _ := openChat(t)
	if err := Send(ctx, "[fake:approval-review]"); err != nil {
		t.Fatal(err)
	}
	s := until(t, ctx, "approval card", func(s State) bool { return s.Approval != nil })
	if s.Approval.Text == "" || !strings.Contains(s.Approval.Text, "A task needs review") {
		t.Fatalf("card text %q", s.Approval.Text)
	}
	for _, approve := range []bool{true, false} {
		err := Decide(ctx, approve)
		if !errors.Is(err, ErrNoButton) || !strings.Contains(err.Error(), "approval card") {
			t.Fatalf("approve=%v: err = %v", approve, err)
		}
	}
	if clicks, err := chromedp.Run(ctx, chromedp.Evaluate[int](`window.cardClicks`)); err != nil || clicks != 0 {
		t.Fatalf("card buttons pressed %d times, err %v", clicks, err)
	}
	if s, err := Snapshot(ctx); err != nil || s.Approval == nil {
		t.Fatalf("card gone: %+v %v", s, err)
	}
}

// muse's "A task needs review" summary for a task in another chat is not this chat's approval:
// the chat's own result still arrives, and the summary is never reported or pressed.
func TestBackgroundReviewCardIgnored(t *testing.T) {
	ctx, _ := openChat(t)
	if err := Send(ctx, "[fake:background-review]"); err != nil {
		t.Fatal(err)
	}
	s := until(t, ctx, "image", func(s State) bool { return len(s.Images) > 0 })
	if s.Approval != nil {
		t.Fatalf("background card reported as an approval: %+v", s.Approval)
	}
	if n, err := chromedp.Run(ctx, chromedp.Evaluate[int](`document.querySelectorAll('[data-hatch-background-approval-surface]').length`)); err != nil || n != 1 {
		t.Fatalf("fake background card missing: %d %v", n, err)
	}
	if clicks, err := chromedp.Run(ctx, chromedp.Evaluate[int](`window.cardClicks`)); err != nil || clicks != 0 {
		t.Fatalf("card buttons pressed %d times, err %v", clicks, err)
	}
}

// While an approval waits, muse disables the composer's file input: Attach says so instead of
// waiting for chips that never come.
func TestAttachWhileComposerPaused(t *testing.T) {
	ctx, _ := openChat(t)
	if _, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`(document.querySelector('input[type=file]').disabled = true, true)`)); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(f, []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Attach(ctx, []string{f}); !errors.Is(err, ErrComposerPaused) {
		t.Fatalf("err = %v", err)
	}
}

// The page gets each file's exact bytes and a MIME type, an empty file included.
func TestAttachSendsBytes(t *testing.T) {
	ctx, _ := openChat(t)
	dir := t.TempDir()
	big := make([]byte, (512<<10)*2+123) // crosses chunk boundaries
	for i := range big {
		big[i] = byte(i * 7)
	}
	want := map[string][]byte{"big.bin": big, "empty.txt": {}, "pic.png": []byte("\x89PNG\r\n\x1a\nrest")}
	var files []string
	for _, n := range []string{"big.bin", "empty.txt", "pic.png"} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, want[n], 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}
	if err := Attach(ctx, files); err != nil {
		t.Fatal(err)
	}
	type got struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Size int    `json:"size"`
		Sum  uint32 `json:"sum"`
	}
	var list []got
	deadline := time.Now().Add(5 * time.Second)
	for len(list) < 3 && time.Now().Before(deadline) {
		l, err := chromedp.Run(ctx, chromedp.Evaluate[[]got](`window.attachedFiles`))
		if err != nil {
			t.Fatal(err)
		}
		list = l
		time.Sleep(100 * time.Millisecond)
	}
	if len(list) != 3 {
		t.Fatalf("page got %d files", len(list))
	}
	for i, g := range list {
		w := want[g.Name]
		var sum uint32
		for _, b := range w {
			sum = sum*31 + uint32(b)
		}
		if filepath.Base(files[i]) != g.Name || g.Size != len(w) || g.Sum != sum {
			t.Errorf("file %d: got %+v, want %s (%d bytes, sum %d)", i, g, filepath.Base(files[i]), len(w), sum)
		}
	}
	if list[2].Type != "image/png" || list[0].Type == "" {
		t.Errorf("types: %+v", list)
	}
}

// Send never presses a button inside muse's background summary card, even one labelled send.
func TestSendSkipsBackgroundCard(t *testing.T) {
	ctx, _ := openChat(t)
	js := `(() => {
	  const bg = document.createElement('div');
	  bg.setAttribute('data-testid', 'hatch-inline-approval-card');
	  bg.setAttribute('data-hatch-background-approval-surface', 'true');
	  bg.innerHTML = '<button aria-label="Send email">Send email</button>';
	  bg.querySelector('button').addEventListener('click', () => window.cardClicks++);
	  const ta = document.querySelector('textarea');
	  ta.parentElement.insertBefore(bg, ta.parentElement.firstChild);
	  return true;
	})()`
	if _, err := chromedp.Run(ctx, chromedp.Evaluate[bool](js)); err != nil {
		t.Fatal(err)
	}
	if err := Send(ctx, "[fake:image]"); err != nil {
		t.Fatal(err)
	}
	if clicks, err := chromedp.Run(ctx, chromedp.Evaluate[int](`window.cardClicks`)); err != nil || clicks != 0 {
		t.Fatalf("background card pressed %d times, err %v", clicks, err)
	}
}

func TestAttachTooBig(t *testing.T) {
	ctx, _ := openChat(t)
	p := filepath.Join(t.TempDir(), "huge.bin")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(100<<20 + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := Attach(ctx, []string{p}); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("err = %v", err)
	}
}

func TestDecidedCardNotPending(t *testing.T) {
	ctx, _ := openChat(t)
	if err := Send(ctx, "[fake:approval-sticky]"); err != nil {
		t.Fatal(err)
	}
	until(t, ctx, "approval card", func(s State) bool { return s.Approval != nil })
	if err := Decide(ctx, true); err != nil {
		t.Fatal(err)
	}
	s := until(t, ctx, "after approve", func(s State) bool { return !s.Busy })
	if s.Approval != nil || lastText(s) != "Uploaded." {
		t.Fatalf("a decided card still counts as pending: %+v", s)
	}
}

func TestSendIgnoresApprovalCardButtons(t *testing.T) {
	ctx, _ := openChat(t)
	if err := Send(ctx, "[fake:send-trap]"); err != nil {
		t.Fatal(err)
	}
	until(t, ctx, "approval card", func(s State) bool { return s.Approval != nil })
	err := Send(ctx, "hello")
	time.Sleep(500 * time.Millisecond)
	s, _ := Snapshot(ctx)
	for _, txt := range s.AgentTexts {
		if txt == "SENT EMAIL" {
			t.Fatal("Send clicked the approval card's button")
		}
	}
	if err != nil || s.Approval == nil {
		t.Fatalf("err=%v approval=%v", err, s.Approval)
	}
}

func TestVideoResult(t *testing.T) {
	ctx, _ := openChat(t)
	if err := Send(ctx, "[fake:video]"); err != nil {
		t.Fatal(err)
	}
	s := until(t, ctx, "a loaded video", func(s State) bool {
		return !s.Busy && len(s.Videos) == 1 && strings.HasPrefix(s.Videos[0].Src, "blob:") && s.Videos[0].Width > 0
	})
	if len(s.Images) != 0 || s.Videos[0].Width != 64 || s.Videos[0].Height != 48 {
		t.Fatalf("state %+v", s)
	}
	data, err := Download(ctx, s.Videos[0].Src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("\x1a\x45\xdf\xa3")) {
		t.Fatalf("not a webm: % x", data[:min(8, len(data))])
	}
}

func TestDecorativeVideosIgnored(t *testing.T) {
	ctx, _ := openChat(t)
	if err := Send(ctx, "[fake:decor-video]"); err != nil {
		t.Fatal(err)
	}
	s := until(t, ctx, "reply", func(s State) bool { return !s.Busy && len(s.AgentTexts) > 0 })
	if len(s.Videos) != 0 {
		t.Fatalf("a video outside a chat attachment counted: %+v", s.Videos)
	}
}

func TestVoiceClip(t *testing.T) {
	ctx, _ := openChat(t)
	if err := Send(ctx, "[fake:voice]"); err != nil {
		t.Fatal(err)
	}
	s := until(t, ctx, "an audio clip", func(s State) bool { return !s.Busy && len(s.Audios) == 1 })
	// The player's "0:00" sits in a bubble-styled box; it is not muse's text.
	if len(s.AgentTexts) != 1 || s.AgentTexts[0] != "Here is your clip." {
		t.Fatalf("agent texts %q", s.AgentTexts)
	}
	data, err := Download(ctx, s.Audios[0].Src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("ID3")) || !bytes.Contains(data, []byte("clip-0")) {
		t.Fatalf("data %q", data)
	}
}

func TestChatTitle(t *testing.T) {
	ctx, _ := openChat(t)
	if s, _ := Snapshot(ctx); s.Title != "" {
		t.Fatalf("placeholder title %q, want empty", s.Title)
	}
	if err := Send(ctx, "[fake:voice]"); err != nil {
		t.Fatal(err)
	}
	until(t, ctx, "the chat title", func(s State) bool { return s.Title == "Fake voice clip" })
}

func TestVoiceClipsDownloadedSeparately(t *testing.T) {
	ctx, _ := openChat(t)
	if err := Send(ctx, "[fake:voices]"); err != nil {
		t.Fatal(err)
	}
	s := until(t, ctx, "two audio clips", func(s State) bool { return !s.Busy && len(s.Audios) == 2 })
	for i, want := range []string{"clip-0", "clip-1"} {
		data, err := Download(ctx, s.Audios[i].Src)
		if err != nil || !bytes.Contains(data, []byte(want)) {
			t.Fatalf("clip %d: %q %v", i, data, err)
		}
	}
}

// startChat sends a message so the fake page moves to /thread/<id>.
func startChat(t *testing.T, ctx context.Context) string {
	t.Helper()
	if err := Send(ctx, "[fake:chat]"); err != nil {
		t.Fatal(err)
	}
	s := until(t, ctx, "reply", func(s State) bool { return !s.Busy && lastText(s) == "Hello from fake muse." })
	id, ok := ThreadID(s.URL)
	if !ok {
		t.Fatalf("no chat id in %s", s.URL)
	}
	return id
}

func TestDeleteOpenChatConfirms(t *testing.T) {
	ctx, srv := openChat(t)
	id := startChat(t, ctx)
	if err := DeleteOpenChat(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := srv.Deleted(); len(got) != 1 || got[0] != id {
		t.Fatalf("deleted %v, want [%s]", got, id)
	}
}

func TestDeleteOpenChatWithoutConfirmation(t *testing.T) {
	ctx, srv := openChat(t)
	srv.SetDeleteConfirm(false)
	id := startChat(t, ctx)
	if err := DeleteOpenChat(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := srv.Deleted(); len(got) != 1 || got[0] != id {
		t.Fatalf("deleted %v, want [%s]", got, id)
	}
}

func TestDeleteRefusesAnotherChat(t *testing.T) {
	ctx, srv := openChat(t)
	startChat(t, ctx)
	if err := DeleteOpenChat(ctx, "123e4567-e89b-42d3-a456-426614174000"); err == nil {
		t.Fatal("deleted while a different chat was open")
	}
	if got := srv.Deleted(); len(got) != 0 {
		t.Fatalf("deleted %v", got)
	}
}

// A chat muse can't open (deleted already, or broken) shows "Something went
// wrong" and no chat switcher.
func TestDeleteUnavailableChat(t *testing.T) {
	ctx, srv := openChat(t)
	if _, err := Open(ctx, ThreadURL(srv.URL, fakemuse.BrokenChat), 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := DeleteOpenChat(ctx, fakemuse.BrokenChat); !errors.Is(err, ErrChatUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if got := srv.Deleted(); len(got) != 0 {
		t.Fatalf("deleted %v", got)
	}
}

// The login steps are told apart, the email goes in through real input events
// (the fake's Continue stays disabled until it sees one), and "Continue with
// Google" is never taken for Continue.
func TestLoginSteps(t *testing.T) {
	ctx := newTab(t)
	srv := fakemuse.New()
	t.Cleanup(srv.Close)
	srv.SetLoginFlow("246810")
	if err := chromedp.Do(ctx, chromedp.Navigate(srv.URL)); err != nil {
		t.Fatal(err)
	}
	step := func(want LoginStep) LoginPage {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		var p LoginPage
		for time.Now().Before(deadline) {
			var err error
			if p, err = LoginSnapshot(ctx); err == nil && p.Step == want {
				return p
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("waiting for step %q; last %+v", want, p)
		return p
	}
	p := step(StepEmail)
	if lines := p.Lines(8); len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "Log in or create an account") {
		t.Fatalf("lines %q", lines)
	}
	if ok, _ := ClickButton(ctx, LabelContinue, 300*time.Millisecond); ok {
		t.Fatal("clicked a disabled Continue")
	}
	if err := TypeEmail(ctx, "a@b.co"); err != nil {
		t.Fatal(err)
	}
	if ok, err := ClickButton(ctx, LabelContinue, 3*time.Second); err != nil || !ok {
		t.Fatalf("Continue: %v %v", ok, err)
	}
	step(StepCode)
	if srv.LoginEmail() != "a@b.co" {
		t.Fatalf("email %q", srv.LoginEmail())
	}
	if err := TypeCode(ctx, "111111"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ClickButton(ctx, LabelSubmitCode, 3*time.Second); !ok {
		t.Fatal("no submit button")
	}
	until2 := time.Now().Add(10 * time.Second)
	for time.Now().Before(until2) {
		if p, err := LoginSnapshot(ctx); err == nil && p.WrongCode {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if p, _ := LoginSnapshot(ctx); !p.WrongCode || p.Step != StepCode {
		t.Fatalf("after a wrong code: %+v", p)
	}
	if err := TypeCode(ctx, "246810"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ClickButton(ctx, LabelSubmitCode, 3*time.Second); !ok {
		t.Fatal("no submit button")
	}
	step(StepLoggedIn)
}
