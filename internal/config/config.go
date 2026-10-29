// Package config resolves where muse-mcp keeps its state on disk.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const defaultChrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

// Paths is the layout of the state root (default ~/.muse-mcp).
type Paths struct {
	Root    string
	Profile string // dedicated Chrome profile the owner logs in to
	Library string // saved character and scene references
	Chats   string // chats.json: chats this server created
	Voices  string // voices.csv: muse's voice catalog, imported by the owner
	Log     string
	Lock    string // keeps a second muse-mcp off the same profile
}

// Load resolves the state root from MUSE_MCP_HOME (or ~/.muse-mcp) and creates
// the root and library directories.
func Load() (Paths, error) {
	root := os.Getenv("MUSE_MCP_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("finding home directory: %w", err)
		}
		root = filepath.Join(home, ".muse-mcp")
	}
	p := Paths{
		Root:    root,
		Profile: filepath.Join(root, "chrome-profile"),
		Library: filepath.Join(root, "library"),
		Chats:   filepath.Join(root, "chats.json"),
		Voices:  filepath.Join(root, "voices.csv"),
		Log:     filepath.Join(root, "muse-mcp.log"),
		Lock:    filepath.Join(root, "lock"),
	}
	for _, dir := range []string{p.Root, p.Library} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Paths{}, err
		}
	}
	return p, nil
}

// ChromePath is MUSE_CHROME, or Google Chrome's standard macOS location.
func ChromePath() string {
	if p := os.Getenv("MUSE_CHROME"); p != "" {
		return p
	}
	return defaultChrome
}
