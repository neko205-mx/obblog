package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContentManagementLifecycle(t *testing.T) {
	a, dir, _ := publishFixture(t)
	first := publishForTest(t, a, "first.md", nil)
	second := publishForTest(t, a, "second.md", nil)
	h := testHandler(t, a)
	const list = "/admin/api/publications"
	const batch = list + "/batch"
	paths := []string{"first.md", "second.md"}
	if w := publicGet(a, list); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
	w := editRequest(h, "GET", list, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), first.URL) || strings.Contains(w.Body.String(), "YAML_NOT_PUBLIC") || strings.Contains(w.Body.String(), `"source"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	set := func(paths []string, enabled bool) *responseResult {
		w := editRequest(h, "POST", batch, map[string]any{"paths": paths, "enabled": enabled})
		return &responseResult{w.Code, w.Body.String()}
	}
	if w := set([]string{"first.md", "missing.md"}, false); w.code != 404 {
		t.Fatal(w)
	}
	if publicGet(a, first.URL).Code != 200 {
		t.Fatal("failed batch changed availability")
	}
	if w := set(paths, false); w.code != 200 || !strings.Contains(w.body, `"changed":2`) {
		t.Fatal(w)
	}
	if publicGet(a, first.URL).Code != 404 || publicGet(a, second.URL).Code != 404 {
		t.Fatal("withdrawn posts are public")
	}
	date := time.Date(2020, 4, 5, 6, 0, 0, 0, time.UTC)
	if w := editRequest(h, "PATCH", "/admin/api/publication", map[string]any{"path": paths[0], "publishedAt": date}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if publicGet(a, first.URL).Code != 404 {
		t.Fatal("changing date published the article")
	}
	if err := os.Rename(filepath.Join(dir, paths[1]), filepath.Join(dir, "renamed.md")); err != nil {
		t.Fatal(err)
	}
	if w := set(paths, true); w.code != 404 {
		t.Fatal(w)
	}
	if publicGet(a, first.URL).Code != 404 {
		t.Fatal("failed batch partially published")
	}
	if w := set(paths[:1], true); w.code != 200 {
		t.Fatal(w)
	}
	updated := a.publications.Snapshot()[paths[0]]
	if publicGet(a, first.URL).Code != 200 || !updated.PublishedAt.Equal(date) {
		t.Fatal("republish changed URL or date")
	}
}

type responseResult struct {
	code int
	body string
}
