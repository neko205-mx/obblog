package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"obblog/internal/markdown"
	"obblog/internal/publication"
	"obblog/internal/vault"
)

type publicationInfo struct {
	ID          string     `json:"id,omitempty"`
	Enabled     bool       `json:"enabled"`
	URL         string     `json:"url,omitempty"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
}

func publicationStatus(article *publication.Article) publicationInfo {
	if article == nil {
		return publicationInfo{}
	}
	return publicationInfo{article.ID, article.Enabled, publication.URL(article.ID), &article.PublishedAt, &article.UpdatedAt}
}

func (a *App) publicationStatus(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	if !vault.ValidPath(name) || vault.Kind(name) != "note" {
		fail(w, 404, "笔记不存在")
		return
	}
	article := a.publications.Snapshot()[name]
	if article == nil {
		if _, _, err := a.vault.ReadNote(name); err != nil {
			fail(w, 404, "笔记不存在")
			return
		}
	}
	respond(w, publicationStatus(article))
}

// blogConfig is what the settings panel loads. Accents and Themes drive the
// pickers so the browser never invents a value the server would reject.
// Preferences carries every switch the panel offers; new features register a
// definition in publication and appear here without touching this type.
type blogConfig struct {
	*publication.Blog
	Accents     []string                 `json:"accents"`
	Themes      []string                 `json:"themes"`
	Vault       string                   `json:"vault"`
	Preferences []publication.Preference `json:"preferences"`
}

// preferenceLocks reports which switches the running installation cannot offer.
func (a *App) preferenceLocks() map[string]string {
	locked := map[string]string{}
	if !a.writable {
		for _, key := range []string{"editor.autoPreview", "editor.spellcheck", "editor.autoSaveDraft"} {
			locked[key] = "当前知识库为只读模式"
		}
	}
	return locked
}

func (a *App) blogConfigView() blogConfig {
	blog := a.publications.Blog()
	if blog == nil {
		blog = publication.DefaultBlog()
	}
	return blogConfig{
		Blog:        blog,
		Accents:     publication.Accents,
		Themes:      []string{"auto", "light", "dark"},
		Vault:       a.vault.Name,
		Preferences: a.publications.Preferences(a.preferenceLocks()),
	}
}

func (a *App) blogSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		respond(w, a.blogConfigView())
	case http.MethodPost:
		a.saveBlogSettings(w, r)
	default:
		fail(w, http.StatusMethodNotAllowed, "方法不允许")
	}
}

func (a *App) saveBlogSettings(w http.ResponseWriter, r *http.Request) {
	if a.publications == nil {
		fail(w, 409, "发布存储不可用")
		return
	}
	var input publication.Blog
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			fail(w, http.StatusRequestEntityTooLarge, "博客配置请求超过大小限制")
			return
		}
		fail(w, http.StatusBadRequest, "无效的博客配置")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		fail(w, http.StatusBadRequest, "无效的博客配置")
		return
	}
	cleaned, err := a.publications.SetBlog(input)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	respond(w, blogConfig{
		Blog:        cleaned,
		Accents:     publication.Accents,
		Themes:      []string{"auto", "light", "dark"},
		Vault:       a.vault.Name,
		Preferences: a.publications.Preferences(a.preferenceLocks()),
	})
}

// preferences returns or updates the panel switches. Unknown or locked keys are
// rejected, so the server stays the authority on what can be configured.
func (a *App) preferences(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		respond(w, a.publications.Preferences(a.preferenceLocks()))
	case http.MethodPost:
		var input struct {
			Values map[string]bool `json:"values"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Values == nil {
			fail(w, http.StatusBadRequest, "无效的设置请求")
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			fail(w, http.StatusBadRequest, "无效的设置请求")
			return
		}
		updated, err := a.publications.SetPreferences(input.Values, a.preferenceLocks())
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		respond(w, updated)
	default:
		fail(w, http.StatusMethodNotAllowed, "方法不允许")
	}
}

func (a *App) setPublication(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(w, 415, "请使用 JSON 请求")
		return
	}
	var input struct {
		Path        string     `json:"path"`
		Enabled     *bool      `json:"enabled"`
		PublishedAt *time.Time `json:"publishedAt"`
		Revision    string     `json:"revision"`
		GitHead     string     `json:"gitHead"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil {
		fail(w, 400, "无效的发布设置")
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF || input.Enabled == nil {
		fail(w, 400, "无效的发布设置")
		return
	}
	if !vault.ValidPath(input.Path) || vault.Kind(input.Path) != "note" {
		fail(w, 400, "无效的笔记路径")
		return
	}
	if input.PublishedAt != nil && (input.PublishedAt.IsZero() || input.PublishedAt.Year() < 1 || input.PublishedAt.Year() > 9999 || input.PublishedAt.After(time.Now().Add(time.Minute))) {
		fail(w, 400, "无效的发布时间，不能晚于当前时间")
		return
	}
	previous := a.publications.Snapshot()[input.Path]
	if *input.Enabled && (previous == nil || !previous.Enabled) {
		if !a.publicationCurrent(w, r) {
			return
		}
		raw, _, err := a.vault.ReadNote(input.Path)
		if err != nil {
			fail(w, 404, "无法读取该笔记")
			return
		}
		if input.Revision != vault.Revision(raw) || (a.git != nil && input.GitHead != a.git.Head(r.Context())) {
			fail(w, 409, "笔记已变化，请重新读取后发布")
			return
		}
	}
	article, err := a.publications.Set(a.vault, input.Path, *input.Enabled, input.PublishedAt, a.gitHead(r))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fail(w, 404, "笔记不存在")
			return
		}
		log.Printf("set publication: %v", err)
		fail(w, 500, "发布设置保存失败，请检查磁盘空间与目录权限")
		return
	}
	respond(w, publicationStatus(article))
}

func (a *App) publicationCurrent(w http.ResponseWriter, r *http.Request) bool {
	if a.git != nil {
		status := a.git.Status(r.Context())
		if !status.CanWrite {
			fail(w, 409, status.Message)
			return false
		}
	}
	return true
}

func (a *App) refreshPublications(w http.ResponseWriter, r *http.Request) {
	if !a.publicationCurrent(w, r) {
		return
	}
	if err := a.updatePublications(r.Context()); err != nil {
		log.Printf("refresh publications: %v", err)
		fail(w, 500, "公开副本更新失败，请检查磁盘空间与目录权限")
		return
	}
	respond(w, map[string]bool{"ok": true})
}

func (a *App) updatePublications(ctx context.Context) error {
	var head string
	var renames map[string]string
	if a.git != nil {
		head = a.git.Head(ctx)
		before := a.publications.Head()
		if before != "" {
			var err error
			renames, err = a.git.Renames(ctx, before, head)
			if err != nil {
				return err
			}
		}
	}
	return a.publications.Refresh(a.vault, renames, head)
}

var publicTemplates = template.Must(template.New("public").Funcs(template.FuncMap{
	"postURL": publication.URL,
	"pageURL": publication.PageURL,
	"date":    func(t time.Time) string { return t.In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02") },
	"isoDate": func(t time.Time) string { return t.Format(time.RFC3339) },
}).ParseFS(assets, "static/public.html"))

// blogShell carries the configured site chrome into every public template.
type blogShell struct {
	*publication.Blog
	Accent    template.CSS
	ThemeAttr string
}

func newBlogShell(blog *publication.Blog) blogShell {
	if blog == nil {
		blog = publication.DefaultBlog()
	}
	// publication.SetBlog only accepts the fixed palette, so the value cannot
	// break out of the custom property it is written into.
	return blogShell{Blog: blog, Accent: template.CSS(blog.Accent), ThemeAttr: blog.Theme}
}

type publicArticle struct {
	*publication.Article
	blogShell
	HTML        template.HTML
	Excerpt     string
	Headings    []markdown.Heading
	ShowUpdated bool
	TitleID     string
}

func (a *App) publicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", a.publicIndex)
	mux.HandleFunc("GET /posts/{id}", a.publicPost)
	mux.HandleFunc("GET /pages/{slug}", a.publicStandalone)
	mux.HandleFunc("GET /media/{id}/{blob}", a.publicAsset)
	mux.HandleFunc("GET /blog.css", func(w http.ResponseWriter, r *http.Request) { a.static(w, r, "blog.css") })
}

func (a *App) publicSnapshot() map[string]*publication.Article {
	if a.publications == nil {
		return map[string]*publication.Article{}
	}
	return a.publications.Snapshot()
}

func (a *App) publicIndex(w http.ResponseWriter, r *http.Request) {
	articles := a.publicSnapshot()
	shell := newBlogShell(a.publications.Blog())
	list := []publicArticle{}
	for _, article := range publication.Published(articles) {
		rendered, err := publication.Render(article, articles)
		if err != nil {
			fail(w, 500, "文章渲染失败")
			return
		}
		list = append(list, publicArticle{Article: article, blogShell: shell, Excerpt: rendered.Excerpt})
	}
	publicPage(w, "index", struct {
		blogShell
		Articles []publicArticle
	}{shell, list})
}

func (a *App) publicPost(w http.ResponseWriter, r *http.Request) {
	articles := a.publicSnapshot()
	for _, article := range articles {
		if !article.Enabled || article.ID != r.PathValue("id") {
			continue
		}
		rendered, err := publication.Render(article, articles)
		if err != nil {
			fail(w, 500, "文章渲染失败")
			return
		}
		body, titleID := rendered.HTML, ""
		if len(rendered.Headings) > 0 && rendered.Headings[0].Level == 1 && strings.HasPrefix(body, "<h1 ") {
			if end := strings.Index(body, "</h1>\n"); end >= 0 {
				body = body[end+len("</h1>\n"):]
				titleID = rendered.Headings[0].ID
			}
		}
		// Only safe-mode Markdown renderer output is trusted as template HTML.
		publicPage(w, "post", publicArticle{Article: article, blogShell: newBlogShell(a.publications.Blog()), HTML: template.HTML(body), Excerpt: rendered.Excerpt, Headings: rendered.Headings, ShowUpdated: article.Revised, TitleID: titleID})
		return
	}
	http.NotFound(w, r)
}

func publicPage(w http.ResponseWriter, name string, data any) {
	var body bytes.Buffer
	if err := publicTemplates.ExecuteTemplate(&body, name, data); err != nil {
		log.Printf("public template: %v", err)
		fail(w, 500, "页面渲染失败")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(body.Bytes())
}

// publicStandalone renders a page that lives with the blog configuration rather
// than in the vault. Links resolve against currently published articles only.
// publishedNotes indexes enabled articles by vault-relative path.
func publishedNotes(articles map[string]*publication.Article) map[string]*vault.Entry {
	entries := make(map[string]*vault.Entry, len(articles))
	for name, article := range articles {
		if !article.Enabled || vault.Kind(name) != "note" {
			continue
		}
		entries[name] = &vault.Entry{Name: path.Base(name), Path: name, Kind: "note"}
	}
	return entries
}

// resolvePublished follows the same preference order as vault.Snapshot.resolve:
// exact path, then an implicit .md, then an unambiguous path suffix.
func resolvePublished(entries map[string]*vault.Entry, target string) *vault.Entry {
	if target == "" || strings.Contains(target, "\\") || strings.HasPrefix(target, "/") {
		return nil
	}
	target = path.Clean(target)
	candidates := []string{target}
	if vault.Kind(target) != "note" {
		candidates = append(candidates, target+".md")
	}
	for _, candidate := range candidates {
		if entry := entries[candidate]; entry != nil {
			return entry
		}
	}
	var found *vault.Entry
	for _, candidate := range candidates {
		for name, entry := range entries {
			if strings.HasSuffix(name, "/"+candidate) {
				if found != nil && found.Path != entry.Path {
					return nil // Ambiguous: report as unresolved instead of guessing.
				}
				found = entry
			}
		}
	}
	return found
}

func (a *App) publicStandalone(w http.ResponseWriter, r *http.Request) {
	page := a.publications.Page(r.PathValue("slug"))
	if page == nil {
		http.NotFound(w, r)
		return
	}
	articles := a.publicSnapshot()
	published := publishedNotes(articles)
	rendered, err := markdown.RenderWithOptions([]byte(page.Source), "page/"+page.Slug+".md", nil, markdown.RenderOptions{
		Resolve: func(from, target string, wiki bool) (*vault.Entry, error) {
			// A standalone page lives outside the vault, so a wiki link without
			// an extension must still find the article it names.
			if entry := resolvePublished(published, target); entry != nil {
				return entry, nil
			}
			return nil, fs.ErrNotExist
		},
		NoteURL: func(name, fragment string) string {
			target := articles[name]
			if target == nil || !target.Enabled {
				return ""
			}
			href := publication.URL(target.ID)
			if fragment != "" {
				return href + "#" + markdown.Slug(fragment)
			}
			return href
		},
		AssetURL: func(name string) string { return "" },
	})
	if err != nil {
		fail(w, 500, "页面渲染失败")
		return
	}
	publicPage(w, "page", struct {
		blogShell
		Page     *publication.BlogPage
		HTML     template.HTML
		Headings []markdown.Heading
	}{newBlogShell(a.publications.Blog()), page, template.HTML(rendered.HTML), rendered.Headings})
}

func (a *App) publicAsset(w http.ResponseWriter, r *http.Request) {
	if a.publications == nil {
		http.NotFound(w, r)
		return
	}
	f, name, err := a.publications.OpenAsset(r.PathValue("id"), r.PathValue("blob"))
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
	w.Header().Set("Content-Type", mime.TypeByExtension(strings.ToLower(path.Ext(name))))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename*=UTF-8''%s", encodeFilename(name)))
	http.ServeContent(w, r, name, info.ModTime(), f)
}
