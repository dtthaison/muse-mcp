// Package voices searches muse's text-to-speech voice catalog. muse-mcp does
// not ship the catalog: the owner asks muse for it as a CSV (name, gender,
// accent, tone, description) and imports it.
package voices

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoCatalog means no catalog has been imported yet.
var ErrNoCatalog = errors.New(`no voice catalog yet: ask muse for its voice catalog as a CSV file (name, gender, accent, tone, description), then run "muse-mcp voices import FILE"`)

// Voice is one catalog entry. Gender is F, M or N.
type Voice struct {
	Name        string `json:"name"`
	Gender      string `json:"gender,omitempty"`
	Accent      string `json:"accent,omitempty"`
	Tone        string `json:"tone,omitempty"`
	Description string `json:"description,omitempty"`
}

// Query filters the catalog; empty fields match everything.
type Query struct {
	Text   string // every word must appear in the name, accent, tone or description
	Gender string // female/F, male/M or neutral/N
	Accent string // substring, e.g. "british"
	Tone   string // substring, e.g. "warm"
	Limit  int    // default 20, at most 200
}

// Catalog is the imported catalog file.
type Catalog struct{ path string }

func Open(path string) *Catalog { return &Catalog{path: path} }

// Import checks that src is a voice catalog CSV and copies it into place.
// It returns the number of voices.
func (c *Catalog) Import(src string) (int, error) {
	f, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	vs, err := parse(f)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", src, err)
	}
	if len(vs) == 0 {
		return 0, fmt.Errorf("%s has no voices", src)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".voices-*.csv")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, f); err != nil {
		tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	return len(vs), os.Rename(tmp.Name(), c.path)
}

// Search returns the voices matching q, in catalog order, and how many matched
// before the limit.
func (c *Catalog) Search(q Query) ([]Voice, int, error) {
	gender, err := normGender(q.Gender)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNoCatalog
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	vs, err := parse(f)
	if err != nil {
		return nil, 0, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 200)
	words := strings.Fields(strings.ToLower(q.Text))
	var out []Voice
	total := 0
	for _, v := range vs {
		if gender != "" && v.Gender != gender ||
			!contains(v.Accent, q.Accent) || !contains(v.Tone, q.Tone) {
			continue
		}
		hay := strings.ToLower(strings.Join([]string{v.Name, v.Accent, v.Tone, v.Description}, " "))
		if !all(words, func(w string) bool { return strings.Contains(hay, w) }) {
			continue
		}
		total++
		if len(out) < limit {
			out = append(out, v)
		}
	}
	return out, total, nil
}

func parse(r io.Reader) ([]Voice, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("reading the CSV header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
	}
	if _, ok := col["name"]; !ok {
		return nil, errors.New(`not a voice catalog: the CSV needs a "name" column (and gender, accent, tone, description)`)
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var vs []Voice
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			return vs, nil
		}
		if err != nil {
			return nil, err
		}
		v := Voice{Name: get(rec, "name"), Gender: strings.ToUpper(get(rec, "gender")), Accent: get(rec, "accent"),
			Tone: get(rec, "tone"), Description: get(rec, "description")}
		if v.Name != "" {
			vs = append(vs, v)
		}
	}
}

func normGender(g string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(g)) {
	case "":
		return "", nil
	case "f", "female", "feminine", "woman":
		return "F", nil
	case "m", "male", "masculine", "man":
		return "M", nil
	case "n", "neutral":
		return "N", nil
	}
	return "", fmt.Errorf("gender %q: use female, male or neutral", g)
}

func contains(field, want string) bool {
	return strings.Contains(strings.ToLower(field), strings.ToLower(strings.TrimSpace(want)))
}

func all(words []string, ok func(string) bool) bool {
	for _, w := range words {
		if !ok(w) {
			return false
		}
	}
	return true
}
