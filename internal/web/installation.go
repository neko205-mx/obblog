package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"obblog/internal/account"
	"obblog/internal/config"
	"obblog/internal/gitsync"
	"obblog/internal/publication"
)

type Runtime struct {
	mu          sync.RWMutex
	installMu   sync.Mutex
	dir         string
	app         *App
	handler     http.Handler
	settings    *config.Config
	problem     string
	clone       func(context.Context, string, string, string, gitsync.Credentials) error
	testMode    bool
	sourceVault string
	stop        chan struct{}
	stopOnce    sync.Once
	closed      bool
	closeErr    error
}

func OpenRuntime(dir string) (*Runtime, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	rt := &Runtime{dir: abs, clone: gitsync.Clone}
	c, err := config.Load(abs)
	if err == nil {
		rt.app, err = appFromConfig(c, rt.dir)
		if err != nil {
			// Keep authenticated recovery settings available if a mount or Git
			// configuration changes, without reopening first-install access.
			rt.problem = "知识库暂时不可用，请重新选择目录或调整 Git 设置"
			rt.app = &App{managed: true, auth: authentication{passwordHash: c.PasswordHash, secure: c.SecureCookie, sessions: make(map[[32]byte]time.Time)}}
			rt.app.publications, _ = publication.Open(rt.dir, c.Vault)
			rt.handler = recoveryHandler(rt.app)
		} else {
			rt.handler = rt.app.Handler()
		}
		rt.settings = c
		return rt, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return rt, nil
}

func recoveryHandler(a *App) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/login", func(w http.ResponseWriter, r *http.Request) { a.static(w, r, "login.html") })
	mux.HandleFunc("GET /admin/login.js", func(w http.ResponseWriter, r *http.Request) { a.static(w, r, "login.js") })
	mux.HandleFunc("GET /admin/entry.css", func(w http.ResponseWriter, r *http.Request) { a.static(w, r, "entry.css") })
	mux.HandleFunc("POST /admin/api/login", a.login)
	mux.HandleFunc("POST /admin/api/logout", a.logout)
	a.publicRoutes(mux)
	mux.HandleFunc("/admin/", func(w http.ResponseWriter, r *http.Request) {
		if !a.authenticated(r) {
			http.Redirect(w, r, "/admin/login", 303)
			return
		}
		http.Redirect(w, r, "/admin/settings", 303)
	})
	return mux
}

func appFromConfig(c *config.Config, data string) (*App, error) {
	return NewWithOptions(c.Vault, Options{Writable: true, Git: true, AutoGit: !c.Git, PasswordHash: c.PasswordHash, SecureCookie: c.SecureCookie, Credentials: c.Credentials, Managed: true, DataDir: data})
}

func (rt *Runtime) Close() error {
	rt.installMu.Lock()
	defer rt.installMu.Unlock()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		return rt.closeErr
	}
	rt.closed = true
	if rt.app != nil {
		rt.closeErr = rt.app.Close()
		rt.app.auth.mu.Lock()
		rt.app.auth.sessions = nil
		rt.app.auth.passwordHash = ""
		rt.app.auth.password = [32]byte{}
		rt.app.auth.mu.Unlock()
	}
	if rt.testMode {
		rt.closeErr = errors.Join(rt.closeErr, os.RemoveAll(rt.dir))
		rt.stopOnce.Do(func() { close(rt.stop) })
	}
	rt.settings, rt.app, rt.handler = nil, nil, nil
	rt.sourceVault, rt.problem = "", ""
	return rt.closeErr
}

func (rt *Runtime) Handler() http.Handler { return protect(http.HandlerFunc(rt.serve)) }
func (rt *Runtime) serve(w http.ResponseWriter, r *http.Request) {
	rt.mu.RLock()
	closed := rt.closed
	rt.mu.RUnlock()
	if closed {
		fail(w, 503, "服务已关闭")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin/api/test/") {
		rt.testAPI(w, r)
		return
	}
	if r.URL.Path == "/install" || r.URL.Path == "/install/" {
		rt.mu.RLock()
		installed := rt.app != nil
		rt.mu.RUnlock()
		if installed {
			http.Redirect(w, r, "/admin/login", 303)
			return
		}
		if r.Method != "GET" {
			fail(w, 405, "方法不允许")
			return
		}
		(&App{}).static(w, r, "install.html")
		return
	}
	for _, asset := range []string{"install.js", "install.css", "entry.css"} {
		if r.URL.Path == "/install/"+asset && r.Method == "GET" {
			(&App{}).static(w, r, asset)
			return
		}
	}
	if r.URL.Path == "/install/api/state" && r.Method == "GET" {
		rt.mu.RLock()
		defer rt.mu.RUnlock()
		_, err := exec.LookPath("git")
		respond(w, map[string]any{"installed": rt.app != nil, "gitAvailable": err == nil, "testMode": rt.testMode, "sourceVault": rt.sourceVault})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/install/api/") {
		rt.installAPI(w, r)
		return
	}
	if r.URL.Path == "/admin/settings" || r.URL.Path == "/admin/settings.js" || r.URL.Path == "/admin/settings.css" || strings.HasPrefix(r.URL.Path, "/admin/api/settings") {
		rt.settingsAPI(w, r)
		return
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.closed {
		fail(w, 503, "服务已关闭")
		return
	}
	if rt.app == nil {
		if r.Method == "GET" && (r.URL.Path == "/" || r.URL.Path == "/admin/" || r.URL.Path == "/admin/login") {
			http.Redirect(w, r, "/install", 303)
		} else {
			fail(w, 503, "系统尚未初始化")
		}
		return
	}
	rt.handler.ServeHTTP(w, r)
}

func (rt *Runtime) installationOpen(w http.ResponseWriter) bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.app != nil {
		fail(w, 410, "安装已完成")
		return false
	}
	if rt.closed {
		fail(w, 503, "服务已关闭")
		return false
	}
	return true
}

func (rt *Runtime) installAPI(w http.ResponseWriter, r *http.Request) {
	if !rt.installationOpen(w) {
		return
	}
	switch {
	case r.URL.Path == "/install/api/directories" && r.Method == "GET":
		rt.directories(w, r)
	case r.URL.Path == "/install/api/configure" && r.Method == "POST":
		rt.configure(w, r, true)
	default:
		http.NotFound(w, r)
	}
}

func (rt *Runtime) settingsAPI(w http.ResponseWriter, r *http.Request) {
	rt.mu.RLock()
	a := rt.app
	if a == nil || !a.authenticated(r) {
		rt.mu.RUnlock()
		if r.URL.Path == "/admin/settings" {
			http.Redirect(w, r, "/admin/login", 303)
		} else {
			fail(w, 401, "请先登录后台")
		}
		return
	}
	c := *rt.settings
	problem := rt.problem
	writable, gitEnabled := a.vault != nil && a.writable, a.git != nil
	rt.mu.RUnlock()
	switch {
	case r.URL.Path == "/admin/settings" && r.Method == "GET":
		(&App{}).static(w, r, "settings.html")
	case (r.URL.Path == "/admin/settings.js" || r.URL.Path == "/admin/settings.css") && r.Method == "GET":
		(&App{}).static(w, r, strings.TrimPrefix(r.URL.Path, "/admin/"))
	case r.URL.Path == "/admin/api/settings" && r.Method == "GET":
		_, err := exec.LookPath("git")
		respond(w, map[string]any{"vault": c.Vault, "writable": writable, "git": gitEnabled, "tokenConfigured": c.Credentials.Token != "", "username": c.Credentials.Username, "gitAvailable": err == nil, "problem": problem, "testMode": rt.testMode})
	case r.URL.Path == "/admin/api/settings" && r.Method == "POST":
		rt.configure(w, r, false)
	case r.URL.Path == "/admin/api/settings/directories" && r.Method == "GET":
		rt.directories(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (rt *Runtime) directories(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	if name == "" {
		name, _ = os.Getwd()
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		fail(w, 400, "目录路径无效")
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		fail(w, 400, "无法读取目录，请检查路径或服务用户权限")
		return
	}
	names := []string{}
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".git" {
			names = append(names, e.Name())
		}
		if len(names) >= 500 {
			break
		}
	}
	respond(w, map[string]any{"path": abs, "parent": filepath.Dir(abs), "directories": names})
}

type setupInput struct {
	Mode         string `json:"mode"`
	Vault        string `json:"vault"`
	Repository   string `json:"repository"`
	Branch       string `json:"branch"`
	Username     string `json:"username"`
	Token        string `json:"token"`
	ClearToken   bool   `json:"clearToken"`
	Password     string `json:"password"`
	Confirmation string `json:"confirmation"`
	// Older browser clients may still send these; they no longer gate features.
	Writable     bool `json:"writable"`
	Git          bool `json:"git"`
	SecureCookie bool `json:"secureCookie"`
}

func (rt *Runtime) configure(w http.ResponseWriter, r *http.Request, initial bool) {
	if !rt.installMu.TryLock() {
		fail(w, 409, "配置保存中，请稍候")
		return
	}
	defer rt.installMu.Unlock()
	rt.mu.RLock()
	closed := rt.closed
	rt.mu.RUnlock()
	if closed {
		fail(w, 503, "服务已关闭")
		return
	}
	if initial && !rt.installationOpen(w) {
		return
	}
	// The exclusive filesystem lock also prevents another process from claiming
	// an uninstalled data directory while a clone is in progress.
	lock := filepath.Join(rt.dir, "setup.lock")
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fail(w, 409, "安装目录被锁定；请确认没有其他实例或通过 VPS 文件管理器处理遗留 setup.lock")
		return
	}
	f.Close()
	defer os.Remove(lock)
	media := r.Header.Get("Content-Type")
	if !strings.HasPrefix(media, "application/json") {
		fail(w, 415, "请使用 JSON 请求")
		return
	}
	var input setupInput
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil {
		fail(w, 400, "无效的设置请求")
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		fail(w, 400, "无效的设置请求")
		return
	}
	rt.mu.RLock()
	var old config.Config
	if rt.settings != nil {
		old = *rt.settings
	}
	rt.mu.RUnlock()
	if input.Password != input.Confirmation {
		fail(w, 400, "两次输入的密码不一致")
		return
	}
	passwordHash := old.PasswordHash
	if initial || input.Password != "" {
		passwordHash, err = account.Hash(input.Password)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	credentials := old.Credentials
	if input.ClearToken {
		credentials = gitsync.Credentials{}
	}
	if initial || input.Token != "" {
		credentials = gitsync.Credentials{Username: strings.TrimSpace(input.Username), Token: strings.TrimSpace(input.Token)}
	}
	if len(credentials.Username) > 256 || len(credentials.Token) > 4096 || strings.ContainsAny(credentials.Token, "\r\n") {
		fail(w, 400, "无效的 Git 凭据")
		return
	}
	if input.Mode == "clone" && !initial && old.Credentials.Token != "" && input.Token == "" && !input.ClearToken {
		fail(w, 400, "切换克隆仓库时请明确重新填写 Token，公开库请勾选清除 Token")
		return
	}
	name, cleanup, err := rt.prepareVault(r.Context(), input, credentials)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	keep := false
	defer func() {
		if !keep {
			cleanup()
		}
	}()
	if input.Mode == "empty" {
		credentials = gitsync.Credentials{}
	}
	if rt.testMode {
		credentials = gitsync.Credentials{}
	}
	if !initial && name != old.Vault && old.Credentials.Token != "" && input.Token == "" && !input.ClearToken && input.Mode != "empty" {
		fail(w, 400, "更换知识库时请重新填写 Token，或明确清除已保存 Token")
		return
	}
	c := &config.Config{Version: 1, Vault: name, Writable: true, SecureCookie: secureRequest(r), PasswordHash: passwordHash, Credentials: credentials}
	a, err := appFromConfig(c, rt.dir)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	c.Git = a.git != nil
	a.testMode = rt.testMode
	if _, err = a.vault.Scan(); err != nil {
		a.Close()
		fail(w, 400, "无法读取所选笔记目录，请检查服务用户的目录权限")
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if initial && rt.app != nil {
		a.Close()
		fail(w, 410, "安装已经完成")
		return
	}
	if !initial && (rt.app == nil || !rt.app.authenticated(r)) {
		a.Close()
		fail(w, 401, "会话已失效，请重新登录后保存设置")
		return
	}
	// A request may have published while the replacement app was being prepared.
	a.publications, err = publication.Open(rt.dir, a.vault.Path)
	if err != nil {
		a.Close()
		fail(w, 500, "无法读取发布记录")
		return
	}
	if err = config.Save(rt.dir, c, initial); err != nil {
		a.Close()
		fail(w, 500, "配置保存失败，请检查目录权限或磁盘空间")
		return
	}
	previous := rt.app
	rt.app = a
	rt.handler = a.Handler()
	rt.settings = c
	rt.problem = ""
	keep = true
	if previous != nil {
		previous.Close()
	}
	// Reconfiguration creates a new application/session store, revoking all
	// previous sessions rather than carrying old privileges across vaults.
	respond(w, map[string]any{"ok": true, "redirect": "/admin/login", "message": "配置已保存，请登录"})
}

func (rt *Runtime) prepareVault(ctx context.Context, input setupInput, credentials gitsync.Credentials) (string, func(), error) {
	noop := func() {}
	switch input.Mode {
	case "empty", "clone":
		remote := ""
		var err error
		if input.Mode == "clone" {
			remote, err = gitsync.GitHubURL(input.Repository)
			if err != nil {
				return "", noop, err
			}
		}
		container, err := os.MkdirTemp(rt.dir, "vault-")
		if err != nil {
			return "", noop, errors.New("无法创建知识库目录")
		}
		cleanup := func() { os.RemoveAll(container) }
		name := filepath.Join(container, "notes")
		if input.Mode == "clone" {
			err = rt.clone(ctx, remote, strings.TrimSpace(input.Branch), name, credentials)
			if err == nil && rt.testMode {
				copied, copiedCleanup, copyErr := rt.copyTestVault(ctx, name)
				cleanup()
				return copied, copiedCleanup, copyErr
			}
		} else {
			err = os.Mkdir(name, 0700)
		}
		if err != nil {
			cleanup()
			return "", noop, err
		}
		return name, cleanup, nil
	case "existing":
		if strings.TrimSpace(input.Vault) == "" {
			return "", noop, errors.New("请选择服务器上的笔记目录")
		}
		name, err := filepath.Abs(input.Vault)
		if err != nil {
			return "", noop, errors.New("无效的目录路径")
		}
		name, err = filepath.EvalSymlinks(name)
		if err != nil {
			return "", noop, errors.New("目录不存在，请重新选择")
		}
		if rt.testMode {
			if rel, err := filepath.Rel(rt.dir, name); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return name, noop, nil
			}
			return rt.copyTestVault(ctx, name)
		}
		data, err := filepath.EvalSymlinks(rt.dir)
		if err != nil {
			return "", noop, errors.New("无法解析数据目录")
		}
		rel, err := filepath.Rel(name, data)
		if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return "", noop, errors.New("笔记库不能包含系统数据目录")
		}
		return name, noop, nil
	default:
		return "", noop, errors.New("请选择连接现有目录、克隆 GitHub 仓库或创建空知识库")
	}
}
