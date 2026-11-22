package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// cmdSetup writes the config file and tests the connection. With any of
// --api-url, --ssh, --token-command it runs without prompts (tokens are never
// taken from argv).
func cmdSetup(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiURL := fs.String("api-url", "", "ShardX Launcher API URL")
	sshHost := fs.String("ssh", "", "ssh host where ShardX runs (empty = local)")
	tokCmd := fs.String("token-command", "", "shell command that prints the API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (tokens are never passed as arguments)", fs.Arg(0))
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	path := configPath(getenv)
	cfg, err := readConfigFile(path)
	if err != nil {
		return err
	}
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}

	if len(given) > 0 {
		if given["api-url"] {
			cfg.APIURL = *apiURL
		}
		if given["ssh"] {
			cfg.SSH = *sshHost
		}
		if given["token-command"] {
			cfg.TokenCommand = *tokCmd
			if *tokCmd != "" {
				cfg.Token = ""
			}
		}
	} else {
		in := bufio.NewReader(stdin)
		ask := func(prompt string) string {
			fmt.Fprint(stdout, prompt)
			line, _ := in.ReadString('\n')
			return strings.TrimSpace(line)
		}
		if v := ask(fmt.Sprintf("ShardX Launcher API URL [%s]: ", cfg.APIURL)); v != "" {
			cfg.APIURL = v
		}
		sshPrompt := "SSH host where ShardX runs (blank = local): "
		if cfg.SSH != "" {
			sshPrompt = fmt.Sprintf("SSH host where ShardX runs [%s] (blank = keep, - = local): ", cfg.SSH)
		}
		switch v := ask(sshPrompt); v {
		case "":
		case "-":
			cfg.SSH = ""
		default:
			cfg.SSH = v
		}
		if isTerminal(stdin) {
			fmt.Fprintln(stdout, "note: the token you type is shown on screen; prefer a token command (Keychain) if that matters")
		}
		if v := ask("API token (blank to use a token command instead): "); v != "" {
			cfg.Token = v
			cfg.TokenCommand = ""
		} else {
			cur := ""
			if cfg.TokenCommand != "" {
				cur = " [" + cfg.TokenCommand + "]"
			}
			if v := ask("Token command, e.g. security find-generic-password -s shardx -w" + cur + ": "); v != "" {
				cfg.TokenCommand = v
				cfg.Token = ""
			}
		}
	}

	if err := validateAPIURL(cfg.APIURL); err != nil {
		return err
	}
	if cfg.SSH != "" {
		if err := validateSSHHost(cfg.SSH); err != nil {
			return err
		}
	}
	if err := saveConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "saved %s\n", path)

	eff, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	sess, err := openSession(ctx, eff)
	if err != nil {
		return fmt.Errorf("config saved, but the connection test failed: %w", err)
	}
	defer sess.Close()
	ps, err := sess.api.listProfiles(ctx)
	if err != nil {
		return fmt.Errorf("config saved, but the connection test failed: %w", err)
	}
	fmt.Fprintf(stdout, "OK: found %d profile(s)\n", len(ps))
	return nil
}
