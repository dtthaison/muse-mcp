package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dtthaison/muse-mcp/internal/library"
	"github.com/dtthaison/muse-mcp/internal/muse"
	"github.com/dtthaison/muse-mcp/internal/voices"
)

type fakeService struct {
	images []muse.ImageRequest
	chats  []muse.ChatRequest
	err    error
}

func (f *fakeService) GenerateImage(_ context.Context, r muse.ImageRequest) (muse.Result, error) {
	f.images = append(f.images, r)
	return muse.Result{Status: "done", ChatID: "c1"}, f.err
}

func (f *fakeService) Chat(_ context.Context, r muse.ChatRequest) (muse.Result, error) {
	f.chats = append(f.chats, r)
	return muse.Result{Status: "done", ChatID: "c1", Reply: "hi"}, f.err
}

func (f *fakeService) Decide(context.Context, string, string, bool) (muse.Result, error) {
	return muse.Result{Status: "done"}, f.err
}

func (f *fakeService) Status(context.Context) (muse.StatusInfo, error) {
	return muse.StatusInfo{Pending: []muse.PendingApproval{}}, f.err
}

func connect(t *testing.T, svc Service, lib *library.Store, cat *voices.Catalog) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "muse-mcp", Version: "test"}, nil)
	Register(server, svc, lib, cat)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return res, text.String()
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

func setup(t *testing.T) (*fakeService, *library.Store, *mcp.ClientSession) {
	t.Helper()
	svc := &fakeService{}
	lib := library.Open(t.TempDir())
	return svc, lib, connect(t, svc, lib, voices.Open(filepath.Join(t.TempDir(), "voices.csv")))
}

func TestListsTools(t *testing.T) {
	_, _, cs := setup(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
	sort.Strings(names)
	want := "muse_chat,muse_decide_approval,muse_delete_reference,muse_generate_image,muse_generate_speech,muse_generate_video,muse_list_references,muse_list_voices,muse_save_reference,muse_status"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools %v", names)
	}
}

func TestGenerateRejectsBadExtension(t *testing.T) {
	svc, _, cs := setup(t)
	res, text := call(t, cs, "muse_generate_image", map[string]any{"output_path": "x.gif", "prompt": "fox"})
	if !res.IsError || !strings.Contains(text, ".png") || len(svc.images) != 0 {
		t.Fatalf("isError=%v text=%q calls=%d", res.IsError, text, len(svc.images))
	}
}

func TestGenerateUnknownReference(t *testing.T) {
	svc, _, cs := setup(t)
	res, text := call(t, cs, "muse_generate_image", map[string]any{"output_path": "x.png", "prompt": "p", "characters": []string{"ghost"}})
	if !res.IsError || !strings.Contains(text, "ghost") || !strings.Contains(text, "muse_list_references") || len(svc.images) != 0 {
		t.Fatalf("isError=%v text=%q", res.IsError, text)
	}
}

func TestGenerateKindMismatch(t *testing.T) {
	svc, lib, cs := setup(t)
	if _, err := lib.Save("fox", library.Character, "d", []string{pngFile(t, t.TempDir(), "f.png")}, false); err != nil {
		t.Fatal(err)
	}
	res, text := call(t, cs, "muse_generate_image", map[string]any{"output_path": "x.png", "prompt": "p", "scene": "fox"})
	if !res.IsError || len(svc.images) != 0 {
		t.Fatalf("isError=%v text=%q", res.IsError, text)
	}
}

func TestGenerateMissingReferenceFile(t *testing.T) {
	svc, _, cs := setup(t)
	res, text := call(t, cs, "muse_generate_image", map[string]any{"output_path": "x.png", "prompt": "p", "reference_images": []string{"/nope.png"}})
	if !res.IsError || !strings.Contains(text, "/nope.png") || len(svc.images) != 0 {
		t.Fatalf("isError=%v text=%q", res.IsError, text)
	}
}

func TestGenerateBuildsPrompt(t *testing.T) {
	svc, lib, cs := setup(t)
	fox, err := lib.Save("fox", library.Character, "red fox", []string{pngFile(t, t.TempDir(), "f.png")}, false)
	if err != nil {
		t.Fatal(err)
	}
	res, text := call(t, cs, "muse_generate_image", map[string]any{"output_path": "out/x.png", "characters": []string{"fox"}, "change": "asleep"})
	if res.IsError {
		t.Fatal(text)
	}
	if len(svc.images) != 1 {
		t.Fatalf("calls %d", len(svc.images))
	}
	got := svc.images[0]
	if !strings.Contains(got.Text, `character reference "fox"`) || !strings.Contains(got.Text, "Change ONLY: asleep") {
		t.Fatalf("text %q", got.Text)
	}
	if len(got.Attachments) != 1 || got.Attachments[0] != fox.Images[0] || got.OutputPath != "out/x.png" {
		t.Fatalf("request %+v", got)
	}
	var out muse.Result
	if err := json.Unmarshal([]byte(text), &out); err != nil || out.Status != "done" {
		t.Fatalf("result text %q (%v)", text, err)
	}
}

func TestGenerateEditsPhoto(t *testing.T) {
	svc, _, cs := setup(t)
	dir := t.TempDir()
	photo, mood := pngFile(t, dir, "bag.png"), pngFile(t, dir, "mood.png")
	res, text := call(t, cs, "muse_generate_image", map[string]any{"output_path": "bag-cream.png", "edit_image": photo,
		"change": "the background to a plain cream studio background", "keep": "the bag's shape, colour and stitches",
		"reference_images": []string{mood}, "reference_roles": []string{"lighting mood only"}, "count": 3})
	if res.IsError {
		t.Fatal(text)
	}
	got := svc.images[0]
	for _, want := range []string{"Image 1 is the photo to edit.", "Image 2 is lighting mood only.",
		"Edit image 1. Change only: the background", "Keep exactly: the bag's shape, colour and stitches.",
		"Make 3 separate versions"} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("missing %q in:\n%s", want, got.Text)
		}
	}
	if len(got.Attachments) != 2 || got.Attachments[0] != photo {
		t.Fatalf("attachments %v", got.Attachments)
	}
}

func TestChatPassesThrough(t *testing.T) {
	svc, _, cs := setup(t)
	res, text := call(t, cs, "muse_chat", map[string]any{"message": "hello", "chat_id": "abc", "save_images_to": "imgs"})
	if res.IsError || len(svc.chats) != 1 {
		t.Fatalf("isError=%v text=%q", res.IsError, text)
	}
	if c := svc.chats[0]; c.Message != "hello" || c.ChatID != "abc" || c.SaveImagesTo != "imgs" {
		t.Fatalf("request %+v", c)
	}
}

func TestDecideRejectsBadDecision(t *testing.T) {
	_, _, cs := setup(t)
	res, _ := call(t, cs, "muse_decide_approval", map[string]any{"chat_id": "c", "approval_id": "a", "decision": "maybe"})
	if !res.IsError {
		t.Fatal("want error")
	}
}

func TestReferenceToolsRoundTrip(t *testing.T) {
	_, _, cs := setup(t)
	img := pngFile(t, t.TempDir(), "f.png")
	res, text := call(t, cs, "muse_save_reference", map[string]any{"name": "fox", "kind": "character", "description": "red fox", "image_paths": []string{img}})
	if res.IsError {
		t.Fatal(text)
	}
	_, text = call(t, cs, "muse_list_references", map[string]any{})
	if !strings.Contains(text, `"fox"`) || !strings.Contains(text, "red fox") {
		t.Fatalf("list %q", text)
	}
	if res, text := call(t, cs, "muse_delete_reference", map[string]any{"name": "fox"}); res.IsError {
		t.Fatal(text)
	}
	_, text = call(t, cs, "muse_list_references", map[string]any{})
	if strings.Contains(text, `"fox"`) {
		t.Fatalf("still listed: %q", text)
	}
}

func TestServiceErrorsSurfaceAsToolErrors(t *testing.T) {
	svc, _, cs := setup(t)
	svc.err = muse.ErrLoggedOut
	res, text := call(t, cs, "muse_chat", map[string]any{"message": "hi"})
	if !res.IsError || !strings.Contains(text, "Log in to muse.ai") {
		t.Fatalf("isError=%v text=%q", res.IsError, text)
	}
}

func TestChatAttachmentsChecked(t *testing.T) {
	svc, _, cs := setup(t)
	dir := t.TempDir()
	for _, a := range []string{filepath.Join(dir, "nope.pdf"), dir} {
		res, text := call(t, cs, "muse_chat", map[string]any{"message": "hi", "attachments": []string{a}})
		if !res.IsError || !strings.Contains(text, a) {
			t.Fatalf("%s: isError=%v text=%q", a, res.IsError, text)
		}
	}
	if len(svc.chats) != 0 {
		t.Fatalf("service called %d times", len(svc.chats))
	}
	pngFile(t, dir, "a.png")
	t.Chdir(dir)
	if res, text := call(t, cs, "muse_chat", map[string]any{"message": "hi", "attachments": []string{"a.png"}}); res.IsError {
		t.Fatal(text)
	}
	if got := svc.chats[0].Attachments; len(got) != 1 || got[0] != filepath.Join(dir, "a.png") {
		t.Fatalf("attachments %q", got)
	}
}

func TestGenerateVideo(t *testing.T) {
	svc, _, cs := setup(t)
	res, text := call(t, cs, "muse_generate_video", map[string]any{"output_path": "v.mp4", "prompt": "steam rising"})
	if res.IsError || len(svc.images) != 1 {
		t.Fatalf("isError=%v text=%q", res.IsError, text)
	}
	got := svc.images[0]
	if got.Media != muse.MediaVideo || got.OutputPath != "v.mp4" || !strings.Contains(got.Text, "Generate a video: steam rising") {
		t.Fatalf("request %+v", got)
	}
	res, text = call(t, cs, "muse_generate_video", map[string]any{"output_path": "v.png", "prompt": "p"})
	if !res.IsError || !strings.Contains(text, ".mp4") || len(svc.images) != 1 {
		t.Fatalf("isError=%v text=%q", res.IsError, text)
	}
}

func TestGenerateSpeech(t *testing.T) {
	svc, _, cs := setup(t)
	res, text := call(t, cs, "muse_generate_speech", map[string]any{"output_dir": "clips", "text": "Xin chào các bạn",
		"voice": "Helio", "language": "Vietnamese (vi-VN)", "speed": 85})
	if res.IsError || len(svc.images) != 1 {
		t.Fatalf("isError=%v text=%q", res.IsError, text)
	}
	got := svc.images[0]
	for _, want := range []string{`Voice: "Helio"`, "Language: Vietnamese (vi-VN)", "Speed: 85 ", "Xin chào các bạn"} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("missing %q in:\n%s", want, got.Text)
		}
	}
	if got.Media != muse.MediaVoice || got.OutputPath != "clips" || len(got.Attachments) != 0 {
		t.Fatalf("request %+v", got)
	}
	// The file is named after the chat, so a file name is refused.
	res, text = call(t, cs, "muse_generate_speech", map[string]any{"output_dir": "hi.mp3", "text": "x"})
	if !res.IsError || !strings.Contains(text, "folder") || len(svc.images) != 1 {
		t.Fatalf("isError=%v text=%q", res.IsError, text)
	}
	if res, text := call(t, cs, "muse_generate_speech", map[string]any{"text": "Xin chào"}); res.IsError || svc.images[1].OutputPath != "." {
		t.Fatalf("default folder: isError=%v text=%q request %+v", res.IsError, text, svc.images[1])
	}
}

func TestVoiceScriptWarningsBlockUntilIgnored(t *testing.T) {
	svc, _, cs := setup(t)
	args := map[string]any{"text": "Giá chỉ 199.000đ, xem tại https://shop.vn"}
	res, text := call(t, cs, "muse_generate_speech", args)
	if !res.IsError || !strings.Contains(text, "digits") || !strings.Contains(text, "link") ||
		!strings.Contains(text, "ignore_script_warnings") || len(svc.images) != 0 {
		t.Fatalf("isError=%v text=%q calls=%d", res.IsError, text, len(svc.images))
	}
	args["ignore_script_warnings"] = true
	if res, text := call(t, cs, "muse_generate_speech", args); res.IsError || len(svc.images) != 1 {
		t.Fatalf("isError=%v text=%q", res.IsError, text)
	}
}

func TestListVoices(t *testing.T) {
	dir := t.TempDir()
	cat := voices.Open(filepath.Join(dir, "voices.csv"))
	cs := connect(t, &fakeService{}, library.Open(dir), cat)
	res, text := call(t, cs, "muse_list_voices", map[string]any{})
	if !res.IsError || !strings.Contains(text, "muse-mcp voices import") {
		t.Fatalf("no catalog: isError=%v text=%q", res.IsError, text)
	}
	src := filepath.Join(dir, "v.csv")
	os.WriteFile(src, []byte("name,gender,accent,tone,description\nAria,F,American,Warm,x\nHelio,M,American,Smooth,y\n"), 0o600)
	if _, err := cat.Import(src); err != nil {
		t.Fatal(err)
	}
	res, text = call(t, cs, "muse_list_voices", map[string]any{"gender": "male"})
	var out struct {
		Voices  []voices.Voice `json:"voices"`
		Matched int            `json:"matched"`
	}
	if res.IsError || json.Unmarshal([]byte(text), &out) != nil || out.Matched != 1 || out.Voices[0].Name != "Helio" {
		t.Fatalf("isError=%v text=%q", res.IsError, text)
	}
}
