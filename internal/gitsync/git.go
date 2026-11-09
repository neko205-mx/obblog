// Package gitsync operates only on an explicitly enabled, existing Git checkout.
package gitsync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"obblog/internal/vault"
)

type Service struct {
	dir, branch, remote, ref string
	credentials              Credentials
}

type Status struct {
	Enabled    bool   `json:"enabled"`
	Branch     string `json:"branch"`
	Head       string `json:"head"`
	RemoteHead string `json:"remoteHead"`
	State      string `json:"state"`
	Message    string `json:"message"`
	CanWrite   bool   `json:"canWrite"`
	CanPush    bool   `json:"canPush"`
}

type Result struct {
	*vault.SavedNote
	GitHead string `json:"gitHead"`
	Synced  bool   `json:"synced"`
	Warning string `json:"warning,omitempty"`
}

// IsRepositoryRoot checks only the chosen directory, never an ancestor checkout.
// A .git file is also valid (Git worktrees use it instead of a directory).
func IsRepositoryRoot(dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// Callers serialize checkout mutations and exclude readers for the transaction.
func Open(dir string) (*Service, error) {
	return OpenWithCredentials(dir, Credentials{})
}

func OpenWithCredentials(dir string, credentials Credentials) (*Service, error) {
	s := &Service{dir: dir, credentials: credentials}
	ctx := context.Background()
	root, err := s.run(ctx, "rev-parse", "--show-toplevel")
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(root)
	if err != nil || want != got {
		return nil, errors.New("Git 模式要求 vault 正好是已有仓库的根目录")
	}
	s.branch, err = s.run(ctx, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return nil, errors.New("Git 模式不支持 detached HEAD")
	}
	s.remote, err = s.run(ctx, "config", "--get", "branch."+s.branch+".remote")
	if err != nil || s.remote == "." || strings.HasPrefix(s.remote, "-") {
		return nil, errors.New("请先在命令行配置当前分支的远端 upstream")
	}
	s.ref, err = s.run(ctx, "config", "--get", "branch."+s.branch+".merge")
	if err != nil || !strings.HasPrefix(s.ref, "refs/heads/") {
		return nil, errors.New("upstream 必须指向远端分支")
	}
	if _, err = s.run(ctx, "check-ref-format", s.ref); err != nil {
		return nil, errors.New("无效的 upstream 分支")
	}
	if s.Head(ctx) == "" {
		return nil, errors.New("仓库还没有初始提交，请选择已有笔记的仓库，或先创建本地空知识库")
	}
	if credentials.Token != "" {
		remote, err := s.run(ctx, "remote", "get-url", s.remote)
		if _, e := GitHubURL(remote); err != nil || e != nil {
			return nil, errors.New("网页 Token 仅支持 GitHub HTTPS remote；SSH 仓库请使用服务器现有凭据")
		}
		push, err := s.run(ctx, "remote", "get-url", "--push", s.remote)
		if _, e := GitHubURL(push); err != nil || e != nil {
			return nil, errors.New("Git push 地址也必须是 GitHub HTTPS 地址")
		}
	}
	return s, nil
}

func (s *Service) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	base := []string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "core.fsmonitor=false", "-C", s.dir}
	if s.credentials.Token != "" {
		base = append(base, "-c", "credential.helper=")
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.WaitDelay = time.Second
	literal := "1"
	// check-ignore takes literal filenames, not Git pathspecs, and rejects
	// the literal magic imposed by GIT_LITERAL_PATHSPECS.
	if len(args) > 0 && args[0] == "check-ignore" {
		literal = "0"
	}
	cmd.Env = append(s.credentials.environment(), "GIT_LITERAL_PATHSPECS="+literal)
	// Never return Git's stderr: remotes may contain credentials or private paths.
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func (s *Service) Head(ctx context.Context) string {
	head, _ := s.run(ctx, "rev-parse", "HEAD")
	return head
}

// Renames returns Git-detected moves, retaining stable public article IDs.
func (s *Service) Renames(ctx context.Context, before, after string) (map[string]string, error) {
	result := map[string]string{}
	if before == after {
		return result, nil
	}
	if before == "" || after == "" {
		return nil, errors.New("无法读取同步版本")
	}
	out, err := s.run(ctx, "diff", "--name-status", "-z", "--find-renames", before, after, "--")
	if err != nil {
		return nil, err
	}
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); {
		status := fields[i]
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if i+2 >= len(fields) {
				return nil, errors.New("无效的 Git 重命名记录")
			}
			if strings.HasPrefix(status, "R") {
				result[fields[i+1]] = fields[i+2]
			}
			i += 3
		} else {
			i += 2
		}
	}
	return result, nil
}

func (s *Service) Status(ctx context.Context) Status {
	st := Status{Enabled: true, Branch: s.branch, Head: s.Head(ctx), State: "blocked"}
	branch, err := s.run(ctx, "symbolic-ref", "--short", "HEAD")
	if err != nil || branch != s.branch || st.Head == "" {
		st.Message = "当前分支已变化，请重启服务核对配置"
		return st
	}
	dirty, err := s.run(ctx, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		st.Message = "无法检查仓库状态"
		return st
	}
	if dirty != "" {
		st.State = "dirty"
		st.Message = "目录有未提交修改，请先提交或处理"
		return st
	}
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply"} {
		location, e := s.run(ctx, "rev-parse", "--git-path", marker)
		if e != nil {
			st.Message = "无法检查 Git 操作状态"
			return st
		}
		if !filepath.IsAbs(location) {
			location = filepath.Join(s.dir, location)
		}
		if _, e = os.Stat(location); !os.IsNotExist(e) {
			st.Message = "仓库有未完成的 Git 操作，请先在命令行处理"
			return st
		}
	}
	out, err := s.run(ctx, "ls-remote", "--exit-code", s.remote, s.ref)
	if err != nil || len(strings.Fields(out)) != 2 {
		st.State = "offline"
		st.Message = "远端连接失败，请检查网络、凭据和 upstream"
		return st
	}
	st.RemoteHead = strings.Fields(out)[0]
	if st.Head == st.RemoteHead {
		st.State = "current"
		st.Message = "本地与远端一致"
		st.CanWrite = true
		return st
	}
	if _, err := s.run(ctx, "merge-base", "--is-ancestor", st.RemoteHead, st.Head); err == nil {
		st.State = "ahead"
		st.Message = "有未推送提交"
		st.CanPush = true
	} else {
		st.State = "changed"
		st.Message = "远端已变化，请拉取更新"
	}
	return st
}

func (s *Service) Sync(ctx context.Context, push bool) (Status, error) {
	st := s.Status(ctx)
	if st.State == "current" {
		return st, nil
	}
	if push {
		if !st.CanPush {
			return st, errors.New(st.Message)
		}
		if _, err := s.run(ctx, "push", s.remote, "HEAD:"+s.ref); err != nil {
			return s.Status(ctx), errors.New("推送失败，请检查远端后重试")
		}
	} else {
		if st.State != "changed" {
			return st, errors.New(st.Message)
		}
		if _, err := s.run(ctx, "fetch", "--no-tags", s.remote, s.ref); err != nil {
			return st, errors.New("拉取失败")
		}
		remote, err := s.run(ctx, "rev-parse", "FETCH_HEAD")
		if err != nil {
			return st, errors.New("无法读取拉取结果")
		}
		if _, err = s.run(ctx, "merge-base", "--is-ancestor", st.Head, remote); err != nil {
			return s.Status(ctx), errors.New("分支已分叉，无法快进拉取")
		}
		// Recheck dirty state because Obsidian may have changed files during fetch.
		dirty, err := s.run(ctx, "status", "--porcelain", "--untracked-files=normal")
		if err != nil || dirty != "" || s.Head(ctx) != st.Head {
			return s.Status(ctx), errors.New("同步期间本地仓库发生变化，已停止")
		}
		if _, err = s.run(ctx, "merge", "--ff-only", remote); err != nil {
			return s.Status(ctx), errors.New("快进失败，请检查仓库状态")
		}
	}
	return s.Status(ctx), nil
}

func (s *Service) Save(ctx context.Context, base, name string, write func() (*vault.SavedNote, error)) (*Result, error) {
	st := s.Status(ctx)
	if !st.CanWrite {
		return nil, errors.New(st.Message)
	}
	if base == "" || base != st.Head {
		return nil, errors.New("编辑基于旧仓库版本，请退出编辑并重新读取笔记")
	}
	flags, err := s.run(ctx, "ls-files", "-v", "--", name)
	if err != nil || (flags != "" && flags[0] != 'H') {
		return nil, errors.New("文件有特殊 Git 索引标记，请先处理")
	}
	_, ignoreErr := s.run(ctx, "check-ignore", "--quiet", "--", name)
	if ignoreErr == nil {
		return nil, errors.New("笔记被 Git 忽略，无法同步保存")
	}
	var exit *exec.ExitError
	if !errors.As(ignoreErr, &exit) || exit.ExitCode() != 1 {
		return nil, errors.New("无法核对 Git 忽略规则，未保存")
	}
	saved, err := write()
	if err != nil {
		return nil, err
	}
	result := &Result{SavedNote: saved, GitHead: st.Head}
	// Once the file is saved, return its revision even if Git fails. Never roll
	// back files or commits: a concurrent device may already have seen them.
	if _, err = s.run(ctx, "add", "--", saved.Path); err != nil {
		result.Warning = "本地已保存，Git 暂存失败"
		return result, nil
	}
	diff, err := s.run(ctx, "diff", "--cached", "--name-only", "-z")
	if err != nil || (diff != "" && diff != saved.Path+"\x00") {
		result.Warning = "本地已保存，暂存区发生变化，未提交"
		return result, nil
	}
	if diff != "" {
		if _, err = s.run(ctx, "-c", "user.name=OB Vault", "-c", "user.email=obvault@localhost", "commit", "-m", "Update note via OB Vault", "--", saved.Path); err != nil {
			result.Warning = "本地已保存，Git 提交失败"
			return result, nil
		}
	}
	result.GitHead = s.Head(ctx)
	if _, err = s.run(ctx, "push", s.remote, "HEAD:"+s.ref); err != nil {
		result.Warning = "本地已提交，推送失败；可在 Git 管理中重试"
		return result, nil
	}
	result.Synced = true
	return result, nil
}
