package output

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// media is a kind of file saved as muse sent it, without conversion.
type media struct {
	what  string            // for messages: "video", "audio clip"
	types map[string]string // sniffed content type → file extension
	names string            // the extensions, for messages
}

var (
	videoMedia = media{"video", map[string]string{"video/mp4": ".mp4", "video/webm": ".webm"}, ".mp4 or .webm"}
	audioMedia = media{"audio clip", map[string]string{"audio/mpeg": ".mp3", "audio/wave": ".wav", "application/ogg": ".ogg"}, ".mp3, .wav or .ogg"}
)

func (m media) check(p string) error {
	ext := strings.ToLower(filepath.Ext(p))
	for _, e := range m.types {
		if ext == e {
			return nil
		}
	}
	return fmt.Errorf("output path %q: use %s", p, m.names)
}

// save writes data to outPath as-is. If muse sent another format than the
// extension says, the file gets the real extension instead: the generation is
// already spent. width and height are as the page reported them.
func (m media) save(data []byte, outPath string, width, height int) (Saved, error) {
	if err := m.check(outPath); err != nil {
		return Saved{}, err
	}
	ct := http.DetectContentType(data)
	ext, ok := m.types[ct]
	if !ok {
		return Saved{}, fmt.Errorf("muse returned data that isn't a %s in %s (%s)", m.what, m.names, ct)
	}
	abs, err := filepath.Abs(outPath)
	if err != nil {
		return Saved{}, err
	}
	if !strings.EqualFold(filepath.Ext(abs), ext) {
		abs = strings.TrimSuffix(abs, filepath.Ext(abs)) + ext
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Saved{}, err
	}
	path, err := writeNew(abs, data)
	if err != nil {
		return Saved{}, err
	}
	return Saved{Path: path, Width: width, Height: height, Bytes: len(data)}, nil
}

// CheckVideoPath reports whether p names a video format SaveVideo writes.
func CheckVideoPath(p string) error { return videoMedia.check(p) }

// SaveVideo writes video data to outPath as-is; see media.save.
func SaveVideo(data []byte, outPath string, width, height int) (Saved, error) {
	return videoMedia.save(data, outPath, width, height)
}

// SaveVideoInDir writes data as dir/base.mp4 or .webm, whichever it is.
func SaveVideoInDir(data []byte, dir, base string, width, height int) (Saved, error) {
	return SaveVideo(data, filepath.Join(dir, base+".mp4"), width, height)
}

// CheckAudioPath reports whether p names an audio format SaveAudio writes.
func CheckAudioPath(p string) error { return audioMedia.check(p) }

// SaveAudio writes audio data to outPath as-is; see media.save.
func SaveAudio(data []byte, outPath string) (Saved, error) {
	return audioMedia.save(data, outPath, 0, 0)
}

// SaveAudioInDir writes data as dir/base.mp3 (or .wav, .ogg).
func SaveAudioInDir(data []byte, dir, base string) (Saved, error) {
	return SaveAudio(data, filepath.Join(dir, base+".mp3"))
}

// ChatFileName is a file name, without extension, for a result from one muse
// chat: the chat ID, then the chat's title with anything but letters and
// digits turned into single dashes (at most 60 characters), or fallback when
// the title is empty.
func ChatFileName(chatID, title, fallback string) string {
	var b strings.Builder
	dash := false
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	slug := []rune(b.String())
	if len(slug) > 60 {
		slug = slug[:60]
	}
	s := strings.Trim(string(slug), "-")
	if s == "" {
		s = fallback
	}
	return chatID + "-" + s
}
