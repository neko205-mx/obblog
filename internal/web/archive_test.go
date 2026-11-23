package web

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicArchiveAndPostsAPI(t *testing.T) {
	a, vault, _ := publishFixture(t)
	writeNote := func(name, source string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(vault, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeNote("first.md", "---\ntitle: 公开标题\ndate: 2024-12-31T17:00:00Z\ntags: [Go, go, 知识管理]\ncategories: [技术]\nsecret: PRIVATE_FRONT_MATTER\n---\n# 正文标题\n\n已发布正文 #不纳入归档\n")
	writeNote("second.md", "---\ndate: 2023-05-01\ntags: [go]\ncategories: [生活]\n---\n# 其他文章\n\n示例文字\n")
	writeNote("private.md", "---\ntitle: PRIVATE_VAULT_TITLE\ntags: [PRIVATE_VAULT_TAG]\n---\n不公开\n")
	first := publishForTest(t, a, "first.md", nil)
	second := publishForTest(t, a, "second.md", nil)
	home := publicGet(a, "/")
	if !strings.Contains(home.Body.String(), `href="/archives"`) || !strings.Contains(home.Body.String(), "公开标题") {
		t.Fatal("archive navigation or front matter title missing")
	}
	post := publicGet(a, first.URL)
	for _, marker := range []string{"公开标题", "正文标题", "2025-01-01", `href="/archives?tag=Go"`, "知识管理"} {
		if !strings.Contains(post.Body.String(), marker) {
			t.Fatalf("post missing %q: %s", marker, post.Body.String())
		}
	}
	type response struct {
		Stats      archiveStats
		Tags       []archiveTerm
		Categories []archiveTerm
		Total      int
		Posts      []themePost
	}
	w := publicGet(a, "/api/posts?tag=gO&limit=1&page=2")
	result := decodePageResponse[response](t, w, 200)
	if result.Total != 2 || len(result.Posts) != 1 || result.Posts[0].URL != second.URL || result.Stats.Posts != 2 || result.Stats.Tags != 2 || result.Stats.Categories != 2 || result.Stats.Words < 1 || result.Tags[0].Count != 2 {
		t.Fatalf("pagination, matching or counts wrong: %s", w.Body.String())
	}
	filter := url.Values{"q": {"标题"}, "tag": {"知识管理"}, "category": {"技术"}}
	result = decodePageResponse[response](t, publicGet(a, "/api/posts?"+filter.Encode()), 200)
	if result.Total != 1 || result.Posts[0].URL != first.URL || result.Posts[0].DateShort != "01-01" {
		t.Fatalf("combined filter: %+v", result)
	}
	archive := publicGet(a, "/archives")
	body := archive.Body.String()
	year1, year2 := strings.Index(body, "2025 年"), strings.Index(body, "2023 年")
	if archive.Code != 200 || year1 < 0 || year2 < year1 || !strings.Contains(body, "/archive.css") {
		t.Fatalf("year grouping failed: %d %s", archive.Code, body)
	}
	filtered := publicGet(a, "/archives?"+filter.Encode()).Body.String()
	if !strings.Contains(filtered, "找到 1 篇文章") || !strings.Contains(filtered, "公开标题") || strings.Contains(filtered, "其他文章") {
		t.Fatalf("archive filter: %s", filtered)
	}
	for _, private := range []string{"PRIVATE_FRONT_MATTER", "PRIVATE_VAULT_TITLE", "PRIVATE_VAULT_TAG", `"source"`, `"path"`, "不纳入归档"} {
		if strings.Contains(body, private) || strings.Contains(w.Body.String(), private) {
			t.Fatalf("private or derived-from-body value in public listing: %q", private)
		}
	}
	if publicGet(a, "/archive.css").Code != 200 || publicGet(a, "/api/posts?limit=101").Code != 400 {
		t.Fatal("missing stylesheet or invalid pagination accepted")
	}

	// The public index does not look at source changed outside the publish flow.
	writeNote("first.md", "---\ntitle: UNPUBLISHED_EDIT\ntags: [DRAFT_TAG]\n---\n草稿\n")
	if strings.Contains(publicGet(a, "/api/posts").Body.String(), "DRAFT_TAG") {
		t.Fatal("live draft changed the archive")
	}
	if _, err := a.publications.Set(a.vault, "first.md", false, nil, ""); err != nil {
		t.Fatal(err)
	}
	result = decodePageResponse[response](t, publicGet(a, "/api/posts"), 200)
	if result.Total != 1 || result.Stats.Tags != 1 || result.Stats.Categories != 1 {
		t.Fatalf("unpublish left stale counts: %+v", result)
	}
}

func TestPublicArchiveThemeFallbackAndOverride(t *testing.T) {
	a, _, data := publishFixture(t)
	h := testHandler(t, a)
	root := filepath.Join(data, "themes", "plain-journal")
	if err := os.CopyFS(root, os.DirFS("../../examples/themes/plain-journal")); err != nil {
		t.Fatal(err)
	}
	if w := editRequest(h, "POST", "/admin/api/themes", map[string]string{"id": "plain-journal"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := publicGet(a, "/archives"); w.Code != 200 || !strings.Contains(w.Body.String(), "archive-heading") {
		t.Fatal("theme without archive.html must fall back")
	}
	if err := os.WriteFile(filepath.Join(root, "archive.html"), []byte(`<main>{{.Archive.Title}} CUSTOM_ARCHIVE {{.Archive.Matches}}</main>`), 0600); err != nil {
		t.Fatal(err)
	}
	if w := publicGet(a, "/archives"); w.Code != 200 || !strings.Contains(w.Body.String(), "CUSTOM_ARCHIVE") || w.Header().Get("Content-Security-Policy") != externalThemeCSP {
		t.Fatal("theme archive did not load safely")
	}
}
