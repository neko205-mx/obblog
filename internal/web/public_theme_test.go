package web

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicThemeLifecycle(t *testing.T) {
	a, vault, data := publishFixture(t)
	post := publishForTest(t, a, "first.md", nil)
	h := testHandler(t, a)
	if w := editRequest(h, "POST", pagesAPI, map[string]string{"slug": "about", "title": "About", "source": "## About\n\nAbout body"}); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	const id = "plain-journal"
	root := filepath.Join(data, "themes", id)
	if err := os.CopyFS(root, os.DirFS("../../examples/themes/"+id)); err != nil {
		t.Fatal(err)
	}
	const css = "/theme-assets/plain-journal/style.css"
	const api = "/admin/api/themes"
	preview := "/admin/theme-preview/" + id
	if w := publicGet(a, api); w.Code != 401 {
		t.Fatal("theme settings not protected", w.Code)
	}
	if w := publicGet(a, preview); w.Code != 303 {
		t.Fatal("theme preview not protected", w.Code)
	}
	info := editRequest(h, "GET", api, nil)
	if info.Code != 200 || !strings.Contains(info.Body.String(), `"active":"default"`) || strings.Contains(info.Body.String(), `"error"`) {
		t.Fatal(info.Code, info.Body.String())
	}
	for _, route := range []string{"/", post.URL, "/pages/about"} {
		w := editRequest(h, "GET", preview+"?"+url.Values{"path": {route}}.Encode(), nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), css) || !strings.Contains(w.Body.String(), "/pages/about") || w.Header().Get("Content-Security-Policy") != externalThemeCSP {
			t.Fatalf("preview %s: %d %s", route, w.Code, w.Body.String())
		}
		for _, private := range []string{"YAML_NOT_PUBLIC", "PRIVATE_BODY", "COMMENT_NOT_PUBLIC"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatal("private content in preview")
			}
		}
	}
	if w := editRequest(h, "GET", preview+"?path=/admin/", nil); w.Code != 404 {
		t.Fatal("admin preview allowed", w.Code)
	}
	if strings.Contains(publicGet(a, "/").Body.String(), css) {
		t.Fatal("preview changed active theme")
	}
	if w := editRequest(h, "POST", api, map[string]string{"id": id}); w.Code != 200 || !strings.Contains(w.Body.String(), `"effective":"plain-journal"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := publicGet(a, "/"); w.Code != 200 || !strings.Contains(w.Body.String(), css) || w.Header().Get("Content-Security-Policy") != externalThemeCSP {
		t.Fatal("public theme not applied")
	}
	if w := publicGet(a, css); w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") {
		t.Fatal("theme stylesheet unavailable")
	}
	if err := os.WriteFile(filepath.Join(data, "private.css"), []byte("PRIVATE_FILE"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(data, "private.css"), filepath.Join(root, "assets", "private.css")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "script.js"), []byte("script"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"private.css", "script.js", "../theme.json", "%2e%2e/theme.json"} {
		if w := publicGet(a, "/theme-assets/"+id+"/"+name); w.Code == 200 {
			t.Fatal("unsafe asset served", name)
		}
	}
	meta := a.publications.Metadata()
	meta.Title = "Updated title"
	if w := editRequest(h, "POST", "/admin/api/blog", meta); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	reopened, err := NewWithOptions(vault, Options{Password: testPassword, DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if w := publicGet(reopened, "/"); !strings.Contains(w.Body.String(), css) || !strings.Contains(w.Body.String(), "Updated title") {
		t.Fatal("theme selection did not persist")
	}
	if err := os.WriteFile(filepath.Join(root, "post.html"), []byte(`{{.PrivateSettings}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if w := editRequest(h, "POST", api, map[string]string{"id": id}); w.Code != 400 {
		t.Fatal("invalid theme could be activated", w.Code)
	}
	if w := publicGet(a, "/"); w.Code != 200 || strings.Contains(w.Body.String(), css) {
		t.Fatal("invalid theme did not fall back globally")
	}
	if w := publicGet(a, post.URL); w.Code != 200 || !strings.Contains(w.Body.String(), "Complete article body") {
		t.Fatal("fallback lost article")
	}
	if err := os.Rename(root, filepath.Join(data, "removed-theme")); err != nil {
		t.Fatal(err)
	}
	status := editRequest(h, "GET", api, nil)
	if !strings.Contains(status.Body.String(), `"active":"plain-journal"`) || !strings.Contains(status.Body.String(), `"effective":"default"`) {
		t.Fatal("missing fallback status")
	}
}
