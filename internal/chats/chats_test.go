package chats

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chats.json")
	r := Load(path)
	if err := r.Add("123e4567-e89b-42d3-a456-426614174000"); err != nil {
		t.Fatal(err)
	}
	if !Load(path).Known("123e4567-e89b-42d3-a456-426614174000") {
		t.Fatal("reloaded registry lost the chat")
	}
}

func TestMissingFileIsEmptyNotLost(t *testing.T) {
	r := Load(filepath.Join(t.TempDir(), "chats.json"))
	if r.Lost() || r.Known("x") || r.Count() != 0 {
		t.Fatalf("lost=%v count=%d", r.Lost(), r.Count())
	}
}

func TestCorruptFileIsLost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chats.json")
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Load(path)
	if !r.Lost() || r.Known("x") {
		t.Fatalf("lost=%v", r.Lost())
	}
	if err := r.Add("new"); err != nil {
		t.Fatal(err)
	}
	if !Load(path).Known("new") {
		t.Fatal("add after corruption not persisted")
	}
}

func TestUnknownID(t *testing.T) {
	r := Load(filepath.Join(t.TempDir(), "chats.json"))
	_ = r.Add("a")
	if r.Known("b") {
		t.Fatal("b should be unknown")
	}
	if r.Count() != 1 {
		t.Fatalf("count %d", r.Count())
	}
}
