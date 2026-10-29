package output

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Just enough of each container for content sniffing.
var (
	mp4Bytes  = append([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp41isom"), make([]byte, 32)...)
	webmBytes = append([]byte("\x1a\x45\xdf\xa3"), make([]byte, 32)...)
)

func TestCheckVideoPath(t *testing.T) {
	for _, p := range []string{"a.mp4", "b.WEBM"} {
		if err := CheckVideoPath(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, p := range []string{"a.png", "a.mov", "a"} {
		if err := CheckVideoPath(p); err == nil {
			t.Errorf("%s: want error", p)
		}
	}
}

func TestSaveVideoVerbatim(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "v.mp4")
	s, err := SaveVideo(mp4Bytes, p, 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if s.Path != p || !bytes.Equal(got, mp4Bytes) || s.Width != 1920 || s.Height != 1080 || s.Bytes != len(mp4Bytes) {
		t.Fatalf("saved %+v", s)
	}
}

func TestSaveVideoKeepsRealFormat(t *testing.T) {
	// The generation is already spent: save under the real extension rather than fail.
	dir := t.TempDir()
	s, err := SaveVideo(webmBytes, filepath.Join(dir, "v.mp4"), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.Path != filepath.Join(dir, "v.webm") {
		t.Fatalf("saved to %s", s.Path)
	}
}

func TestSaveVideoGarbage(t *testing.T) {
	if _, err := SaveVideo([]byte("nope"), filepath.Join(t.TempDir(), "v.mp4"), 0, 0); err == nil {
		t.Fatal("want error")
	}
}

func TestSaveVideoInDir(t *testing.T) {
	dir := t.TempDir()
	s, err := SaveVideoInDir(webmBytes, dir, "muse-reply", 0, 0)
	if err != nil || s.Path != filepath.Join(dir, "muse-reply.webm") {
		t.Fatalf("%+v %v", s, err)
	}
}

var mp3Bytes = append([]byte("ID3\x03\x00\x00\x00\x00\x00\x1b"), make([]byte, 32)...)

func TestSaveAudio(t *testing.T) {
	dir := t.TempDir()
	if err := CheckAudioPath("a.MP3"); err != nil {
		t.Fatal(err)
	}
	if err := CheckAudioPath("a.mp4"); err == nil {
		t.Fatal("want error for .mp4")
	}
	s, err := SaveAudio(mp3Bytes, filepath.Join(dir, "hello.mp3"))
	if err != nil || s.Path != filepath.Join(dir, "hello.mp3") || s.Bytes != len(mp3Bytes) {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := SaveAudio(webmBytes, filepath.Join(dir, "x.mp3")); err == nil {
		t.Fatal("saved a video as audio")
	}
	s, err = SaveAudioInDir(mp3Bytes, dir, "muse-reply")
	if err != nil || s.Path != filepath.Join(dir, "muse-reply.mp3") {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestChatFileName(t *testing.T) {
	const id = "123e4567-e89b-42d3-a456-426614174002"
	for _, tc := range []struct{ title, want string }{
		{"Generate Vietnamese text to speech", id + "-Generate-Vietnamese-text-to-speech"},
		{"Giọng đọc: quán trà sữa / khai trương?", id + "-Giọng-đọc-quán-trà-sữa-khai-trương"},
		{"  ", id + "-voice"},
		{"", id + "-voice"},
		{strings.Repeat("ab ", 50), id + "-" + strings.TrimSuffix(strings.Repeat("ab-", 20), "-")},
	} {
		if got := ChatFileName(id, tc.title, "voice"); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.title, got, tc.want)
		}
	}
}
