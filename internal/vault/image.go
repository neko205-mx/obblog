package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxImageSize = 20 << 20

var (
	ErrInvalidImage  = errors.New("unsupported image")
	ErrImageTooLarge = errors.New("image exceeds 20 MiB")
)

func imageExtension(data []byte) string {
	switch http.DetectContentType(data) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	if len(data) >= 16 && string(data[4:8]) == "ftyp" {
		// AVIF's major brand or a compatible brand identifies the container.
		for i := 8; i+4 <= len(data) && i < 64; i += 4 {
			if i != 12 && (string(data[i:i+4]) == "avif" || string(data[i:i+4]) == "avis") {
				return ".avif"
			}
		}
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		if root, ok := token.(xml.StartElement); ok {
			if root.Name.Local == "svg" && (root.Name.Space == "" || root.Name.Space == "http://www.w3.org/2000/svg") {
				return ".svg"
			}
			return ""
		}
	}
}

// ImageName chooses a root-level name safe for Obsidian embeds. Creation still
// checks for collisions atomically, including files created by another process.
func (v *Vault) ImageName(original string, data []byte) (string, error) {
	if len(data) > MaxImageSize {
		return "", ErrImageTooLarge
	}
	ext := imageExtension(data)
	if ext == "" {
		return "", ErrInvalidImage
	}
	original = path.Base(strings.ReplaceAll(original, "\\", "/"))
	stem := strings.TrimSuffix(original, path.Ext(original))
	stem = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`[]|#^:<>"?*%`, r) {
			return '-'
		}
		return r
	}, stem)
	stem = strings.Trim(stem, " .")
	// Leave room for extensions and collision suffixes on common filesystems.
	for len(stem) > 180 {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = stem[:len(stem)-size]
	}
	if stem == "" {
		stem = "Pasted image " + time.Now().Format("20060102150405")
	}
	if ext == ".jpg" && strings.EqualFold(path.Ext(original), ".jpeg") {
		ext = ".jpeg"
	}
	v.writeMu.Lock()
	defer v.writeMu.Unlock()
	snapshot, err := v.Scan()
	if err != nil {
		return "", err
	}
	used := make(map[string]bool)
	for existing := range snapshot.Files {
		used[path.Base(existing)] = true
	}
	for i := 0; i < 10000; i++ {
		name := stem + ext
		if i > 0 {
			name = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		// A same-named image beside the note would shadow a root-level embed.
		if used[name] {
			continue
		}
		if _, err := v.root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			return name, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", ErrConflict
}

func (v *Vault) SaveImage(name string, data []byte) (*SavedNote, error) {
	if len(data) > MaxImageSize {
		return nil, ErrImageTooLarge
	}
	ext := imageExtension(data)
	if !ValidPath(name) || path.Base(name) != name || ext == "" || (path.Ext(name) != ext && !(ext == ".jpg" && path.Ext(name) == ".jpeg")) {
		return nil, ErrInvalidImage
	}
	v.writeMu.Lock()
	defer v.writeMu.Unlock()
	temporary := ".obblog-" + rand.Text() + ".tmp"
	f, err := v.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer func() { f.Close(); v.root.Remove(temporary) }()
	if _, err := f.Write(data); err != nil {
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
	if err := v.root.Link(temporary, name); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return &SavedNote{Path: name, Revision: Revision(data), Modified: info.ModTime()}, nil
}
