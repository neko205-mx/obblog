package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"obblog/internal/account"
)

func TestAtomicPrivateConfiguration(t *testing.T) {
	dir := t.TempDir()
	hash, err := account.Hash("temporary-config-password")
	if err != nil {
		t.Fatal(err)
	}
	c := &Config{Version: 1, Vault: t.TempDir(), PasswordHash: hash}
	if err = Save(dir, c, true); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	c.Writable = true
	if err = Save(dir, c, true); !os.IsExist(err) {
		t.Fatal("initial install overwrote existing configuration")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("failed install replaced configuration")
	}
	if err = Save(dir, c, false); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil || !loaded.Writable {
		t.Fatal("updated configuration not readable")
	}
	info, _ := os.Stat(filepath.Join(dir, "config.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("configuration is not private")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("temporary configuration file leaked")
	}
}
