package config

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"obblog/internal/account"
	"obblog/internal/gitsync"
)

type Config struct {
	Version int    `json:"version"`
	Vault   string `json:"vault"`
	// Retained for older configuration files; managed features are now automatic.
	Writable     bool                `json:"writable"`
	Git          bool                `json:"git"`
	SecureCookie bool                `json:"secureCookie"`
	PasswordHash string              `json:"passwordHash"`
	Credentials  gitsync.Credentials `json:"gitCredentials,omitzero"`
}

func Load(dir string) (*Config, error) {
	name := filepath.Join(dir, "config.json")
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("配置必须是普通文件，不能使用符号链接")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var c Config
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return nil, errors.New("配置文件损坏，请恢复备份")
	}
	var extra any
	if d.Decode(&extra) != io.EOF || c.Version != 1 || !filepath.IsAbs(c.Vault) || !account.Valid(c.PasswordHash) {
		return nil, errors.New("配置文件无效")
	}
	return &c, nil
}

// First install uses an exclusive hard link; subsequent changes use atomic
// replacement. A failed write leaves the previous configuration intact.
func Save(dir string, c *Config, initial bool) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	target := filepath.Join(dir, "config.json")
	if initial {
		return os.Link(name, target)
	}
	return os.Rename(name, target)
}
