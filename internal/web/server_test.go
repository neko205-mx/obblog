package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"obblog/internal/vault"
)

func appForTest(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"欢迎.md": "# Hello\n\n[[other]]", "other.md": "# Other", "photo.svg": `<svg xmlns="http://www.w3.org/2000/svg"/>`, ".hidden.md": "secret", "settings.json": "secret"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	app, err := NewWithOptions(dir, Options{Password: testPassword, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	return app, dir
}

func TestEndpoints(t *testing.T) {
	app, _ := appForTest(t)
	handler := testHandler(t, app)
	for _, tc := range []struct {
		method, path string
		code         int
		contains     string
	}{
		{"GET", "/admin/", 200, "OB Vault"},
		{"GET", "/admin/app.js", 200, "openNote"},
		{"GET", "/admin/style.css", 200, "@media"},
		{"GET", "/admin/graph.js", 200, "GraphView"},
		{"GET", "/admin/force.js", 200, "Mike Bostock"},
		{"GET", "/admin/graph-model.mjs", 200, "selectGraph"},
		{"GET", "/admin/editor.js", 200, "NoteEditor"},
		{"GET", "/admin/editor-model.mjs", 200, "Draft"},
		{"GET", "/admin/api/graph", 200, `"edges"`},
		{"GET", "/unknown", 404, ""},
		{"GET", "/admin/api/vault", 200, `"notes"`},
		{"GET", "/admin/api/note?path=" + url.QueryEscape("欢迎.md"), 200, "Hello"},
		{"GET", "/admin/api/note?path=missing.md", 404, "笔记不存在"},
		{"GET", "/admin/api/note?path=..%2Foutside.md", 404, "笔记不存在"},
		{"GET", "/admin/api/note?path=.hidden.md", 404, "笔记不存在"},
		{"GET", "/admin/api/note?path=settings.json", 404, "笔记不存在"},
		{"GET", "/admin/api/asset?path=photo.svg", 200, "<svg"},
		{"GET", "/admin/api/asset?path=.hidden.md", 404, ""},
		{"GET", "/admin/api/asset?path=other.md", 404, ""},
		{"POST", "/admin/api/note?path=other.md", 403, "只读模式"},
		{"PUT", "/admin/api/vault", 405, ""},
		{"POST", "/admin/api/graph", 405, ""},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing response headers")
			}
		})
	}
}

func TestNoteResponseAndRefresh(t *testing.T) {
	app, dir := appForTest(t)
	handler := testHandler(t, app)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/note?path="+url.QueryEscape("欢迎.md"), nil))
	var data struct {
		Source string `json:"source"`
		HTML   string `json:"html"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.Source != "# Hello\n\n[[other]]" || !strings.Contains(data.HTML, "note=other.md") || data.Title != "欢迎" {
		t.Fatalf("unexpected response: %+v", data)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.md"), []byte("# New"), 0644); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/vault", nil))
	var snapshot vault.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil || len(snapshot.Notes) != 3 {
		t.Fatalf("refresh failed: %+v, %v", snapshot, err)
	}
	if strings.Contains(w.Body.String(), ".hidden") || strings.Contains(w.Body.String(), "settings.json") {
		t.Fatal("hidden and unsupported files must not appear in the API")
	}
}

func TestReadOnlyAndConcurrentRequests(t *testing.T) {
	app, dir := appForTest(t)
	handler := testHandler(t, app)
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Go(func() {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/note?path="+url.QueryEscape("欢迎.md"), nil))
			if w.Code != http.StatusOK {
				t.Errorf("request failed: %d", w.Code)
			}
		})
	}
	group.Wait()
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 5 {
		t.Fatal("requests should not create files")
	}
	b, err := os.ReadFile(filepath.Join(dir, "欢迎.md"))
	if err != nil || string(b) != "# Hello\n\n[[other]]" {
		t.Fatal("requests should not modify source")
	}
}

func TestAssetRange(t *testing.T) {
	app, _ := appForTest(t)
	r := httptest.NewRequest("GET", "/admin/api/asset?path=photo.svg", nil)
	r.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	testHandler(t, app).ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "<svg" {
		t.Fatalf("range response: %d %q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("direct asset access must not execute scripts")
	}
}

func TestNoteSizeLimit(t *testing.T) {
	app, dir := appForTest(t)
	f, err := os.Create(filepath.Join(dir, "large.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(vault.MaxNoteSize + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	w := httptest.NewRecorder()
	testHandler(t, app).ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/note?path=large.md", nil))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected size limit, got %d", w.Code)
	}
}

func TestRealVaultHTTP(t *testing.T) {
	dir := os.Getenv("OBBLOG_TEST_VAULT")
	if dir == "" {
		t.Skip("set OBBLOG_TEST_VAULT for read-only integration testing")
	}
	app, err := NewWithOptions(dir, Options{Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler := testHandler(t, app)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/vault", nil))
	var snapshot vault.Snapshot
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &snapshot) != nil {
		t.Fatal("real vault directory API failed")
	}
	for _, n := range snapshot.Notes {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/note?"+url.Values{"path": {n.Path}}.Encode(), nil))
		if w.Code != http.StatusOK {
			t.Fatalf("real note API returned %d", w.Code)
		}
		var note struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &note); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(n.Path)))
		if err != nil || note.Source != string(raw) {
			t.Fatal("API must preserve the original source exactly")
		}
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/graph", nil))
	if w.Code != http.StatusOK || !json.Valid(w.Body.Bytes()) {
		t.Fatal("real vault graph API failed")
	}
	t.Logf("real vault HTTP: %d note renders passed; raw Markdown preserved; graph endpoint passed", len(snapshot.Notes))
}
