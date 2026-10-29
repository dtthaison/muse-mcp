// Package fakemuse serves an offline stand-in for the muse.ai chat page, for
// tests. See page.html for the scenarios it can act out.
package fakemuse

import (
	"bytes"
	_ "embed"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
)

//go:embed page.html
var page []byte

// BrokenChat is a chat ID the fake page can't open: like muse.ai for a chat
// that was deleted, it shows "Something went wrong" and no chat switcher.
const BrokenChat = "deadbeef-0000-4000-8000-000000000000"

const brokenPage = `<!doctype html><html><body><div data-testid="chat-empty-loading"></div>
<div><p>Something went wrong</p><button>Try again</button></div>
<div data-hatch-shell-hydration-state="hydrated"><textarea aria-label="Message"></textarea></div></body></html>`

// The login page moves on by itself once the test "logs in" (SetLoggedOut(false)),
// like the real page does after the owner finishes logging in.
const loginPage = `<!doctype html><html><body><h1>Muse</h1><p>Log in or create an account</p>
<input placeholder="Mobile number or email (required)"><button>Continue</button>
<script>setInterval(async () => {
  const r = await fetch('/__loggedin');
  if ((await r.text()) === 'yes') location.href = '/thread/new';
}, 300);</script></body></html>`

// Server is an httptest server that answers every path with the fake page.
type Server struct {
	*httptest.Server
	loggedOut atomic.Bool
	noConfirm atomic.Bool

	mu      sync.Mutex
	deleted []string
}

func New() *Server {
	s := &Server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__deleted" {
			id, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			s.deleted = append(s.deleted, string(id))
			s.mu.Unlock()
			return
		}
		if r.URL.Path == "/__loggedin" {
			if s.loggedOut.Load() {
				w.Write([]byte("no"))
			} else {
				w.Write([]byte("yes"))
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if s.loggedOut.Load() {
			w.Write([]byte(loginPage))
			return
		}
		if r.URL.Path == "/thread/"+BrokenChat {
			w.Write([]byte(brokenPage))
			return
		}
		if s.noConfirm.Load() {
			w.Write(bytes.Replace(page, []byte("const deleteConfirm = true;"), []byte("const deleteConfirm = false;"), 1))
			return
		}
		w.Write(page)
	}))
	return s
}

// SetDeleteConfirm sets whether Delete in a chat's menu asks for confirmation
// first (it does by default).
func (s *Server) SetDeleteConfirm(v bool) { s.noConfirm.Store(!v) }

// Deleted lists the chat IDs deleted through the page, in order.
func (s *Server) Deleted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.deleted...)
}

// SetLoggedOut makes every page show muse's login screen instead of the chat.
func (s *Server) SetLoggedOut(v bool) { s.loggedOut.Store(v) }
