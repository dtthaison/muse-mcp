package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesEnvRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MUSE_MCP_HOME", root)

	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		Root:    root,
		Profile: filepath.Join(root, "chrome-profile"),
		Library: filepath.Join(root, "library"),
		Chats:   filepath.Join(root, "chats.json"),
		Voices:  filepath.Join(root, "voices.csv"),
		Log:     filepath.Join(root, "muse-mcp.log"),
		Lock:    filepath.Join(root, "lock"),
	}
	if p != want {
		t.Fatalf("got %+v\nwant %+v", p, want)
	}
	fi, err := os.Stat(p.Library)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("library: dir=%v perm=%v", fi.IsDir(), fi.Mode().Perm())
	}
}

func TestLoadDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUSE_MCP_HOME", "")
	t.Setenv("HOME", home)

	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".muse-mcp"); p.Root != want {
		t.Fatalf("root = %q, want %q", p.Root, want)
	}
}

func TestChromePathOverride(t *testing.T) {
	t.Setenv("MUSE_CHROME", "/x/chrome")
	if got := ChromePath(); got != "/x/chrome" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("MUSE_CHROME", "")
	if got := ChromePath(); got != "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" {
		t.Fatalf("got %q", got)
	}
}

func TestAccountPaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MUSE_MCP_HOME", root)
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if p.AccountsFile() != filepath.Join(root, "accounts.json") {
		t.Fatalf("accounts file %s", p.AccountsFile())
	}
	// "default" keeps the paths of versions before accounts.
	d, err := p.Account("default")
	if err != nil || d.Profile != filepath.Join(root, "chrome-profile") || d.Chats != filepath.Join(root, "chats.json") || d.Lock != filepath.Join(root, "lock") {
		t.Fatalf("default: %+v %v", d, err)
	}
	if _, err := os.Stat(filepath.Join(root, "accounts")); err == nil {
		t.Fatal("the default account created accounts/")
	}
	a, err := p.Account("sg1")
	dir := filepath.Join(root, "accounts", "sg1")
	if err != nil || a.Profile != filepath.Join(dir, "chrome-profile") || a.Chats != filepath.Join(dir, "chats.json") || a.Lock != filepath.Join(dir, "lock") {
		t.Fatalf("sg1: %+v %v", a, err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("account dir: %v", err)
	}
	// library, voices and log stay shared
	if p.Library != filepath.Join(root, "library") || p.Voices != filepath.Join(root, "voices.csv") {
		t.Fatalf("shared paths moved: %+v", p)
	}
}
