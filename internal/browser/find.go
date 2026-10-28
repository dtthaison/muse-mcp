package browser

import (
	"errors"
	"os"
	"os/exec"

	"pp/muse-mcp/internal/config"
)

// FindChrome returns the Chrome binary to launch: MUSE_CHROME or the macOS
// default if it exists, else a Chrome or Chromium on PATH.
func FindChrome() (string, error) {
	if p := config.ChromePath(); fileExists(p) {
		return p, nil
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("Google Chrome not found; install it or set MUSE_CHROME to its binary")
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
