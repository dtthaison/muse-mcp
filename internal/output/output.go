// Package output writes generated images and videos to disk in the format the file
// extension asks for, never overwriting an existing file.
package output

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/webp"
)

// Saved describes a written file.
type Saved struct {
	Path   string `json:"path"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Bytes  int    `json:"bytes"`
}

const supported = ".webp, .png, .jpg or .jpeg"

// CheckPath reports whether the extension of p is a format Save can write.
func CheckPath(p string) error {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".webp", ".png", ".jpg", ".jpeg":
		return nil
	}
	return fmt.Errorf("output path %q: use one of %s", p, supported)
}

// Save writes image data to outPath (relative to the working directory),
// converting to the extension's format. An existing file gets -1, -2, … added.
func Save(data []byte, outPath string) (Saved, error) {
	if err := CheckPath(outPath); err != nil {
		return Saved{}, err
	}
	abs, err := filepath.Abs(outPath)
	if err != nil {
		return Saved{}, err
	}
	img, format, err := decode(data)
	if err != nil {
		return Saved{}, err
	}

	ext := strings.ToLower(filepath.Ext(abs))
	out := data
	switch {
	case ext == ".webp" && format == "webp":
	case ext == ".webp":
		// Go has no webp encoder; keep the requested name honest by refusing.
		return Saved{}, fmt.Errorf("muse returned %s, which can't be saved as .webp; use .png or .jpg", format)
	case ext == ".png":
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			return Saved{}, err
		}
		out = b.Bytes()
	default:
		var b bytes.Buffer
		if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 92}); err != nil {
			return Saved{}, err
		}
		out = b.Bytes()
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Saved{}, err
	}
	path, err := writeNew(abs, out)
	if err != nil {
		return Saved{}, err
	}
	bounds := img.Bounds()
	return Saved{Path: path, Width: bounds.Dx(), Height: bounds.Dy(), Bytes: len(out)}, nil
}

// SaveInDir writes data as dir/base.png (or base-1.png, …).
func SaveInDir(data []byte, dir, base string) (Saved, error) {
	return Save(data, filepath.Join(dir, base+".png"))
}

// decode sniffs the bytes rather than trusting any MIME type from the page.
func decode(data []byte) (image.Image, string, error) {
	r := bytes.NewReader(data)
	switch ct := http.DetectContentType(data); ct {
	case "image/webp":
		img, err := webp.Decode(r)
		return img, "webp", err
	case "image/png":
		img, err := png.Decode(r)
		return img, "png", err
	case "image/jpeg":
		img, err := jpeg.Decode(r)
		return img, "jpeg", err
	default:
		return nil, "", fmt.Errorf("muse returned data that isn't a webp, png or jpeg image (%s)", ct)
	}
}

// writeNew creates path, or the first free path-N variant, with O_EXCL so a
// concurrent writer can never be overwritten.
func writeNew(path string, data []byte) (string, error) {
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 0; i < 10000; i++ {
		p := path
		if i > 0 {
			p = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			os.Remove(p)
			return "", err
		}
		return p, f.Close()
	}
	return "", fmt.Errorf("too many files named like %s", path)
}
