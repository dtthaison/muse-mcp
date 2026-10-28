package library

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writePNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeJPG(t *testing.T, path string) {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 3)), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (*Store, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	src := t.TempDir()
	a := filepath.Join(src, "a.png")
	b := filepath.Join(src, "B.JPG")
	writePNG(t, a)
	writeJPG(t, b)
	return Open(dir), dir, a, b
}

func sameBytes(t *testing.T, x, y string) {
	t.Helper()
	bx, err := os.ReadFile(x)
	if err != nil {
		t.Fatal(err)
	}
	by, err := os.ReadFile(y)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bx, by) {
		t.Fatalf("%s and %s differ", x, y)
	}
}

func TestSaveCopiesImagesAndRoundTrips(t *testing.T) {
	s, dir, a, b := setup(t)
	if _, err := s.Save("fox", Character, "red fox, white chest", []string{a, b}, false); err != nil {
		t.Fatal(err)
	}
	r, err := s.Get("fox")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "fox" || r.Kind != Character || r.Description != "red fox, white chest" {
		t.Fatalf("got %+v", r)
	}
	want := []string{filepath.Join(dir, "fox", "1.png"), filepath.Join(dir, "fox", "2.jpg")}
	if len(r.Images) != 2 || r.Images[0] != want[0] || r.Images[1] != want[1] {
		t.Fatalf("images = %v, want %v", r.Images, want)
	}
	sameBytes(t, a, r.Images[0])
	sameBytes(t, b, r.Images[1])
	if r.Created.IsZero() || !r.Created.Equal(r.Updated) {
		t.Fatalf("created %v updated %v", r.Created, r.Updated)
	}
}

func TestSaveRejectsBadName(t *testing.T) {
	s, _, a, _ := setup(t)
	for _, name := range []string{"Fox", "-fox", "fox_1", "", "../x"} {
		if _, err := s.Save(name, Character, "d", []string{a}, false); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%q: err = %v", name, err)
		}
	}
}

func TestSaveRejectsBadKind(t *testing.T) {
	s, _, a, _ := setup(t)
	if _, err := s.Save("fox", Kind("prop"), "d", []string{a}, false); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveExistingNeedsReplace(t *testing.T) {
	s, dir, a, b := setup(t)
	first, err := s.Save("fox", Character, "v1", []string{a, b}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save("fox", Character, "v2", []string{a}, false); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	second, err := s.Save("fox", Scene, "v2", []string{b}, true)
	if err != nil {
		t.Fatal(err)
	}
	if second.Description != "v2" || second.Kind != Scene || len(second.Images) != 1 {
		t.Fatalf("got %+v", second)
	}
	if _, err := os.Stat(filepath.Join(dir, "fox", "2.jpg")); !os.IsNotExist(err) {
		t.Fatalf("old image still there: %v", err)
	}
	sameBytes(t, b, second.Images[0])
	if !second.Created.Equal(first.Created) || !second.Updated.After(first.Updated) {
		t.Fatalf("created %v→%v updated %v→%v", first.Created, second.Created, first.Updated, second.Updated)
	}
}

func TestSaveMissingImage(t *testing.T) {
	s, dir, _, _ := setup(t)
	_, err := s.Save("fox", Character, "d", []string{"/nope.png"}, false)
	if err == nil || !strings.Contains(err.Error(), "/nope.png") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fox")); !os.IsNotExist(err) {
		t.Fatalf("fox dir left behind: %v", err)
	}
}

func TestSaveNotAnImage(t *testing.T) {
	s, _, _, _ := setup(t)
	bad := filepath.Join(t.TempDir(), "bad.png")
	if err := os.WriteFile(bad, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Save("fox", Character, "d", []string{bad}, false)
	if err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveRequiresAtLeastOneImage(t *testing.T) {
	s, _, _, _ := setup(t)
	if _, err := s.Save("fox", Character, "d", nil, false); err == nil {
		t.Fatal("want error")
	}
}

func TestListAndDelete(t *testing.T) {
	s, _, a, _ := setup(t)
	for _, n := range []string{"b", "a"} {
		if _, err := s.Save(n, Scene, "d", []string{a}, false); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Name != "a" || refs[1].Name != "b" {
		t.Fatalf("list = %+v", refs)
	}
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if err := s.Delete("zzz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestListEmptyStore(t *testing.T) {
	refs, err := Open(filepath.Join(t.TempDir(), "missing")).List()
	if err != nil || len(refs) != 0 {
		t.Fatalf("refs=%v err=%v", refs, err)
	}
}
