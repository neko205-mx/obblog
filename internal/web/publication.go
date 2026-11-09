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
	"date":    func(t time.Time) string { return t.In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02") },
	"isoDate": func(t time.Time) string { return t.Format(time.RFC3339) },
}).ParseFS(assets, "static/public.html"))

type publicArticle struct {
	*publication.Article
	HTML        template.HTML
	Excerpt     string
	Headings    []markdown.Heading
	ShowUpdated bool
	TitleID     string
}

func (a *App) publicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", a.publicIndex)
	mux.HandleFunc("GET /posts/{id}", a.publicPost)
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
	list := []publicArticle{}
	for _, article := range publication.Published(articles) {
		rendered, err := publication.Render(article, articles)
		if err != nil {
			fail(w, 500, "文章渲染失败")
			return
		}
		list = append(list, publicArticle{Article: article, Excerpt: rendered.Excerpt})
	}
	publicPage(w, "index", map[string]any{"Articles": list})
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
		publicPage(w, "post", publicArticle{Article: article, HTML: template.HTML(body), Excerpt: rendered.Excerpt, Headings: rendered.Headings, ShowUpdated: article.Revised, TitleID: titleID})
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
