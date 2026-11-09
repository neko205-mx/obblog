package vault

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var sampleImage = []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="green"/></svg>`)

func TestImageRootNamingAndExclusiveCreation(t *testing.T) {
	v := testVault(t, map[string]string{"notes/photo.svg": "original", "note.md": "unchanged"})
	name, err := v.ImageName("photo.svg", sampleImage)
	if err != nil || name != "photo (1).svg" {
		t.Fatalf("name: %q %v", name, err)
	}
	saved, err := v.SaveImage(name, sampleImage)
	if err != nil || saved.Path != name || saved.Revision != Revision(sampleImage) {
		t.Fatalf("save: %+v %v", saved, err)
	}
	b, err := os.ReadFile(filepath.Join(v.Path, name))
	if err != nil || !bytes.Equal(b, sampleImage) {
		t.Fatal("image bytes changed")
	}
	if _, err := v.SaveImage(name, []byte(`<svg/>`)); !errors.Is(err, ErrConflict) {
		t.Fatalf("overwritten: %v", err)
	}
	if err := os.Symlink(filepath.Join(v.Path, "note.md"), filepath.Join(v.Path, "linked.svg")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.SaveImage("linked.svg", sampleImage); !errors.Is(err, ErrConflict) {
		t.Fatalf("symlink overwritten: %v", err)
	}
	next, err := v.ImageName("linked.svg", sampleImage)
	if err != nil || next != "linked (1).svg" {
		t.Fatalf("symlink collision: %q %v", next, err)
	}
	entries, _ := os.ReadDir(v.Path)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".obblog-") {
			t.Fatal("temporary image leaked")
		}
	}
}

func TestImageNormalizationAndRejection(t *testing.T) {
	v := testVault(t, nil)
	for _, original := range []string{"", "../测试[]|#%.png", `C:\notes\photo.png`, strings.Repeat("图", 200) + ".svg"} {
		name, err := v.ImageName(original, sampleImage)
		if err != nil || !ValidPath(name) || strings.ContainsAny(name, `/\\[]|#%`) || !strings.HasSuffix(name, ".svg") || len(name) > 200 {
			t.Fatalf("invalid name for %q: %q %v", original, name, err)
		}
	}
	for _, name := range []string{"../out.svg", "notes/image.svg", ".hidden.svg", "image.png"} {
		if _, err := v.SaveImage(name, sampleImage); !errors.Is(err, ErrInvalidImage) {
			t.Fatalf("invalid name accepted %q: %v", name, err)
		}
	}
	for _, data := range [][]byte{nil, []byte("not an image"), []byte("<html><svg/></html>")} {
		if _, err := v.ImageName("image.png", data); !errors.Is(err, ErrInvalidImage) {
			t.Fatalf("invalid content accepted: %v", err)
		}
	}
	large := bytes.Repeat([]byte("x"), MaxImageSize+1)
	if _, err := v.ImageName("big.png", large); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("oversize accepted: %v", err)
	}
}
