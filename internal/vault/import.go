package vault

import (
	"errors"
	"io/fs"
)

// CheckTypechoDirectory validates only the reserved migration directory. It
// may be absent for a new installation; a file or symlink is never accepted.
func (v *Vault) CheckTypechoDirectory(create bool) error {
	if create {
		v.writeMu.Lock()
		defer v.writeMu.Unlock()
	}
	info, err := v.root.Lstat("typecho")
	if errors.Is(err, fs.ErrNotExist) {
		if !create {
			return nil
		}
		if err := v.root.Mkdir("typecho", 0755); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, err = v.root.Lstat("typecho")
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ErrConflict
	}
	return nil
}

// CheckNoteDestination distinguishes an absent note from an existing entity
// that ReadNote intentionally hides (for example a symlink or a directory).
func (v *Vault) CheckNoteDestination(name string) (bool, error) {
	if !ValidPath(name) || Kind(name) != "note" {
		return false, ErrInvalidNote
	}
	info, err := v.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return true, ErrConflict
	}
	return true, nil
}
