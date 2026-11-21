package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"obblog/internal/markdown"
	"obblog/internal/publication"
	"obblog/internal/vault"
)

const pagesAPI = "/admin/api/pages"

func decodePageResponse[T any](t *testing.T, w *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if w.Code != status {
		t.Fatalf("response = %d, want %d: %.400s", w.Code, status, w.Body.String())
	}
	var result T
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestStandalonePageCRUD(t *testing.T) {
	a, _, _ := publishFixture(t)
	h := testHandler(t, a)
	save := map[string]string{"slug": "about", "title": " About us ", "source": "## Example\n\nPAGE_PRIVATE_FROM_LIST"}
	created := decodePageResponse[pageDetail](t, editRequest(h, "POST", pagesAPI, save), 201)
	if created.Slug != "about" || created.Title != "About us" || created.URL != "/pages/about" || created.Source != save["source"] || created.CreatedAt.IsZero() || !created.CreatedAt.Equal(created.UpdatedAt) || created.Revision == "" {
		t.Fatalf("create: %+v", created)
	}
	duplicate := editRequest(h, "POST", pagesAPI, save)
	if duplicate.Code != 409 {
		t.Fatalf("duplicate page: %d %s", duplicate.Code, duplicate.Body)
	}
	_ = decodePageResponse[pageDetail](t, editRequest(h, "POST", pagesAPI, map[string]string{"slug": "other", "title": "Other", "source": ""}), 201)
	listed := editRequest(h, "GET", pagesAPI, nil)
	list := decodePageResponse[struct {
		Pages         []publication.PageSummary `json:"pages"`
		Limit         int                       `json:"limit"`
		LinkableNotes []any                     `json:"linkableNotes"`
	}](t, listed, 200)
	if len(list.Pages) != 2 || list.Pages[0] != created.PageSummary || list.Limit != 12 || list.LinkableNotes == nil || strings.Contains(listed.Body.String(), "PAGE_PRIVATE_FROM_LIST") {
		t.Fatalf("invalid list: %s", listed.Body.String())
	}
	// A template can expose starter Markdown, but saved page summaries never
	// include their source.
	entries := decodePageResponse[struct{ Pages []map[string]json.RawMessage }](t, listed, 200)
	for _, entry := range entries.Pages {
		if _, ok := entry["source"]; ok {
			t.Fatalf("saved source leaked in page summary: %s", listed.Body.String())
		}
	}
	get := decodePageResponse[pageDetail](t, editRequest(h, "GET", pagesAPI+"/about", nil), 200)
	if !reflect.DeepEqual(get, created) {
		t.Fatalf("read differs from create: %+v", get)
	}

	update := map[string]string{"slug": "other", "title": "Renamed", "source": "Changed", "revision": created.Revision}
	if w := editRequest(h, "PUT", pagesAPI+"/about", update); w.Code != 409 {
		t.Fatalf("rename conflict: %d %s", w.Code, w.Body)
	}
	update["slug"] = "team"
	updated := decodePageResponse[pageDetail](t, editRequest(h, "PUT", pagesAPI+"/about", update), 200)
	if updated.Title != update["title"] || updated.Source != update["source"] || !updated.CreatedAt.Equal(created.CreatedAt) || !updated.UpdatedAt.After(created.UpdatedAt) || updated.URL != "/pages/team" || updated.Revision == created.Revision {
		t.Fatalf("update: %+v", updated)
	}
	if w := editRequest(h, "PUT", pagesAPI+"/team", update); w.Code != 409 {
		t.Fatalf("stale update: %d %s", w.Code, w.Body)
	}
	if w := editRequest(h, "DELETE", pagesAPI+"/team", map[string]string{"revision": created.Revision}); w.Code != 409 {
		t.Fatalf("stale deletion: %d %s", w.Code, w.Body)
	}
	for _, route := range []string{pagesAPI + "/about", "/pages/about"} {
		if w := editRequest(h, "GET", route, nil); w.Code != 404 {
			t.Fatalf("old route %s: %d", route, w.Code)
		}
	}
	home := editRequest(a.Handler(), "GET", "/", nil).Body.String()
	team, other := strings.Index(home, `href="/pages/team"`), strings.Index(home, `href="/pages/other"`)
	if team < 0 || other <= team || strings.Contains(home, `href="/pages/about"`) {
		t.Fatal("rename must keep navigation placement and drop the old URL")
	}
	if w := editRequest(a.Handler(), "GET", "/pages/team", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Changed") {
		t.Fatalf("renamed public page: %d %s", w.Code, w.Body)
	}
	remove := map[string]string{"revision": updated.Revision}
	deleted := decodePageResponse[map[string]bool](t, editRequest(h, "DELETE", pagesAPI+"/team", remove), 200)
	if !deleted["ok"] {
		t.Fatal("missing delete acknowledgement")
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		var body any
		if method == "PUT" {
			body = update
		} else if method == "DELETE" {
			body = remove
		}
		if w := editRequest(h, method, pagesAPI+"/team", body); w.Code != 404 {
			t.Fatalf("%s missing: %d %s", method, w.Code, w.Body)
		}
	}
	if w := editRequest(a.Handler(), "GET", "/pages/team", nil); w.Code != 404 {
		t.Fatal("deleted page is still public")
	}
	if home := editRequest(a.Handler(), "GET", "/", nil).Body.String(); strings.Contains(home, `href="/pages/team"`) || !strings.Contains(home, `href="/pages/other"`) {
		t.Fatal("deleting one page must remove only its navigation entry")
	}
}

func TestStandalonePagePreviewParityAndLinkIsolation(t *testing.T) {
	a, _, _ := publishFixture(t)
	h := testHandler(t, a)
	first := publishForTest(t, a, "first.md", nil)
	second := publishForTest(t, a, "second.md", nil)
	raw, _, err := a.vault.ReadNote("second.md")
	if err != nil {
		t.Fatal(err)
	}
	if w := editRequest(h, "PUT", "/admin/api/publication", map[string]any{"path": "second.md", "enabled": false, "revision": vault.Revision(raw)}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	listed := decodePageResponse[struct {
		LinkableNotes []struct{ Path string } `json:"linkableNotes"`
	}](t, editRequest(h, "GET", pagesAPI, nil), 200)
	if len(listed.LinkableNotes) != 1 || listed.LinkableNotes[0].Path != "first.md" {
		t.Fatalf("must list only enabled posts: %+v", listed.LinkableNotes)
	}
	source := `# Test

[[first|Public wiki]] [Public Markdown](first.md#Section)
[[private|Private wiki]] [Private Markdown](private.md)
[[second|Withdrawn]] [Admin](/admin/api/vault) [External](https://example.org/)

![Local](photo.svg) ![Admin asset](/admin/api/asset?path=photo.svg)
![Relative host](//other.example/photo.png) ![Memory](data:image/png;base64,aaa)
![External image](https://example.org/image.png)
<script>alert(0)</script>
<img onerror="alert(0)" src="bad">`
	preview := decodePageResponse[markdown.Result](t, editRequest(h, "POST", pagesAPI+"/preview", map[string]string{"slug": "?incomplete", "title": " ", "source": source}), 200)
	for _, want := range []string{first.URL, first.URL + "#section", `href="https://example.org/"`, `src="https://example.org/image.png"`, "unresolved-link"} {
		if !strings.Contains(preview.HTML, want) {
			t.Errorf("preview missing %q: %s", want, preview.HTML)
		}
	}
	for _, forbidden := range []string{second.URL, "private.md", "/admin/", "src=\"/", "src=\"data:", "<script", "onerror="} {
		if strings.Contains(preview.HTML, forbidden) {
			t.Errorf("unsafe preview exposed %q: %s", forbidden, preview.HTML)
		}
	}
	if w := editRequest(h, "GET", pagesAPI, nil).Body.String(); !strings.Contains(w, `"pages":[]`) {
		t.Fatalf("preview saved a page: %s", w)
	}
	_ = decodePageResponse[markdown.Result](t, editRequest(h, "POST", pagesAPI+"/preview", map[string]string{"source": ""}), 200)
	_ = decodePageResponse[pageDetail](t, editRequest(h, "POST", pagesAPI, map[string]string{"slug": "about", "title": "Escaped <title>", "source": source}), 201)
	page := editRequest(a.Handler(), "GET", "/pages/about", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `<article class="article-body">`+preview.HTML+`</article>`) {
		t.Fatalf("public body differs from preview: %s", page.Body)
	}
}

func TestStandalonePageRequestValidation(t *testing.T) {
	a, _, _ := publishFixture(t)
	h := testHandler(t, a)
	cases := []struct {
		name, method, path, media, body string
		status                          int
	}{
		{"mime", "POST", pagesAPI, "text/plain", `{"slug":"ok","title":"Title","source":""}`, 415},
		{"missing MIME", "POST", pagesAPI + "/preview", "", `{"source":"x"}`, 415},
		{"bad JSON", "POST", pagesAPI, "application/json", `{`, 400},
		{"trailing JSON", "POST", pagesAPI, "application/json", `{"slug":"ok","title":"Title","source":""}{}`, 400},
		{"bad slug", "POST", pagesAPI, "application/json", `{"slug":"Nope/../test","title":"Title","source":""}`, 400},
		{"uppercase slug", "POST", pagesAPI, "application/json", `{"slug":"Upper","title":"Title","source":""}`, 400},
		{"long slug", "POST", pagesAPI, "application/json", `{"slug":"` + strings.Repeat("a", 65) + `","title":"Title","source":""}`, 400},
		{"no title", "POST", pagesAPI, "application/json", `{"slug":"ok","source":""}`, 400},
		{"long title", "POST", pagesAPI, "application/json", `{"slug":"ok","title":"` + strings.Repeat("a", 121) + `","source":""}`, 400},
		{"no source", "POST", pagesAPI, "application/json", `{"slug":"ok","title":"Title"}`, 400},
		{"null source", "POST", pagesAPI + "/preview", "application/json", `{"source":null}`, 400},
		{"server timestamp", "POST", pagesAPI, "application/json", `{"slug":"ok","title":"Title","source":"","createdAt":"2000-01-01T00:00:00Z"}`, 400},
		{"server revision", "POST", pagesAPI, "application/json", `{"slug":"ok","title":"Title","source":"","revision":"mine"}`, 400},
		{"source limit", "POST", pagesAPI + "/preview", "application/json", `{"source":"` + strings.Repeat("a", publication.MaxPageSource+1) + `"}`, 413},
		{"decoded write limit", "POST", pagesAPI, "application/json", `{"slug":"ok","title":"Title","source":"` + strings.Repeat("a", publication.MaxPageSource+1) + `"}`, 413},
		{"delete request limit", "DELETE", pagesAPI + "/ok", "application/json", `{"revision":"` + strings.Repeat("a", 1024) + `"}`, 413},
		{"missing delete revision", "DELETE", pagesAPI + "/ok", "application/json", `{}`, 400},
		{"missing update revision", "PUT", pagesAPI + "/ok", "application/json", `{"slug":"ok","title":"Title","source":""}`, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.media != "" {
				r.Header.Set("Content-Type", tc.media)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d, want %d: %.400s", w.Code, tc.status, w.Body.String())
			}
		})
	}
	for i := range publication.PageLimit + 1 {
		w := editRequest(h, "POST", pagesAPI, map[string]string{"slug": fmt.Sprintf("page-%d", i), "title": "Title", "source": ""})
		want := 201
		if i == publication.PageLimit {
			want = 409
		}
		if w.Code != want {
			t.Fatalf("page limit response: %d, want %d", w.Code, want)
		}
	}
}

func TestStandalonePageAuthenticationAndCSRF(t *testing.T) {
	a, _, _ := publishFixture(t)
	h := a.Handler()
	for _, endpoint := range []struct{ method, path string }{
		{"GET", pagesAPI}, {"GET", pagesAPI + "/about"}, {"POST", pagesAPI},
		{"PUT", pagesAPI + "/about"}, {"DELETE", pagesAPI + "/about"}, {"POST", pagesAPI + "/preview"},
	} {
		if w := editRequest(h, endpoint.method, endpoint.path, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: %d, want 401", endpoint.method, endpoint.path, w.Code)
		}
	}
	r := httptest.NewRequest("POST", pagesAPI+"/preview", strings.NewReader(`{"source":"body"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	testHandler(t, a).ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site preview: %d, want 403", w.Code)
	}
}
