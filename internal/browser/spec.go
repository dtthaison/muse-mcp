package browser

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Kind says where an account's browser comes from.
type Kind int

const (
	// KindLocal is Chrome on this computer, started by muse-mcp with its own profile.
	KindLocal Kind = iota
	// KindCDP attaches to a browser at a DevTools address.
	KindCDP
	// KindProvider runs an external provider program that prints the DevTools address.
	KindProvider
)

// ProviderPrefix is the start of a provider executable's name: the browser
// spec "shardx:muse-sg-1" runs "muse-browser-shardx -- muse-sg-1" (the "--" keeps
// an argument such as "setup" or "-x" from being read as a provider command or flag).
const ProviderPrefix = "muse-browser-"

var providerName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Spec says which browser an account uses. Build one with ParseSpec; the zero
// value is the local Chrome.
type Spec struct {
	Kind    Kind
	Name    string   // KindProvider from "name:arg": the provider name
	Arg     string   // KindProvider from "name:arg": its argument
	URL     string   // KindCDP: the DevTools address
	Command []string // KindProvider from a command array
}

// ParseSpec reads a browser spec ("local", "cdp:URL" or "NAME:ARG") or, when
// command is not empty, a provider command. Giving both is an error.
func ParseSpec(browser string, command []string) (Spec, error) {
	if browser != "" && len(command) > 0 {
		return Spec{}, errors.New("give either a browser or a command, not both")
	}
	if len(command) > 0 {
		if command[0] == "" {
			return Spec{}, errors.New("the browser command is empty")
		}
		return Spec{Kind: KindProvider, Command: append([]string(nil), command...)}, nil
	}
	if browser == "" {
		return Spec{}, errors.New("no browser given (use local, cdp:URL or NAME:ARG)")
	}
	if browser == "local" {
		return Spec{Kind: KindLocal}, nil
	}
	name, arg, ok := strings.Cut(browser, ":")
	if !ok {
		if providerName.MatchString(browser) {
			return Spec{}, fmt.Errorf("browser %q needs an argument: write %s:ARG (or local, cdp:URL)", browser, browser)
		}
		return Spec{}, fmt.Errorf("unknown browser spec %q (use local, cdp:URL or NAME:ARG)", browser)
	}
	if !providerName.MatchString(name) {
		return Spec{}, fmt.Errorf("bad browser name %q in %q: use lowercase letters, digits, - and _", name, browser)
	}
	if arg == "" {
		return Spec{}, fmt.Errorf("browser %q has an empty argument after the colon", browser)
	}
	if name == "cdp" {
		if err := CheckDevToolsURL(arg); err != nil {
			return Spec{}, err
		}
		return Spec{Kind: KindCDP, URL: arg}, nil
	}
	if name == "local" {
		return Spec{}, errors.New(`the local browser takes no argument: write "local"`)
	}
	return Spec{Kind: KindProvider, Name: name, Arg: arg}, nil
}

// CheckDevToolsURL says whether s looks like a DevTools address.
func CheckDevToolsURL(s string) error {
	for _, p := range []string{"http://", "https://", "ws://", "wss://"} {
		if strings.HasPrefix(s, p) && len(s) > len(p) {
			return nil
		}
	}
	return fmt.Errorf("%q is not a DevTools address: it must start with http://, https://, ws:// or wss://", s)
}

// String is the spec as it is written in accounts.json and shown to people.
func (s Spec) String() string {
	switch s.Kind {
	case KindCDP:
		return "cdp:" + s.URL
	case KindProvider:
		if len(s.Command) > 0 {
			return "cmd: " + strings.Join(s.Command, " ")
		}
		return s.Name + ":" + s.Arg
	}
	return "local"
}

// Attached reports whether the browser belongs to someone else, so muse-mcp
// connects to it but never closes it.
func (s Spec) Attached() bool { return s.Kind != KindLocal }

// ErrNoProvider is returned (wrapped) when a provider executable is not on PATH.
type ErrNoProvider struct{ Name string }

func (e *ErrNoProvider) Error() string {
	return fmt.Sprintf("no browser provider %q: install %s%s (see README › External browsers), or run muse-mcp browsers to see what you have",
		e.Name, ProviderPrefix, e.Name)
}

// Argv resolves a provider spec to the program and arguments to run,
// looking the program up on PATH. A "NAME:ARG" spec runs the provider as
// "muse-browser-NAME -- ARG"; a command array is run exactly as written.
func (s Spec) Argv() ([]string, error) {
	if s.Kind != KindProvider {
		return nil, errors.New("not a provider browser")
	}
	if len(s.Command) > 0 {
		p, err := exec.LookPath(s.Command[0])
		if err != nil {
			return nil, fmt.Errorf("browser command %q not found: %w", s.Command[0], err)
		}
		return append([]string{p}, s.Command[1:]...), nil
	}
	p, err := exec.LookPath(ProviderPrefix + s.Name)
	if err != nil {
		return nil, &ErrNoProvider{Name: s.Name}
	}
	return []string{p, "--", s.Arg}, nil
}

// Check reports a spec that can't work, such as a provider that isn't installed.
func (s Spec) Check() error {
	if s.Kind == KindProvider {
		_, err := s.Argv()
		return err
	}
	return nil
}

// SplitCommand splits a command line the way a shell would for quotes and
// backslashes, without running a shell: no expansion, pipes or redirects.
func SplitCommand(line string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inWord := false
	var quote rune
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case quote == '"':
			switch {
			case c == '"':
				quote = 0
			case c == '\\' && i+1 < len(rs) && strings.ContainsRune(`"\$`+"`", rs[i+1]):
				i++
				cur.WriteRune(rs[i])
			default:
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case c == '\\':
			if i+1 >= len(rs) {
				return nil, errors.New("the command ends with a backslash")
			}
			i++
			cur.WriteRune(rs[i])
			inWord = true
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("the command has an unclosed %c quote", quote)
	}
	if inWord {
		args = append(args, cur.String())
	}
	if len(args) == 0 {
		return nil, errors.New("the browser command is empty")
	}
	return args, nil
}
