package vault

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func testVault(t *testing.T, files map[string]string) *Vault {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

func TestScan(t *testing.T) {
	v := testVault(t, map[string]string{
		"欢迎.md": "# 欢迎", "notes/A.md": "A", "notes/B.MD": "B", "empty/.keep": "",
		"assets/image.png": "image", ".git/config": "secret", ".obsidian/settings.md": "secret",
		".secret.md": "secret", "notes/.hidden/private.md": "secret", "config.json": "secret",
	})
	if err := os.Symlink("欢迎.md", filepath.Join(v.Path, "symlink.md")); err != nil {
		t.Fatal(err)
	}
	s, err := v.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Notes) != 3 || s.Attachments != 1 || len(s.Files) != 4 {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
	if s.Tree[0].Kind != "folder" || s.Tree[len(s.Tree)-1].Name != "欢迎.md" {
		t.Fatal("folders should sort before notes")
	}
}

func TestResolve(t *testing.T) {
	v := testVault(t, map[string]string{
		"欢迎.md": "", "notes/A.md": "", "notes/B.md": "", "other/B.md": "", "third/From.md": "",
		"assets/x.png": "", "notes/z.v1.md": "",
	})
	s, err := v.Scan()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ from, target, want string }{
		{"欢迎.md", "A", "notes/A.md"},
		{"notes/A.md", "B", "notes/B.md"},
		{"notes/A.md", "../欢迎.md", "欢迎.md"},
		{"notes/A.md", "other/B", "other/B.md"},
		{"notes/A.md", "", "notes/A.md"},
		{"notes/A.md", "x.png", "assets/x.png"},
		{"欢迎.md", "notes/z.v1", "notes/z.v1.md"},
	} {
		t.Run(tc.target+tc.from, func(t *testing.T) {
			e, err := s.Resolve(tc.from, tc.target)
			if err != nil || e.Path != tc.want {
				t.Fatalf("got %+v, %v; want %s", e, err, tc.want)
			}
		})
	}
	if _, err := s.Resolve("third/From.md", "B"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("expected ambiguity, got %v", err)
	}
	for _, target := range []string{"../../outside", ".git/config", "/etc/passwd", "missing", `notes\A.md`} {
		if _, err := s.Resolve("欢迎.md", target); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("expected missing for %s, got %v", target, err)
		}
	}
}

func TestReadBoundaries(t *testing.T) {
	v := testVault(t, map[string]string{"good.md": "original", ".hidden.md": "secret", "config.json": "secret"})
	out := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(out, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(v.Path, "outside.md")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../outside.md", ".hidden.md", "outside.md", "config.json", "/etc/passwd"} {
		if f, err := v.OpenFile(name); err == nil {
			f.Close()
			t.Errorf("read should be denied: %s", name)
		}
	}
	b, _, err := v.ReadNote("good.md")
	if err != nil || string(b) != "original" {
		t.Fatalf("read note: %q, %v", b, err)
	}
}

func TestScanRefreshAndEmpty(t *testing.T) {
	v := testVault(t, nil)
	s, err := v.Scan()
	if err != nil || s.Tree == nil || s.Notes == nil || len(s.Notes) != 0 {
		t.Fatalf("empty vault: %+v, %v", s, err)
	}
	if err := os.WriteFile(filepath.Join(v.Path, "new.md"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err = v.Scan()
	if err != nil || len(s.Notes) != 1 {
		t.Fatalf("refresh: %+v, %v", s, err)
	}
}

func TestWikiAndMarkdownPathResolution(t *testing.T) {
	v := testVault(t, map[string]string{
		"folder/B.md": "", "notes/folder/B.md": "", "notes/A.md": "", "deep/unique/C.md": "",
	})
	s, err := v.Scan()
	if err != nil {
		t.Fatal(err)
	}
	wiki, err := s.ResolveWiki("notes/A.md", "folder/B")
	if err != nil || wiki.Path != "folder/B.md" {
		t.Fatalf("explicit wiki paths should prefer vault root: %+v, %v", wiki, err)
	}
	link, err := s.Resolve("notes/A.md", "folder/B.md")
	if err != nil || link.Path != "notes/folder/B.md" {
		t.Fatalf("Markdown paths should prefer source directory: %+v, %v", link, err)
	}
	suffix, err := s.ResolveWiki("notes/A.md", "unique/C")
	if err != nil || suffix.Path != "deep/unique/C.md" {
		t.Fatalf("unique path suffix should resolve: %+v, %v", suffix, err)
	}
}
