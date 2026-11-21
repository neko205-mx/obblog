package web

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"obblog/internal/markdown"
	"obblog/internal/publication"
)

const (
	friendsTemplateID = "default/friends"
	plainPageFormat   = "markdown"
	friendLinksFormat = "friend-links"
)

// A template is a starter document plus an optional public layout fragment.
// Only values safe to copy into the page editor are serialized to clients.
type pageTemplate struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Source      string `json:"source"`
	Format      string `json:"format"`
	fragment    string
	style       string
}

type pageTemplateSpec struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Format      string `json:"format"`
	Style       string `json:"style"`
}

var friendsPageTemplate = pageTemplate{
	ID: friendsTemplateID, Label: "友情链接", Description: "从 Markdown 链接生成带头像和简介的响应式卡片。",
	Title: "友情链接", Slug: "friends", Format: friendLinksFormat,
	Source: "欢迎访问这些站点。\n\n- [示例博客](https://example.com/ \"这里填写一句简介\")\n- [另一站点](https://example.org/) — 简介写在链接后\n",
}

func loadPageTemplates(root *os.Root, themeID string, specs []pageTemplateSpec, tmpl *template.Template) ([]pageTemplate, error) {
	if len(specs) > 16 {
		return nil, errors.New("一个主题最多可注册 16 个页面模板")
	}
	seen := map[string]bool{}
	var result []pageTemplate
	for _, spec := range specs {
		item := pageTemplate{
			ID: themeID + "/" + spec.ID, Label: strings.TrimSpace(spec.Label), Description: strings.TrimSpace(spec.Description),
			Title: strings.TrimSpace(spec.Title), Slug: spec.Slug, Format: spec.Format,
		}
		if item.Title == "" {
			item.Title = item.Label
		}
		if item.Slug == "" {
			item.Slug = spec.ID
		}
		if item.Format == "" {
			item.Format = plainPageFormat
		}
		if !publication.ValidPageTemplate(item.ID) || seen[item.ID] || item.Label == "" || len(item.Label) > 120 || len(item.Title) > 120 || len(item.Description) > 500 || !publication.ValidPageTemplate(themeID+"/"+item.Slug) {
			return nil, fmt.Errorf("无效或重复的页面模板：%q", spec.ID)
		}
		if item.Format != plainPageFormat && item.Format != friendLinksFormat {
			return nil, fmt.Errorf("页面模板 %q 的 format 无效", spec.ID)
		}
		if spec.Style != "" {
			if !strings.EqualFold(path.Ext(spec.Style), ".css") || themeAssetType(spec.Style) == "" {
				return nil, fmt.Errorf("页面模板 %q 的 style 必须是主题 assets 内的 CSS 文件", spec.ID)
			}
			file, err := openThemeFile(root, "assets/"+spec.Style)
			if err != nil {
				return nil, fmt.Errorf("页面模板 %q 的样式文件不可用", spec.ID)
			}
			file.Close()
			item.style = "/theme-assets/" + themeID + "/" + spec.Style
		}
		base := "page-templates/" + spec.ID
		item.fragment = base + ".html"
		raw, err := readThemeFile(root, item.fragment, 128<<10)
		if err != nil {
			return nil, err
		}
		if _, err := tmpl.New(item.fragment).Parse(string(raw)); err != nil {
			return nil, err
		}
		if _, err := root.Lstat(base + ".md"); err == nil {
			source, err := readThemeFile(root, base+".md", 32<<10)
			if err != nil {
				return nil, err
			}
			if !utf8.Valid(source) {
				return nil, fmt.Errorf("%s.md 必须是 UTF-8", base)
			}
			item.Source = string(source)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("无法读取 %s.md", base)
		}
		seen[item.ID] = true
		result = append(result, item)
	}
	return result, nil
}

func (t publicTheme) pageTemplate(id string) (pageTemplate, bool) {
	if id == friendsTemplateID {
		return friendsPageTemplate, true
	}
	for _, item := range t.pageTemplates {
		if item.ID == id {
			return item, true
		}
	}
	return pageTemplate{}, false
}

func (t publicTheme) pageTemplateCatalog() []pageTemplate {
	return append([]pageTemplate{friendsPageTemplate}, t.pageTemplates...)
}

func (a *App) acceptsPageTemplate(id string) bool {
	if id == "" || id == friendsTemplateID {
		return true
	}
	if !publication.ValidPageTemplate(id) {
		return false
	}
	_, ok := a.effectivePublicTheme().pageTemplate(id)
	return ok
}

func (t publicTheme) executePageFragment(item pageTemplate, data themeData) (template.HTML, error) {
	var body bytes.Buffer
	if err := t.tmpl.ExecuteTemplate(&body, item.fragment, data); err != nil {
		return "", err
	}
	if len(bytes.TrimSpace(body.Bytes())) == 0 {
		return "", errors.New("页面模板输出为空")
	}
	return template.HTML(body.String()), nil
}

func pageThemeData(blog *publication.Blog, page publication.BlogPage, rendered *markdown.Result) themeData {
	data := newThemeData(blog)
	data.Page = &themePage{Title: page.Title, URL: publication.PageURL(page.Slug), UpdatedAt: page.UpdatedAt, Template: page.Template}
	data.Content, data.Headings, data.FriendLinks = template.HTML(rendered.HTML), rendered.Headings, rendered.FriendLinks
	return data
}

func templateStyleLinks(item pageTemplate) template.HTML {
	var links strings.Builder
	if item.Format == friendLinksFormat {
		links.WriteString(`<link rel="stylesheet" href="/page-templates.css">`)
	}
	if item.style != "" {
		links.WriteString(`<link rel="stylesheet" href="` + template.HTMLEscapeString(item.style) + `">`)
	}
	return template.HTML(links.String())
}

// Theme fragments are trusted only on public responses with their restrictive
// CSP. In the admin the same fragment is rendered in an opaque sandboxed frame.
func themeFragmentPreview(item pageTemplate, content template.HTML) string {
	return `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		// srcdoc has an opaque origin under sandbox, so a new 'self' in the
		// meta policy would block even our styles. The embedding page's CSP
		// is inherited and still limits these requests to its allowed origins.
		`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src http: https: 'unsafe-inline'; img-src http: https:; font-src http: https:; form-action 'none'; base-uri 'none'">` +
		`<link rel="stylesheet" href="/admin/palette.css"><style>body{margin:0;padding:10px;background:var(--bg);color:var(--text);font:15px/1.65 var(--sans);overflow-wrap:anywhere}a{color:var(--accent)}</style>` +
		string(templateStyleLinks(item)) + `<main>` + string(content) + `</main></html>`
}
