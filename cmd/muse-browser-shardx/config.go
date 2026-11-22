package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultAPIURL = "http://127.0.0.1:40325"

// Config is the on-disk configuration (config.json).
type Config struct {
	APIURL       string `json:"api_url"`
	Token        string `json:"token,omitempty"`
	TokenCommand string `json:"token_command,omitempty"`
	SSH          string `json:"ssh,omitempty"`
	Headless     bool   `json:"headless"`
}

// configPath returns $XDG_CONFIG_HOME/muse-browser-shardx/config.json or
// ~/.config/muse-browser-shardx/config.json.
func configPath(getenv func(string) string) string {
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		home := getenv("HOME")
		if home == "" {
			if h, err := os.UserHomeDir(); err == nil {
				home = h
			}
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "muse-browser-shardx", "config.json")
}

// readConfigFile reads the config file; a missing file gives a zero Config.
func readConfigFile(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// loadConfig reads the file, applies env overrides and defaults.
func loadConfig(getenv func(string) string) (Config, error) {
	cfg, err := readConfigFile(configPath(getenv))
	if err != nil {
		return cfg, err
	}
	if v := getenv("SHARDX_API_URL"); v != "" {
		cfg.APIURL = v
	}
	if v := getenv("SHARDX_API_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := getenv("SHARDX_TOKEN_COMMAND"); v != "" {
		cfg.TokenCommand = v
	}
	if v := getenv("SHARDX_SSH"); v != "" {
		cfg.SSH = v
	}
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}
	if err := validateAPIURL(cfg.APIURL); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func validateAPIURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("invalid api_url %q (want http://host:port)", s)
	}
	return nil
}

// saveConfig writes the config with mode 0600 in a 0700 directory.
func saveConfig(path string, cfg Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// urlPort returns the port of a URL, defaulting by scheme.
func urlPort(raw string) (int, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return 0, err
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return 0, fmt.Errorf("bad port in %q", raw)
		}
		return n, nil
	}
	switch u.Scheme {
	case "https":
		return 443, nil
	case "http":
		return 80, nil
	}
	return 0, fmt.Errorf("no port in %q", raw)
}

// resolveToken returns the configured token, or runs token_command. An empty
// result is allowed (no Authorization header is sent). The token is never
// logged or put on a command line.
func resolveToken(ctx context.Context, cfg Config) (string, error) {
	if cfg.Token != "" {
		return strings.TrimSpace(cfg.Token), nil
	}
	if cfg.TokenCommand == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", cfg.TokenCommand)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		if msg != "" {
			return "", fmt.Errorf("token_command failed: %v: %s", err, msg)
		}
		return "", fmt.Errorf("token_command failed: %v", err)
	}
	tok := strings.TrimSpace(out.String())
	if tok == "" {
		return "", errors.New("token_command printed nothing")
	}
	return tok, nil
}
