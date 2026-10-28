package output

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/webp"
)

func tinyWebp(t *testing.T) ([]byte, image.Config) {
	t.Helper()
	data, err := os.ReadFile("testdata/tiny.webp")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := webp.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return data, cfg
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCheckPath(t *testing.T) {
	for _, ok := range []string{"a.png", "a.JPG", "a.webp", "a.jpeg", "dir/x.PNG"} {
		if err := CheckPath(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"a.gif", "a", ""} {
		err := CheckPath(bad)
		if err == nil || !strings.Contains(err.Error(), ".webp, .png, .jpg") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestSaveWebpAsWebpIsVerbatim(t *testing.T) {
	data, cfg := tinyWebp(t)
	out := filepath.Join(t.TempDir(), "a.webp")
	s, err := Save(data, out)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(s.Path)
	if !bytes.Equal(got, data) {
		t.Fatal("webp not written verbatim")
	}
	if s.Width != cfg.Width || s.Height != cfg.Height || s.Bytes != len(data) {
		t.Fatalf("saved %+v, cfg %+v", s, cfg)
	}
}

func TestSaveWebpAsPNG(t *testing.T) {
	data, cfg := tinyWebp(t)
	s, err := Save(data, filepath.Join(t.TempDir(), "a.png"))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(s.Path)
	defer f.Close()
	got, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatalf("not a png: %v", err)
	}
	if got.Width != cfg.Width || got.Height != cfg.Height {
		t.Fatalf("dims %dx%d", got.Width, got.Height)
	}
}

func TestSavePNGInputAsJPG(t *testing.T) {
	s, err := Save(pngBytes(t), filepath.Join(t.TempDir(), "a.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(s.Path)
	defer f.Close()
	if _, err := jpeg.DecodeConfig(f); err != nil {
		t.Fatalf("not a jpeg: %v", err)
	}
	if s.Width != 4 || s.Height != 3 {
		t.Fatalf("dims %dx%d", s.Width, s.Height)
	}
}

func TestSaveNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out", "a.png")
	first, err := Save(pngBytes(t), out)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(first.Path)
	second, err := Save(pngBytes(t), out)
	if err != nil {
		t.Fatal(err)
	}
	third, err := Save(pngBytes(t), out)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != out || second.Path != filepath.Join(dir, "out", "a-1.png") || third.Path != filepath.Join(dir, "out", "a-2.png") {
		t.Fatalf("paths %s %s %s", first.Path, second.Path, third.Path)
	}
	after, _ := os.ReadFile(first.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("first file changed")
	}
}

func TestSaveMakesDirs(t *testing.T) {
	out := filepath.Join(t.TempDir(), "x", "y", "a.png")
	if _, err := Save(pngBytes(t), out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
}

func TestSaveRelativeResolvesAgainstCwd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := Save(pngBytes(t), "rel.png")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(dir, "rel.png"))
	got, _ := filepath.EvalSymlinks(s.Path)
	if got != want || !filepath.IsAbs(s.Path) {
		t.Fatalf("path %s, want %s", s.Path, want)
	}
}

func TestSaveGarbage(t *testing.T) {
	if _, err := Save([]byte("nope"), filepath.Join(t.TempDir(), "a.png")); err == nil {
		t.Fatal("want error")
	}
}

func TestSaveInDir(t *testing.T) {
	dir := t.TempDir()
	a, err := SaveInDir(pngBytes(t), dir, "muse-reply")
	if err != nil {
		t.Fatal(err)
	}
	b, err := SaveInDir(pngBytes(t), dir, "muse-reply")
	if err != nil {
		t.Fatal(err)
	}
	if a.Path != filepath.Join(dir, "muse-reply.png") || b.Path != filepath.Join(dir, "muse-reply-1.png") {
		t.Fatalf("paths %s %s", a.Path, b.Path)
	}
}
