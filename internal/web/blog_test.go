package web

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
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
	for _, want := range []string{"CUSTOM_BLOG_TITLE", "CUSTOM_DESCRIPTION", "CUSTOM_FOOTER", `data-theme="light"`, "/theme.css", "/pages/about"} {
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

func prefsRequest(t *testing.T, a *App, method string, payload any) (*httptestResponse, []publication.Preference) {
	t.Helper()
	var body any
	if payload != nil {
		body = payload
	}
	w := editRequest(testHandler(t, a), method, "/admin/api/preferences", body)
	var result []publication.Preference
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode preferences: %v (%s)", err, w.Body.String())
		}
	}
	return &httptestResponse{w.Code, w.Body.String()}, result
}

func prefValue(list []publication.Preference, key string) (bool, bool) {
	for _, pref := range list {
		if pref.Key == key {
			return pref.Value, true
		}
	}
	return false, false
}

// TestPreferencesAreRegistryDriven asserts the panel is fed by the registry, so
// new features appear without a bespoke UI field.
func TestPreferencesAreRegistryDriven(t *testing.T) {
	a, _, _ := publishFixture(t)
	response, list := prefsRequest(t, a, "GET", nil)
	if response.Code != 200 {
		t.Fatalf("GET: %d %s", response.Code, response.Body)
	}
	if len(list) != len(publication.PreferenceKeys()) {
		t.Fatalf("panel received %d options, registry has %d", len(list), len(publication.PreferenceKeys()))
	}
	for _, pref := range list {
		if pref.Kind != "toggle" || pref.Label == "" || pref.Section == "" {
			t.Errorf("option not renderable: %+v", pref)
		}
		if pref.Value != pref.Default {
			t.Errorf("unset option %s = %v, want default %v", pref.Key, pref.Value, pref.Default)
		}
	}
}

func TestPreferencesToggleAndReject(t *testing.T) {
	a, _, _ := publishFixture(t)
	key := "editor.spellcheck"
	response, list := prefsRequest(t, a, "POST", map[string]any{"values": map[string]bool{key: true}})
	if response.Code != 200 {
		t.Fatalf("save: %d %s", response.Code, response.Body)
	}
	if value, ok := prefValue(list, key); !ok || !value {
		t.Fatalf("%s was not enabled: %+v", key, list)
	}

	// Persisted across a fresh open of the same data directory.
	_, list = prefsRequest(t, a, "GET", nil)
	if value, _ := prefValue(list, key); !value {
		t.Fatal("preference did not survive a reload")
	}

	for _, payload := range []map[string]any{
		{"values": map[string]bool{"no.such.option": true}},
		{},
	} {
		if response, _ := prefsRequest(t, a, "POST", payload); response.Code != 400 {
			t.Errorf("payload %v: want 400, got %d", payload, response.Code)
		}
	}
}

// TestPreferencesRejectLockedOptions makes sure a read-only vault cannot be
// flipped into an editable-only behaviour by a crafted request.
func TestPreferencesRejectLockedOptions(t *testing.T) {
	dir := t.TempDir()
	a, err := NewWithOptions(dir, Options{Password: testPassword, Writable: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	_, list := prefsRequest(t, a, "GET", nil)
	for _, pref := range list {
		if strings.HasPrefix(pref.Key, "editor.") && pref.Locked == "" {
			t.Errorf("%s must be locked on a read-only vault", pref.Key)
		}
	}
	if response, _ := prefsRequest(t, a, "POST", map[string]any{"values": map[string]bool{"editor.autoPreview": false}}); response.Code != 400 {
		t.Fatalf("locked option must be rejected, got %d", response.Code)
	}
}

func TestPreferencesRequireAuthentication(t *testing.T) {
	a, _, _ := publishFixture(t)
	handler := a.Handler()
	for _, method := range []string{"GET", "POST"} {
		if w := editRequest(handler, method, "/admin/api/preferences", map[string]any{"values": map[string]bool{}}); w.Code != 401 {
			t.Fatalf("%s without a session: %d", method, w.Code)
		}
	}
}

// TestThemeCssDerivesPaletteFromAccent guards the rule that a saved accent
// repaints every surface. Regressions here are silent: the panel updates but
// the pages keep their old colors.
func TestThemeCssDerivesPaletteFromAccent(t *testing.T) {
	for _, accent := range []string{"#2f6f8f", "#a0403f"} {
		blog := *publication.DefaultBlog()
		blog.Accent = accent
		css := themeCSS(&blog)
		// Every surface token must be mixed from the chosen accent.
		for _, token := range []string{"--bg", "--surface", "--sidebar", "--raised", "--border", "--text", "--heading", "--muted", "--accent", "--accent-bg", "--paper", "--ink"} {
			needle := token + ": color-mix(in srgb, " + accent
			if !strings.Contains(css, needle) {
				t.Errorf("accent %s: %s is not derived from the accent", accent, token)
			}
		}
		// Both schemes must be present so light and dark both follow the accent.
		if !strings.Contains(css, `html[data-theme="dark"]`) || !strings.Contains(css, "prefers-color-scheme: dark") {
			t.Error("theme.css must define an explicit dark block and a media-query fallback")
		}
	}
}

// TestNoSurfaceShipsItsOwnPalette asserts the per-page stylesheets no longer
// hardcode a color scheme; they must read the shared tokens instead.
func TestNoSurfaceShipsItsOwnPalette(t *testing.T) {
	for _, name := range []string{"style.css", "entry.css", "settings.css", "install.css", "blog.css"} {
		data, err := assets.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		css := string(data)
		if strings.Contains(css, ":root {\n  --bg:") || strings.Contains(css, "--accent: #") {
			t.Errorf("%s still declares its own palette", name)
		}
	}
}

// TestEveryPageLinksTheSharedTheme makes sure the solved problem cannot come
// back: each HTML surface must load the generated stylesheet.
func TestEveryPageLinksTheSharedTheme(t *testing.T) {
	pages := map[string]string{
		"index.html":    "/theme.css",
		"login.html":    "/theme.css",
		"settings.html": "/theme.css",
		"install.html":  "/install/theme.css",
		"public.html":   "/theme.css",
	}
	for name, want := range pages {
		data, err := assets.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s must link %s", name, want)
		}
	}
}

// TestBlogPanelOffersVisualPickers asserts the settings page shows colors as
// swatches with names instead of a hex-only dropdown.
func TestBlogPanelOffersVisualPickers(t *testing.T) {
	html, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(html)
	if strings.Contains(page, `<select id="blog-accent"`) {
		t.Error("accent must not be a dropdown of hex values")
	}
	for _, want := range []string{`id="blog-accent"`, `class="accent-swatches"`, `name="blog-theme"`, `class="theme-options"`} {
		if !strings.Contains(page, want) {
			t.Errorf("panel missing visual picker markup: %q", want)
		}
	}
	js, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"松绿", "雾蓝", "赭石", "紫藤", "石青", "朱砂", "苔绿", "墨灰"} {
		if !strings.Contains(string(js), name) {
			t.Errorf("swatch names should be human readable, missing %q", name)
		}
	}
}

// TestBlogSettingsAcceptRoundTrippedResponse reproduces the failing flow: the
// panel loads the configuration, changes one field and posts the whole payload
// back. The GET response includes read-only extras, so the handler must not
// reject them as unknown fields.
func TestBlogSettingsAcceptRoundTrippedResponse(t *testing.T) {
	a, _, _ := publishFixture(t)
	handler := testHandler(t, a)

	loaded := editRequest(handler, "GET", "/admin/api/blog", nil)
	if loaded.Code != 200 {
		t.Fatalf("GET: %d %s", loaded.Code, loaded.Body)
	}
	var config map[string]any
	if err := json.Unmarshal(loaded.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	// The response carries read-only keys the client cannot send back blindly.
	for _, extra := range []string{"accents", "themes", "vault", "preferences"} {
		if _, ok := config[extra]; !ok {
			t.Fatalf("expected %s in the GET response", extra)
		}
	}

	config["accent"] = publication.Accents[1]
	body, _ := json.Marshal(config)
	r := httptest.NewRequest("POST", "/admin/api/blog", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("round-tripped save failed: %d %s", w.Code, w.Body)
	}

	_, current := blogRequest(t, a, "GET", nil)
	if current.Accent != publication.Accents[1] {
		t.Fatalf("accent not saved: %s", current.Accent)
	}
}

// TestThemeCssDefinesFontStacks guards a failure that is invisible in markup:
// blog.css uses `font: ... var(--serif)`, and an undefined variable invalidates
// the whole shorthand, collapsing headings to the 16px default and making them
// overlap.
func TestThemeCssDefinesFontStacks(t *testing.T) {
	css := themeCSS(publication.DefaultBlog())
	for _, token := range []string{"--serif:", "--sans:", "--mono:"} {
		if !strings.Contains(css, token) {
			t.Errorf("theme.css must define %s", token)
		}
	}
	// Any sheet that consumes a stack must not be the only place it is named.
	for _, name := range []string{"blog.css", "style.css"} {
		data, err := assets.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "var(--serif)") && !strings.Contains(string(data), "var(--sans)") {
			continue
		}
		if strings.Contains(string(data), "--serif:") || strings.Contains(string(data), "--sans:") {
			t.Errorf("%s should rely on theme.css rather than redeclaring stacks", name)
		}
	}
}
