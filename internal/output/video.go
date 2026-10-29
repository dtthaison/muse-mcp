package output

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// videoExt maps a sniffed video type to its file extension.
var videoExt = map[string]string{"video/mp4": ".mp4", "video/webm": ".webm"}

// CheckVideoPath reports whether p names a video format SaveVideo writes.
func CheckVideoPath(p string) error {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".mp4", ".webm":
		return nil
	}
	return fmt.Errorf("output path %q: use .mp4 or .webm", p)
}

// SaveVideo writes video data to outPath as-is (there is no conversion). If
// muse sent another format than the extension says, the file gets the real
// extension instead: the generation is already spent. width and height are as
// the page reported them.
func SaveVideo(data []byte, outPath string, width, height int) (Saved, error) {
	if err := CheckVideoPath(outPath); err != nil {
		return Saved{}, err
	}
	ct := http.DetectContentType(data)
	ext, ok := videoExt[ct]
	if !ok {
		return Saved{}, fmt.Errorf("muse returned data that isn't an mp4 or webm video (%s)", ct)
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

// SaveVideoInDir writes data as dir/base.mp4 or .webm, whichever it is.
func SaveVideoInDir(data []byte, dir, base string, width, height int) (Saved, error) {
	return SaveVideo(data, filepath.Join(dir, base+".mp4"), width, height)
}
