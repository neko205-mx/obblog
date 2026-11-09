package vault

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrConflict    = errors.New("note changed or already exists")
	ErrInvalidNote = errors.New("invalid note path or content")
)

type SavedNote struct {
	Path     string    `json:"path"`
	Revision string    `json:"revision"`
	Modified time.Time `json:"modified"`
}

func Revision(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ValidRevision(revision string) bool {
	decoded, err := hex.DecodeString(revision)
	return err == nil && len(decoded) == sha256.Size
}

// SaveNote serializes web writes and checks the original content again immediately
// before replacing a file. The temporary file is confined to its parent directory.
func (v *Vault) SaveNote(name string, source []byte, expected string, create bool) (*SavedNote, error) {
	if !ValidPath(name) || Kind(name) != "note" || !utf8.Valid(source) || create && expected != "" || !create && !ValidRevision(expected) {
		return nil, ErrInvalidNote
	}
	if len(source) > MaxNoteSize {
		return nil, ErrNoteTooLarge
	}
	v.writeMu.Lock()
	defer v.writeMu.Unlock()
	parts := strings.Split(name, "/")
	for i := 0; i < len(parts)-1; i++ {
		info, err := v.root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrInvalidNote
		}
	}
	parent, err := v.root.OpenRoot(path.Dir(name))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	base := path.Base(name)
	mode := fs.FileMode(0600)
	var original fs.FileInfo
	if create {
		if _, err := parent.Lstat(base); !errors.Is(err, fs.ErrNotExist) {
			if err != nil {
				return nil, err
			}
			return nil, ErrConflict
		}
	} else {
		raw, info, err := readForUpdate(parent, base)
		if err != nil {
			return nil, err
		}
		if Revision(raw) != expected {
			return nil, ErrConflict
		}
		if info.Mode().Perm()&0222 == 0 {
			return nil, fs.ErrPermission
		}
		mode, original = info.Mode().Perm(), info
	}
	temporary := ".obblog-" + rand.Text() + ".tmp"
	f, err := parent.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer func() {
		f.Close()
		parent.Remove(temporary)
	}()
	if _, err := f.Write(source); err != nil {
		return nil, err
	}
	if err := f.Chmod(mode); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if create {
		// Linking a complete temporary file creates the destination atomically and
		// never overwrites an existing file, unlike a plain rename.
		if err := parent.Link(temporary, base); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return nil, ErrConflict
			}
			return nil, err
		}
	} else {
		current, currentInfo, err := readForUpdate(parent, base)
		if err != nil {
			return nil, err
		}
		if Revision(current) != expected || !os.SameFile(original, currentInfo) {
			return nil, ErrConflict
		}
		if err := parent.Rename(temporary, base); err != nil {
			return nil, err
		}
	}
	return &SavedNote{Path: name, Revision: Revision(source), Modified: info.ModTime()}, nil
}

func readForUpdate(root *os.Root, name string) ([]byte, fs.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, ErrInvalidNote
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	openedInfo, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() {
		return nil, nil, ErrConflict
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxNoteSize+1))
	if err == nil && len(raw) > MaxNoteSize {
		err = ErrNoteTooLarge
	}
	return raw, openedInfo, err
}
