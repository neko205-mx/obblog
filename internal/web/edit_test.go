package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"obblog/internal/vault"
)

// Editing fixtures are always created in t.TempDir. Never use OBBLOG_TEST_VAULT here.
func editableApp(t *testing.T) (*App, string) {
	t.Helper()
	_, dir := appForTest(t)
	app, err := NewWithOptions(dir, Options{Writable: true, Password: testPassword, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	return app, dir
}

func editRequest(handler http.Handler, method, endpoint string, body any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, endpoint, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestEditSaveAndRead(t *testing.T) {
	app, dir := editableApp(t)
	handler := testHandler(t, app)
	source := "\ufeff---\r\ncustom:\r\n  nested: true\r\n---\r\n# 编辑测试\r\n[[other]]\r\n"
	w := editRequest(handler, "PUT", "/admin/api/note", noteInput{Path: "欢迎.md", Source: source, Revision: vault.Revision([]byte("# Hello\n\n[[other]]"))})
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var saved vault.SavedNote
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.Revision != vault.Revision([]byte(source)) {
		t.Fatalf("revision mismatch: %+v, %v", saved, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "欢迎.md"))
	if err != nil || string(b) != source {
		t.Fatal("raw Markdown changed during save")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/note?path=%E6%AC%A2%E8%BF%8E.md", nil))
	var note struct {
		Source   string `json:"source"`
		Revision string `json:"revision"`
		HTML     string `json:"html"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &note); err != nil || note.Source != source || note.Revision != saved.Revision || !strings.Contains(note.HTML, "编辑测试") {
		t.Fatal("saved note not readable with matching revision")
	}
	w = editRequest(handler, "POST", "/admin/api/note", noteInput{Path: "new.md", Source: "# New\n[[other]]"})
	if w.Code != 201 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/graph", nil))
	if !strings.Contains(w.Body.String(), "file:new.md") {
		t.Fatal("new note missing from refreshed graph")
	}
}

func TestEditRejections(t *testing.T) {
	app, dir := editableApp(t)
	handler := testHandler(t, app)
	for _, tc := range []struct {
		method string
		body   noteInput
		code   int
	}{
		{"PUT", noteInput{Path: "other.md", Source: "changed", Revision: vault.Revision([]byte("stale"))}, 409},
		{"POST", noteInput{Path: "other.md", Source: "overwrite"}, 409},
		{"PUT", noteInput{Path: "other.md", Source: "changed"}, 400},
		{"POST", noteInput{Path: "../outside.md", Source: "unsafe"}, 400},
		{"POST", noteInput{Path: ".git/private.md", Source: "unsafe"}, 400},
		{"POST", noteInput{Path: "image.svg", Source: "unsafe"}, 400},
		{"POST", noteInput{Path: "missing/new.md", Source: "new"}, 404},
	} {
		w := editRequest(handler, tc.method, "/admin/api/note", tc.body)
		if w.Code != tc.code {
			t.Errorf("%s %+v: %d %s", tc.method, tc.body, w.Code, w.Body.String())
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "other.md"))
	if err != nil || string(b) != "# Other" {
		t.Fatal("rejected writes changed source")
	}
	r := httptest.NewRequest("PUT", "/admin/api/note", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal("non-JSON write accepted")
	}
	for _, body := range []string{`{"path":"new.md","source":"a"} {}`, `{"path":"new.md","source":"a","unexpected":true}`, `{"path":"new.md"}`, `{"path":"new.md","source":null}`, `{`} {
		r := httptest.NewRequest("POST", "/admin/api/note", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("invalid JSON accepted: %d", w.Code)
		}
	}
}

func TestReadOnlyRejectsAllWrites(t *testing.T) {
	app, dir := appForTest(t)
	for _, method := range []string{"POST", "PUT"} {
		w := editRequest(testHandler(t, app), method, "/admin/api/note", noteInput{Path: "new.md", Source: "must not write"})
		if w.Code != 403 {
			t.Fatalf("read-only %s accepted", method)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "new.md")); !os.IsNotExist(err) {
		t.Fatal("read-only mode created a file")
	}
	w := httptest.NewRecorder()
	testHandler(t, app).ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/vault", nil))
	if !strings.Contains(w.Body.String(), `"writable":false`) {
		t.Fatal("read-only capability missing")
	}
}

func TestCrossOriginWriteRejected(t *testing.T) {
	app, dir := editableApp(t)
	r := httptest.NewRequest("POST", "/admin/api/note", strings.NewReader(`{"path":"new.md","source":"cross-site"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://unrelated.invalid")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	testHandler(t, app).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-origin write accepted: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.md")); !os.IsNotExist(err) {
		t.Fatal("cross-origin request created a file")
	}
}

func TestPreviewNeverWrites(t *testing.T) {
	app, dir := appForTest(t)
	w := editRequest(testHandler(t, app), "POST", "/admin/api/preview", noteInput{Path: "draft.md", Source: "# Preview\n\n[[other]] <script>bad()</script>"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "note=other.md") || strings.Contains(w.Body.String(), "<script>") {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "draft.md")); !os.IsNotExist(err) {
		t.Fatal("preview created a file")
	}
}
