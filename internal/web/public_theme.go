package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"obblog/internal/markdown"
	"obblog/internal/publication"
)

const defaultPublicTheme = "default"

// An installed theme is trusted local HTML, not trusted application code.
// In particular it must never execute script with the administrator's origin.
const externalThemeCSP = "default-src 'none'; script-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; font-src 'self'; media-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'; sandbox"

var publicThemeID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
var themeAssetPath = regexp.MustCompile(`^[a-zA-Z0-9_-][a-zA-Z0-9_./-]{0,255}$`)
var themeAssetTypes = map[string]string{
	".css": "text/css; charset=utf-8", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp", ".avif": "image/avif", ".ico": "image/x-icon", ".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf",
}

var themeViews = []string{"index", "post", "page"}

type publicThemeInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Error       string `json:"error,omitempty"`
}

type publicTheme struct {
	info          publicThemeInfo
	tmpl          *template.Template
	pageTemplates []pageTemplate
}

// Only these values cross the template boundary. Never embed publication or
// vault records here: they hold source paths, private references and raw data.
type themeLink struct{ Title, URL string }
type themeSite struct {
	Title, Description, Author, Footer, Mode, Accent string
	Navigation                                       []themeLink
	ArchiveURL                                       string
}
type themePost struct {
	Title       string        `json:"title"`
	URL         string        `json:"url"`
	Excerpt     string        `json:"-"`
	TitleID     string        `json:"-"`
	DateShort   string        `json:"dateShort"`
	PublishedAt time.Time     `json:"publishedAt"`
	UpdatedAt   time.Time     `json:"updatedAt"`
	ShowUpdated bool          `json:"-"`
	Words       int           `json:"words"`
	Tags        []archiveTerm `json:"tags"`
	Categories  []archiveTerm `json:"categories"`
}
type themePage struct {
	Title, URL, Template string
	UpdatedAt            time.Time
}
type themeData struct {
	Site        themeSite
	Posts       []themePost
	Post        *themePost
	Page        *themePage
	Content     template.HTML
	Headings    []markdown.Heading
	FriendLinks []markdown.FriendLink
	ThemeAttr   template.HTMLAttr
	Archive     *archiveData
}

func newThemeData(blog *publication.Blog) themeData {
	if blog == nil {
		blog = publication.DefaultBlog()
	}
	data := themeData{Site: themeSite{
		Title: blog.Title, Description: blog.Description, Author: blog.Author,
		Footer: blog.Footer, Mode: blog.Theme, Accent: blog.Accent,
		ArchiveURL: archivePath,
	}}
	for _, page := range blog.Pages {
		data.Site.Navigation = append(data.Site.Navigation, themeLink{page.Title, publication.PageURL(page.Slug)})
	}
	if blog.Theme == "light" || blog.Theme == "dark" {
		data.ThemeAttr = template.HTMLAttr(` data-theme="` + blog.Theme + `"`)
	}
	return data
}

func themeFunctions(id string) template.FuncMap {
	return template.FuncMap{
		"date":    func(t time.Time) string { return t.In(blogTimeZone).Format("2006-01-02") },
		"isoDate": func(t time.Time) string { return t.Format(time.RFC3339) },
		"asset": func(name string) (string, error) {
			if themeAssetType(name) == "" {
				return "", errors.New("无效的主题资源路径")
			}
			return "/theme-assets/" + id + "/" + name, nil
		},
	}
}

var builtinPublicTheme = publicTheme{
	info: publicThemeInfo{ID: defaultPublicTheme, Name: "OB Journal", Version: "1", Description: "内置默认主题"},
	tmpl: template.Must(template.New("public").Funcs(themeFunctions(defaultPublicTheme)).ParseFS(assets, "static/public.html")),
}

func (a *App) themeDirectory() string { return filepath.Join(a.dataDir, "themes") }

func (a *App) openTheme(id string) (*os.Root, error) {
	if !publicThemeID.MatchString(id) || id == defaultPublicTheme {
		return nil, fs.ErrNotExist
	}
	root, err := os.OpenRoot(a.themeDirectory())
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return openThemeDir(root, id)
}

func openThemeDir(root *os.Root, name string) (*os.Root, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fs.ErrNotExist
	}
	return root.OpenRoot(name)
}

func openThemeFile(root *os.Root, name string) (*os.File, error) {
	// Check every component for symlinks. os.Root also confines opens if a
	// local author edits the directory while a request is in progress.
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, err
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() || i < len(parts)-1 && !info.IsDir() {
			return nil, fs.ErrNotExist
		}
	}
	return root.Open(name)
}

func readThemeFile(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := openThemeFile(root, name)
	if err != nil {
		return nil, fmt.Errorf("无法读取 %s", name)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, fmt.Errorf("无法读取 %s 或文件过大", name)
	}
	return data, nil
}

func (a *App) loadPublicTheme(id string) (publicTheme, error) {
	if id == defaultPublicTheme {
		return builtinPublicTheme, nil
	}
	theme := publicTheme{info: publicThemeInfo{ID: id, Name: id}}
	root, err := a.openTheme(id)
	if err != nil {
		return theme, errors.New("主题目录不存在或无法读取")
	}
	defer root.Close()
	data, err := readThemeFile(root, "theme.json", 16<<10)
	if err != nil {
		return theme, err
	}
	var manifest struct {
		Name          string             `json:"name"`
		Version       string             `json:"version"`
		Description   string             `json:"description"`
		APIVersion    int                `json:"apiVersion"`
		PageTemplates []pageTemplateSpec `json:"pageTemplates"`
	}
	if json.Unmarshal(data, &manifest) != nil || strings.TrimSpace(manifest.Name) == "" || len(manifest.Name) > 160 || strings.TrimSpace(manifest.Version) == "" || len(manifest.Version) > 40 || len(manifest.Description) > 1000 {
		return theme, errors.New("theme.json 缺少有效的主题名称或版本")
	}
	theme.info = publicThemeInfo{ID: id, Name: manifest.Name, Version: manifest.Version, Description: manifest.Description}
	if manifest.APIVersion != 1 {
		return theme, errors.New("不支持的主题模板接口版本")
	}
	tmpl := template.New("public").Funcs(themeFunctions(id)).Option("missingkey=error")
	parse := func(name string) error {
		raw, err := readThemeFile(root, name, 128<<10)
		if err != nil {
			return err
		}
		_, err = tmpl.New(path.Base(name)).Parse(string(raw))
		return err
	}
	partials, err := openThemeDir(root, "partials")
	if err == nil {
		dir, err := partials.Open(".")
		if err != nil {
			partials.Close()
			return theme, errors.New("无法读取 partials")
		}
		entries, err := dir.ReadDir(33)
		dir.Close()
		partials.Close()
		if err != nil && err != io.EOF || len(entries) > 32 {
			return theme, errors.New("partials 最多可有 32 个文件")
		}
		for _, entry := range entries {
			name := entry.Name()
			if path.Ext(name) != ".html" {
				continue
			}
			if name == "index.html" || name == "post.html" || name == "page.html" || name == "archive.html" {
				return theme, errors.New("partials 不可使用入口模板的文件名")
			}
			if err := parse("partials/" + name); err != nil {
				return theme, err
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return theme, errors.New("无法读取 partials")
	}
	for _, view := range themeViews {
		if err := parse(view + ".html"); err != nil {
			return theme, err
		}
	}
	if _, err := root.Lstat("archive.html"); err == nil {
		if err := parse("archive.html"); err != nil {
			return theme, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return theme, errors.New("无法读取 archive.html")
	}
	theme.pageTemplates, err = loadPageTemplates(root, id, manifest.PageTemplates, tmpl)
	if err != nil {
		return theme, err
	}
	theme.tmpl = tmpl
	return theme, nil
}

func (t publicTheme) execute(view string, data themeData) ([]byte, error) {
	var out bytes.Buffer
	name := view
	if t.info.ID != defaultPublicTheme {
		name += ".html"
	}
	if view == "page" && data.Page != nil {
		if item, ok := t.pageTemplate(data.Page.Template); ok {
			if item.fragment != "" {
				fragment, err := t.executePageFragment(item, data)
				if err != nil {
					return nil, err
				}
				data.Content = fragment
			}
			// Rel=stylesheet is valid in the body, so a public theme need not
			// opt into the card stylesheet or the fragment's optional CSS.
			data.Content = templateStyleLinks(item) + data.Content
		}
	}
	if err := t.tmpl.ExecuteTemplate(&out, name, data); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(out.Bytes())) == 0 {
		return nil, errors.New("主题输出为空")
	}
	return out.Bytes(), nil
}

func (t publicTheme) validate() error {
	data := themeData{
		Site:    themeSite{Title: "示例博客", Navigation: []themeLink{{"示例页面", "/pages/example"}}},
		Content: template.HTML("<p>示例正文</p>"), Headings: []markdown.Heading{{ID: "example", Text: "示例章节", Level: 2}},
	}
	post := themePost{Title: "示例文章", URL: "/posts/example", PublishedAt: time.Now(), UpdatedAt: time.Now(), ShowUpdated: true}
	page := themePage{Title: "示例页面", URL: "/pages/example", UpdatedAt: time.Now()}
	for _, view := range themeViews {
		fixture := data
		switch view {
		case "index":
			fixture.Posts = []themePost{post}
		case "post":
			fixture.Post = &post
		case "page":
			fixture.Page = &page
		}
		if _, err := t.execute(view, fixture); err != nil {
			return fmt.Errorf("%s.html: %w", view, err)
		}
	}
	for _, item := range t.pageTemplates {
		fixture := data
		page := page
		page.Template = item.ID
		fixture.Page = &page
		fixture.FriendLinks = []markdown.FriendLink{{Title: "示例博客", URL: "https://example.com/", Description: "示例简介", Initial: "示"}}
		if _, err := t.execute("page", fixture); err != nil {
			return fmt.Errorf("页面模板 %s: %w", item.ID, err)
		}
	}
	if t.tmpl.Lookup("archive.html") != nil {
		fixture := data
		fixture.Archive = &archiveData{Title: "归档", Years: []archiveYear{{Year: "2024", Open: true, Posts: []themePost{post}}}, Matches: 1, Stats: archiveStats{Posts: 1}}
		if _, err := t.execute("archive", fixture); err != nil {
			return fmt.Errorf("archive.html: %w", err)
		}
	}
	return nil
}

func themeAssetType(name string) string {
	if !fs.ValidPath(name) || !themeAssetPath.MatchString(name) {
		return ""
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return ""
		}
	}
	return themeAssetTypes[strings.ToLower(path.Ext(name))]
}

func (a *App) serveThemeAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	typ := themeAssetType(name)
	if typ == "" {
		http.NotFound(w, r)
		return
	}
	root, err := a.openTheme(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	assetRoot, err := openThemeDir(root, "assets")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer assetRoot.Close()
	file, err := openThemeFile(assetRoot, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if strings.HasPrefix(typ, "font/") {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func (a *App) themeStatus(w http.ResponseWriter, r *http.Request) {
	active := a.publications.PublicTheme()
	if active == "" {
		active = defaultPublicTheme
	}
	effective := defaultPublicTheme
	list := []publicThemeInfo{builtinPublicTheme.info}
	entries, err := os.ReadDir(a.themeDirectory())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fail(w, 500, "无法读取博客主题目录")
		return
	}
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || id == defaultPublicTheme || !publicThemeID.MatchString(id) {
			continue
		}
		t, err := a.loadPublicTheme(id)
		if err == nil {
			err = t.validate()
		}
		if err != nil {
			t.info.Error = err.Error()
		} else if id == active {
			effective = id
		}
		list = append(list, t.info)
	}
	if active != defaultPublicTheme {
		found := false
		for _, info := range list {
			if info.ID == active {
				found = true
				break
			}
		}
		if !found {
			list = append(list, publicThemeInfo{ID: active, Name: active, Error: "主题目录已移除"})
		}
	}
	previews := map[string]string{}
	if posts := publication.Published(a.publicSnapshot()); len(posts) > 0 {
		previews["post"] = publication.URL(posts[0].ID)
	}
	if pages := a.publications.Blog().Pages; len(pages) > 0 {
		previews["page"] = publication.PageURL(pages[0].Slug)
	}
	respond(w, map[string]any{"active": active, "effective": effective, "themes": list, "previews": previews})
}

func (a *App) activateTheme(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID string `json:"id"`
	}
	if !decodeSettingsJSON(w, r, &input, 1024, "无效的主题设置") {
		return
	}
	t, err := a.loadPublicTheme(input.ID)
	if err == nil {
		err = t.validate()
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := a.publications.SetPublicTheme(input.ID); err != nil {
		fail(w, 500, "主题设置保存失败")
		return
	}
	a.themeStatus(w, r)
}

func (a *App) previewTheme(w http.ResponseWriter, r *http.Request) {
	t, err := a.loadPublicTheme(r.PathValue("id"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Content-Security-Policy", externalThemeCSP)
	target := r.URL.Query().Get("path")
	if target == "/" || target == "" {
		a.renderPublicIndex(w, &t)
		return
	}
	if target == archivePath {
		a.renderArchive(w, archiveFilter{}, &t)
		return
	}
	kind, value, ok := strings.Cut(strings.TrimPrefix(target, "/"), "/")
	if !ok || !strings.HasPrefix(target, "/") || value == "" || strings.Contains(value, "/") {
		http.NotFound(w, r)
		return
	}
	if kind == "posts" {
		a.renderPublicPost(w, value, &t)
		return
	}
	if kind == "pages" {
		a.renderPublicStandalone(w, value, &t)
		return
	}
	http.NotFound(w, r)
}

func (a *App) effectivePublicTheme() publicTheme {
	if a.publications != nil {
		if id := a.publications.PublicTheme(); id != "" && id != defaultPublicTheme {
			if candidate, err := a.loadPublicTheme(id); err == nil && candidate.validate() == nil {
				return candidate
			}
		}
	}
	return builtinPublicTheme
}

func (a *App) renderThemedPage(w http.ResponseWriter, view string, data themeData, preview *publicTheme) {
	if preview != nil {
		writeThemedPage(w, view, data, *preview, true)
		return
	}
	writeThemedPage(w, view, data, a.effectivePublicTheme(), false)
}

func writeThemedPage(w http.ResponseWriter, view string, data themeData, t publicTheme, preview bool) {
	if view == "archive" && t.info.ID != defaultPublicTheme && t.tmpl.Lookup("archive.html") == nil {
		t = builtinPublicTheme
	}
	body, err := t.execute(view, data)
	if err != nil && !preview && t.info.ID != defaultPublicTheme {
		// data still contains only safe Markdown, not untrusted theme markup.
		t = builtinPublicTheme
		body, err = t.execute(view, data)
	}
	if err != nil {
		if preview {
			fail(w, 400, fmt.Sprintf("主题预览失败：%v", err))
		} else {
			fail(w, 500, "页面渲染失败")
		}
		return
	}
	if preview || t.info.ID != defaultPublicTheme {
		w.Header().Set("Content-Security-Policy", externalThemeCSP)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

func postThemeSummary(article *publication.Article, rendered *markdown.Result) themePost {
	post := themePost{Title: article.Title, URL: publication.URL(article.ID), DateShort: article.PublishedAt.In(blogTimeZone).Format("01-02"),
		PublishedAt: article.PublishedAt, UpdatedAt: article.UpdatedAt, ShowUpdated: article.Revised,
		Words: article.Words, Tags: articleTerms(article.Tags, false), Categories: articleTerms(article.Categories, true)}
	if rendered != nil {
		post.Excerpt = rendered.Excerpt
	}
	return post
}
