package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"obblog/internal/gitsync"
	"obblog/internal/vault"
)

var uploadFixture = []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><circle cx="32" cy="32" r="28" fill="green"/></svg>`)

func imageRequest(h http.Handler, name string, data []byte, head string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", name)
	part.Write(data)
	form.WriteField("gitHead", head)
	form.Close()
	r := httptest.NewRequest("POST", "/admin/api/asset", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestImageUploadPreviewAndSave(t *testing.T) {
	a, dir := editableApp(t)
	h := testHandler(t, a)
	for _, want := range []string{"测试图.svg", "测试图 (1).svg"} {
		w := imageRequest(h, "测试图.svg", uploadFixture, "")
		var saved vault.SavedNote
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.Path != want || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("upload: %d %s", w.Code, w.Body.String())
		}
		data, err := os.ReadFile(filepath.Join(dir, saved.Path))
		if err != nil || !bytes.Equal(data, uploadFixture) {
			t.Fatal("image not saved unchanged at vault root")
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/asset?path="+url.QueryEscape(saved.Path), nil))
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), uploadFixture) {
			t.Fatal("uploaded image not readable")
		}
		source := "# 图片\n\n![[" + saved.Path + "]]"
		w = editRequest(h, "POST", "/admin/api/preview", noteInput{Path: "nested/draft.md", Source: source})
		var preview struct{ HTML string }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || !strings.Contains(preview.HTML, "<img") || !strings.Contains(preview.HTML, "/admin/api/asset?path=") {
			t.Fatalf("image preview: %d %s", w.Code, w.Body.String())
		}
	}
	w := editRequest(h, "POST", "/admin/api/note", noteInput{Path: "images.md", Source: "![[测试图.svg]]"})
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/note?path=images.md", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "api/asset?path=") {
		t.Fatal("saved embed cannot render")
	}
}

func TestImageUploadRejections(t *testing.T) {
	a, _ := editableApp(t)
	h := testHandler(t, a)
	if w := imageRequest(a.Handler(), "image.svg", uploadFixture, ""); w.Code != 401 {
		t.Fatalf("unauthenticated upload: %d", w.Code)
	}
	ro, _ := appForTest(t)
	if w := imageRequest(testHandler(t, ro), "image.svg", uploadFixture, ""); w.Code != 403 {
		t.Fatalf("read-only upload: %d", w.Code)
	}
	if w := imageRequest(h, "fake.png", []byte("<html>not an image</html>"), ""); w.Code != 415 {
		t.Fatalf("fake image: %d %s", w.Code, w.Body.String())
	}
	if w := imageRequest(h, "big.png", bytes.Repeat([]byte("x"), vault.MaxImageSize+1), ""); w.Code != 413 {
		t.Fatalf("oversize: %d %s", w.Code, w.Body.String())
	}
	if w := editRequest(h, "POST", "/admin/api/asset", map[string]string{}); w.Code != 415 {
		t.Fatalf("non-multipart: %d", w.Code)
	}
	files, err := os.ReadDir(a.vault.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".png") || strings.HasPrefix(f.Name(), ".obblog-") {
			t.Fatalf("rejected upload wrote %s", f.Name())
		}
	}
}

func TestGitImageUploadAndFollowingNoteSave(t *testing.T) {
	a, dir, remote, _ := gitApp(t)
	h := testHandler(t, a)
	head := gitCmd(t, dir, "rev-parse", "HEAD")
	if w := imageRequest(h, "image.svg", uploadFixture, "stale"); w.Code != 409 {
		t.Fatalf("old Git HEAD accepted: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "image.svg")); !os.IsNotExist(err) {
		t.Fatal("blocked upload wrote image")
	}
	w := imageRequest(h, "image.svg", uploadFixture, head)
	var uploaded gitsync.Result
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &uploaded) != nil || !uploaded.Synced || uploaded.GitHead == head {
		t.Fatalf("Git upload: %d %s", w.Code, w.Body.String())
	}
	if gitCmd(t, remote, "rev-parse", "main") != uploaded.GitHead || gitCmd(t, dir, "status", "--porcelain") != "" {
		t.Fatal("image not pushed cleanly")
	}
	if files := gitCmd(t, dir, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); files != "image.svg" {
		t.Fatalf("unexpected committed files: %s", files)
	}
	w = editRequest(h, "PUT", "/admin/api/note", noteInput{Path: "note.md", Source: "# Initial\n![[image.svg]]", Revision: vault.Revision([]byte("# Initial\n")), GitHead: uploaded.GitHead})
	var saved gitsync.Result
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || !saved.Synced {
		t.Fatalf("save after image upload: %d %s", w.Code, w.Body.String())
	}
	gitCmd(t, dir, "config", "remote.origin.pushurl", filepath.Join(t.TempDir(), "missing.git"))
	w = imageRequest(h, "pending.svg", uploadFixture, saved.GitHead)
	var pending gitsync.Result
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &pending) != nil || pending.Warning == "" || pending.Synced {
		t.Fatalf("failed push lost image acknowledgement: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, pending.Path)); err != nil {
		t.Fatal("pending image lost")
	}
}

func TestUploadedImageJoinsPublicCopyWhenNoteIsSaved(t *testing.T) {
	a, _, _ := publishFixture(t)
	article := publishForTest(t, a, "first.md", nil)
	h := testHandler(t, a)
	w := imageRequest(h, "new-image.svg", uploadFixture, "")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if _, ok := a.publications.Snapshot()["first.md"].Assets["new-image.svg"]; ok {
		t.Fatal("unreferenced upload was published")
	}
	raw, _, err := a.vault.ReadNote("first.md")
	if err != nil {
		t.Fatal(err)
	}
	w = editRequest(h, "PUT", "/admin/api/note", noteInput{Path: "first.md", Source: string(raw) + "\n\n![[new-image.svg]]", Revision: vault.Revision(raw)})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	asset := a.publications.Snapshot()["first.md"].Assets["new-image.svg"]
	if asset == "" || !bytes.Equal(publicGet(a, "/media/"+article.ID+"/"+asset).Body.Bytes(), uploadFixture) {
		t.Fatal("saved embed not copied into public article")
	}
}
