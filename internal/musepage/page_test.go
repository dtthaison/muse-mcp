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

	"pp/muse-mcp/internal/browser"
	"pp/muse-mcp/internal/fakemuse"
	. "pp/muse-mcp/internal/musepage"
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
	id, ok := ThreadID("https://muse.ai/thread/ff97ae08-1c93-400d-9bc3-ba8040d7f6ba")
	if !ok || id != "ff97ae08-1c93-400d-9bc3-ba8040d7f6ba" {
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
