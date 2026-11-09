package gitsync

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Credentials struct {
	Username string `json:"username,omitempty"`
	Token    string `json:"token,omitempty"`
}

func (c Credentials) environment() []string {
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -oBatchMode=yes -oConnectTimeout=10")
	if c.Token != "" {
		user := c.Username
		if user == "" {
			user = "x-access-token"
		}
		value := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+c.Token))
		// Scope the header to GitHub HTTPS. Never put credentials in command
		// arguments, repository URLs, Git config files, or error responses.
		env = append(env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader", "GIT_CONFIG_VALUE_0="+value)
	}
	return env
}

func GitHubURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("请填写不含密码的 GitHub HTTPS 仓库地址，如 https://github.com/用户名/仓库.git")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return "", errors.New("GitHub 地址必须包含用户名和仓库名")
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, " \\\t\n\r%") {
			return "", errors.New("无效的 GitHub 仓库路径")
		}
	}
	u.Path = "/" + strings.Join(parts, "/")
	u.RawPath = ""
	return u.String(), nil
}

func Clone(ctx context.Context, remote, branch, destination string, credentials Credentials) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if branch != "" {
		cmd := exec.CommandContext(ctx, "git", "check-ref-format", "--branch", branch)
		if strings.HasPrefix(branch, "-") || cmd.Run() != nil {
			return errors.New("无效的 Git 分支名称")
		}
	}
	args := []string{"-c", "core.hooksPath=/dev/null", "-c", "credential.helper=", "clone", "--no-tags"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, "--", remote, destination)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = credentials.environment()
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return errors.New("克隆失败：请检查仓库地址、分支、网络及 Token 权限（私有库需 Contents 权限）")
	}
	return nil
}
