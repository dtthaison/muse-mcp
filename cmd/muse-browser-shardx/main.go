// Command muse-browser-shardx is a browser provider for muse-mcp: it gives
// muse-mcp a Chrome DevTools (CDP) URL for a ShardX anti-detect browser
// profile, optionally through an SSH tunnel.
//
//	muse-browser-shardx --describe
//	muse-browser-shardx <profile name or id>   # prints the CDP URL, runs until stdin EOF
//	muse-browser-shardx setup | list
//
// It never stops or closes a ShardX profile.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
)

const describeText = "ShardX profiles (local Launcher API or over SSH)"

const usageText = `usage:
  muse-browser-shardx --describe        one-line description
  muse-browser-shardx <profile>         print the DevTools URL, run until stdin closes
  muse-browser-shardx setup [--api-url URL] [--ssh HOST] [--token-command CMD]
  muse-browser-shardx list              profile names, ids and whether they run
  muse-browser-shardx -- <profile>      same as <profile>, for names like "list"

config: $XDG_CONFIG_HOME/muse-browser-shardx/config.json
env:    SHARDX_API_URL SHARDX_API_TOKEN SHARDX_TOKEN_COMMAND SHARDX_SSH
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	var err error
	switch args[0] {
	case "--describe", "-describe":
		fmt.Fprintln(stdout, describeText)
		return 0
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usageText)
		return 0
	case "setup":
		err = cmdSetup(ctx, args[1:], stdin, stdout, stderr, getenv)
	case "list":
		err = cmdList(ctx, stdout, getenv)
	default:
		rest := args
		if args[0] == "--" {
			rest = args[1:]
		} else if strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(stderr, "muse-browser-shardx: unknown option %q\n%s", args[0], usageText)
			return 2
		}
		if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
			fmt.Fprint(stderr, usageText)
			return 2
		}
		err = cmdProvide(ctx, rest[0], stdin, stdout, stderr, getenv)
	}
	if err != nil {
		fmt.Fprintf(stderr, "muse-browser-shardx: %v\n", err)
		return 1
	}
	return 0
}

// session is an API client plus the SSH tunnel it goes through, if any.
type session struct {
	api *apiClient
	tun *tunnel
}

func (s *session) Close() {
	if s.tun != nil {
		s.tun.Close()
		s.tun = nil
	}
}

func openSession(ctx context.Context, cfg Config) (*session, error) {
	token, err := resolveToken(ctx, cfg)
	if err != nil {
		return nil, err
	}
	base := cfg.APIURL
	s := &session{}
	if cfg.SSH != "" {
		port, err := urlPort(cfg.APIURL)
		if err != nil {
			return nil, err
		}
		s.tun, err = openTunnel(ctx, cfg.SSH, port)
		if err != nil {
			return nil, err
		}
		base = fmt.Sprintf("http://127.0.0.1:%d", s.tun.LocalPort)
	}
	s.api = newAPIClient(base, token)
	return s, nil
}

// verifyCDP checks that DevTools answers at base.
func verifyCDP(ctx context.Context, base string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/json/version", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("DevTools not answering at %s: %w", base, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("DevTools at %s/json/version: HTTP %d", base, resp.StatusCode)
	}
	return nil
}

func cmdProvide(ctx context.Context, query string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	sess, err := openSession(ctx, cfg)
	if err != nil {
		return err
	}
	defer sess.Close()

	profiles, err := sess.api.listProfiles(ctx)
	if err != nil {
		return err
	}
	p, err := matchProfile(profiles, query)
	if err != nil {
		return err
	}
	info, err := ensureRunning(ctx, sess.api, p, cfg.Headless, stderr)
	if err != nil {
		return err
	}
	port, err := info.portNumber()
	if err != nil {
		return err
	}

	cdpURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	var cdpTun *tunnel
	if cfg.SSH != "" {
		cdpTun, err = openTunnel(ctx, cfg.SSH, port)
		if err != nil {
			return err
		}
		defer cdpTun.Close()
		cdpURL = fmt.Sprintf("http://127.0.0.1:%d", cdpTun.LocalPort)
	}
	if err := verifyCDP(ctx, cdpURL); err != nil {
		return err
	}
	sess.Close() // the API tunnel is no longer needed

	fmt.Fprintln(stdout, cdpURL)

	eof := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stdin)
		close(eof)
	}()
	var tunDone <-chan struct{}
	if cdpTun != nil {
		tunDone = cdpTun.done
	}
	select {
	case <-eof:
	case <-ctx.Done():
	case <-tunDone:
		return fmt.Errorf("ssh tunnel exited: %s", cdpTun.stderr.String())
	}
	return nil
}

func cmdList(ctx context.Context, stdout io.Writer, getenv func(string) string) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	sess, err := openSession(ctx, cfg)
	if err != nil {
		return err
	}
	defer sess.Close()
	profiles, err := sess.api.listProfiles(ctx)
	if err != nil {
		return err
	}
	running, err := sess.api.listRunning(ctx)
	if err != nil {
		return err
	}
	isRunning := map[flexString]bool{}
	for _, r := range running {
		isRunning[r.ProfileID] = true
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		return strings.ToLower(profiles[i].Name) < strings.ToLower(profiles[j].Name)
	})
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tID\tSTATE")
	for _, p := range profiles {
		state := "stopped"
		if isRunning[p.ID] {
			state = "running"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, string(p.ID), state)
	}
	return tw.Flush()
}
