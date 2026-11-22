package accounts

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dtthaison/muse-mcp/internal/browser"
)

func TestValidName(t *testing.T) {
	for _, ok := range []string{"a", "sg1", "default", "a-b_c", "0x", strings.Repeat("a", 32)} {
		if err := ValidName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Sg1", "-a", "_a", "a b", "a/b", "..", strings.Repeat("a", 33), "é"} {
		if ValidName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestMissingFileIsTheImplicitDefaultAccount(t *testing.T) {
	st := Open(filepath.Join(t.TempDir(), "accounts.json"))
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	name, acc, err := f.Resolve("")
	if err != nil || name != "default" {
		t.Fatalf("Resolve: %q %v", name, err)
	}
	spec, err := acc.Spec()
	if err != nil || spec.Kind != browser.KindLocal || spec.String() != "local" {
		t.Fatalf("spec %+v %v", spec, err)
	}
	if !reflect.DeepEqual(f.Names(), []string{"default"}) {
		t.Fatalf("names %v", f.Names())
	}
	if _, _, err := f.Resolve("sg1"); err == nil || !strings.Contains(err.Error(), "known accounts: default") {
		t.Fatalf("unknown account error: %v", err)
	}
	// A nil store behaves the same.
	var none *Store
	if f, err := none.Load(); err != nil || f.DefaultName() != "default" {
		t.Fatalf("nil store: %v", err)
	}
	if _, err := os.Stat(st.Path()); err == nil {
		t.Fatal("Load created the file")
	}
}

func TestSaveLoadRoundTripAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "accounts.json")
	st := Open(path)
	f := File{Default: "sg1", Accounts: map[string]Account{
		"sg1": {Browser: "shardx:muse-sg-1"},
		"y":   {Command: []string{"some-tool", "--flag", "arg"}},
	}}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v mode %v", err, fi.Mode())
	}
	got, err := st.Load()
	if err != nil || !reflect.DeepEqual(got, f) {
		t.Fatalf("got %+v err %v", got, err)
	}
	raw, _ := os.ReadFile(path)
	for _, want := range []string{`"default": "sg1"`, `"browser": "shardx:muse-sg-1"`, `"command": [`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("file lacks %s:\n%s", want, raw)
		}
	}
	// default is sg1 and "default" still exists implicitly
	if name, _, _ := got.Resolve(""); name != "sg1" {
		t.Fatalf("default resolves to %q", name)
	}
	if !reflect.DeepEqual(got.Names(), []string{"default", "sg1", "y"}) {
		t.Fatalf("names %v", got.Names())
	}
	// no temp files left behind
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("directory has %d entries", len(entries))
	}
}

func TestLoadReadsTheDocumentedFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	os.WriteFile(path, []byte(`{"default":"sg1","accounts":{"sg1":{"browser":"shardx:muse-sg-1"},"y":{"command":["some-tool","--flag","arg"]}}}`), 0o600)
	f, err := Open(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := f.Accounts["y"].Spec()
	if spec.Kind != browser.KindProvider || !reflect.DeepEqual(spec.Command, []string{"some-tool", "--flag", "arg"}) {
		t.Fatalf("y: %+v", spec)
	}
	spec, _ = f.Accounts["sg1"].Spec()
	if spec.Name != "shardx" || spec.Arg != "muse-sg-1" {
		t.Fatalf("sg1: %+v", spec)
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]struct {
		f   File
		err string
	}{
		"bad name":               {File{Accounts: map[string]Account{"Bad Name": {Browser: "local"}}}, "bad account name"},
		"bad spec":               {File{Accounts: map[string]Account{"a": {Browser: "cdp:nope"}}}, `account "a"`},
		"both":                   {File{Accounts: map[string]Account{"a": {Browser: "local", Command: []string{"x"}}}}, "both browser and command"},
		"unknown default":        {File{Default: "zz", Accounts: map[string]Account{"a": {Browser: "local"}}}, `default account "zz"`},
		"bad default":            {File{Default: "Z!", Accounts: map[string]Account{}}, "default account"},
		"implicit default ok":    {File{Default: "default", Accounts: map[string]Account{}}, ""},
		"empty account is local": {File{Accounts: map[string]Account{"a": {}}}, ""},
	}
	for name, c := range cases {
		err := c.f.Validate()
		if c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%s: error %v, want %q", name, err, c.err)
		}
	}
	if err := Open(filepath.Join(t.TempDir(), "a.json")).Save(File{Accounts: map[string]Account{"X": {}}}); err == nil {
		t.Error("Save accepted a bad name")
	}
}

func TestLoadRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"junk.json":    `{not json`,
		"badname.json": `{"accounts":{"Bad":{"browser":"local"}}}`,
		"badspec.json": `{"accounts":{"a":{"browser":"shardx"}}}`,
	} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(content), 0o600)
		if _, err := Open(path).Load(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: error %v", name, err)
		}
	}
}

func TestExplicitDefaultEntryOverridesTheImplicitOne(t *testing.T) {
	f := File{Accounts: map[string]Account{"default": {Browser: "cdp:http://127.0.0.1:9222"}}}
	_, acc, err := f.Resolve("default")
	if err != nil || acc.Browser != "cdp:http://127.0.0.1:9222" {
		t.Fatalf("%+v %v", acc, err)
	}
	if !reflect.DeepEqual(f.Names(), []string{"default"}) {
		t.Fatalf("names %v", f.Names())
	}
}

func TestFromSpec(t *testing.T) {
	s, _ := browser.ParseSpec("shardx:a", nil)
	if a := FromSpec(s); a.Browser != "shardx:a" || a.Command != nil {
		t.Fatalf("%+v", a)
	}
	s, _ = browser.ParseSpec("", []string{"t", "x y"})
	if a := FromSpec(s); a.Browser != "" || !reflect.DeepEqual(a.Command, []string{"t", "x y"}) {
		t.Fatalf("%+v", a)
	}
}
