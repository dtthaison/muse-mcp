package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type apiClient struct {
	base  string
	token string
	hc    *http.Client
}

func newAPIClient(base, token string) *apiClient {
	return &apiClient{
		base:  strings.TrimRight(base, "/"),
		token: token,
		hc:    &http.Client{Timeout: 2 * time.Minute},
	}
}

func (c *apiClient) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet := strings.TrimSpace(string(data))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		hint := ""
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			hint = " (check the API token; run muse-browser-shardx setup)"
		}
		return nil, fmt.Errorf("%s %s: HTTP %d%s: %s", method, path, resp.StatusCode, hint, snippet)
	}
	return data, nil
}

// flexString accepts a JSON string or number.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*f = flexString(v)
		return nil
	}
	*f = flexString(s)
	return nil
}

type profile struct {
	ID   flexString `json:"id"`
	Name string     `json:"name"`
}

type cdpInfo struct {
	Port    int    `json:"port"`
	HTTPURL string `json:"http_url"`
	WS      string `json:"web_socket_debugger_url"`
}

// portNumber returns the DevTools port, from port or http_url.
func (c *cdpInfo) portNumber() (int, error) {
	if c.Port > 0 {
		return c.Port, nil
	}
	if c.HTTPURL != "" {
		if p, err := urlPort(c.HTTPURL); err == nil {
			return p, nil
		}
	}
	return 0, errors.New("ShardX returned a DevTools endpoint without a port")
}

type runningEntry struct {
	ProfileID flexString `json:"profile_id"`
	CDP       *cdpInfo   `json:"cdp"`
}

type startResponse struct {
	ProfileID flexString      `json:"profile_id"`
	CDP       *cdpInfo        `json:"cdp"`
	CDPError  json.RawMessage `json:"cdp_error"`
}

// extractArray finds the list in a bare array or an object wrapping one.
func extractArray(raw []byte, depth int) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0 || string(raw) == "null":
		return json.RawMessage("[]"), nil
	case raw[0] == '[':
		return raw, nil
	case raw[0] == '{' && depth < 3:
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		for _, k := range []string{"profiles", "running", "items", "data", "results"} {
			if v, ok := m[k]; ok {
				return extractArray(v, depth+1)
			}
		}
	}
	return nil, errors.New("unexpected response shape (no list found)")
}

func (c *apiClient) listProfiles(ctx context.Context) ([]profile, error) {
	data, err := c.do(ctx, http.MethodGet, "/profiles", nil)
	if err != nil {
		return nil, err
	}
	arr, err := extractArray(data, 0)
	if err != nil {
		return nil, fmt.Errorf("GET /profiles: %w", err)
	}
	var ps []profile
	if err := json.Unmarshal(arr, &ps); err != nil {
		return nil, fmt.Errorf("GET /profiles: %w", err)
	}
	return ps, nil
}

func (c *apiClient) listRunning(ctx context.Context) ([]runningEntry, error) {
	data, err := c.do(ctx, http.MethodGet, "/running", nil)
	if err != nil {
		return nil, err
	}
	arr, err := extractArray(data, 0)
	if err != nil {
		return nil, fmt.Errorf("GET /running: %w", err)
	}
	var rs []runningEntry
	if err := json.Unmarshal(arr, &rs); err != nil {
		return nil, fmt.Errorf("GET /running: %w", err)
	}
	return rs, nil
}

func (c *apiClient) startProfile(ctx context.Context, id string, headless bool) (*startResponse, error) {
	body := map[string]any{"cdp": true, "headless": headless}
	data, err := c.do(ctx, http.MethodPost, "/profiles/"+url.PathEscape(id)+"/start", body)
	if err != nil {
		return nil, err
	}
	var sr startResponse
	if err := json.Unmarshal(data, &sr); err != nil {
		return nil, fmt.Errorf("start profile: %w", err)
	}
	return &sr, nil
}

func describe(p profile) string {
	return fmt.Sprintf("%q (%s)", p.Name, string(p.ID))
}

// matchProfile picks a profile by exact id, exact name, then
// case-insensitive name. Several matches at one level are an error.
func matchProfile(ps []profile, query string) (profile, error) {
	q := strings.TrimSpace(query)
	levels := []func(profile) bool{
		func(p profile) bool { return string(p.ID) == q },
		func(p profile) bool { return p.Name == q },
		func(p profile) bool { return strings.EqualFold(p.Name, q) },
	}
	for _, match := range levels {
		var found []profile
		for _, p := range ps {
			if match(p) {
				found = append(found, p)
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		}
		var parts []string
		for _, p := range found {
			parts = append(parts, describe(p))
		}
		return profile{}, fmt.Errorf("profile %q is ambiguous, matches: %s (use the id)", q, strings.Join(parts, ", "))
	}
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	msg := fmt.Sprintf("no ShardX profile matches %q", q)
	if len(names) > 0 {
		shown := names
		more := ""
		if len(shown) > 10 {
			shown = shown[:10]
			more = ", ..." + strconv.Itoa(len(names)-10) + " more"
		}
		msg += "; available: " + strings.Join(shown, ", ") + more
	}
	return profile{}, errors.New(msg)
}

// ensureRunning returns the DevTools info for the profile, starting it if it
// is not running yet. It never stops a profile.
func ensureRunning(ctx context.Context, api *apiClient, p profile, headless bool, progress io.Writer) (*cdpInfo, error) {
	running, err := api.listRunning(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range running {
		if r.ProfileID != p.ID {
			continue
		}
		if r.CDP == nil {
			return nil, fmt.Errorf("profile %s is running without a DevTools port; close it in ShardX and try again", describe(p))
		}
		return r.CDP, nil
	}
	fmt.Fprintf(progress, "starting profile %s\n", describe(p))
	sr, err := api.startProfile(ctx, string(p.ID), headless)
	if err != nil {
		return nil, err
	}
	if sr.CDP == nil {
		reason := "no reason given"
		if s := strings.TrimSpace(string(sr.CDPError)); s != "" && s != "null" {
			var str string
			if json.Unmarshal(sr.CDPError, &str) == nil {
				s = str
			}
			reason = s
		}
		return nil, fmt.Errorf("profile %s started but ShardX gave no DevTools endpoint: %s", describe(p), reason)
	}
	return sr.CDP, nil
}
