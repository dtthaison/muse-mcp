package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dtthaison/muse-mcp/internal/accounts"
)

// fakeProviderOnPath installs a muse-browser-fake that describes itself.
func fakeProviderOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = \"--describe\" ]; then echo 'a fake provider'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "muse-browser-fake"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func (h *harness) file(t *testing.T) accounts.File {
	t.Helper()
	f, err := h.accounts.Load()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestWithoutAccountsFileTheDefaultAccountIsUsed(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "status"); code != 0 {
		t.Fatalf("code=%d %q", code, h.errOut.String())
	}
	if !reflect.DeepEqual(h.started, []string{"default"}) {
		t.Fatalf("started %v", h.started)
	}
	if _, err := os.Stat(h.accounts.Path()); err == nil {
		t.Fatal("a command created accounts.json")
	}
}

func TestAccountFlagReachesEveryMuseCommand(t *testing.T) {
	h := newHarness(t)
	if err := h.accounts.Save(accounts.File{Accounts: map[string]accounts.Account{"sg1": {Browser: "local"}}}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"login", "--account", "sg1"},
		{"status", "--account=sg1"},
		{"image", "-o", "x.png", "--account", "sg1", "a fox"},
		{"video", "-o", "x.mp4", "a fox", "--account", "sg1"},
		{"speech", "--account", "sg1", "Hello there."},
		{"chat", "--account", "sg1", "hi"},
		{"chats", "delete", "c1", "--account", "sg1"},
		{"--account", "sg1", "status"}, // before the command name
	} {
		h.started = nil
		if code := h.run("", true, args...); code != 0 {
			t.Fatalf("%v: code=%d stderr=%q", args, code, h.errOut.String())
		}
		if !reflect.DeepEqual(h.started, []string{"sg1"}) {
			t.Errorf("%v: started %v", args, h.started)
		}
	}
	// chats list and forget read the account's own list without starting muse
	h.started, h.chatsFor = nil, nil
	for _, args := range [][]string{{"chats", "--account", "sg1"}, {"chats", "forget", "zzz", "--account", "sg1"}} {
		if code := h.run("", true, args...); code != 0 {
			t.Fatalf("%v: code=%d %q", args, code, h.errOut.String())
		}
	}
	if len(h.started) != 0 || !reflect.DeepEqual(h.chatsFor, []string{"sg1", "sg1"}) {
		t.Fatalf("started %v chatsFor %v", h.started, h.chatsFor)
	}
}

func TestDefaultAccountFromFile(t *testing.T) {
	h := newHarness(t)
	h.accounts.Save(accounts.File{Default: "y", Accounts: map[string]accounts.Account{"y": {Browser: "local"}}})
	if code := h.run("", true, "status"); code != 0 || !reflect.DeepEqual(h.started, []string{"y"}) {
		t.Fatalf("code=%d started=%v", code, h.started)
	}
	// the flag still wins, and "default" still exists
	h.started = nil
	if code := h.run("", true, "status", "--account", "default"); code != 0 || !reflect.DeepEqual(h.started, []string{"default"}) {
		t.Fatalf("code=%d started=%v", code, h.started)
	}
}

func TestUnknownAccountIsRefusedBeforeMuseStarts(t *testing.T) {
	h := newHarness(t)
	h.accounts.Save(accounts.File{Accounts: map[string]accounts.Account{"sg1": {Browser: "local"}}})
	for _, args := range [][]string{{"status", "--account", "nope"}, {"image", "-o", "x.png", "--account", "nope", "a fox"}, {"chats", "--account", "nope"}, {"login", "--account", "nope"}} {
		if code := h.run("", true, args...); code != 1 {
			t.Fatalf("%v: code=%d", args, code)
		}
		if e := h.errOut.String(); !strings.Contains(e, `unknown muse account "nope"`) || !strings.Contains(e, "default, sg1") {
			t.Fatalf("%v: stderr %q", args, e)
		}
	}
	if len(h.started) != 0 {
		t.Fatalf("started %v", h.started)
	}
}

func TestLoginCreatesAccountWithBrowser(t *testing.T) {
	fakeProviderOnPath(t)
	h := newHarness(t)
	if code := h.run("", true, "login", "--account", "sg1", "--browser", "fake:muse-sg-1"); code != 0 {
		t.Fatalf("code=%d %q", code, h.errOut.String())
	}
	f := h.file(t)
	if f.Accounts["sg1"].Browser != "fake:muse-sg-1" || f.Default != "" {
		t.Fatalf("file %+v", f)
	}
	if h.svc.logins != 1 || !reflect.DeepEqual(h.started, []string{"sg1"}) {
		t.Fatalf("logins=%d started=%v", h.svc.logins, h.started)
	}
	// update it to a cdp browser
	if code := h.run("", true, "login", "--account", "sg1", "--browser", "cdp:http://127.0.0.1:9222"); code != 0 {
		t.Fatalf("code=%d %q", code, h.errOut.String())
	}
	if got := h.file(t).Accounts["sg1"].Browser; got != "cdp:http://127.0.0.1:9222" {
		t.Fatalf("browser %q", got)
	}
}

func TestLoginBrowserCmd(t *testing.T) {
	fakeProviderOnPath(t)
	h := newHarness(t)
	if code := h.run("", true, "login", "--account", "y", "--browser-cmd", `muse-browser-fake --flag "two words"`); code != 0 {
		t.Fatalf("code=%d %q", code, h.errOut.String())
	}
	if got := h.file(t).Accounts["y"]; got.Browser != "" || !reflect.DeepEqual(got.Command, []string{"muse-browser-fake", "--flag", "two words"}) {
		t.Fatalf("account %+v", got)
	}
}

func TestLoginBrowserWithoutAccountAppliesToTheDefaultAccount(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "login", "--browser", "cdp:http://127.0.0.1:9222"); code != 0 {
		t.Fatalf("code=%d %q", code, h.errOut.String())
	}
	if got := h.file(t).Accounts["default"].Browser; got != "cdp:http://127.0.0.1:9222" || !reflect.DeepEqual(h.started, []string{"default"}) {
		t.Fatalf("browser %q started %v", got, h.started)
	}
	// with another default account configured, that one is meant
	h.accounts.Save(accounts.File{Default: "sg1", Accounts: map[string]accounts.Account{"sg1": {Browser: "local"}}})
	h.started = nil
	if code := h.run("", true, "login", "--browser", "cdp:http://h:1"); code != 0 {
		t.Fatalf("code=%d %q", code, h.errOut.String())
	}
	if got := h.file(t).Accounts["sg1"].Browser; got != "cdp:http://h:1" || !reflect.DeepEqual(h.started, []string{"sg1"}) {
		t.Fatalf("browser %q started %v", got, h.started)
	}
}

func TestLoginValidatesBeforeSavingOrOpeningBrowsers(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h := newHarness(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--account", "sg1", "--browser", "shardx:x"}, `no browser provider "shardx": install muse-browser-shardx (see README › External browsers), or run muse-mcp browsers to see what you have`},
		{[]string{"--account", "sg1", "--browser", "non sense"}, "unknown browser spec"},
		{[]string{"--account", "sg1", "--browser", "cdp:127.0.0.1:1"}, "not a DevTools address"},
		{[]string{"--account", "Bad", "--browser", "local"}, "bad account name"},
		{[]string{"--account", "sg1", "--browser", "local", "--browser-cmd", "x"}, "not both"},
		{[]string{"--account", "sg1", "--browser-cmd", "no-such-tool-xyz"}, "no-such-tool-xyz"},
		{[]string{"--account", "sg1", "--browser-cmd", `unclosed "quote`}, "unclosed"},
	} {
		if code := h.run("", true, append([]string{"login"}, c.args...)...); code == 0 || !strings.Contains(h.errOut.String(), c.want) {
			t.Errorf("%q: code=%d stderr=%q, want %q", c.args, code, h.errOut.String(), c.want)
		}
	}
	if _, err := os.Stat(h.accounts.Path()); err == nil || len(h.started) != 0 || h.svc.logins != 0 {
		t.Fatalf("a failed login saved or started something (started %v)", h.started)
	}
}

func TestAccountsList(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "accounts"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if out := h.out.String(); !strings.Contains(out, "*  default  local") {
		t.Fatalf("implicit default not shown:\n%s", out)
	}
	h.accounts.Save(accounts.File{Default: "sg1", Accounts: map[string]accounts.Account{
		"sg1": {Browser: "shardx:muse-sg-1"}, "y": {Command: []string{"some-tool", "--flag", "arg"}}}})
	if code := h.run("", true, "accounts"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	out := h.out.String()
	for _, want := range []string{"NAME", "BROWSER", "*  sg1", "shardx:muse-sg-1", "   y", "cmd: some-tool --flag arg", "   default  local"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if len(h.started) != 0 {
		t.Fatal("listing started muse")
	}
}

func TestAccountsDefault(t *testing.T) {
	h := newHarness(t)
	h.accounts.Save(accounts.File{Accounts: map[string]accounts.Account{"sg1": {Browser: "local"}}})
	if code := h.run("", true, "accounts", "default", "sg1"); code != 0 {
		t.Fatalf("code=%d %q", code, h.errOut.String())
	}
	if h.file(t).Default != "sg1" {
		t.Fatalf("file %+v", h.file(t))
	}
	if code := h.run("", true, "accounts", "default", "nope"); code != 1 || !strings.Contains(h.errOut.String(), "unknown muse account") {
		t.Fatalf("code=%d %q", code, h.errOut.String())
	}
	if code := h.run("", true, "accounts", "default"); code != 2 {
		t.Fatalf("code=%d", code)
	}
	if h.file(t).Default != "sg1" {
		t.Fatal("a failed command changed the default")
	}
}

func TestAccountsRemove(t *testing.T) {
	h := newHarness(t)
	h.accounts.Save(accounts.File{Default: "sg1", Accounts: map[string]accounts.Account{
		"sg1": {Browser: "local"}, "y": {Browser: "local"}}})
	if code := h.run("", true, "accounts", "remove", "y"); code != 0 {
		t.Fatalf("code=%d %q", code, h.errOut.String())
	}
	if _, ok := h.file(t).Accounts["y"]; ok || h.file(t).Default != "sg1" {
		t.Fatalf("file %+v", h.file(t))
	}
	if !strings.Contains(h.out.String(), "untouched") {
		t.Fatalf("stdout %q", h.out.String())
	}
	// removing the default account makes "default" the default again
	if code := h.run("", true, "accounts", "remove", "sg1"); code != 0 || h.file(t).Default != "" || !strings.Contains(h.out.String(), "default account is now default") {
		t.Fatalf("code=%d file=%+v out=%q", code, h.file(t), h.out.String())
	}
	if code := h.run("", true, "accounts", "remove", "sg1"); code != 1 || !strings.Contains(h.errOut.String(), "unknown muse account") {
		t.Fatalf("code=%d %q", code, h.errOut.String())
	}
	if code := h.run("", true, "accounts", "remove", "default"); code != 1 || !strings.Contains(h.errOut.String(), "built-in") {
		t.Fatalf("code=%d %q", code, h.errOut.String())
	}
	if len(h.started) != 0 || h.svc.closed {
		t.Fatal("remove touched muse")
	}
}

func TestBrowsersListsBuiltInsAndProviders(t *testing.T) {
	fakeProviderOnPath(t)
	h := newHarness(t)
	if code := h.run("", true, "browsers"); code != 0 {
		t.Fatalf("code=%d %q", code, h.errOut.String())
	}
	out := h.out.String()
	for _, want := range []string{
		"local  Chrome on this computer",
		"(built in)",
		"cdp    a browser at a DevTools address you give",
		"fake   a fake provider",
		"(muse-browser-fake, on PATH)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("browsers lacks %q:\n%s", want, out)
		}
	}
}

func TestUsageMentionsAccounts(t *testing.T) {
	h := newHarness(t)
	h.run("", true, "--help")
	for _, want := range []string{"accounts default N", "accounts remove N", "muse-mcp browsers", "--account NAME", "--browser-cmd", "login [--account N]"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("usage lacks %q", want)
		}
	}
}
