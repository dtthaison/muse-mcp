// Package library stores named character and scene references: a fixed
// description plus copies of the reference images, one directory per name.
package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Kind says what a reference pins down.
type Kind string

const (
	Character Kind = "character" // who: face, markings, outfit, proportions
	Scene     Kind = "scene"     // where: layout, objects, lighting, style
)

var (
	ErrNotFound    = errors.New("no saved reference with that name")
	ErrExists      = errors.New("a saved reference with that name already exists (pass replace to overwrite)")
	ErrInvalidName = errors.New("reference names use lowercase letters, digits and dashes, starting with a letter or digit")
	ErrInvalidKind = errors.New(`kind must be "character" or "scene"`)
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Ref is one saved reference. Images are absolute paths inside the library.
type Ref struct {
	Name        string    `json:"name"`
	Kind        Kind      `json:"kind"`
	Description string    `json:"description"`
	Images      []string  `json:"images"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
}

// Store is a library rooted at one directory.
type Store struct{ dir string }

func Open(dir string) *Store { return &Store{dir: dir} }

const refFile = "ref.json"

// Save stores a reference, copying its images in. An existing name needs replace.
func (s *Store) Save(name string, kind Kind, description string, imagePaths []string, replace bool) (Ref, error) {
	if !nameRE.MatchString(name) {
		return Ref{}, fmt.Errorf("%q: %w", name, ErrInvalidName)
	}
	if kind != Character && kind != Scene {
		return Ref{}, fmt.Errorf("%q: %w", kind, ErrInvalidKind)
	}
	if len(imagePaths) == 0 {
		return Ref{}, errors.New("a reference needs at least one image")
	}
	for _, p := range imagePaths {
		if err := CheckImage(p); err != nil {
			return Ref{}, fmt.Errorf("reference image %q: %w", p, err)
		}
	}

	now := time.Now().UTC()
	created := now
	old, err := s.Get(name)
	switch {
	case err == nil && !replace:
		return Ref{}, fmt.Errorf("%q: %w", name, ErrExists)
	case err == nil:
		created = old.Created
	case !errors.Is(err, ErrNotFound):
		return Ref{}, err
	}

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return Ref{}, err
	}
	tmp, err := os.MkdirTemp(s.dir, ".save-"+name+"-")
	if err != nil {
		return Ref{}, err
	}
	defer os.RemoveAll(tmp)

	files := make([]string, len(imagePaths))
	for i, p := range imagePaths {
		files[i] = fmt.Sprintf("%d%s", i+1, strings.ToLower(filepath.Ext(p)))
		if err := copyFile(p, filepath.Join(tmp, files[i])); err != nil {
			return Ref{}, fmt.Errorf("reference image %q: %w", p, err)
		}
	}
	stored := Ref{Name: name, Kind: kind, Description: description, Images: files, Created: created, Updated: now}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return Ref{}, err
	}
	if err := os.WriteFile(filepath.Join(tmp, refFile), data, 0o600); err != nil {
		return Ref{}, err
	}

	final := filepath.Join(s.dir, name)
	if err := os.RemoveAll(final); err != nil {
		return Ref{}, err
	}
	if err := os.Rename(tmp, final); err != nil {
		return Ref{}, err
	}
	return s.Get(name)
}

// Get loads one reference.
func (s *Store) Get(name string) (Ref, error) {
	if !nameRE.MatchString(name) {
		return Ref{}, fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	dir := filepath.Join(s.dir, name)
	data, err := os.ReadFile(filepath.Join(dir, refFile))
	if errors.Is(err, os.ErrNotExist) {
		return Ref{}, fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	if err != nil {
		return Ref{}, err
	}
	var r Ref
	if err := json.Unmarshal(data, &r); err != nil {
		return Ref{}, fmt.Errorf("reading reference %q: %w", name, err)
	}
	for i, f := range r.Images {
		r.Images[i] = filepath.Join(dir, f)
	}
	return r, nil
}

// List returns every reference, sorted by name.
func (s *Store) List() ([]Ref, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var refs []Ref
	for _, e := range entries {
		if !e.IsDir() || !nameRE.MatchString(e.Name()) {
			continue
		}
		r, err := s.Get(e.Name())
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	return refs, nil
}

// Delete removes a reference and its images.
func (s *Store) Delete(name string) error {
	if _, err := s.Get(name); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.dir, name))
}

// CheckImage requires a readable file whose content sniffs as an image.
func CheckImage(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	if ct := http.DetectContentType(head[:n]); !strings.HasPrefix(ct, "image/") {
		return fmt.Errorf("not an image (%s)", ct)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
