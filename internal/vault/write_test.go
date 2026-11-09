package vault

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSaveNotePreservesContentAndMode(t *testing.T) {
	original := "\ufeff---\r\ncustom:\r\n  nested: untouched\r\n---\r\n# 标题\r\n[[另一个笔记]]\r\n"
	v := testVault(t, map[string]string{"笔记/A.md": original})
	name := filepath.Join(v.Path, "笔记/A.md")
	if err := os.Chmod(name, 0600); err != nil {
		t.Fatal(err)
	}
	source := original + "\r\n新内容\r\n"
	saved, err := v.SaveNote("笔记/A.md", []byte(source), Revision([]byte(original)), false)
	if err != nil {
		t.Fatal(err)
	}
	b, info, err := v.ReadNote("笔记/A.md")
	if err != nil || string(b) != source || saved.Revision != Revision(b) || saved.Path != "笔记/A.md" || info.Mode().Perm() != 0600 {
		t.Fatalf("save changed content, revision or permissions: %+v, %v", saved, err)
	}
	assertNoTemporaryFiles(t, filepath.Dir(name))
}

func TestSaveConflictNeverOverwrites(t *testing.T) {
	v := testVault(t, map[string]string{"A.md": "original"})
	revision := Revision([]byte("original"))
	if err := os.WriteFile(filepath.Join(v.Path, "A.md"), []byte("Obsidian update"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := v.SaveNote("A.md", []byte("stale web edit"), revision, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}
	b, _, err := v.ReadNote("A.md")
	if err != nil || string(b) != "Obsidian update" {
		t.Fatal("external content was overwritten")
	}
	if _, err := v.SaveNote("A.md", []byte("new"), "", true); !errors.Is(err, ErrConflict) {
		t.Fatal("create must not overwrite")
	}
	assertNoTemporaryFiles(t, v.Path)
}

func TestConcurrentSavesUseOneRevision(t *testing.T) {
	v := testVault(t, map[string]string{"A.md": "original"})
	var group sync.WaitGroup
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		group.Go(func() {
			_, err := v.SaveNote("A.md", []byte("new"), Revision([]byte("original")), false)
			results <- err
		})
	}
	group.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("expected one winner, got %d", success)
	}
	assertNoTemporaryFiles(t, v.Path)
}

func TestCreateAndWriteBoundaries(t *testing.T) {
	v := testVault(t, map[string]string{"notes/A.md": "original", "locked.md": "locked"})
	saved, err := v.SaveNote("notes/new.md", []byte("# 新笔记\n"), "", true)
	if err != nil || saved.Revision != Revision([]byte("# 新笔记\n")) {
		t.Fatalf("create: %+v, %v", saved, err)
	}
	for _, name := range []string{"../outside.md", "/absolute.md", ".git/config.md", "notes/.hidden.md", "image.png", `notes\A.md`} {
		if _, err := v.SaveNote(name, []byte("must not write"), "", true); !errors.Is(err, ErrInvalidNote) {
			t.Errorf("invalid path allowed: %q, %v", name, err)
		}
	}
	if _, err := v.SaveNote("missing/new.md", []byte("new"), "", true); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing parent: %v", err)
	}
	if _, err := v.SaveNote("missing.md", []byte("new"), Revision(nil), false); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing update: %v", err)
	}
	if _, err := v.SaveNote("notes/A.md", []byte("new"), "", false); !errors.Is(err, ErrInvalidNote) {
		t.Fatal("updates must carry a revision")
	}
	if _, err := v.SaveNote("too-large.md", []byte(strings.Repeat("x", MaxNoteSize+1)), "", true); !errors.Is(err, ErrNoteTooLarge) {
		t.Fatal("size limit not enforced")
	}
	if err := os.Chmod(filepath.Join(v.Path, "locked.md"), 0400); err != nil {
		t.Fatal(err)
	}
	if _, err := v.SaveNote("locked.md", []byte("new"), Revision([]byte("locked")), false); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("read-only file replaced: %v", err)
	}
	assertNoTemporaryFiles(t, v.Path)
}

func TestWritesRejectSymlinks(t *testing.T) {
	v := testVault(t, nil)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "A.md"), []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(v.Path, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "A.md"), filepath.Join(v.Path, "A.md")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		create bool
	}{{"linked/new.md", true}, {"A.md", false}, {"A.md", true}} {
		revision := ""
		if !tc.create {
			revision = Revision([]byte("outside"))
		}
		if _, err := v.SaveNote(tc.name, []byte("unsafe"), revision, tc.create); err == nil {
			t.Fatalf("symlink write allowed: %+v", tc)
		}
	}
	b, err := os.ReadFile(filepath.Join(outside, "A.md"))
	if err != nil || string(b) != "outside" {
		t.Fatal("symlink target changed")
	}
	if _, err := os.Stat(filepath.Join(outside, "new.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("created outside root")
	}
}

func assertNoTemporaryFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".obblog-") {
			t.Fatal("leftover save temporary file")
		}
	}
}
