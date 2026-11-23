package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const typechoEndpoint = "/admin/api/import/typecho"
const sampleTypechoSQL = `
INSERT INTO blog_contents (cid,title,slug,created,text,type,status,password) VALUES
(42,'旧博客标题','old-slug',1577836800,'<!--markdown-->\n旧站正文。 ![[hidden.svg]]','post','publish',NULL),
(43,'上一篇','another',1514764800,'<!--markdown-->\n另一个公开文章','post','publish',''),
(44,'DRAFT_PRIVATE','draft',1577836800,'<!--markdown-->\nDRAFT_BODY','post','draft',NULL),
(45,'PASSWORD_PRIVATE','secret',1577836800,'<!--markdown-->\nPASSWORD_BODY','post','publish','p'),
(46,'不公开页面','page',1577836800,'<!--markdown-->\n页面内容','page','publish',NULL),
(47,'旧 HTML','html',1577836800,'<p>旧正文</p>','post','publish',NULL);
INSERT INTO blog_metas (mid,name,slug,type) VALUES (1,'Go','go','tag'), (2,'技术','tech','category'), (3,'typecho','typecho','tag');
INSERT INTO blog_relationships (cid,mid) VALUES (42,1),(42,2),(42,3);
`

func typechoRequest(t *testing.T, h http.Handler, endpoint, sql, checksum, origin string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "typecho.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(sql)); err != nil {
		t.Fatal(err)
	}
	if checksum != "" {
		if err := form.WriteField("checksum", checksum); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, endpoint, &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestTypechoUploadPublishesOnlyEligiblePostsAndPreservesDate(t *testing.T) {
	a, dir, _ := publishFixture(t)
	h := testHandler(t, a)
	if w := typechoRequest(t, a.Handler(), typechoEndpoint+"/preview", sampleTypechoSQL, "", ""); w.Code != 401 {
		t.Fatal("missing session accepted", w.Code)
	}
	if w := typechoRequest(t, h, typechoEndpoint+"/preview", sampleTypechoSQL, "", "https://other.invalid"); w.Code != 403 {
		t.Fatal("cross-origin request accepted", w.Code)
	}
	w := typechoRequest(t, h, typechoEndpoint+"/preview", sampleTypechoSQL, "", "")
	p := decodePageResponse[typechoImportPlan](t, w, 200)
	if p.Summary.Ready != 2 || p.Summary.Skipped != 4 || len(p.Posts) != 2 || p.Posts[0].Path != "typecho/42.md" || p.Blocked != "" {
		t.Fatalf("unexpected preview %+v", p)
	}
	for _, hidden := range []string{"DRAFT_PRIVATE", "DRAFT_BODY", "PASSWORD_PRIVATE", "PASSWORD_BODY", "旧站正文"} {
		if strings.Contains(w.Body.String(), hidden) {
			t.Fatal("preview leaked content", hidden)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, "typecho")); !os.IsNotExist(err) {
		t.Fatal("preview wrote to vault", err)
	}
	if w := typechoRequest(t, h, typechoEndpoint+"/apply", sampleTypechoSQL+"\n-- another file\n", p.Checksum, ""); w.Code != 409 {
		t.Fatal("changed file accepted", w.Code, w.Body)
	}
	applied := decodePageResponse[typechoImportPlan](t, typechoRequest(t, h, typechoEndpoint+"/apply", sampleTypechoSQL, p.Checksum, ""), 200)
	if applied.Summary.Imported != 2 || applied.Posts[0].Status != "imported" || !strings.HasPrefix(applied.Posts[0].URL, "/posts/") {
		t.Fatalf("import failed: %+v", applied)
	}
	note, err := os.ReadFile(filepath.Join(dir, "typecho", "42.md"))
	if err != nil || !strings.Contains(string(note), "tags: [typecho, Go]") || !strings.Contains(string(note), "categories: [技术]") {
		t.Fatalf("note metadata: %s %v", note, err)
	}
	article := a.publications.Snapshot()["typecho/42.md"]
	if article == nil || article.Title != "旧博客标题" || !article.PublishedAt.Equal(time.Unix(1577836800, 0)) || len(article.Assets) != 0 || !article.ExternalImport {
		t.Fatalf("bad publication: %+v", article)
	}
	page := publicGet(a, applied.Posts[0].URL)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "旧站正文") || strings.Contains(page.Body.String(), "/media/") {
		t.Fatal("bad imported public page", page.Code)
	}
	archive := publicGet(a, "/api/posts?tag=typecho")
	if archive.Code != 200 || !strings.Contains(archive.Body.String(), `"total":2`) || !strings.Contains(archive.Body.String(), "旧博客标题") {
		t.Fatal("archive missing import", archive.Body)
	}
	already := decodePageResponse[typechoImportPlan](t, typechoRequest(t, h, typechoEndpoint+"/preview", sampleTypechoSQL, "", ""), 200)
	if already.Summary.Existing != 2 || already.Summary.Ready != 0 || already.Posts[0].URL != applied.Posts[0].URL {
		t.Fatalf("reimport changes publication: %+v", already)
	}
	if _, err := a.publications.Set(a.vault, "typecho/42.md", false, nil, ""); err != nil {
		t.Fatal(err)
	}
	withdrawn := decodePageResponse[typechoImportPlan](t, typechoRequest(t, h, typechoEndpoint+"/preview", sampleTypechoSQL, "", ""), 200)
	if withdrawn.Summary.Conflicts != 1 {
		t.Fatal("withdrawn article not treated as a conflict")
	}
}

func TestTypechoImportGuardsExistingFilesAndReadonlyVault(t *testing.T) {
	a, dir, _ := publishFixture(t)
	h := testHandler(t, a)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "typecho")); err != nil {
		t.Fatal(err)
	}
	p := decodePageResponse[typechoImportPlan](t, typechoRequest(t, h, typechoEndpoint+"/preview", sampleTypechoSQL, "", ""), 200)
	if p.Blocked == "" || p.Summary.Conflicts != 2 {
		t.Fatal("symlink accepted")
	}
	if w := typechoRequest(t, h, typechoEndpoint+"/apply", sampleTypechoSQL, p.Checksum, ""); w.Code != 409 {
		t.Fatal("symlink import accepted", w.Code)
	}
	if matches, err := os.ReadDir(outside); err != nil || len(matches) != 0 {
		t.Fatal("wrote outside migration directory", err)
	}
	a.writable = false
	if w := typechoRequest(t, h, typechoEndpoint+"/apply", sampleTypechoSQL, p.Checksum, ""); w.Code != 403 {
		t.Fatal("read-only import accepted", w.Code)
	}
}

func TestTypechoGitImportPushesOneBatch(t *testing.T) {
	a, dir, remote, _ := gitApp(t)
	h := testHandler(t, a)
	p := decodePageResponse[typechoImportPlan](t, typechoRequest(t, h, typechoEndpoint+"/preview", sampleTypechoSQL, "", ""), 200)
	if p.Blocked != "" {
		t.Fatal(p.Blocked)
	}
	result := decodePageResponse[typechoImportPlan](t, typechoRequest(t, h, typechoEndpoint+"/apply", sampleTypechoSQL, p.Checksum, ""), 200)
	if result.Summary.Imported != 2 {
		t.Fatalf("Git import: %+v", result)
	}
	if gitCmd(t, dir, "rev-list", "--count", "HEAD") != "2" || gitCmd(t, dir, "rev-parse", "HEAD") != gitCmd(t, remote, "rev-parse", "main") || gitCmd(t, dir, "status", "--porcelain") != "" {
		t.Fatal("expected exactly one clean commit on remote")
	}
	if !strings.Contains(gitCmd(t, remote, "show", "main:typecho/42.md"), "旧站正文") {
		t.Fatal("Git remote missing note")
	}
}
