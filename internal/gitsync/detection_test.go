package gitsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryRootDetection(t *testing.T) {
	root := t.TempDir()
	if found, err := IsRepositoryRoot(root); found || err != nil {
		t.Fatalf("ordinary directory: %v %v", found, err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if found, err := IsRepositoryRoot(root); !found || err != nil {
		t.Fatal("Git directory not detected")
	}
	child := filepath.Join(root, "notes")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if found, err := IsRepositoryRoot(child); found || err != nil {
		t.Fatal("ancestor checkout incorrectly treated as vault repository")
	}
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /temporary/fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if found, err := IsRepositoryRoot(worktree); !found || err != nil {
		t.Fatal("Git worktree marker not detected")
	}
}
