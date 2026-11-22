// Package accounts keeps accounts.json: which muse accounts muse-mcp knows
// and which browser each one uses.
package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dtthaison/muse-mcp/internal/browser"
	"github.com/dtthaison/muse-mcp/internal/config"
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ValidName checks an account name: [a-z0-9][a-z0-9_-]{0,31}.
func ValidName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("bad account name %q: use 1 to 32 lowercase letters, digits, - or _, starting with a letter or digit", name)
	}
	return nil
}

// Account is one muse account's browser: either a spec string ("local",
// "cdp:URL", "NAME:ARG") or a provider command, never both.
type Account struct {
	Browser string   `json:"browser,omitempty"`
	Command []string `json:"command,omitempty"`
}

// Spec parses the account's browser. An account with neither field uses the local Chrome.
func (a Account) Spec() (browser.Spec, error) {
	if a.Browser == "" && len(a.Command) == 0 {
		return browser.Spec{}, nil
	}
	return browser.ParseSpec(a.Browser, a.Command)
}

// FromSpec is the Account that stores spec.
func FromSpec(spec browser.Spec) Account {
	if len(spec.Command) > 0 {
		return Account{Command: spec.Command}
	}
	return Account{Browser: spec.String()}
}

// File is the content of accounts.json.
type File struct {
	Default  string             `json:"default,omitempty"`
	Accounts map[string]Account `json:"accounts"`
}

// DefaultName is the account used when none is named.
func (f File) DefaultName() string {
	if f.Default != "" {
		return f.Default
	}
	return config.DefaultAccount
}

// Get returns the named account. "default" exists without an entry (the
// local Chrome) until accounts.json defines it.
func (f File) Get(name string) (Account, bool) {
	if a, ok := f.Accounts[name]; ok {
		return a, true
	}
	if name == config.DefaultAccount {
		return Account{Browser: "local"}, true
	}
	return Account{}, false
}

// Names lists the known accounts, sorted, including the implicit "default".
func (f File) Names() []string {
	var names []string
	for n := range f.Accounts {
		names = append(names, n)
	}
	if _, ok := f.Accounts[config.DefaultAccount]; !ok {
		names = append(names, config.DefaultAccount)
	}
	sort.Strings(names)
	return names
}

// Resolve turns a name ("" for the default account) into the account's name
// and settings, or an error that lists the known accounts.
func (f File) Resolve(name string) (string, Account, error) {
	if name == "" {
		name = f.DefaultName()
	}
	if a, ok := f.Get(name); ok {
		return name, a, nil
	}
	return "", Account{}, f.Unknown(name)
}

// Unknown is the error for an account that isn't in the file.
func (f File) Unknown(name string) error {
	return fmt.Errorf("unknown muse account %q; known accounts: %s (create one with: muse-mcp login --account %s --browser SPEC)",
		name, strings.Join(f.Names(), ", "), name)
}

// Validate checks every name and browser spec, and that the default exists.
func (f File) Validate() error {
	for name, a := range f.Accounts {
		if err := ValidName(name); err != nil {
			return err
		}
		if a.Browser != "" && len(a.Command) > 0 {
			return fmt.Errorf("account %q has both browser and command; keep one", name)
		}
		if _, err := a.Spec(); err != nil {
			return fmt.Errorf("account %q: %w", name, err)
		}
	}
	if f.Default != "" {
		if err := ValidName(f.Default); err != nil {
			return fmt.Errorf("default account: %w", err)
		}
		if _, ok := f.Get(f.Default); !ok {
			return fmt.Errorf("the default account %q is not in accounts", f.Default)
		}
	}
	return nil
}

// Store reads and writes accounts.json.
type Store struct{ path string }

// Open returns the store for the file at path; the file need not exist.
func Open(path string) *Store { return &Store{path: path} }

// Path is the file's location.
func (s *Store) Path() string { return s.path }

// Load reads the file. A missing file is no accounts: the implicit "default".
// A nil Store is the same.
func (s *Store) Load() (File, error) {
	f := File{Accounts: map[string]Account{}}
	if s == nil {
		return f, nil
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return File{}, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("reading %s: %w", s.path, err)
	}
	if f.Accounts == nil {
		f.Accounts = map[string]Account{}
	}
	if err := f.Validate(); err != nil {
		return File{}, fmt.Errorf("%s: %w", s.path, err)
	}
	return f, nil
}

// Save validates f and writes it (mode 0600) by replacing the file atomically.
func (s *Store) Save(f File) error {
	if err := f.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".accounts-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
