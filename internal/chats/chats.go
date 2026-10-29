// Package chats remembers which muse chats this server created, so it never
// sends into one of the owner's own chats.
package chats

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Chat is one chat this server created.
type Chat struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
}

type file struct {
	Chats []Chat `json:"chats"`
}

// Registry is safe for concurrent use.
type Registry struct {
	path string
	mu   sync.Mutex
	ids  map[string]time.Time
	lost bool
}

// Load reads path. A missing file is an empty registry; an unreadable or
// corrupt one is empty and Lost, so earlier chat IDs are refused.
func Load(path string) *Registry {
	r := &Registry{path: path, ids: map[string]time.Time{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r
	}
	var f file
	if err != nil || json.Unmarshal(data, &f) != nil {
		r.lost = true
		return r
	}
	for _, e := range f.Chats {
		r.ids[e.ID] = e.Created
	}
	return r
}

// Add records id and writes the file atomically.
func (r *Registry) Add(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reload()
	if _, ok := r.ids[id]; ok {
		return nil
	}
	r.ids[id] = time.Now().UTC()
	return r.save()
}

// reload re-reads the file before a write, so chats another muse-mcp process
// added or removed (the CLI next to a server) aren't overwritten. A file
// that can't be read keeps what is in memory.
func (r *Registry) reload() {
	data, err := os.ReadFile(r.path)
	var f file
	if err != nil || json.Unmarshal(data, &f) != nil {
		return
	}
	r.ids = map[string]time.Time{}
	for _, e := range f.Chats {
		r.ids[e.ID] = e.Created
	}
}

// Remove forgets id (after the chat was deleted) and writes the file.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reload()
	if _, ok := r.ids[id]; !ok {
		return nil
	}
	delete(r.ids, id)
	return r.save()
}

// List returns the chats, oldest first.
func (r *Registry) List() []Chat {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Chat, 0, len(r.ids))
	for id, created := range r.ids {
		out = append(out, Chat{ID: id, Created: created})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

func (r *Registry) Known(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.ids[id]
	return ok
}

// Lost reports that chats.json existed but couldn't be read.
func (r *Registry) Lost() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lost
}

func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ids)
}

func (r *Registry) save() error {
	var f file
	for id, created := range r.ids {
		f.Chats = append(f.Chats, Chat{ID: id, Created: created})
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".chats-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), r.path)
}
