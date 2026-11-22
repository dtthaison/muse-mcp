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

// AccountsFile is accounts.json: the muse accounts and the browser each uses.
func (p Paths) AccountsFile() string { return filepath.Join(p.Root, "accounts.json") }

// DefaultAccount is the account that exists with no accounts.json: Chrome on
// this computer, with the state files at the root as in versions before accounts.
const DefaultAccount = "default"

// AccountPaths is the per-account state: what two accounts never share.
type AccountPaths struct {
	Profile string // Chrome profile (used by the local browser)
	Chats   string // chats.json
	Lock    string // keeps a second muse-mcp off this account
}

// Account returns the state paths of the named account. "default" keeps the
// root paths for backward compatibility; any other account lives in
// Root/accounts/NAME, created here. The caller has checked the name.
func (p Paths) Account(name string) (AccountPaths, error) {
	if name == DefaultAccount {
		return AccountPaths{Profile: p.Profile, Chats: p.Chats, Lock: p.Lock}, nil
	}
	dir := filepath.Join(p.Root, "accounts", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return AccountPaths{}, err
	}
	return AccountPaths{
		Profile: filepath.Join(dir, "chrome-profile"),
		Chats:   filepath.Join(dir, "chats.json"),
		Lock:    filepath.Join(dir, "lock"),
	}, nil
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

// RemoteURL is MUSE_CDP_URL: the DevTools address of a Chrome that runs
// already (http://host:port or ws://host:port/devtools/browser/ID). When set,
// muse-mcp attaches to it instead of starting its own Chrome.
func RemoteURL() string {
	return os.Getenv("MUSE_CDP_URL")
}
