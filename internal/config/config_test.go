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
