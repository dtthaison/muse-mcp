package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtthaison/muse-mcp/internal/library"
)

func touch(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func contains(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("prompt missing %q:\n%s", want, text)
	}
}

func TestPlainPrompt(t *testing.T) {
	b, err := Build(Input{Prompt: "a red fox"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Generate an image: a red fox\n\nAttach the final image here in the chat as a single image."
	if b.Text != want {
		t.Fatalf("got %q", b.Text)
	}
	if len(b.Attachments) != 0 {
		t.Fatalf("attachments %v", b.Attachments)
	}
}

func TestCharacterAndSceneNumbering(t *testing.T) {
	dir := t.TempDir()
	fox := library.Ref{Name: "fox", Kind: library.Character, Description: "red fox, white chest",
		Images: []string{touch(t, dir, "f1.png"), touch(t, dir, "f2.png")}}
	forest := library.Ref{Name: "snowy-forest", Kind: library.Scene, Description: "pine left, birch right",
		Images: []string{touch(t, dir, "s1.png")}}
	adhoc := touch(t, dir, "extra.png")

	b, err := Build(Input{Prompt: "fox by a stream", Characters: []library.Ref{fox}, Scene: &forest, References: []string{adhoc}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{fox.Images[0], fox.Images[1], forest.Images[0], adhoc}
	if strings.Join(b.Attachments, ",") != strings.Join(want, ",") {
		t.Fatalf("attachments %v, want %v", b.Attachments, want)
	}
	contains(t, b.Text, `Images 1–2 are the character reference "fox": red fox, white chest.`)
	contains(t, b.Text, `Image 3 is the scene reference "snowy-forest": pine left, birch right.`)
	contains(t, b.Text, "Image 4 is a reference image.")
	contains(t, b.Text, "Generate an image: fox by a stream")
	if !strings.HasSuffix(b.Text, "\n\nAttach the final image here in the chat as a single image.") {
		t.Fatalf("bad ending:\n%s", b.Text)
	}
}

func TestChangeWording(t *testing.T) {
	dir := t.TempDir()
	fox := library.Ref{Name: "fox", Kind: library.Character, Description: "d", Images: []string{touch(t, dir, "f.png")}}
	b, err := Build(Input{Change: "curled up asleep", Characters: []library.Ref{fox}})
	if err != nil {
		t.Fatal(err)
	}
	contains(t, b.Text, "Change ONLY: curled up asleep.")
	contains(t, b.Text, "Keep everything else identical")
}

func TestReferenceNotes(t *testing.T) {
	dir := t.TempDir()
	b, err := Build(Input{Prompt: "p", References: []string{touch(t, dir, "a.png")}, ReferenceNotes: "use its colours"})
	if err != nil {
		t.Fatal(err)
	}
	contains(t, b.Text, "Image 1 is a reference image. Note: use its colours")
}

// Editing a real photo: it is attached first as the canvas, and the prompt
// says what may change and what must stay (muse regenerates, so keep is a
// request, not a guarantee).
func TestEditImage(t *testing.T) {
	dir := t.TempDir()
	photo := touch(t, dir, "bag.png")
	b, err := Build(Input{EditImage: photo, Change: "the background to a plain cream studio background",
		Keep: "the bag's shape, colour, stitch pattern and handles", References: []string{touch(t, dir, "mood.png")},
		ReferenceRoles: []string{"lighting mood only"}})
	if err != nil {
		t.Fatal(err)
	}
	if b.Attachments[0] != photo || len(b.Attachments) != 2 {
		t.Fatalf("attachments %v", b.Attachments)
	}
	contains(t, b.Text, "Image 1 is the photo to edit.")
	contains(t, b.Text, "Image 2 is lighting mood only.")
	contains(t, b.Text, "Edit image 1. Change only: the background to a plain cream studio background.")
	contains(t, b.Text, "Keep exactly: the bag's shape, colour, stitch pattern and handles.")
	if strings.Contains(b.Text, "Generate a new image") {
		t.Fatalf("edit worded as a new image:\n%s", b.Text)
	}
	if _, err := Build(Input{EditImage: photo, Prompt: "nicer"}); err == nil {
		t.Fatal("edit without change accepted")
	}
	if _, err := Build(Input{EditImage: filepath.Join(dir, "missing.png"), Change: "x"}); err == nil {
		t.Fatal("missing photo accepted")
	}
}

func TestKeepWithChange(t *testing.T) {
	dir := t.TempDir()
	fox := library.Ref{Name: "fox", Kind: library.Character, Description: "d", Images: []string{touch(t, dir, "f.png")}}
	b, err := Build(Input{Change: "asleep", Keep: "the red scarf", Characters: []library.Ref{fox}})
	if err != nil {
		t.Fatal(err)
	}
	contains(t, b.Text, "Keep exactly: the red scarf.")
}

func TestReferenceRoles(t *testing.T) {
	dir := t.TempDir()
	b, err := Build(Input{Prompt: "p", References: []string{touch(t, dir, "a.png"), touch(t, dir, "b.png")},
		ReferenceRoles: []string{"the product: its shape and colour must stay exact."}})
	if err != nil {
		t.Fatal(err)
	}
	contains(t, b.Text, "Image 1 is the product: its shape and colour must stay exact.\n")
	contains(t, b.Text, "Image 2 is a reference image.")
	if _, err := Build(Input{Prompt: "p", ReferenceRoles: []string{"x"}}); err == nil {
		t.Fatal("more roles than references accepted")
	}
}

// muse makes at most 4 separate images per request.
func TestCount(t *testing.T) {
	b, err := Build(Input{Prompt: "p", Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(b.Text, "\n\nMake 3 separate versions and attach all 3 images here in the chat.") {
		t.Fatalf("ending:\n%s", b.Text)
	}
	for _, bad := range []Input{{Prompt: "p", Count: 5}, {Prompt: "p", Count: -1}, {Prompt: "p", Count: 2, Video: true}} {
		if _, err := Build(bad); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
}

func TestAspectRatio(t *testing.T) {
	b, err := Build(Input{Prompt: "p", AspectRatio: "16:9"})
	if err != nil {
		t.Fatal(err)
	}
	contains(t, b.Text, "Aspect ratio 16:9.")
}

func TestNeedsPromptOrChange(t *testing.T) {
	if _, err := Build(Input{}); err == nil {
		t.Fatal("want error")
	}
}

func TestTooManyAttachments(t *testing.T) {
	dir := t.TempDir()
	var refs []string
	for i := 0; i < 11; i++ {
		refs = append(refs, touch(t, dir, string(rune('a'+i))+".png"))
	}
	_, err := Build(Input{Prompt: "p", References: refs})
	if err == nil || !strings.Contains(err.Error(), "10") {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingAdHocReference(t *testing.T) {
	_, err := Build(Input{Prompt: "p", References: []string{"/nope.png"}})
	if err == nil || !strings.Contains(err.Error(), "/nope.png") {
		t.Fatalf("err = %v", err)
	}
}

func TestRelativeReferenceMadeAbsolute(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "x.png")
	t.Chdir(dir)
	b, err := Build(Input{Prompt: "p", References: []string{"x.png"}})
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(b.Attachments[0]) || filepath.Base(b.Attachments[0]) != "x.png" {
		t.Fatalf("got %q", b.Attachments[0])
	}
}

func TestAdHocReferenceMustBeImage(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{txt, dir} {
		if _, err := Build(Input{Prompt: "p", References: []string{p}}); err == nil || !strings.Contains(err.Error(), p) {
			t.Fatalf("%s: err = %v", p, err)
		}
	}
}

func TestVideoWording(t *testing.T) {
	b, err := Build(Input{Prompt: "a teapot with steam", Video: true})
	if err != nil {
		t.Fatal(err)
	}
	contains(t, b.Text, "Generate a video: a teapot with steam")
	contains(t, b.Text, "Attach the final video here in the chat as a single video.")
	if strings.Contains(b.Text, "image") {
		t.Fatalf("video prompt mentions an image:\n%s", b.Text)
	}
	b, _ = Build(Input{Change: "the steam stops", Video: true})
	contains(t, b.Text, "Generate a new video. Change ONLY: the steam stops.")
	if !strings.Contains(NudgeVideo, "video") {
		t.Fatal(NudgeVideo)
	}
}

func TestVoice(t *testing.T) {
	b, err := Voice(VoiceInput{Text: "Xin chào!\nHello.", Voice: "Helio", Language: "Vietnamese (vi-VN)", Speed: 85})
	if err != nil {
		t.Fatal(err)
	}
	want := `Generate a voice clip (text to speech) and attach it here in the chat as a single audio file.

Voice: "Helio" from your voice catalog.
Language: Vietnamese (vi-VN)
Speed: 85 (your voice tool's speed setting: 1 to 200, 100 is normal)

Script (say exactly this, word for word; add nothing, leave nothing out):
"""
Xin chào!
Hello.
"""`
	if b.Text != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b.Text, want)
	}
	if len(b.Attachments) != 0 {
		t.Fatalf("attachments %v", b.Attachments)
	}
	b, _ = Voice(VoiceInput{Text: "hi"})
	contains(t, b.Text, "Voice: pick one from your voice catalog that suits the script.")
	contains(t, b.Text, "Speed: 100 ") // always set, so muse doesn't guess one
	if strings.Contains(b.Text, "Language:") {
		t.Fatalf("empty language line:\n%s", b.Text)
	}
	for _, bad := range []int{-1, 201} {
		if _, err := Voice(VoiceInput{Text: "hi", Speed: bad}); err == nil {
			t.Fatalf("speed %d accepted", bad)
		}
	}
	if _, err := Voice(VoiceInput{Text: "  "}); err == nil {
		t.Fatal("want error for empty text")
	}
	if !strings.Contains(NudgeVoice, "audio") {
		t.Fatal(NudgeVoice)
	}
}

func TestCheckScript(t *testing.T) {
	for _, tc := range []struct {
		text, lang, want string
	}{
		{"Giá chỉ 199.000đ", "", "digits"},
		{"Giảm năm mươi phần trăm %", "", "%"},
		{"Xem tại https://shop.vn nhé", "", "link"},
		{"Email us at a@b.com", "", "link"},
		{"Hello 😀 there", "", "emoji"},
		{"[whispers] hello", "", "brackets"},
		{"Say <break time=\"1s\"/> now", "", "brackets"},
		{"**Big** sale", "", "markdown"},
		{"Lan: chào bạn\nMinh: chào", "", "speaker"},
		{"xin chao cac ban, hom nay troi dep", "Vietnamese (vi-VN)", "tone marks"},
		{strings.Repeat("word ", 1000), "", "long"},
		{"SALE SỐC năm mươi phần trăm", "", "capitals"},
		{"Big news. HUGE SALE today!", "", "capitals"},
		{"Hôm nay GIẢM GIÁ", "", "capitals"},
	} {
		got := strings.Join(CheckScript(tc.text, tc.lang), " | ")
		if !strings.Contains(got, tc.want) {
			t.Errorf("%q: warnings %q, want one mentioning %q", tc.text, got, tc.want)
		}
	}
	for _, ok := range []struct{ text, lang string }{
		{"Xin chào, hôm nay trời đẹp quá, chúng ta cùng đi dạo nhé!", "Vietnamese"},
		{"Hello there. It's a lovely day, isn't it?", "English"},
		{"Guten Tag! Heute ist ein schöner Tag.", ""},
		{"Our new API is here, says the BBC.", "English"},
	} {
		if w := CheckScript(ok.text, ok.lang); len(w) != 0 {
			t.Errorf("%q: unexpected warnings %q", ok.text, w)
		}
	}
}
