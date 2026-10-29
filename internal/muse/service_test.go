package muse

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/dtthaison/muse-mcp/internal/browser"
	"github.com/dtthaison/muse-mcp/internal/chats"
	"github.com/dtthaison/muse-mcp/internal/fakemuse"
	"github.com/dtthaison/muse-mcp/internal/musepage"
)

func fastTiming() Timing {
	return Timing{Poll: 50 * time.Millisecond, Ready: 15 * time.Second,
		ImageTimeout: 3 * time.Second, ImageMax: 6 * time.Second, ImageSettle: 300 * time.Millisecond,
		TextOnlyGrace: 500 * time.Millisecond, ChatTimeout: 3 * time.Second, ChatMax: 6 * time.Second,
		// ChatForcePolls is ~18s in production (12 × 1.5s); 100 × 50ms keeps a comparable window.
		ChatStable: 300 * time.Millisecond, ChatForcePolls: 100, ApprovalTimeout: 5 * time.Second,
		Unresponsive: time.Second}
}

type fixture struct {
	svc   *Service
	srv   *fakemuse.Server
	br    *browser.Browser
	chats *chats.Registry
	out   string
}

func newFixture(t *testing.T, tm Timing) *fixture {
	t.Helper()
	exe, err := browser.FindChrome()
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	br, err := browser.New(browser.Options{ExecPath: exe, ProfileDir: filepath.Join(dir, "profile"), LockPath: filepath.Join(dir, "lock")})
	if err != nil {
		t.Fatal(err)
	}
	srv := fakemuse.New()
	reg := chats.Load(filepath.Join(dir, "chats.json"))
	svc := New(Options{BaseURL: srv.URL, Browser: br, Chats: reg, Timing: tm,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	t.Cleanup(func() { svc.Close(); br.Close(); srv.Close() })
	return &fixture{svc: svc, srv: srv, br: br, chats: reg, out: filepath.Join(dir, "out")}
}

func needVisible(t *testing.T) {
	t.Helper()
	if os.Getenv("MUSE_TEST_VISIBLE") == "" {
		t.Skip("set MUSE_TEST_VISIBLE=1 to run (opens a Chrome window)")
	}
}

func refImage(t *testing.T, dir, name string) string {
	t.Helper()
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGenerateImageSavesFile(t *testing.T) {
	f := newFixture(t, fastTiming())
	res, err := f.svc.GenerateImage(context.Background(), ImageRequest{Text: "[fake:image] fox", OutputPath: filepath.Join(f.out, "fox.png")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "done" || len(res.Images) != 1 {
		t.Fatalf("result %+v", res)
	}
	im := res.Images[0]
	if im.Path != filepath.Join(f.out, "fox.png") || im.Width != 64 || im.Height != 48 {
		t.Fatalf("image %+v", im)
	}
	if !f.chats.Known(res.ChatID) || res.ChatURL != musepage.ThreadURL(f.srv.URL, res.ChatID) {
		t.Fatalf("chat %q url %q", res.ChatID, res.ChatURL)
	}
	if !strings.Contains(res.Reply, "Here is your image.") {
		t.Fatalf("reply %q", res.Reply)
	}
}

func TestChatAttachmentsOrder(t *testing.T) {
	f := newFixture(t, fastTiming())
	dir := t.TempDir()
	res, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:echo-attachments]",
		Attachments: []string{refImage(t, dir, "a.png"), refImage(t, dir, "b.png")}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reply != "files: a.png,b.png" {
		t.Fatalf("reply %q", res.Reply)
	}
}

func TestTextOnlyNudgeThenImage(t *testing.T) {
	f := newFixture(t, fastTiming())
	res, err := f.svc.GenerateImage(context.Background(), ImageRequest{Text: "[fake:textonly-then-image]", OutputPath: filepath.Join(f.out, "a.png")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "done" || len(res.Images) != 1 {
		t.Fatalf("result %+v", res)
	}
}

func TestTextOnlyRefused(t *testing.T) {
	f := newFixture(t, fastTiming())
	_, err := f.svc.GenerateImage(context.Background(), ImageRequest{Text: "[fake:textonly]", OutputPath: filepath.Join(f.out, "a.png")})
	var refused RefusedError
	if !errors.As(err, &refused) || !strings.Contains(refused.Reply, "I can't make that.") {
		t.Fatalf("err = %v", err)
	}
}

func TestApprovalApproveFlow(t *testing.T) {
	f := newFixture(t, fastTiming())
	ctx := context.Background()
	res, err := f.svc.GenerateImage(ctx, ImageRequest{Text: "[fake:approval]", OutputPath: filepath.Join(f.out, "a.png")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "needs_approval" || res.Approval == nil || !strings.Contains(res.Approval.Text, "Upload image to Page album") {
		t.Fatalf("result %+v", res)
	}
	if res.Approval.ChatID != res.ChatID || res.Approval.ID == "" {
		t.Fatalf("approval %+v", res.Approval)
	}

	// The global lock is released while the approval waits.
	other, err := f.svc.GenerateImage(ctx, ImageRequest{Text: "[fake:image]", OutputPath: filepath.Join(f.out, "b.png")})
	if err != nil || other.Status != "done" {
		t.Fatalf("concurrent request: %+v %v", other, err)
	}

	done, err := f.svc.Decide(ctx, res.ChatID, res.Approval.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "done" || len(done.Images) != 1 || !strings.Contains(done.Reply, "Uploaded.") {
		t.Fatalf("after approve %+v", done)
	}
	if done.Images[0].Path != filepath.Join(f.out, "a.png") {
		t.Fatalf("saved to %s", done.Images[0].Path)
	}
}

func TestApprovalBlocksSameChat(t *testing.T) {
	f := newFixture(t, fastTiming())
	res, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:approval]"})
	if err != nil || res.Status != "needs_approval" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := f.svc.Chat(context.Background(), ChatRequest{ChatID: res.ChatID, Message: "hi"}); !errors.Is(err, ErrAwaitingApproval) {
		t.Fatalf("err = %v", err)
	}
}

func TestApprovalTimeoutDenies(t *testing.T) {
	tm := fastTiming()
	tm.ApprovalTimeout = 700 * time.Millisecond
	f := newFixture(t, tm)
	res, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:approval]"})
	if err != nil || res.Status != "needs_approval" {
		t.Fatalf("%+v %v", res, err)
	}
	time.Sleep(2 * time.Second)
	if texts := f.svc.agentTexts(res.ChatID); len(texts) == 0 || texts[len(texts)-1] != "Okay, not uploading." {
		t.Fatalf("page after timeout: %v", texts)
	}
	if _, err := f.svc.Decide(context.Background(), res.ChatID, res.Approval.ID, true); !errors.Is(err, ErrApprovalTimedOut) {
		t.Fatalf("err = %v", err)
	}
	st, _ := f.svc.Status(context.Background())
	if len(st.Pending) != 0 {
		t.Fatalf("pending %+v", st.Pending)
	}
}

func TestDecideUnknown(t *testing.T) {
	f := newFixture(t, fastTiming())
	if _, err := f.svc.Decide(context.Background(), "nope", "x", true); !errors.Is(err, ErrNoSuchApproval) {
		t.Fatalf("err = %v", err)
	}
}

func TestOddApproveLabelStaysPending(t *testing.T) {
	f := newFixture(t, fastTiming())
	res, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:approval-oddlabel]"})
	if err != nil || res.Status != "needs_approval" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := f.svc.Decide(context.Background(), res.ChatID, res.Approval.ID, true); !errors.Is(err, musepage.ErrNoButton) {
		t.Fatalf("err = %v", err)
	}
	st, _ := f.svc.Status(context.Background())
	if len(st.Pending) != 1 || st.Pending[0].ID != res.Approval.ID {
		t.Fatalf("pending %+v", st.Pending)
	}
}

func TestChatContinue(t *testing.T) {
	f := newFixture(t, fastTiming())
	res, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:chat]"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "done" || res.Reply != "Hello from fake muse." {
		t.Fatalf("first %+v", res)
	}
	next, err := f.svc.Chat(context.Background(), ChatRequest{ChatID: res.ChatID, Message: "hi again"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Reply != "ok" || next.ChatID != res.ChatID {
		t.Fatalf("second %+v", next)
	}
}

func TestChatSavesImages(t *testing.T) {
	f := newFixture(t, fastTiming())
	res, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:image]", SaveImagesTo: f.out})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || res.Images[0].Path != filepath.Join(f.out, "muse-reply.png") {
		t.Fatalf("images %+v", res.Images)
	}
}

func TestChatUnknownID(t *testing.T) {
	f := newFixture(t, fastTiming())
	_, err := f.svc.Chat(context.Background(), ChatRequest{ChatID: "123e4567-e89b-42d3-a456-426614174000", Message: "hi"})
	if !errors.Is(err, ErrUnknownChat) {
		t.Fatalf("err = %v", err)
	}
}

func TestChatTimeoutReturnsPartial(t *testing.T) {
	tm := fastTiming()
	tm.ChatMax = time.Second
	f := newFixture(t, tm)
	res, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:slow]"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "timeout" || res.ChatID == "" {
		t.Fatalf("result %+v", res)
	}
}

func TestCancelReleasesMutex(t *testing.T) {
	f := newFixture(t, fastTiming())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := f.svc.GenerateImage(ctx, ImageRequest{Text: "[fake:slow]", OutputPath: filepath.Join(f.out, "a.png")})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	res, err := f.svc.GenerateImage(context.Background(), ImageRequest{Text: "[fake:image]", OutputPath: filepath.Join(f.out, "b.png")})
	if err != nil || res.Status != "done" {
		t.Fatalf("after cancel: %+v %v", res, err)
	}
	st, _ := f.svc.Status(context.Background())
	if len(st.Pending) != 0 {
		t.Fatalf("pending %+v", st.Pending)
	}
}

func TestLoggedOut(t *testing.T) {
	needVisible(t)
	f := newFixture(t, fastTiming())
	f.srv.SetLoggedOut(true)
	_, err := f.svc.GenerateImage(context.Background(), ImageRequest{Text: "[fake:image]", OutputPath: filepath.Join(f.out, "a.png")})
	if !errors.Is(err, ErrLoggedOut) {
		t.Fatalf("err = %v", err)
	}
	if st, _ := f.svc.Status(context.Background()); !st.Visible || st.LoggedIn {
		t.Fatalf("status %+v", st)
	}
	f.srv.SetLoggedOut(false) // the owner logs in
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st, _ := f.svc.Status(context.Background()); !st.Visible && st.LoggedIn {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	st, _ := f.svc.Status(context.Background())
	t.Fatalf("still visible after login: %+v", st)
}

func TestRelaunchDropsPending(t *testing.T) {
	needVisible(t)
	f := newFixture(t, fastTiming())
	res, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:approval]"})
	if err != nil || res.Status != "needs_approval" {
		t.Fatalf("%+v %v", res, err)
	}
	if err := f.br.SetVisible(true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Decide(context.Background(), res.ChatID, res.Approval.ID, true); !errors.Is(err, ErrNoSuchApproval) {
		t.Fatalf("err = %v", err)
	}
	if st, _ := f.svc.Status(context.Background()); len(st.Pending) != 0 {
		t.Fatalf("pending %+v", st.Pending)
	}
}

func TestApprovalIDNotReusedAfterTabReopens(t *testing.T) {
	tm := fastTiming()
	tm.ApprovalTimeout = 700 * time.Millisecond
	f := newFixture(t, tm)
	ctx := context.Background()
	first, err := f.svc.GenerateImage(ctx, ImageRequest{Text: "[fake:approval]", OutputPath: filepath.Join(f.out, "a.png")})
	if err != nil || first.Status != "needs_approval" {
		t.Fatalf("%+v %v", first, err)
	}
	time.Sleep(2 * time.Second) // times out; the image tab is closed
	next, err := f.svc.Chat(ctx, ChatRequest{ChatID: first.ChatID, Message: "[fake:approval]"})
	if err != nil || next.Status != "needs_approval" {
		t.Fatalf("%+v %v", next, err)
	}
	if next.Approval.ID == first.Approval.ID {
		t.Fatalf("approval ID %s reused", next.Approval.ID)
	}
	if _, err := f.svc.Decide(ctx, next.ChatID, next.Approval.ID, true); err != nil {
		t.Fatalf("deciding the new approval: %v", err)
	}
}

func TestClosedTabFailsFast(t *testing.T) {
	f := newFixture(t, fastTiming())
	errc := make(chan error, 1)
	go func() {
		_, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:slow]"})
		errc <- err
	}()
	var tb *tab
	for deadline := time.Now().Add(15 * time.Second); tb == nil && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		tb = f.svc.onlyTab()
	}
	if tb == nil {
		t.Fatal("chat never started")
	}
	c := chromedp.FromContext(tb.ctx)
	if _, err := cdp.Call(context.Background(), c.Browser, target.CloseTarget, target.CloseTargetParams{TargetID: c.Target.TargetID}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err == nil || !strings.Contains(err.Error(), "closed or stopped responding") {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still waiting on a closed tab")
	}
	res, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:chat]"})
	if err != nil || res.Status != "done" {
		t.Fatalf("after close: %+v %v", res, err)
	}
}

func TestStatusKeepsActiveTab(t *testing.T) {
	var skew atomic.Int64
	tm := fastTiming()
	tm.ChatMax = 4 * time.Second
	f := newFixture(t, tm)
	f.svc.o.Now = func() time.Time { return time.Now().Add(time.Duration(skew.Load())) }
	type out struct {
		res Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:slow]"})
		done <- out{res, err}
	}()
	for deadline := time.Now().Add(15 * time.Second); f.svc.onlyTab() == nil && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
	}
	skew.Store(int64(20 * time.Minute))
	if _, err := f.svc.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	o := <-done
	if o.err != nil || o.res.Status != "timeout" {
		t.Fatalf("Status dropped the tab in use: %+v %v", o.res, o.err)
	}
}

func TestContinueWithUntrackedApprovalRefuses(t *testing.T) {
	f := newFixture(t, fastTiming())
	ctx := context.Background()
	res, err := f.svc.Chat(ctx, ChatRequest{Message: "[fake:send-trap]"})
	if err != nil || res.Status != "needs_approval" {
		t.Fatalf("%+v %v", res, err)
	}
	f.svc.forgetPending(res.ChatID) // as if muse-mcp restarted
	_, err = f.svc.Chat(ctx, ChatRequest{ChatID: res.ChatID, Message: "hello"})
	if !errors.Is(err, ErrUntrackedApproval) || !strings.Contains(err.Error(), res.ChatURL) {
		t.Fatalf("err = %v", err)
	}
}

func TestGenerateVideoSavesFile(t *testing.T) {
	f := newFixture(t, fastTiming())
	res, err := f.svc.GenerateImage(context.Background(), ImageRequest{Text: "[fake:video] teapot", Media: MediaVideo,
		OutputPath: filepath.Join(f.out, "v.mp4")})
	if err != nil {
		t.Fatal(err)
	}
	// The fake records webm, so the file keeps its real extension.
	if res.Status != "done" || len(res.Images) != 0 || len(res.Videos) != 1 || res.Videos[0].Path != filepath.Join(f.out, "v.webm") {
		t.Fatalf("result %+v", res)
	}
	if res.Videos[0].Width != 64 || res.Videos[0].Bytes == 0 {
		t.Fatalf("video %+v", res.Videos[0])
	}
}

func TestVideoNudgeAsksForVideo(t *testing.T) {
	f := newFixture(t, fastTiming())
	res, err := f.svc.GenerateImage(context.Background(), ImageRequest{Text: "[fake:textonly-video]", Media: MediaVideo,
		OutputPath: filepath.Join(f.out, "v.webm")})
	if err != nil || len(res.Videos) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestChatSavesVideos(t *testing.T) {
	f := newFixture(t, fastTiming())
	res, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:video]", SaveImagesTo: f.out})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Videos) != 1 || res.Videos[0].Path != filepath.Join(f.out, "muse-reply.webm") {
		t.Fatalf("result %+v", res)
	}
}

func TestLoginWhenLoggedIn(t *testing.T) {
	f := newFixture(t, fastTiming())
	if err := f.svc.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.svc.Status(context.Background()); !st.LoggedIn || st.Visible {
		t.Fatalf("status %+v", st)
	}
}

func TestLoginWaitsForOwner(t *testing.T) {
	needVisible(t)
	f := newFixture(t, fastTiming())
	f.srv.SetLoggedOut(true)
	errc := make(chan error, 1)
	go func() { errc <- f.svc.Login(context.Background()) }()
	time.Sleep(3 * time.Second)
	select {
	case err := <-errc:
		t.Fatalf("returned before login: %v", err)
	default:
	}
	f.srv.SetLoggedOut(false) // the owner logs in
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Login did not notice the login")
	}
	if st, _ := f.svc.Status(context.Background()); !st.LoggedIn || st.Visible {
		t.Fatalf("status %+v", st)
	}
}

func TestGenerateVoiceSavesFile(t *testing.T) {
	f := newFixture(t, fastTiming())
	res, err := f.svc.GenerateImage(context.Background(), ImageRequest{Text: "[fake:voice] hello", Media: MediaVoice,
		OutputPath: filepath.Join(f.out, "hello.mp3")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "done" || res.Reply != "Here is your clip." || len(res.Audio) != 1 || res.Audio[0].Path != filepath.Join(f.out, "hello.mp3") {
		t.Fatalf("result %+v", res)
	}
	if data, _ := os.ReadFile(res.Audio[0].Path); !bytes.Contains(data, []byte("clip-0")) {
		t.Fatalf("file %q", data)
	}
}

func TestChatSavesAudio(t *testing.T) {
	f := newFixture(t, fastTiming())
	res, err := f.svc.Chat(context.Background(), ChatRequest{Message: "[fake:voices]", SaveImagesTo: f.out})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Audio) != 2 || res.Audio[0].Path != filepath.Join(f.out, "muse-reply.mp3") || res.Audio[1].Path != filepath.Join(f.out, "muse-reply-1.mp3") {
		t.Fatalf("result %+v", res)
	}
}
