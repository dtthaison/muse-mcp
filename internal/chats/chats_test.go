package chats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestRemovePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chats.json")
	r := Load(path)
	for _, id := range []string{"a", "b"} {
		if err := r.Add(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Remove("a"); err != nil {
		t.Fatal(err)
	}
	re := Load(path)
	if re.Known("a") || !re.Known("b") || re.Count() != 1 {
		t.Fatalf("after remove: a=%v b=%v count=%d", re.Known("a"), re.Known("b"), re.Count())
	}
	if err := r.Remove("missing"); err != nil {
		t.Fatalf("removing an unknown chat: %v", err)
	}
}

func TestListOldestFirst(t *testing.T) {
	r := Load(filepath.Join(t.TempDir(), "chats.json"))
	for _, id := range []string{"first", "second"} {
		if err := r.Add(id); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	got := r.List()
	if len(got) != 2 || got[0].ID != "first" || got[1].ID != "second" || got[0].Created.IsZero() {
		t.Fatalf("list %+v", got)
	}
}
