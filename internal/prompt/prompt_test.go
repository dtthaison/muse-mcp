package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pp/muse-mcp/internal/library"
)

func touch(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
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
