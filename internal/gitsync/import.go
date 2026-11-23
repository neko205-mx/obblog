package gitsync

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"

	"obblog/internal/vault"
)

// ImportNotes commits only the newly created paths, in one commit. Once files
// have been written we do not roll them back on Git failures. A retry can
// publish identical files after the user synchronizes the repository.
func (s *Service) ImportNotes(ctx context.Context, paths []string, write func() error) error {
	status := s.Status(ctx)
	if !status.CanWrite {
		return errors.New(status.Message)
	}
	if len(paths) == 0 {
		return nil
	}
	wanted := slices.Clone(paths)
	slices.Sort(wanted)
	for i, p := range wanted {
		if !strings.HasPrefix(p, "typecho/") || !vault.ValidPath(p) || vault.Kind(p) != "note" || (i > 0 && p == wanted[i-1]) {
			return errors.New("导入路径无效")
		}
	}
	_, err := s.run(ctx, append([]string{"check-ignore", "--"}, wanted...)...)
	var exit *exec.ExitError
	if err == nil {
		return errors.New("部分导入笔记被 Git 忽略，未写入")
	}
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return errors.New("无法核对 Git 忽略规则，未写入")
	}
	if s.Head(ctx) != status.Head {
		return errors.New("仓库已发生变化，请重新预览")
	}
	if err := write(); err != nil {
		return err
	}
	if _, err := s.run(ctx, append([]string{"add", "--"}, wanted...)...); err != nil {
		return errors.New("笔记已写入，Git 暂存失败；尚未发布，请同步后重新导入")
	}
	staged, err := s.run(ctx, "diff", "--cached", "--name-only", "--no-renames", "-z")
	actual := strings.Split(strings.TrimSuffix(staged, "\x00"), "\x00")
	slices.Sort(actual)
	if err != nil || !slices.Equal(actual, wanted) || s.Head(ctx) != status.Head {
		return errors.New("笔记已写入，暂存区或仓库发生变化；尚未发布，请同步后重新导入")
	}
	if _, err := s.run(ctx, append([]string{"-c", "user.name=OB Vault", "-c", "user.email=obvault@localhost", "commit", "-m", "Import Typecho posts via OB Vault", "--"}, wanted...)...); err != nil {
		return errors.New("笔记已写入，Git 提交失败；尚未发布，请同步后重新导入")
	}
	if _, err := s.run(ctx, "push", s.remote, "HEAD:"+s.ref); err != nil {
		return errors.New("笔记已提交，Git 推送失败；请在 Git 管理中推送后，用同一备份重新导入")
	}
	return nil
}
