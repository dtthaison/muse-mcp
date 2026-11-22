package browser

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseSpec(t *testing.T) {
	cases := []struct {
		in      string
		cmd     []string
		want    Spec
		wantStr string
		err     string
	}{
		{in: "local", want: Spec{Kind: KindLocal}, wantStr: "local"},
		{in: "cdp:http://127.0.0.1:9222", want: Spec{Kind: KindCDP, URL: "http://127.0.0.1:9222"}, wantStr: "cdp:http://127.0.0.1:9222"},
		{in: "cdp:ws://h:1/devtools/browser/abc", want: Spec{Kind: KindCDP, URL: "ws://h:1/devtools/browser/abc"}, wantStr: "cdp:ws://h:1/devtools/browser/abc"},
		{in: "shardx:muse-sg-1", want: Spec{Kind: KindProvider, Name: "shardx", Arg: "muse-sg-1"}, wantStr: "shardx:muse-sg-1"},
		{in: "foo:a:b c", want: Spec{Kind: KindProvider, Name: "foo", Arg: "a:b c"}, wantStr: "foo:a:b c"},
		{cmd: []string{"tool", "--flag", "arg"}, want: Spec{Kind: KindProvider, Command: []string{"tool", "--flag", "arg"}}, wantStr: "cmd: tool --flag arg"},
		{in: "cdp:127.0.0.1:9222", err: "not a DevTools address"},
		{in: "cdp:", err: "empty argument"},
		{in: "shardx", err: "needs an argument"},
		{in: "shardx:", err: "empty argument"},
		{in: "Shardx:x", err: "bad browser name"},
		{in: "local:x", err: "takes no argument"},
		{in: "", err: "no browser given"},
		{in: "x y", err: "unknown browser spec"},
		{in: "local", cmd: []string{"a"}, err: "not both"},
		{cmd: []string{""}, err: "empty"},
	}
	for _, c := range cases {
		got, err := ParseSpec(c.in, c.cmd)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("ParseSpec(%q, %v) error = %v, want %q", c.in, c.cmd, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSpec(%q, %v): %v", c.in, c.cmd, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) || got.String() != c.wantStr {
			t.Errorf("ParseSpec(%q, %v) = %+v (%s), want %+v (%s)", c.in, c.cmd, got, got, c.want, c.wantStr)
		}
	}
}

func TestSpecAttached(t *testing.T) {
	for in, want := range map[string]bool{"local": false, "cdp:http://h:1": true, "x:y": true} {
		s, err := ParseSpec(in, nil)
		if err != nil || s.Attached() != want {
			t.Errorf("%s: attached=%v err=%v", in, s.Attached(), err)
		}
	}
}

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		err  string
	}{
		{in: "some-tool --flag arg", want: []string{"some-tool", "--flag", "arg"}},
		{in: `  tool   "two words"  'single $x'  `, want: []string{"tool", "two words", "single $x"}},
		{in: `tool a\ b "q\"uote" "back\\slash"`, want: []string{"tool", "a b", `q"uote`, `back\slash`}},
		{in: `tool "" x`, want: []string{"tool", "", "x"}},
		{in: `tool pre"mid dle"post`, want: []string{"tool", "pre" + "mid dle" + "post"}},
		{in: `tool $HOME | cat`, want: []string{"tool", "$HOME", "|", "cat"}}, // no shell: nothing expands
		{in: `tool "open`, err: "unclosed"},
		{in: `tool \`, err: "backslash"},
		{in: `   `, err: "empty"},
	}
	for _, c := range cases {
		got, err := SplitCommand(c.in)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("SplitCommand(%q) error = %v, want %q", c.in, err, c.err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitCommand(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestUnknownProviderError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s, _ := ParseSpec("shardx:muse-sg-1", nil)
	_, err := s.Argv()
	want := `no browser provider "shardx": install muse-browser-shardx (see README › External browsers), or run muse-mcp browsers to see what you have`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v\nwant    %s", err, want)
	}
	if s.Check() == nil {
		t.Fatal("Check passed for a missing provider")
	}
	var np *ErrNoProvider
	if !errors.As(err, &np) || np.Name != "shardx" {
		t.Fatalf("not an ErrNoProvider: %v", err)
	}
	c, _ := ParseSpec("", []string{"no-such-tool-xyz", "a"})
	if err := c.Check(); err == nil || !strings.Contains(err.Error(), "no-such-tool-xyz") {
		t.Fatalf("command check: %v", err)
	}
	if l, _ := ParseSpec("local", nil); l.Check() != nil {
		t.Fatal("local spec failed Check")
	}
}
