package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtthaison/muse-mcp/internal/chats"
	"github.com/dtthaison/muse-mcp/internal/library"
	"github.com/dtthaison/muse-mcp/internal/muse"
	"github.com/dtthaison/muse-mcp/internal/output"
	"github.com/dtthaison/muse-mcp/internal/voices"
	"github.com/dtthaison/muse-mcp/skills"
)

type decision struct {
	chatID, approvalID string
	approve            bool
}

type fakeService struct {
	images    []muse.ImageRequest
	chats     []muse.ChatRequest
	decisions []decision
	logins    int
	result    muse.Result // returned by GenerateImage and Chat
	closed    bool
	deleted   []string
}

func (f *fakeService) DeleteChat(_ context.Context, id string) error {
	if id == "theirs" {
		return muse.ErrUnknownChat
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeService) ListChats() []chats.Chat { return nil }

func (f *fakeService) GenerateImage(_ context.Context, r muse.ImageRequest) (muse.Result, error) {
	f.images = append(f.images, r)
	return f.result, nil
}

func (f *fakeService) Chat(_ context.Context, r muse.ChatRequest) (muse.Result, error) {
	f.chats = append(f.chats, r)
	return f.result, nil
}

func (f *fakeService) Decide(_ context.Context, chatID, approvalID string, approve bool) (muse.Result, error) {
	f.decisions = append(f.decisions, decision{chatID, approvalID, approve})
	return muse.Result{Status: "done", ChatID: chatID, Reply: "decided"}, nil
}

func (f *fakeService) Status(context.Context) (muse.StatusInfo, error) {
	return muse.StatusInfo{ChromeRunning: true, LoggedIn: true, TrackedChats: 2, Pending: []muse.PendingApproval{}}, nil
}

func (f *fakeService) Login(context.Context) error {
	f.logins++
	return nil
}

type harness struct {
	svc         *fakeService
	lib         *library.Store
	voices      *voices.Catalog
	out, errOut bytes.Buffer
	claude      [][]string
	served      bool
	home, dir   string
	claudeErr   func(args []string) error
	chats       *chats.Registry
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{svc: &fakeService{result: muse.Result{Status: "done", ChatID: "c1", ChatURL: "https://muse.ai/thread/c1", Reply: "Here you go."}},
		lib: library.Open(filepath.Join(t.TempDir(), "library")), voices: voices.Open(filepath.Join(t.TempDir(), "voices.csv")),
		home: t.TempDir(), dir: t.TempDir(), chats: chats.Load(filepath.Join(t.TempDir(), "chats.json"))}
}

func (h *harness) run(stdin string, interactive bool, args ...string) int {
	h.out.Reset()
	h.errOut.Reset()
	return Run(context.Background(), args, Env{
		Stdin: strings.NewReader(stdin), Stdout: &h.out, Stderr: &h.errOut, Interactive: interactive,
		Library: h.lib, Voices: h.voices, Chats: h.chats,
		Muse: func() (Service, func(), error) {
			return h.svc, func() { h.svc.closed = true }, nil
		},
		Serve: func(context.Context) error { h.served = true; return nil },
		Claude: func(_ context.Context, args ...string) error {
			h.claude = append(h.claude, args)
			if h.claudeErr != nil {
				return h.claudeErr(args)
			}
			return nil
		},
		Exe:  "/opt/bin/muse-mcp",
		Home: h.home,
		Dir:  h.dir,
	})
}

func pngFile(t *testing.T, dir, name string) string {
	t.Helper()
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNoArgsServesWhenPiped(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", false); code != 0 || !h.served {
		t.Fatalf("code=%d served=%v", code, h.served)
	}
}

func TestNoArgsInTerminalShowsUsage(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true); code != 2 || h.served || !strings.Contains(h.errOut.String(), "muse-mcp image") {
		t.Fatalf("code=%d served=%v stderr=%q", code, h.served, h.errOut.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "paint"); code != 2 || !strings.Contains(h.errOut.String(), `unknown command "paint"`) {
		t.Fatalf("code=%d stderr=%q", code, h.errOut.String())
	}
}

func TestImage(t *testing.T) {
	h := newHarness(t)
	ref := pngFile(t, t.TempDir(), "r.png")
	h.svc.result.Images = []output.Saved{{Path: "/w/fox.png", Width: 1920, Height: 1280, Bytes: 400 << 10}}
	// Flags may come before or after the prompt.
	code := h.run("", true, "image", "-o", "fox.png", "a red fox", "in snow", "--ref", ref, "--aspect", "16:9")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.errOut.String())
	}
	got := h.svc.images[0]
	if got.Media != muse.MediaImage || got.OutputPath != "fox.png" || !strings.Contains(got.Text, "Generate an image: a red fox in snow Aspect ratio 16:9.") ||
		len(got.Attachments) != 1 || got.Attachments[0] != ref {
		t.Fatalf("request %+v", got)
	}
	if out := h.out.String(); !strings.Contains(out, "/w/fox.png (1920x1280") || !strings.Contains(out, "Here you go.") {
		t.Fatalf("stdout %q", out)
	}
	if !h.svc.closed {
		t.Fatal("muse not closed")
	}
}

func TestImageWithSavedReferences(t *testing.T) {
	h := newHarness(t)
	if _, err := h.lib.Save("fox", library.Character, "a red fox", []string{pngFile(t, t.TempDir(), "f.png")}, false); err != nil {
		t.Fatal(err)
	}
	if code := h.run("", true, "image", "-o", "x.png", "--character", "fox", "--change", "asleep"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.errOut.String())
	}
	if got := h.svc.images[0].Text; !strings.Contains(got, `character reference "fox"`) || !strings.Contains(got, "Change ONLY: asleep") {
		t.Fatalf("text %q", got)
	}
}

func TestImageValidatesBeforeMuse(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "image", "-o", "x.gif", "a fox"); code != 1 || len(h.svc.images) != 0 {
		t.Fatalf("code=%d calls=%d", code, len(h.svc.images))
	}
	if code := h.run("", true, "image", "a fox"); code != 2 || !strings.Contains(h.errOut.String(), "-o") {
		t.Fatalf("missing -o: code=%d stderr=%q", code, h.errOut.String())
	}
}

func TestVideo(t *testing.T) {
	h := newHarness(t)
	h.svc.result.Videos = []output.Saved{{Path: "/w/v.webm", Width: 1920, Height: 1280, Bytes: 2 << 20}}
	if code := h.run("", true, "video", "-o", "v.mp4", "steam rising"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.errOut.String())
	}
	if got := h.svc.images[0]; got.Media != muse.MediaVideo || !strings.Contains(got.Text, "Generate a video: steam rising") {
		t.Fatalf("request %+v", got)
	}
	if !strings.Contains(h.out.String(), "/w/v.webm") || !strings.Contains(h.errOut.String(), "experimental") {
		t.Fatalf("stdout %q stderr %q", h.out.String(), h.errOut.String())
	}
}

func TestChat(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	a := pngFile(t, dir, "a.png")
	if code := h.run("", true, "chat", "--chat", "c1", "--attach", a, "--save", dir, "hello", "muse"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.errOut.String())
	}
	got := h.svc.chats[0]
	if got.Message != "hello muse" || got.ChatID != "c1" || got.SaveImagesTo != dir || len(got.Attachments) != 1 {
		t.Fatalf("request %+v", got)
	}
	if out := h.out.String(); !strings.Contains(out, "Here you go.") || !strings.Contains(out, "c1") {
		t.Fatalf("stdout %q", out)
	}
}

func TestJSONOutput(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "chat", "--json", "hi"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	var r muse.Result
	if err := json.Unmarshal(h.out.Bytes(), &r); err != nil || r.Reply != "Here you go." {
		t.Fatalf("stdout %q (%v)", h.out.String(), err)
	}
}

func approvalResult() muse.Result {
	return muse.Result{Status: "needs_approval", ChatID: "c1", ChatURL: "https://muse.ai/thread/c1",
		Approval: &muse.PendingApproval{ChatID: "c1", ID: "c1-1", Text: "Post to Facebook"}}
}

func TestApprovalAskedInTerminal(t *testing.T) {
	for _, tc := range []struct {
		answer  string
		approve bool
	}{{"y\n", true}, {"yes\n", true}, {"n\n", false}, {"\n", false}, {"", false}} {
		h := newHarness(t)
		h.svc.result = approvalResult()
		if code := h.run(tc.answer, true, "chat", "post it"); code != 0 {
			t.Fatalf("%q: code=%d stderr=%q", tc.answer, code, h.errOut.String())
		}
		if len(h.svc.decisions) != 1 || h.svc.decisions[0] != (decision{"c1", "c1-1", tc.approve}) {
			t.Fatalf("%q: decisions %+v", tc.answer, h.svc.decisions)
		}
		if !strings.Contains(h.errOut.String(), "Post to Facebook") {
			t.Fatalf("%q: stderr %q", tc.answer, h.errOut.String())
		}
	}
}

func TestApprovalNeverDecidedWithoutTerminal(t *testing.T) {
	h := newHarness(t)
	h.svc.result = approvalResult()
	if code := h.run("y\n", false, "chat", "post it"); code != 3 || len(h.svc.decisions) != 0 {
		t.Fatalf("code=%d decisions=%+v", code, h.svc.decisions)
	}
	if !strings.Contains(h.errOut.String(), "https://muse.ai/thread/c1") {
		t.Fatalf("stderr %q", h.errOut.String())
	}
}

func TestTimeoutExitCode(t *testing.T) {
	h := newHarness(t)
	h.svc.result = muse.Result{Status: "timeout", ChatID: "c1", ChatURL: "https://muse.ai/thread/c1"}
	if code := h.run("", true, "image", "-o", "x.png", "fox"); code != 1 || !strings.Contains(h.errOut.String(), "timed out") {
		t.Fatalf("code=%d stderr=%q", code, h.errOut.String())
	}
}

func TestLoginAndStatus(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "login"); code != 0 || h.svc.logins != 1 {
		t.Fatalf("code=%d logins=%d", code, h.svc.logins)
	}
	if code := h.run("", true, "status"); code != 0 || !strings.Contains(h.out.String(), "logged in: yes") {
		t.Fatalf("code=%d stdout=%q", code, h.out.String())
	}
}

func TestInstallAndUninstall(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "install"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.errOut.String())
	}
	if code := h.run("", true, "install", "--scope", "project"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if code := h.run("", true, "uninstall"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	want := []string{
		"mcp remove -s user muse", // install replaces an earlier install
		"mcp add -s user muse -- /opt/bin/muse-mcp",
		"mcp remove -s project muse",
		"mcp add -s project muse -- /opt/bin/muse-mcp",
		"mcp remove -s user muse",
	}
	for i, w := range want {
		if got := strings.Join(h.claude[i], " "); got != w {
			t.Fatalf("claude call %d: %q, want %q", i, got, w)
		}
	}
}

func TestRefs(t *testing.T) {
	h := newHarness(t)
	img := pngFile(t, t.TempDir(), "f.png")
	if code := h.run("", true, "refs", "save", "fox", "--kind", "character", "--desc", "a red fox", img); code != 0 {
		t.Fatalf("save: code=%d stderr=%q", code, h.errOut.String())
	}
	if code := h.run("", true, "refs", "list"); code != 0 || !strings.Contains(h.out.String(), "fox (character): a red fox") {
		t.Fatalf("list: code=%d stdout=%q", code, h.out.String())
	}
	if code := h.run("", true, "refs", "delete", "fox"); code != 0 {
		t.Fatalf("delete: code=%d", code)
	}
	if refs, _ := h.lib.List(); len(refs) != 0 {
		t.Fatalf("refs %+v", refs)
	}
}

func TestReferenceCommandsDontStartMuse(t *testing.T) {
	h := newHarness(t)
	started := false
	code := Run(context.Background(), []string{"refs", "list"}, Env{Stdout: &h.out, Stderr: &h.errOut, Library: h.lib,
		Muse: func() (Service, func(), error) { started = true; return h.svc, func() {}, nil }})
	if code != 0 || started {
		t.Fatalf("code=%d started=%v", code, started)
	}
}

type coldService struct{ fakeService }

func (c *coldService) Status(context.Context) (muse.StatusInfo, error) {
	return muse.StatusInfo{Pending: []muse.PendingApproval{}}, nil
}

func TestStatusBeforeChromeStarts(t *testing.T) {
	h := newHarness(t)
	var out bytes.Buffer
	code := Run(context.Background(), []string{"status"}, Env{Stdout: &out, Stderr: &h.errOut, Library: h.lib,
		Muse: func() (Service, func(), error) { return &coldService{}, func() {}, nil }})
	if code != 0 || !strings.Contains(out.String(), "logged in: unknown") {
		t.Fatalf("code=%d stdout=%q", code, out.String())
	}
}

func TestImageEdit(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	photo, mood := pngFile(t, dir, "bag.png"), pngFile(t, dir, "mood.png")
	code := h.run("", true, "image", "-o", "bag.png", "--edit", photo, "--change", "a cream background",
		"--keep", "the stitches", "--ref", mood, "--role", "lighting mood only", "--count", "2")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.errOut.String())
	}
	got := h.svc.images[0].Text
	for _, want := range []string{"Image 1 is the photo to edit.", "Image 2 is lighting mood only.", "Keep exactly: the stitches.", "Make 2 separate versions"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestVoice(t *testing.T) {
	h := newHarness(t)
	h.svc.result.Audio = []output.Saved{{Path: "/w/c1-Hello.mp3", Bytes: 16 << 10}}
	if code := h.run("", true, "speech", "-o", "clips", "--voice", "Helio", "--speed", "85", "Xin chào", "bạn"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.errOut.String())
	}
	got := h.svc.images[0]
	if got.Media != muse.MediaVoice || got.OutputPath != "clips" || !strings.Contains(got.Text, `Voice: "Helio"`) || !strings.Contains(got.Text, "Xin chào bạn") {
		t.Fatalf("request %+v", got)
	}
	if !strings.Contains(h.out.String(), "saved /w/c1-Hello.mp3 (16 KB)") {
		t.Fatalf("stdout %q", h.out.String())
	}
}

func TestVoiceDefaultsToCurrentFolder(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "speech", "Xin chào"); code != 0 || h.svc.images[0].OutputPath != "." {
		t.Fatalf("code=%d stderr=%q request %+v", code, h.errOut.String(), h.svc.images)
	}
	if code := h.run("", true, "speech", "-o", "hi.mp3", "Xin chào"); code != 1 || !strings.Contains(h.errOut.String(), "folder") {
		t.Fatalf("file name: code=%d stderr=%q", code, h.errOut.String())
	}
}

func TestVoices(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "voices"); code != 1 || !strings.Contains(h.errOut.String(), "voices import") {
		t.Fatalf("no catalog: code=%d stderr=%q", code, h.errOut.String())
	}
	src := filepath.Join(t.TempDir(), "voice-list.csv")
	os.WriteFile(src, []byte("name,gender,accent,tone,description\nAria,F,American,Warm,Friendly\nLan,F,Vietnamese,Bright,Young\nHelio,M,American,Smooth,Calm\n"), 0o600)
	if code := h.run("", true, "voices", "import", src); code != 0 || !strings.Contains(h.out.String(), "3 voices") {
		t.Fatalf("import: code=%d stdout=%q stderr=%q", code, h.out.String(), h.errOut.String())
	}
	if code := h.run("", true, "voices", "--gender", "female", "--accent", "vietnamese"); code != 0 ||
		!strings.Contains(h.out.String(), "Lan (F, Vietnamese, Bright): Young") || strings.Contains(h.out.String(), "Aria") {
		t.Fatalf("search: code=%d stdout=%q", code, h.out.String())
	}
}

func TestVoicesAllAndAccents(t *testing.T) {
	h := newHarness(t)
	var csv strings.Builder
	csv.WriteString("name,gender,accent,tone,description\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&csv, "V%d,F,British,Warm,x\n", i)
	}
	csv.WriteString("Lan,M,Vietnamese,Bright,y\n")
	src := filepath.Join(t.TempDir(), "v.csv")
	os.WriteFile(src, []byte(csv.String()), 0o600)
	if code := h.run("", true, "voices", "import", src); code != 0 {
		t.Fatal(h.errOut.String())
	}
	if code := h.run("", true, "voices", "--all"); code != 0 || strings.Count(h.out.String(), "\n") != 31 || strings.Contains(h.out.String(), "more") {
		t.Fatalf("all: code=%d stdout=%q", code, h.out.String())
	}
	if code := h.run("", true, "voices", "accents"); code != 0 ||
		!strings.Contains(h.out.String(), "British") || !strings.Contains(h.out.String(), "30") ||
		strings.Index(h.out.String(), "British") > strings.Index(h.out.String(), "Vietnamese") {
		t.Fatalf("accents: code=%d stdout=%q", code, h.out.String())
	}
}

func TestVoiceDirectionAndWarnings(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "speech", "--language", "Vietnamese (vi-VN)", "--speed", "120",
		"Xin chào các bạn"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.errOut.String())
	}
	if got := h.svc.images[0].Text; !strings.Contains(got, "Language: Vietnamese (vi-VN)") || !strings.Contains(got, "Speed: 120 ") {
		t.Fatalf("text %q", got)
	}
	if code := h.run("", true, "speech", "Giá 199.000đ"); code != 1 || len(h.svc.images) != 1 ||
		!strings.Contains(h.errOut.String(), "digits") || !strings.Contains(h.errOut.String(), "--force") {
		t.Fatalf("code=%d calls=%d stderr=%q", code, len(h.svc.images), h.errOut.String())
	}
	if code := h.run("", true, "speech", "--force", "Giá 199.000đ"); code != 0 || len(h.svc.images) != 2 {
		t.Fatalf("--force: code=%d stderr=%q", code, h.errOut.String())
	}
}

func TestInstallIgnoresNothingToRemove(t *testing.T) {
	h := newHarness(t)
	h.claudeErr = func(args []string) error {
		if args[1] == "remove" {
			return errors.New("no MCP server found")
		}
		return nil
	}
	if code := h.run("", true, "install"); code != 0 || len(h.claude) != 2 {
		t.Fatalf("code=%d calls=%v stderr=%q", code, h.claude, h.errOut.String())
	}
}

func TestInstallCopiesSkills(t *testing.T) {
	h := newHarness(t)
	skillDir := filepath.Join(h.home, ".claude", "skills", "muse")
	projDir := filepath.Join(h.dir, ".claude", "skills", "muse")
	// An older muse-mcp installed the voice skill as muse-voice.
	old := filepath.Join(h.home, ".claude", "skills", "muse-voice", "SKILL.md")
	os.MkdirAll(filepath.Dir(old), 0o755)
	os.WriteFile(old, []byte("old"), 0o644)

	if code := h.run("", true, "install"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.errOut.String())
	}
	for _, f := range []string{"SKILL.md", "speech.md", "image.md", "video.md"} {
		want, err := fs.ReadFile(skills.FS, "muse/"+f)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(filepath.Join(skillDir, f)); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Dir(old)); !os.IsNotExist(err) {
		t.Fatalf("old muse-voice skill still there: %v", err)
	}
	if !strings.Contains(h.out.String(), "skill muse") {
		t.Fatalf("stdout %q", h.out.String())
	}
	if code := h.run("", true, "install", "--scope", "project"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if _, err := os.Stat(filepath.Join(projDir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	// Uninstall removes only what install wrote.
	extra := filepath.Join(skillDir, "notes.md")
	os.WriteFile(extra, []byte("mine"), 0o600)
	if code := h.run("", true, "uninstall"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	for _, f := range []string{"SKILL.md", "speech.md", "image.md", "video.md"} {
		if _, err := os.Stat(filepath.Join(skillDir, f)); !os.IsNotExist(err) {
			t.Fatalf("%s still there: %v", f, err)
		}
	}
	if _, err := os.Stat(extra); err != nil {
		t.Fatalf("uninstall removed the owner's file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projDir, "SKILL.md")); err != nil {
		t.Fatalf("project skill removed by a user-scope uninstall: %v", err)
	}
}

func TestChatsList(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "chats"); code != 0 || !strings.Contains(h.out.String(), "No chats") {
		t.Fatalf("code %d out %q", code, h.out.String())
	}
	for _, id := range []string{"aaa", "bbb"} {
		if err := h.chats.Add(id); err != nil {
			t.Fatal(err)
		}
	}
	if code := h.run("", true, "chats"); code != 0 {
		t.Fatalf("code %d: %s", code, h.errOut.String())
	}
	out := h.out.String()
	if !strings.Contains(out, "aaa") || !strings.Contains(out, "bbb") || strings.Index(out, "aaa") > strings.Index(out, "bbb") {
		t.Fatalf("out %q", out)
	}
	if h.svc.closed {
		t.Fatal("listing chats started Chrome")
	}
}

func TestChatsDelete(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "chats", "delete", "aaa", "bbb"); code != 0 {
		t.Fatalf("code %d: %s", code, h.errOut.String())
	}
	if len(h.svc.deleted) != 2 || !strings.Contains(h.out.String(), "Deleted chat bbb") {
		t.Fatalf("deleted %v out %q", h.svc.deleted, h.out.String())
	}
	if code := h.run("", true, "chats", "delete", "theirs"); code != 1 || !strings.Contains(h.errOut.String(), "only continues or deletes") {
		t.Fatalf("code %d err %q", code, h.errOut.String())
	}
	if code := h.run("", true, "chats", "delete"); code != 2 {
		t.Fatalf("no ids: code %d", code)
	}
}

func TestChatsForget(t *testing.T) {
	h := newHarness(t)
	for _, id := range []string{"aaa", "bbb"} {
		if err := h.chats.Add(id); err != nil {
			t.Fatal(err)
		}
	}
	if code := h.run("", true, "chats", "forget", "aaa", "zzz"); code != 0 {
		t.Fatalf("code %d: %s", code, h.errOut.String())
	}
	if h.chats.Known("aaa") || !h.chats.Known("bbb") || !strings.Contains(h.out.String(), "Forgot chat aaa") ||
		!strings.Contains(h.out.String(), "zzz is not in the list") || h.svc.closed {
		t.Fatalf("out %q closed=%v", h.out.String(), h.svc.closed)
	}
}
