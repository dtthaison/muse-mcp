// Package fakemuse serves an offline stand-in for the muse.ai chat page, for
// tests. See page.html for the scenarios it can act out.
package fakemuse

import (
	_ "embed"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
)

//go:embed page.html
var page []byte

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
}

func New() *Server {
	s := &Server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		w.Write(page)
	}))
	return s
}

// SetLoggedOut makes every page show muse's login screen instead of the chat.
func (s *Server) SetLoggedOut(v bool) { s.loggedOut.Store(v) }
