package gitsync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// InitSandbox replaces only an owned, disposable checkout's Git metadata.
// Its origin is another directory in the same sandbox, never the source remote.
func InitSandbox(ctx context.Context, dir, remote string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		return err
	}
	commands := [][]string{
		{"init", "--bare", "--initial-branch=main", remote},
		{"init", "--initial-branch=main", dir},
		{"-C", dir, "add", "--all", "--force", "."},
		{"-C", dir, "commit", "--allow-empty", "-m", "Initialize test vault"},
		{"-C", dir, "remote", "add", "origin", remote},
		{"-C", dir, "push", "-u", "origin", "main"},
	}
	for _, args := range commands {
		base := []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "commit.gpgsign=false", "-c", "user.name=OB Test", "-c", "user.email=obtest@localhost"}
		cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
		cmd.Env = Credentials{}.environment()
		cmd.WaitDelay = time.Second
		if err := cmd.Run(); err != nil {
			return errors.New("无法创建临时 Git 仓库")
		}
	}
	return nil
}
