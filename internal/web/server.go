package web

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"obblog/internal/account"
	"obblog/internal/gitsync"
	"obblog/internal/graph"
	"obblog/internal/markdown"
	"obblog/internal/publication"
	"obblog/internal/vault"
)

//go:embed static/*
var assets embed.FS

type App struct {
	vault        *vault.Vault
	writable     bool
	managed      bool
	testMode     bool
	auth         authentication
	git          *gitsync.Service
	publications *publication.Store
	opMu         sync.RWMutex
}

type Options struct {
	Writable     bool
	Password     string
	SecureCookie bool
	Git          bool
	AutoGit      bool
	PasswordHash string
	Credentials  gitsync.Credentials
	Managed      bool
	DataDir      string
}

func New(dir string) (*App, error) {
	return NewWithOptions(dir, Options{Writable: true, Git: true, AutoGit: true, Password: os.Getenv("OBBLOG_ADMIN_PASSWORD")})
}

func NewWithOptions(dir string, options Options) (*App, error) {
	if options.PasswordHash != "" && !account.Valid(options.PasswordHash) {
		return nil, errors.New("无效的持久密码摘要")
	}
	if options.PasswordHash == "" && len(options.Password) < 12 {
		return nil, errors.New("后台密码至少需要 12 个字节，请设置 OBBLOG_ADMIN_PASSWORD")
	}
	v, err := vault.Open(dir)
	if err != nil {
		return nil, err
	}
	a := &App{vault: v, writable: options.Writable, managed: options.Managed, auth: authentication{password: sha256.Sum256([]byte(options.Password)), passwordHash: options.PasswordHash, secure: options.SecureCookie, sessions: make(map[[32]byte]time.Time)}}
	if options.AutoGit && options.Git {
		options.Git, err = gitsync.IsRepositoryRoot(v.Path)
		if err != nil {
			v.Close()
			return nil, err
		}
	}
	if options.Git {
		a.git, err = gitsync.OpenWithCredentials(v.Path, options.Credentials)
		if err != nil {
			v.Close()
			return nil, err
		}
	}
	data := options.DataDir
	if data == "" {
		data = "./data"
	}
	a.publications, err = publication.Open(data, v.Path)
	if err != nil {
		v.Close()
		return nil, err
	}
	return a, nil
}

func (a *App) Close() error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if a.vault == nil {
		return nil
	}
	return a.vault.Close()
}
func (a *App) VaultPath() string { return a.vault.Path }

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	private := http.NewServeMux()
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/admin/", 303) })
	mux.HandleFunc("GET /admin/login", func(w http.ResponseWriter, r *http.Request) { a.static(w, r, "login.html") })
	mux.HandleFunc("GET /admin/login.js", func(w http.ResponseWriter, r *http.Request) { a.static(w, r, "login.js") })
	mux.HandleFunc("GET /admin/entry.css", func(w http.ResponseWriter, r *http.Request) { a.static(w, r, "entry.css") })
	mux.HandleFunc("POST /admin/api/login", a.login)
	private.HandleFunc("POST /api/logout", a.logout)
	private.HandleFunc("GET /api/git", a.gitStatus)
	private.HandleFunc("POST /api/git/pull", a.gitPull)
	private.HandleFunc("POST /api/git/push", a.gitPush)
	private.HandleFunc("GET /api/publication", a.publicationStatus)
	private.HandleFunc("PUT /api/publication", a.setPublication)
	private.HandleFunc("POST /api/publications/refresh", a.refreshPublications)
	// Strip only the admin prefix; no private route is registered at the public root.
	mux.Handle("/admin/", a.gate(http.StripPrefix("/admin", private)))
	private.HandleFunc("GET /api/vault", a.tree)
	private.HandleFunc("GET /api/note", a.note)
	private.HandleFunc("PUT /api/note", a.saveNote)
	private.HandleFunc("POST /api/note", a.saveNote)
	private.HandleFunc("POST /api/preview", a.preview)
	private.HandleFunc("GET /api/asset", a.asset)
	private.HandleFunc("POST /api/asset", a.uploadImage)
	private.HandleFunc("GET /api/graph", a.graph)
	private.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if name != "index.html" && name != "app.js" && name != "style.css" && name != "graph.js" && name != "graph-model.mjs" && name != "force.js" && name != "editor.js" && name != "editor-model.mjs" {
			http.NotFound(w, r)
			return
		}
		data, err := assets.ReadFile("static/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
		w.Write(data)
	})
	a.publicRoutes(mux)
	return protect(mux)
}

func protect(next http.Handler) http.Handler {
	crossOrigin := http.NewCrossOriginProtection()
	crossOrigin.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusForbidden, "不允许跨站写入请求")
	}))
	protected := crossOrigin.Handler(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/admin") || strings.HasPrefix(r.URL.Path, "/install") {
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https:; media-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		protected.ServeHTTP(w, r)
	})
}

func (a *App) static(w http.ResponseWriter, r *http.Request, name string) {
	data, err := assets.ReadFile("static/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	w.Write(data)
}

func (a *App) gitStatus(w http.ResponseWriter, r *http.Request) {
	if a.git == nil {
		message := "当前使用本地目录模式"
		respond(w, gitsync.Status{State: "disabled", Message: message})
		return
	}
	respond(w, a.git.Status(r.Context()))
}
func (a *App) gitPull(w http.ResponseWriter, r *http.Request) { a.gitAction(w, r, false) }
func (a *App) gitPush(w http.ResponseWriter, r *http.Request) { a.gitAction(w, r, true) }
func (a *App) gitAction(w http.ResponseWriter, r *http.Request, push bool) {
	if a.git == nil {
		fail(w, 409, "Git 同步未启用")
		return
	}
	st, err := a.git.Sync(r.Context(), push)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	var warning string
	if err := a.updatePublications(r.Context()); err != nil {
		log.Printf("refresh publications after sync: %v", err)
		warning = "Git 同步已完成，公开副本更新失败，请重试更新公开副本"
	}
	respond(w, struct {
		gitsync.Status
		Warning string `json:"warning,omitempty"`
	}{st, warning})
}

func respond(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func fail(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (a *App) snapshot(w http.ResponseWriter) *vault.Snapshot {
	s, err := a.vault.Scan()
	if err != nil {
		log.Printf("scan vault: %v", err)
		fail(w, http.StatusInternalServerError, "无法读取笔记目录，请检查服务端日志")
		return nil
	}
	return s
}

func (a *App) tree(w http.ResponseWriter, r *http.Request) {
	if s := a.snapshot(w); s != nil {
		respond(w, struct {
			*vault.Snapshot
			Writable bool   `json:"writable"`
			SaveMode string `json:"saveMode"`
			Managed  bool   `json:"managed"`
			TestMode bool   `json:"testMode"`
		}{s, a.writable, a.saveMode(), a.managed, a.testMode})
	}
}

func (a *App) saveMode() string {
	if a.git != nil {
		return "git"
	}
	return "local"
}

func (a *App) graph(w http.ResponseWriter, r *http.Request) {
	snapshot := a.snapshot(w)
	if snapshot == nil {
		return
	}
	result, err := graph.Build(r.Context(), a.vault, snapshot)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		log.Printf("build graph: %v", err)
		fail(w, http.StatusInternalServerError, "无法建立笔记关系索引")
		return
	}
	respond(w, result)
}

func (a *App) note(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	if !vault.ValidPath(name) || vault.Kind(name) != "note" {
		fail(w, http.StatusNotFound, "笔记不存在")
		return
	}
	s := a.snapshot(w)
	if s == nil {
		return
	}
	if s.Files[name] == nil {
		fail(w, http.StatusNotFound, "笔记不存在")
		return
	}
	raw, info, err := a.vault.ReadNote(name)
	if err != nil {
		if errors.Is(err, vault.ErrNoteTooLarge) {
			fail(w, http.StatusRequestEntityTooLarge, "笔记超过 8 MiB")
		} else {
			log.Printf("read note: %v", err)
			fail(w, http.StatusNotFound, "无法读取该笔记")
		}
		return
	}
	result, err := markdown.Render(raw, name, s)
	if err != nil {
		log.Printf("render note: %v", err)
		fail(w, http.StatusInternalServerError, "笔记渲染失败")
		return
	}
	respond(w, struct {
		*markdown.Result
		Path     string    `json:"path"`
		Title    string    `json:"title"`
		Source   string    `json:"source"`
		Modified time.Time `json:"modified"`
		Revision string    `json:"revision"`
		GitHead  string    `json:"gitHead"`
	}{result, name, strings.TrimSuffix(path.Base(name), path.Ext(name)), string(raw), info.ModTime(), vault.Revision(raw), a.gitHead(r)})
}

func (a *App) gitHead(r *http.Request) string {
	if a.git != nil {
		return a.git.Head(r.Context())
	}
	return ""
}

type noteInput struct {
	Path     string `json:"path"`
	Source   string `json:"source"`
	Revision string `json:"revision"`
	GitHead  string `json:"gitHead,omitempty"`
}

func decodeNote(w http.ResponseWriter, r *http.Request) (*noteInput, bool) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "请求必须使用 application/json")
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 6*vault.MaxNoteSize+4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var payload struct {
		Path     string  `json:"path"`
		Source   *string `json:"source"`
		Revision string  `json:"revision"`
		GitHead  string  `json:"gitHead"`
	}
	err = decoder.Decode(&payload)
	if err == nil {
		var extra any
		if trailing := decoder.Decode(&extra); trailing != io.EOF {
			err = trailing
			if err == nil {
				err = errors.New("trailing JSON")
			}
		}
	}
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			fail(w, http.StatusRequestEntityTooLarge, "请求超过大小限制")
		} else {
			fail(w, http.StatusBadRequest, "无效的 JSON 请求")
		}
		return nil, false
	}
	if payload.Source == nil {
		fail(w, http.StatusBadRequest, "必须明确提供字符串类型的 Markdown 正文")
		return nil, false
	}
	input := noteInput{Path: payload.Path, Source: *payload.Source, Revision: payload.Revision, GitHead: payload.GitHead}
	if !vault.ValidPath(input.Path) || vault.Kind(input.Path) != "note" {
		fail(w, http.StatusBadRequest, "只能使用目录内非隐藏的 Markdown 文件路径")
		return nil, false
	}
	if len(input.Source) > vault.MaxNoteSize {
		fail(w, http.StatusRequestEntityTooLarge, "笔记超过 8 MiB")
		return nil, false
	}
	return &input, true
}

func (a *App) saveNote(w http.ResponseWriter, r *http.Request) {
	if !a.writable {
		fail(w, http.StatusForbidden, "当前为只读模式")
		return
	}
	input, ok := decodeNote(w, r)
	if !ok {
		return
	}
	write := func() (*vault.SavedNote, error) {
		return a.vault.SaveNote(input.Path, []byte(input.Source), input.Revision, r.Method == http.MethodPost)
	}
	var saved any
	var err error
	if a.git != nil {
		saved, err = a.git.Save(r.Context(), input.GitHead, input.Path, write)
	} else {
		saved, err = write()
	}
	if err != nil {
		switch {
		case errors.Is(err, vault.ErrConflict):
			fail(w, http.StatusConflict, "笔记已变更或同名文件已存在，请重新读取")
		case errors.Is(err, vault.ErrInvalidNote):
			fail(w, http.StatusBadRequest, "路径、版本或内容无效")
		case errors.Is(err, vault.ErrNoteTooLarge):
			fail(w, http.StatusRequestEntityTooLarge, "笔记超过 8 MiB")
		case errors.Is(err, fs.ErrNotExist):
			fail(w, http.StatusNotFound, "笔记或目标目录不存在，请先刷新目录")
		case errors.Is(err, fs.ErrPermission):
			fail(w, http.StatusForbidden, "该笔记或目录没有写入权限")
		default:
			if a.git != nil {
				fail(w, 409, err.Error())
				return
			}
			log.Printf("save note: %v", err)
			fail(w, http.StatusInternalServerError, "保存失败")
		}
		return
	}
	canPublish := true
	if result, ok := saved.(*gitsync.Result); ok {
		canPublish = result.Synced
	}
	if canPublish {
		if err := a.updatePublications(r.Context()); err != nil {
			log.Printf("refresh publications after save: %v", err)
			warning := "笔记已保存，公开副本更新失败，请重试更新公开副本"
			if result, ok := saved.(*gitsync.Result); ok {
				result.Warning = warning
			} else {
				saved = struct {
					*vault.SavedNote
					Warning string `json:"warning"`
				}{saved.(*vault.SavedNote), warning}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == http.MethodPost {
		w.WriteHeader(http.StatusCreated)
	}
	respond(w, saved)
}

func (a *App) preview(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeNote(w, r)
	if !ok {
		return
	}
	snapshot := a.snapshot(w)
	if snapshot == nil {
		return
	}
	if snapshot.Files[input.Path] == nil {
		snapshot.Files[input.Path] = &vault.Entry{Name: path.Base(input.Path), Path: input.Path, Kind: "note"}
	}
	result, err := markdown.Render([]byte(input.Source), input.Path, snapshot)
	if err != nil {
		fail(w, http.StatusInternalServerError, "预览失败")
		return
	}
	respond(w, result)
}

func (a *App) asset(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	if !vault.ValidPath(name) || vault.Kind(name) != "asset" {
		http.NotFound(w, r)
		return
	}
	f, err := a.vault.OpenFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename*=UTF-8''%s", encodeFilename(path.Base(name))))
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func encodeFilename(s string) string {
	var out strings.Builder
	for _, b := range []byte(s) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '.' || b == '-' || b == '_' {
			out.WriteByte(b)
		} else {
			fmt.Fprintf(&out, "%%%02X", b)
		}
	}
	return out.String()
}
