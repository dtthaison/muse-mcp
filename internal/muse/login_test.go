package muse

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dtthaison/muse-mcp/internal/browser"
	"github.com/dtthaison/muse-mcp/internal/chats"
	"github.com/dtthaison/muse-mcp/internal/fakemuse"
)

const (
	testEmail = "owner.secret@example.com"
	testPIN   = "482915"
)

// syncBuffer is a log sink two goroutines can write to.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// debugChrome starts a headless Chrome with a DevTools port and returns its
// address. The login window of an attached browser is whatever the owner shows,
// so these tests open no window.
func debugChrome(t *testing.T) string {
	t.Helper()
	exe, err := browser.FindChrome()
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	cmd := exec.Command(exe, "--headless=new", "--remote-debugging-port=0", "--user-data-dir="+dir,
		"--no-first-run", "--no-default-browser-check", "--disable-gpu", "about:blank")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() { cmd.Process.Kill(); <-done })
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if f, err := os.Open(filepath.Join(dir, "DevToolsActivePort")); err == nil {
			sc := bufio.NewScanner(f)
			port := ""
			if sc.Scan() {
				port = strings.TrimSpace(sc.Text())
			}
			f.Close()
			if port != "" {
				return "http://127.0.0.1:" + port
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("Chrome did not print its DevTools port")
	return ""
}

type loginFixture struct {
	svc *Service
	srv *fakemuse.Server
	log *syncBuffer
}

// newLoginFixture is a service on an attached headless Chrome, with the fake
// muse showing the email and code login.
func newLoginFixture(t *testing.T) *loginFixture {
	t.Helper()
	spec, err := browser.ParseSpec("cdp:"+debugChrome(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	br, err := browser.New(browser.Options{Spec: spec, LockPath: filepath.Join(dir, "lock")})
	if err != nil {
		t.Fatal(err)
	}
	srv := fakemuse.New()
	srv.SetLoginFlow(testPIN)
	logs := &syncBuffer{}
	svc := New(Options{BaseURL: srv.URL, Browser: br, Chats: chats.Load(filepath.Join(dir, "chats.json")), Timing: fastTiming(),
		Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	t.Cleanup(func() { svc.Close(); br.Close(); srv.Close() })
	return &loginFixture{svc: svc, srv: srv, log: logs}
}

// owner plays the person at the terminal: it answers prompts from a list and
// records what was asked and said.
type owner struct {
	mu      sync.Mutex
	answers []string
	prompts []string
	said    []string
}

func (o *owner) opts(email string) LoginOptions {
	return LoginOptions{Email: email, Ask: o.ask, Say: func(m string) {
		o.mu.Lock()
		o.said = append(o.said, m)
		o.mu.Unlock()
	}}
}

func (o *owner) ask(prompt string) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.prompts = append(o.prompts, prompt)
	if len(o.answers) == 0 {
		return "", io.EOF
	}
	a := o.answers[0]
	o.answers = o.answers[1:]
	return a, nil
}

func (o *owner) asked() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.prompts...)
}

func (o *owner) allSaid() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Join(o.said, "\n")
}

func login(t *testing.T, f *loginFixture, opts LoginOptions) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return f.svc.Login(ctx, opts)
}

func noSecretsInLog(t *testing.T, f *loginFixture) {
	t.Helper()
	logs := f.log.String()
	for _, secret := range []string{testEmail, "owner.secret", testPIN, "000000"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("the log contains %q:\n%s", secret, logs)
		}
	}
}

func assertLoggedIn(t *testing.T, f *loginFixture) {
	t.Helper()
	if st, _ := f.svc.Status(context.Background()); !st.LoggedIn {
		t.Fatalf("status %+v", st)
	}
}

func TestTerminalLoginTypesEmailAndCode(t *testing.T) {
	f := newLoginFixture(t)
	o := &owner{answers: []string{testEmail, testPIN}}
	if err := login(t, f, o.opts("")); err != nil {
		t.Fatal(err)
	}
	if got := f.srv.LoginEmail(); got != testEmail {
		t.Fatalf("the page was given email %q", got)
	}
	if got := f.srv.LoginCodes(); len(got) != 1 || got[0] != testPIN {
		t.Fatalf("the page was given codes %q", got)
	}
	want := []string{"Muse email: ", "PIN code sent to " + testEmail + " (empty = send a new one): "}
	if got := o.asked(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("prompts %q", got)
	}
	assertLoggedIn(t, f)
	noSecretsInLog(t, f)
}

func TestTerminalLoginWithEmailGivenSkipsTheEmailPrompt(t *testing.T) {
	f := newLoginFixture(t)
	o := &owner{answers: []string{" " + testPIN[:3] + " " + testPIN[3:] + " "}} // spaces are dropped
	if err := login(t, f, o.opts(testEmail)); err != nil {
		t.Fatal(err)
	}
	if f.srv.LoginEmail() != testEmail || len(o.asked()) != 1 || !strings.HasPrefix(o.asked()[0], "PIN code sent to "+testEmail) {
		t.Fatalf("email %q, prompts %q", f.srv.LoginEmail(), o.asked())
	}
	if got := f.srv.LoginCodes(); len(got) != 1 || got[0] != testPIN {
		t.Fatalf("codes %q", got)
	}
	noSecretsInLog(t, f)
}

func TestTerminalLoginWrongCodeThenRight(t *testing.T) {
	f := newLoginFixture(t)
	o := &owner{answers: []string{"000000", testPIN}}
	if err := login(t, f, o.opts(testEmail)); err != nil {
		t.Fatal(err)
	}
	if got := f.srv.LoginCodes(); strings.Join(got, ",") != "000000,"+testPIN {
		t.Fatalf("codes %q", got)
	}
	if !strings.Contains(o.allSaid(), "That code isn't right.") || len(o.asked()) != 2 {
		t.Fatalf("said %q, prompts %q", o.allSaid(), o.asked())
	}
	noSecretsInLog(t, f)
}

func TestTerminalLoginEmptyAnswerResendsTheCode(t *testing.T) {
	f := newLoginFixture(t)
	o := &owner{answers: []string{"", testPIN}}
	if err := login(t, f, o.opts(testEmail)); err != nil {
		t.Fatal(err)
	}
	if f.srv.LoginResends() != 1 {
		t.Fatalf("resend clicked %d times", f.srv.LoginResends())
	}
	if got := f.srv.LoginCodes(); len(got) != 1 || got[0] != testPIN || len(o.asked()) != 2 {
		t.Fatalf("codes %q, prompts %q", got, o.asked())
	}
	noSecretsInLog(t, f)
}

// Without a terminal nothing is typed: Login waits for the owner, as before.
func TestLoginWithoutTerminalLeavesThePageAlone(t *testing.T) {
	f := newLoginFixture(t)
	errc := make(chan error, 1)
	go func() { errc <- login(t, f, LoginOptions{}) }()
	time.Sleep(2 * time.Second)
	select {
	case err := <-errc:
		t.Fatalf("returned before the owner logged in: %v", err)
	default:
	}
	if f.srv.LoginEmail() != "" || len(f.srv.LoginCodes()) != 0 {
		t.Fatal("the page was typed into")
	}
	f.srv.SetLoggedOut(false) // the owner finishes by hand
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Login did not notice the login")
	}
	assertLoggedIn(t, f)
}

// An answer that can't be used (no email) hands over to the owner, showing
// what the page says, and Login still ends when the owner logs in.
func TestTerminalLoginFallsBackToTheOwner(t *testing.T) {
	f := newLoginFixture(t)
	o := &owner{answers: []string{""}} // an empty email
	errc := make(chan error, 1)
	go func() { errc <- login(t, f, o.opts("")) }()
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(o.allSaid(), "Finish logging in in the browser window") {
		if time.Now().After(deadline) {
			t.Fatalf("no hand-over message; said %q", o.allSaid())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(o.allSaid(), "Log in or create an account") {
		t.Fatalf("the page text is missing: %q", o.allSaid())
	}
	f.srv.SetLoggedOut(false)
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Login did not notice the login")
	}
}

func TestTerminalLoginGivesUpAfterFiveWrongCodes(t *testing.T) {
	f := newLoginFixture(t)
	o := &owner{answers: []string{"000000", "000000", "000000", "000000", "000000"}}
	errc := make(chan error, 1)
	go func() { errc <- login(t, f, o.opts(testEmail)) }()
	deadline := time.Now().Add(40 * time.Second)
	for !strings.Contains(o.allSaid(), "muse did not accept the code") {
		if time.Now().After(deadline) {
			t.Fatalf("no hand-over message; said %q", o.allSaid())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(f.srv.LoginCodes()) != 5 || len(o.asked()) != 5 {
		t.Fatalf("codes %q, prompts %q", f.srv.LoginCodes(), o.asked())
	}
	f.srv.SetLoggedOut(false)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	noSecretsInLog(t, f)
}
