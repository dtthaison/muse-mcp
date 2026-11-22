package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "tok-SECRET-9f3a"

// fakeShardX is a fake Launcher API plus a fake DevTools endpoint.
type fakeShardX struct {
	t         *testing.T
	api       *httptest.Server
	cdp       *httptest.Server
	mu        sync.Mutex
	calls     []string
	startBody map[string]any
	profiles  string // raw JSON for GET /profiles
	running   map[string]bool
	cdpNull   bool
	cdpCode   int
}

func newFake(t *testing.T) *fakeShardX {
	f := &fakeShardX{t: t, running: map[string]bool{}, cdpCode: 200}
	f.profiles = `[{"id":"id-1","name":"muse-sg-1"},{"id":"id-2","name":"Muse-US"},{"id":"id-3","name":"muse-us"},{"id":"id-4","name":"fb-1"}]`
	f.cdp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(f.cdpCode)
		fmt.Fprint(w, `{"Browser":"Chrome/1"}`)
	}))
	f.api = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(func() { f.api.Close(); f.cdp.Close() })
	return f
}

func (f *fakeShardX) cdpPort() int {
	_, port, _ := strings.Cut(strings.TrimPrefix(f.cdp.URL, "http://"), ":")
	var n int
	fmt.Sscan(port, &n)
	return n
}

func (f *fakeShardX) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		http.Error(w, "unauthorized", 401)
		return
	}
	cdp := fmt.Sprintf(`{"port":%d,"http_url":"http://127.0.0.1:%d","web_socket_debugger_url":"ws://127.0.0.1:%d/devtools/browser/x"}`, f.cdpPort(), f.cdpPort(), f.cdpPort())
	switch {
	case r.Method == "GET" && r.URL.Path == "/profiles":
		fmt.Fprint(w, f.profiles)
	case r.Method == "GET" && r.URL.Path == "/running":
		var parts []string
		f.mu.Lock()
		for id := range f.running {
			parts = append(parts, fmt.Sprintf(`{"profile_id":%q,"cdp":%s}`, id, cdp))
		}
		f.mu.Unlock()
		fmt.Fprintf(w, "[%s]", strings.Join(parts, ","))
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/start"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/profiles/"), "/start")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.startBody = body
		f.mu.Unlock()
		if f.cdpNull {
			fmt.Fprintf(w, `{"profile_id":%q,"pid":1,"headless":false,"cdp":null,"cdp_error":"debug port refused"}`, id)
			return
		}
		fmt.Fprintf(w, `{"profile_id":%q,"pid":1,"headless":false,"cdp":%s,"cdp_error":null}`, id, cdp)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeShardX) called(substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

// testEnv returns a getenv with a temp config dir and the fake API.
func testEnv(t *testing.T, f *fakeShardX, extra map[string]string) func(string) string {
	t.Helper()
	m := map[string]string{
		"XDG_CONFIG_HOME": t.TempDir(),
		"HOME":            t.TempDir(),
		"SHARDX_API_URL":  f.api.URL,
	}
	if extra == nil {
		m["SHARDX_API_TOKEN"] = testToken
	}
	for k, v := range extra {
		m[k] = v
	}
	return func(k string) string { return m[k] }
}

type result struct {
	code           int
	stdout, stderr string
}

func runEOF(t *testing.T, getenv func(string) string, stdin string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, strings.NewReader(stdin), &out, &errb, getenv)
	return result{code, out.String(), errb.String()}
}

func TestDescribe(t *testing.T) {
	r := runEOF(t, func(string) string { return "" }, "", "--describe")
	if r.code != 0 || strings.TrimSpace(r.stdout) != describeText || strings.Count(r.stdout, "\n") != 1 {
		t.Fatalf("got %+v", r)
	}
}

func TestMatchProfile(t *testing.T) {
	ps := []profile{
		{"id-1", "muse-sg-1"}, {"id-2", "Muse-US"}, {"id-3", "muse-us"}, {"id-4", "fb-1"}, {"muse-sg-1", "other"},
	}
	tests := []struct {
		q       string
		want    string
		wantErr string
	}{
		{"id-4", "id-4", ""},
		{"fb-1", "id-4", ""},
		{"FB-1", "id-4", ""},
		{"muse-us", "id-3", ""}, // exact name wins over case-insensitive
		{"Muse-US", "id-2", ""}, // exact
		{"MUSE-us", "", "ambiguous"},
		{"muse-sg-1", "muse-sg-1", ""}, // exact id beats exact name
		{"nope", "", "no ShardX profile matches"},
	}
	for _, tc := range tests {
		got, err := matchProfile(ps, tc.q)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q: err = %v, want %q", tc.q, err, tc.wantErr)
			}
			continue
		}
		if err != nil || string(got.ID) != tc.want {
			t.Errorf("%q: got %v, %v want %s", tc.q, got, err, tc.want)
		}
	}
	_, err := matchProfile(ps, "MUSE-us")
	if err == nil || !strings.Contains(err.Error(), "id-2") || !strings.Contains(err.Error(), "id-3") {
		t.Errorf("ambiguity should list matches, got %v", err)
	}
}

func TestExtractArrayShapes(t *testing.T) {
	for _, raw := range []string{
		`[{"id":"a","name":"x"}]`,
		`{"profiles":[{"id":"a","name":"x"}]}`,
		`{"items":[{"id":"a","name":"x"}]}`,
		`{"data":[{"id":"a","name":"x"}]}`,
		`{"data":{"items":[{"id":"a","name":"x"}]}}`,
	} {
		f := newFake(t)
		f.profiles = raw
		ps, err := newAPIClient(f.api.URL, testToken).listProfiles(context.Background())
		if err != nil || len(ps) != 1 || ps[0].ID != "a" {
			t.Errorf("%s: %v %v", raw, ps, err)
		}
	}
	f := newFake(t)
	f.profiles = `{"oops":1}`
	if _, err := newAPIClient(f.api.URL, testToken).listProfiles(context.Background()); err == nil {
		t.Error("expected error for unknown shape")
	}
}

func TestProvideStartsProfile(t *testing.T) {
	f := newFake(t)
	r := runEOF(t, testEnv(t, f, nil), "", "muse-sg-1")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %q", r.code, r.stderr)
	}
	want := fmt.Sprintf("http://127.0.0.1:%d\n", f.cdpPort())
	if r.stdout != want {
		t.Fatalf("stdout %q want %q", r.stdout, want)
	}
	if f.called("POST /profiles/id-1/start") != 1 {
		t.Fatalf("calls: %v", f.calls)
	}
	if f.startBody["cdp"] != true || f.startBody["headless"] != false {
		t.Fatalf("start body: %v", f.startBody)
	}
	if f.called("stop") != 0 {
		t.Fatalf("must never stop: %v", f.calls)
	}
}

func TestProvideAlreadyRunning(t *testing.T) {
	f := newFake(t)
	f.running["id-1"] = true
	r := runEOF(t, testEnv(t, f, nil), "", "id-1")
	if r.code != 0 || r.stdout != fmt.Sprintf("http://127.0.0.1:%d\n", f.cdpPort()) {
		t.Fatalf("got %+v", r)
	}
	if f.called("/start") != 0 || f.called("stop") != 0 {
		t.Fatalf("should neither start nor stop: %v", f.calls)
	}
}

func TestProvideHeadlessConfig(t *testing.T) {
	f := newFake(t)
	env := testEnv(t, f, nil)
	dir := filepath.Dir(configPath(env))
	os.MkdirAll(dir, 0o700)
	os.WriteFile(configPath(env), []byte(`{"headless":true}`), 0o600)
	if r := runEOF(t, env, "", "fb-1"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if f.startBody["headless"] != true {
		t.Fatalf("start body: %v", f.startBody)
	}
}

func TestProvideCDPNull(t *testing.T) {
	f := newFake(t)
	f.cdpNull = true
	r := runEOF(t, testEnv(t, f, nil), "", "fb-1")
	if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, "debug port refused") {
		t.Fatalf("got %+v", r)
	}
}

func TestProvideErrorsLeaveStdoutEmpty(t *testing.T) {
	f := newFake(t)
	r := runEOF(t, testEnv(t, f, nil), "", "does-not-exist")
	if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, "no ShardX profile") {
		t.Fatalf("unknown profile: %+v", r)
	}
	r = runEOF(t, testEnv(t, f, nil), "", "MUSE-us")
	if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, "ambiguous") {
		t.Fatalf("ambiguous: %+v", r)
	}
	f.cdpCode = 500
	r = runEOF(t, testEnv(t, f, nil), "", "fb-1")
	if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, "HTTP 500") {
		t.Fatalf("cdp down: %+v", r)
	}
}

func TestBadToken(t *testing.T) {
	f := newFake(t)
	env := testEnv(t, f, map[string]string{"SHARDX_API_TOKEN": "wrong-token-zzz"})
	r := runEOF(t, env, "", "fb-1")
	if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, "401") {
		t.Fatalf("got %+v", r)
	}
	if strings.Contains(r.stderr, "wrong-token-zzz") {
		t.Fatal("token leaked")
	}
}

func TestTokenCommand(t *testing.T) {
	f := newFake(t)
	env := testEnv(t, f, map[string]string{"SHARDX_TOKEN_COMMAND": "printf '  " + testToken + "\\n'"})
	r := runEOF(t, env, "", "fb-1")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.stdout+r.stderr, testToken) {
		t.Fatalf("token leaked: %+v", r)
	}
	// failing command
	env = testEnv(t, f, map[string]string{"SHARDX_TOKEN_COMMAND": "echo oops >&2; exit 3"})
	r = runEOF(t, env, "", "fb-1")
	if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, "token_command failed") {
		t.Fatalf("%+v", r)
	}
}

func TestProvideRunsUntilStdinEOF(t *testing.T) {
	f := newFake(t)
	pr, pw := io.Pipe()
	outR, outW := io.Pipe()
	var errb syncBuf
	done := make(chan int, 1)
	go func() { done <- run(context.Background(), []string{"fb-1"}, pr, outW, &errb, testEnv(t, f, nil)) }()

	line, err := bufio.NewReader(outR).ReadString('\n')
	if err != nil {
		t.Fatalf("read first line: %v (stderr %s)", err, errb.String())
	}
	if line != fmt.Sprintf("http://127.0.0.1:%d\n", f.cdpPort()) {
		t.Fatalf("first line %q", line)
	}
	select {
	case code := <-done:
		t.Fatalf("exited early with %d", code)
	case <-time.After(200 * time.Millisecond):
	}
	pw.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("did not exit on stdin EOF")
	}
}

func TestProvideExitsOnCancel(t *testing.T) {
	f := newFake(t)
	pr, pw := io.Pipe()
	defer pw.Close()
	outR, outW := io.Pipe()
	var errb syncBuf
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"fb-1"}, pr, outW, &errb, testEnv(t, f, nil)) }()
	if _, err := bufio.NewReader(outR).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("did not exit on cancel")
	}
}

func TestBuiltBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain")
	}
	bin := filepath.Join(t.TempDir(), "muse-browser-shardx")
	if out, err := exec.Command(goBin, "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	f := newFake(t)
	xdg := t.TempDir()
	cmd := exec.Command(bin, "fb-1")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg, "SHARDX_API_URL="+f.api.URL, "SHARDX_API_TOKEN="+testToken, "SHARDX_SSH=")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != fmt.Sprintf("http://127.0.0.1:%d\n", f.cdpPort()) {
		cmd.Process.Kill()
		t.Fatalf("line %q err %v stderr %s", line, err, stderr.String())
	}
	stdin.Close()
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("exit: %v (%s)", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("binary did not exit on stdin EOF")
	}
}

func TestList(t *testing.T) {
	f := newFake(t)
	f.running["id-4"] = true
	r := runEOF(t, testEnv(t, f, nil), "", "list")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"NAME", "fb-1", "id-4", "running", "muse-sg-1", "stopped"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("list output missing %q:\n%s", want, r.stdout)
		}
	}
	for _, l := range strings.Split(r.stdout, "\n") {
		if strings.Contains(l, "id-1") && strings.Contains(l, "running") {
			t.Errorf("id-1 should be stopped: %q", l)
		}
	}
}

func TestSSHArgs(t *testing.T) {
	got := strings.Join(sshArgs("root@vps-sing-1", 51000, 40325), " ")
	want := "-N -o BatchMode=yes -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -L 51000:127.0.0.1:40325 root@vps-sing-1"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	for _, bad := range []string{"", "-oProxyCommand=x", "a b"} {
		if validateSSHHost(bad) == nil {
			t.Errorf("host %q should be rejected", bad)
		}
	}
	if validateSSHHost("root@vps-sing-1") != nil {
		t.Error("good host rejected")
	}
}

func TestURLPort(t *testing.T) {
	for in, want := range map[string]int{
		"http://127.0.0.1:40325": 40325, "http://h": 80, "https://h": 443,
	} {
		if got, err := urlPort(in); err != nil || got != want {
			t.Errorf("%s: %d %v", in, got, err)
		}
	}
}

func TestSetupNonInteractive(t *testing.T) {
	f := newFake(t)
	env := testEnv(t, f, nil)
	r := runEOF(t, env, "", "setup", "--api-url", f.api.URL, "--token-command", "printf "+testToken, "--ssh", "")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.stdout, "found 4 profile") {
		t.Fatalf("stdout %q", r.stdout)
	}
	path := configPath(env)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(path))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", di.Mode().Perm())
	}
	var cfg Config
	data, _ := os.ReadFile(path)
	json.Unmarshal(data, &cfg)
	if cfg.APIURL != f.api.URL || cfg.TokenCommand != "printf "+testToken || cfg.Token != "" {
		t.Errorf("config %+v", cfg)
	}
	if strings.Contains(r.stdout+r.stderr, testToken) {
		t.Error("token leaked")
	}
	if _, err := newFlagTokenGuard(env); err != nil {
		t.Error(err)
	}
}

// newFlagTokenGuard asserts setup rejects a positional token.
func newFlagTokenGuard(env func(string) string) (int, error) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"setup", "--token", "x"}, strings.NewReader(""), &out, &errb, env)
	if code == 0 {
		return code, fmt.Errorf("--token flag must not exist")
	}
	return code, nil
}

func TestSetupInteractive(t *testing.T) {
	f := newFake(t)
	env := testEnv(t, f, map[string]string{"SHARDX_API_URL": ""})
	input := f.api.URL + "\n\n" + testToken + "\n"
	r := runEOF(t, env, input, "setup")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.stdout, "found 4 profile") {
		t.Fatalf("stdout %q", r.stdout)
	}
	data, _ := os.ReadFile(configPath(env))
	var cfg Config
	json.Unmarshal(data, &cfg)
	if cfg.Token != testToken || cfg.APIURL != f.api.URL || cfg.SSH != "" {
		t.Errorf("config %+v", cfg)
	}
	// the saved config works without env overrides
	r = runEOF(t, env, "", "fb-1")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestSetupConnectionFailureKeepsConfig(t *testing.T) {
	f := newFake(t)
	env := testEnv(t, f, map[string]string{"SHARDX_API_TOKEN": "bad"})
	r := runEOF(t, env, "", "setup", "--api-url", f.api.URL)
	if r.code == 0 || !strings.Contains(r.stderr, "connection test failed") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(configPath(env)); err != nil {
		t.Errorf("config should be saved: %v", err)
	}
}

func TestUsageErrors(t *testing.T) {
	env := func(string) string { return "" }
	for _, args := range [][]string{{}, {"--bogus"}, {"a", "b"}} {
		r := runEOF(t, env, "", args...)
		if r.code != 2 || r.stdout != "" {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

// muse-mcp runs "muse-browser-shardx -- <profile>", so a profile named like a
// command or a flag is still a profile.
func TestProvideAfterDoubleDash(t *testing.T) {
	f := newFake(t)
	env := testEnv(t, f, nil)
	want := fmt.Sprintf("http://127.0.0.1:%d\n", f.cdpPort())
	if r := runEOF(t, env, "", "--", "fb-1"); r.code != 0 || r.stdout != want {
		t.Fatalf("-- fb-1: %+v", r)
	}
	for _, name := range []string{"list", "setup", "-x", "--describe", "-h"} {
		before := len(f.calls)
		r := runEOF(t, env, "", "--", name)
		if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "no ShardX profile matches") {
			t.Errorf("-- %s was not taken as a profile name: %+v", name, r)
		}
		for _, c := range f.calls[before:] {
			if strings.Contains(c, "/start") {
				t.Errorf("-- %s started something: %v", name, f.calls[before:])
			}
		}
	}
	if _, err := os.Stat(configPath(env)); err == nil {
		t.Error("-- setup ran setup")
	}
}

// A token typed where an argument goes must not be printed back.
func TestUnexpectedArgumentsAreNotEchoed(t *testing.T) {
	f := newFake(t)
	env := testEnv(t, f, nil)
	for _, args := range [][]string{
		{"setup", testToken},
		{"setup", "--ssh", "host", testToken},
		{"setup", "--token=" + testToken},
		{"--token=" + testToken},
		{"-token=" + testToken, "x"},
	} {
		r := runEOF(t, env, "", args...)
		if r.code == 0 {
			t.Errorf("%v: exit 0", args[:1])
		}
		if strings.Contains(r.stdout+r.stderr, testToken) {
			t.Errorf("%v echoed the token:\n%s%s", args[:1], r.stdout, r.stderr)
		}
	}
}
