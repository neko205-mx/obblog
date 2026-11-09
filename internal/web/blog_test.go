package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"obblog/internal/publication"
	"obblog/internal/vault"
)

func blogRequest(t *testing.T, a *App, method string, payload any) (*httptestResponse, blogConfig) {
	t.Helper()
	var body any
	if payload != nil {
		body = payload
	}
	w := editRequest(testHandler(t, a), method, "/admin/api/blog", body)
	var result blogConfig
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode blog config: %v (%s)", err, w.Body.String())
		}
	}
	return &httptestResponse{w.Code, w.Body.String()}, result
}

type httptestResponse struct {
	Code int
	Body string
}

func TestBlogConfigDefaultsAndRoundTrip(t *testing.T) {
	a, _, data := publishFixture(t)
	response, current := blogRequest(t, a, "GET", nil)
	if response.Code != 200 {
		t.Fatalf("GET /admin/api/blog: %d %s", response.Code, response.Body)
	}
	if current.Title != publication.DefaultBlog().Title || current.Accent != publication.Accents[0] || current.Theme != "auto" {
		t.Fatalf("unexpected defaults: %+v", current.Blog)
	}
	if len(current.Accents) != len(publication.Accents) || len(current.Themes) != 3 {
		t.Fatal("pickers must receive the server-side palette and theme list")
	}

	payload := map[string]any{
		"title":       "山间笔记",
		"description": "关于阅读与写作",
		"author":      "Neko",
		"accent":      publication.Accents[2],
		"theme":       "dark",
		"footer":      "© 山间笔记",
		"pages": []map[string]any{
			{"slug": "about", "title": "关于本站", "source": "# 关于\n\n这是一个**独立页面**。"},
		},
	}
	response, saved := blogRequest(t, a, "POST", payload)
	if response.Code != 200 {
		t.Fatalf("POST: %d %s", response.Code, response.Body)
	}
	if saved.Title != "山间笔记" || saved.Accent != publication.Accents[2] || saved.Theme != "dark" {
		t.Fatalf("unexpected saved config: %+v", saved.Blog)
	}
	if len(saved.Pages) != 1 || saved.Pages[0].Slug != "about" {
		t.Fatalf("page not stored: %+v", saved.Pages)
	}
	if saved.Pages[0].CreatedAt.IsZero() || saved.Pages[0].UpdatedAt.IsZero() {
		t.Fatal("page timestamps must be filled in")
	}

	// Restart from the same data directory: the settings must survive.
	reopened, err := publication.Open(data, filepath.Join(filepath.Dir(data), "notes"))
	if err != nil {
		t.Fatal(err)
	}
	reloaded := reopened.Blog()
	if reloaded.Title != "山间笔记" || reloaded.Theme != "dark" || len(reloaded.Pages) != 1 {
		t.Fatalf("settings did not persist: %+v", reloaded)
	}
}

func TestBlogConfigRejectsInvalidValues(t *testing.T) {
	a, _, _ := publishFixture(t)
	cases := []struct {
		name    string
		payload map[string]any
	}{
		{"empty title", map[string]any{"title": "  ", "accent": publication.Accents[0], "theme": "auto"}},
		{"unlisted accent", map[string]any{"title": "x", "accent": "#123456", "theme": "auto"}},
		{"css injection", map[string]any{"title": "x", "accent": "red;background:url(x)", "theme": "auto"}},
		{"unknown theme", map[string]any{"title": "x", "accent": publication.Accents[0], "theme": "sepia"}},
		{"bad slug", map[string]any{"title": "x", "accent": publication.Accents[0], "theme": "auto", "pages": []map[string]any{{"slug": "About Me", "title": "t"}}}},
		{"duplicate slug", map[string]any{"title": "x", "accent": publication.Accents[0], "theme": "auto", "pages": []map[string]any{{"slug": "a", "title": "t"}, {"slug": "a", "title": "u"}}}},
		{"missing page title", map[string]any{"title": "x", "accent": publication.Accents[0], "theme": "auto", "pages": []map[string]any{{"slug": "a", "title": " "}}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response, _ := blogRequest(t, a, "POST", testCase.payload)
			if response.Code != 400 {
				t.Fatalf("want 400, got %d %s", response.Code, response.Body)
			}
		})
	}
	// A rejected save must leave the previous configuration untouched.
	_, current := blogRequest(t, a, "GET", nil)
	if current.Title != publication.DefaultBlog().Title {
		t.Fatalf("failed saves changed the configuration: %+v", current.Blog)
	}
}

func TestPublicSiteUsesBlogConfiguration(t *testing.T) {
	a, _, _ := publishFixture(t)
	publishForTest(t, a, "first.md", nil)
	payload := map[string]any{
		"title":       "CUSTOM_BLOG_TITLE",
		"description": "CUSTOM_DESCRIPTION",
		"author":      "CUSTOM_AUTHOR",
		"accent":      publication.Accents[1],
		"theme":       "light",
		"footer":      "CUSTOM_FOOTER",
		"pages": []map[string]any{
			{"slug": "about", "title": "ABOUT_PAGE", "source": "## ABOUT_SECTION\n\nPage body with [[first|a link]]."},
		},
	}
	if response, _ := blogRequest(t, a, "POST", payload); response.Code != 200 {
		t.Fatalf("save blog: %d %s", response.Code, response.Body)
	}

	index := editRequest(testHandler(t, a), "GET", "/", nil)
	body := index.Body.String()
	for _, want := range []string{"CUSTOM_BLOG_TITLE", "CUSTOM_DESCRIPTION", "CUSTOM_FOOTER", `data-theme="light"`, "style=\"--accent:" + publication.Accents[1], "/pages/about"} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}

	posts := a.publications.Snapshot()
	var id string
	for _, article := range posts {
		if article.Path == "first.md" {
			id = article.ID
		}
	}
	if id == "" {
		t.Fatal("fixture article was not published")
	}
	post := editRequest(testHandler(t, a), "GET", "/posts/"+id, nil)
	if post.Code != 200 {
		t.Fatalf("post: %d", post.Code)
	}
	postBody := post.Body.String()
	for _, want := range []string{"CUSTOM_BLOG_TITLE", "CUSTOM_AUTHOR", `data-theme="light"`} {
		if !strings.Contains(postBody, want) {
			t.Errorf("post missing %q", want)
		}
	}

	page := editRequest(testHandler(t, a), "GET", "/pages/about", nil)
	if page.Code != 200 {
		t.Fatalf("page: %d %s", page.Code, page.Body)
	}
	pageBody := page.Body.String()
	for _, want := range []string{"ABOUT_PAGE", "ABOUT_SECTION", "/posts/" + id} {
		if !strings.Contains(pageBody, want) {
			t.Errorf("page missing %q", want)
		}
	}

	if missing := editRequest(testHandler(t, a), "GET", "/pages/unknown", nil); missing.Code != 404 {
		t.Fatalf("unknown page must 404, got %d", missing.Code)
	}
}

func TestBlogConfigRequiresAuthentication(t *testing.T) {
	a, _, _ := publishFixture(t)
	handler := a.Handler()
	for _, method := range []string{"GET", "POST"} {
		w := editRequest(handler, method, "/admin/api/blog", map[string]any{"title": "x"})
		if w.Code != 401 {
			t.Fatalf("%s without a session: %d", method, w.Code)
		}
	}
}

// TestBlogConfigStaysOutOfTheVault guards the rule that publishing data never
// writes into the Markdown directory.
func TestBlogConfigStaysOutOfTheVault(t *testing.T) {
	a, dir, _ := publishFixture(t)
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if response, _ := blogRequest(t, a, "POST", map[string]any{"title": "x", "accent": publication.Accents[0], "theme": "auto", "pages": []map[string]any{{"slug": "about", "title": "t", "source": "body"}}}); response.Code != 200 {
		t.Fatalf("save: %d %s", response.Code, response.Body)
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("blog settings wrote into the vault: %d -> %d entries", len(before), len(after))
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(dir), "data", "publications"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one publication store directory, got %v (%v)", entries, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "data", "publications", entries[0].Name(), "publications.json")); err != nil {
		t.Fatal(err)
	}
}

// TestAdminStaticAssetsAreReachable guards the hand-maintained allowlist in
// Handler: every file referenced by app.js must be served.
func TestAdminStaticAssetsAreReachable(t *testing.T) {
	a, _, _ := publishFixture(t)
	handler := testHandler(t, a)
	for _, asset := range []string{"index.html", "app.js", "style.css", "graph.js", "graph-model.mjs", "force.js", "editor.js", "editor-model.mjs", "blog-model.mjs"} {
		w := editRequest(handler, "GET", "/admin/"+asset, nil)
		if w.Code != 200 {
			t.Errorf("%s: %d", asset, w.Code)
		}
	}
	// Imports that are not allowlisted fail closed rather than leaking files.
	if w := editRequest(handler, "GET", "/admin/settings.js", nil); w.Code != 404 {
		t.Errorf("unlisted asset must 404, got %d", w.Code)
	}
}

func TestResolvePublishedMirrorsVaultPreference(t *testing.T) {
	entries := map[string]*vault.Entry{
		"a/笔记.md":  {Name: "笔记.md", Path: "a/笔记.md", Kind: "note"},
		"b/笔记.md":  {Name: "笔记.md", Path: "b/笔记.md", Kind: "note"},
		"a/唯一.md":  {Name: "唯一.md", Path: "a/唯一.md", Kind: "note"},
		"深/层/独.md": {Name: "独.md", Path: "深/层/独.md", Kind: "note"},
	}
	cases := []struct {
		target string
		path   string
	}{
		{"a/笔记.md", "a/笔记.md"}, // exact path wins even when ambiguous by suffix
		{"a/唯一", "a/唯一.md"},    // implicit .md
		{"独", "深/层/独.md"},      // unique suffix
		{"笔记", ""},             // two matches: unresolved rather than arbitrary
		{"missing", ""},
		{"", ""},
		{"/abs", ""},
	}
	for _, testCase := range cases {
		got := resolvePublished(entries, testCase.target)
		path := ""
		if got != nil {
			path = got.Path
		}
		if path != testCase.path {
			t.Errorf("resolve(%q) = %q, want %q", testCase.target, path, testCase.path)
		}
	}
}
