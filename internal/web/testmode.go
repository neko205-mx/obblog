package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"obblog/internal/gitsync"
	"obblog/internal/vault"
)

// OpenTestRuntime never opens the persistent installation directory.
func OpenTestRuntime(source string) (*Runtime, error) {
	if source != "" {
		var err error
		source, err = filepath.Abs(source)
		if err != nil {
			return nil, err
		}
	}
	dir, err := os.MkdirTemp("", "obblog-test-")
	if err != nil {
		return nil, err
	}
	rt, err := OpenRuntime(dir)
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	rt.testMode = true
	rt.sourceVault = source
	rt.stop = make(chan struct{})
	return rt, nil
}

func (rt *Runtime) Done() <-chan struct{} { return rt.stop }

func (rt *Runtime) testAPI(w http.ResponseWriter, r *http.Request) {
	if !rt.testMode {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path != "/admin/api/test/stop" || r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.closed {
		fail(w, 503, "测试已结束")
		return
	}
	if rt.app != nil && !rt.app.authenticated(r) {
		fail(w, 401, "请先登录后台")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "obvault_test_session", Path: "/admin/", MaxAge: -1, HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode})
	respond(w, map[string]bool{"ok": true})
	rt.stopOnce.Do(func() { close(rt.stop) })
}

func (rt *Runtime) copyTestVault(ctx context.Context, source string) (string, func(), error) {
	container, err := os.MkdirTemp(rt.dir, "vault-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.RemoveAll(container) }
	destination := filepath.Join(container, "notes")
	keep := false
	defer func() {
		if !keep {
			cleanup()
		}
	}()
	if err := os.Mkdir(destination, 0700); err != nil {
		return "", cleanup, err
	}
	v, err := vault.Open(source)
	if err != nil {
		return "", cleanup, err
	}
	defer v.Close()
	// Prevent copying the active sandbox into itself if TMPDIR is inside source.
	rel, err := filepath.Rel(v.Path, rt.dir)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", cleanup, errors.New("测试临时目录不能位于来源知识库内")
	}
	snapshot, err := v.Scan()
	if err != nil {
		return "", cleanup, err
	}
	var copyEntries func([]*vault.Entry) error
	copyEntries = func(entries []*vault.Entry) error {
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := filepath.Join(destination, filepath.FromSlash(entry.Path))
			if entry.Kind == "folder" {
				if err := os.Mkdir(name, 0700); err != nil {
					return err
				}
				if err := copyEntries(entry.Children); err != nil {
					return err
				}
				continue
			}
			if err := copyTestFile(ctx, v, entry.Path, name); err != nil {
				return err
			}
		}
		return nil
	}
	if err := copyEntries(snapshot.Tree); err != nil {
		return "", cleanup, err
	}
	repository, err := gitsync.IsRepositoryRoot(v.Path)
	if err != nil {
		return "", cleanup, err
	}
	if repository {
		if err := gitsync.InitSandbox(ctx, destination, filepath.Join(container, "remote.git")); err != nil {
			return "", cleanup, err
		}
	}
	keep = true
	return destination, cleanup, nil
}

type testCopyReader struct {
	context.Context
	io.Reader
}

func (r testCopyReader) Read(p []byte) (int, error) {
	if err := r.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func copyTestFile(ctx context.Context, v *vault.Vault, source, destination string) error {
	in, err := v.OpenFile(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, testCopyReader{ctx, in})
	return errors.Join(copyErr, out.Close())
}
