// Package fakemuse serves an offline stand-in for the muse.ai chat page, for
// tests. See page.html for the scenarios it can act out.
package fakemuse

import (
	"bytes"
	_ "embed"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// flowPage is muse's login as a terminal login sees it: an email step (with a
// "Continue with Google" button that must not be pressed), then a code step
// that refuses every code but the right one. The Continue button stays
// disabled until the page has seen an input event, like a React form.
const flowPage = `<!doctype html><html><body><div id="app"></div><script>
const app = document.getElementById('app');
const post = (path, body) => fetch(path, {method: 'POST', body}).then(r => r.text());
function emailStep() {
  app.innerHTML = '<h1>Muse</h1><p>Log in or create an account</p>' +
    '<input id="email" placeholder="Mobile number or email (required)">' +
    '<button id="google">Continue with Google</button><button id="go" disabled>Continue</button>';
  let seen = '';
  const box = document.getElementById('email'), go = document.getElementById('go');
  box.addEventListener('input', () => { seen = box.value; go.disabled = seen === ''; });
  go.addEventListener('click', async () => { await post('/__login/email', seen); codeStep(); });
}
function codeStep() {
  app.innerHTML = '<p>Enter your code</p><input id="code" inputmode="numeric">' +
    '<button id="submit" disabled>Continue</button><button id="resend">Resend code</button><p id="err"></p>';
  let seen = '';
  const box = document.getElementById('code'), go = document.getElementById('submit'), err = document.getElementById('err');
  box.addEventListener('input', () => { seen = box.value; go.disabled = seen === ''; err.textContent = ''; });
  go.addEventListener('click', async () => {
    if ((await post('/__login/code', seen)) === 'ok') location.href = '/thread/new';
    else err.textContent = "That code isn't right";
  });
  document.getElementById('resend').addEventListener('click', () => post('/__login/resend', ''));
}
emailStep();
setInterval(async () => {
  if ((await (await fetch('/__loggedin')).text()) === 'yes') location.href = '/thread/new';
}, 300);
</script></body></html>`

// Server is an httptest server that answers every path with the fake page.
type Server struct {
	*httptest.Server
	loggedOut atomic.Bool
	noConfirm atomic.Bool

	mu      sync.Mutex
	deleted []string

	flow    bool // the login page is the email and code flow
	goodPIN string
	email   string
	codes   []string
	resends int
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
		if strings.HasPrefix(r.URL.Path, "/__login/") {
			body, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			defer s.mu.Unlock()
			switch r.URL.Path {
			case "/__login/email":
				s.email = string(body)
			case "/__login/resend":
				s.resends++
			case "/__login/code":
				s.codes = append(s.codes, string(body))
				if string(body) == s.goodPIN {
					s.loggedOut.Store(false)
					w.Write([]byte("ok"))
				} else {
					w.Write([]byte("bad"))
				}
			}
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
			s.mu.Lock()
			flow := s.flow
			s.mu.Unlock()
			if flow {
				w.Write([]byte(flowPage))
			} else {
				w.Write([]byte(loginPage))
			}
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

// SetLoginFlow makes the login page the email and code flow, which accepts only
// pin, and logs the page out. The flow records what the page was given.
func (s *Server) SetLoginFlow(pin string) {
	s.mu.Lock()
	s.flow, s.goodPIN = true, pin
	s.mu.Unlock()
	s.loggedOut.Store(true)
}

// LoginEmail is the email the login flow's first step was submitted with.
func (s *Server) LoginEmail() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.email
}

// LoginCodes lists the codes the login flow was given, in order.
func (s *Server) LoginCodes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.codes...)
}

// LoginResends counts the clicks on the login flow's "Resend code".
func (s *Server) LoginResends() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resends
}
