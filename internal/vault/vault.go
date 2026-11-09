package vault

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const MaxNoteSize = 8 << 20

var (
	ErrAmbiguous    = errors.New("ambiguous link")
	ErrNoteTooLarge = errors.New("note exceeds size limit")
)

type Entry struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Kind     string   `json:"kind"`
	Children []*Entry `json:"children,omitempty"`
}

type Snapshot struct {
	Name        string            `json:"name"`
	Tree        []*Entry          `json:"tree"`
	Notes       []*Entry          `json:"notes"`
	Attachments int               `json:"attachments"`
	Files       map[string]*Entry `json:"-"`
}

type Vault struct {
	root    *os.Root
	Path    string
	Name    string
	writeMu sync.Mutex
}

func Open(dir string) (*Vault, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("open vault: %w", err)
	}
	return &Vault{root: root, Path: abs, Name: filepath.Base(abs)}, nil
}

func (v *Vault) Close() error { return v.root.Close() }

func ValidPath(name string) bool {
	if !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func Kind(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".md":
		return "note"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".pdf",
		".mp3", ".m4a", ".ogg", ".wav", ".flac", ".mp4", ".webm", ".mov":
		return "asset"
	default:
		return ""
	}
}

func (v *Vault) Scan() (*Snapshot, error) {
	s := &Snapshot{Name: v.Name, Tree: []*Entry{}, Notes: []*Entry{}, Files: map[string]*Entry{}}
	folders := map[string]*Entry{".": {Kind: "folder", Children: []*Entry{}}}
	err := fs.WalkDir(v.root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		if !ValidPath(name) || d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		kind := Kind(name)
		if d.IsDir() {
			kind = "folder"
		} else if !d.Type().IsRegular() || kind == "" {
			return nil
		}
		e := &Entry{Name: d.Name(), Path: name, Kind: kind}
		parent := folders[path.Dir(name)]
		parent.Children = append(parent.Children, e)
		if kind == "folder" {
			folders[name] = e
		} else {
			s.Files[name] = e
			if kind == "note" {
				s.Notes = append(s.Notes, e)
			} else {
				s.Attachments++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var sortTree func(*Entry)
	sortTree = func(e *Entry) {
		sort.SliceStable(e.Children, func(i, j int) bool {
			a, b := e.Children[i], e.Children[j]
			if (a.Kind == "folder") != (b.Kind == "folder") {
				return a.Kind == "folder"
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		})
		for _, child := range e.Children {
			if child.Kind == "folder" {
				sortTree(child)
			}
		}
	}
	sortTree(folders["."])
	s.Tree = folders["."].Children
	return s, nil
}

// Root confines all reads to the vault even if a file is replaced with a symlink.
func (v *Vault) OpenFile(name string) (*os.File, error) {
	if !ValidPath(name) || Kind(name) == "" {
		return nil, fs.ErrNotExist
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := v.root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, fs.ErrNotExist
		}
	}
	f, err := v.root.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}

func (v *Vault) ReadNote(name string) ([]byte, fs.FileInfo, error) {
	if Kind(name) != "note" {
		return nil, nil, fs.ErrNotExist
	}
	f, err := v.OpenFile(name)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxNoteSize+1))
	if err == nil && len(b) > MaxNoteSize {
		err = ErrNoteTooLarge
	}
	return b, info, err
}

// Resolve prefers a sibling or an explicit vault path; a short name must be unique.
func (s *Snapshot) Resolve(from, target string) (*Entry, error) {
	return s.resolve(from, target, false)
}

func (s *Snapshot) ResolveWiki(from, target string) (*Entry, error) {
	return s.resolve(from, target, true)
}

func (s *Snapshot) resolve(from, target string, wiki bool) (*Entry, error) {
	if target == "" {
		if e := s.Files[from]; e != nil {
			return e, nil
		}
		return nil, fs.ErrNotExist
	}
	if strings.Contains(target, "\\") || strings.HasPrefix(target, "/") {
		return nil, fs.ErrNotExist
	}
	variants := []string{target}
	if Kind(target) != "note" {
		variants = append(variants, target+".md")
	}
	relative := strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../")
	bases := []string{path.Dir(from), "."}
	if wiki && strings.Contains(target, "/") && !relative {
		bases = []string{".", path.Dir(from)}
	}
	if relative {
		bases = bases[:1]
	}
	for _, base := range bases {
		for _, variant := range variants {
			candidate := path.Clean(path.Join(base, variant))
			if ValidPath(candidate) {
				if e := s.Files[candidate]; e != nil {
					return e, nil
				}
			}
		}
	}
	if relative || !ValidPath(target) {
		return nil, fs.ErrNotExist
	}
	var found *Entry
	for _, e := range s.Files {
		for _, variant := range variants {
			if e.Path == variant || strings.HasSuffix(e.Path, "/"+variant) {
				if found != nil && found.Path != e.Path {
					return nil, ErrAmbiguous
				}
				found = e
			}
		}
	}
	if found == nil {
		return nil, fs.ErrNotExist
	}
	return found, nil
}
