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
	"time"

	"obblog/internal/publication"
	"obblog/internal/vault"
)

// Publication fixtures, including all Git remotes, are temporary and synthetic.
func publishFixture(t *testing.T) (*App, string, string) {
	t.Helper()
	root := t.TempDir()
	dir, data := filepath.Join(root, "notes"), filepath.Join(root, "data")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"first.md":   "---\nsecret: YAML_NOT_PUBLIC\ncover: '[[hidden.svg]]'\n---\n# First article\n\nComplete article body. [[private|Private link]] [[second|Next article]] [next](second.md#Section)\n\n![[photo.svg]]\n\n%% COMMENT_NOT_PUBLIC %%\n\n## Section\n\nArticle ending. <script>alert(1)</script>",
		"second.md":  "# Second article\n\nSecond body.\n\n## Section\n\nDetails.",
		"private.md": "# PRIVATE_TITLE\n\nPRIVATE_BODY",
		"photo.svg":  `<svg xmlns="http://www.w3.org/2000/svg"><text>PUBLIC_IMAGE</text></svg>`,
		"hidden.svg": `<svg>PRIVATE_IMAGE</svg>`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := NewWithOptions(dir, Options{Password: testPassword, Writable: true, DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, dir, data
}

func publishForTest(t *testing.T, a *App, name string, date *time.Time) publicationInfo {
	t.Helper()
	raw, _, err := a.vault.ReadNote(name)
	if err != nil {
		t.Fatal(err)
	}
	head := ""
	if a.git != nil {
		head = a.git.Head(t.Context())
	}
	w := editRequest(testHandler(t, a), "PUT", "/admin/api/publication", map[string]any{"path": name, "enabled": true, "publishedAt": date, "revision": vault.Revision(raw), "gitHead": head})
	var result publicationInfo
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Enabled {
		t.Fatalf("publish: %d %s", w.Code, w.Body.String())
	}
	return result
}

func publicGet(a *App, endpoint string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("GET", endpoint, nil))
	return w
}

func TestPublicationCopiesLinksAndWithdrawal(t *testing.T) {
	a, dir, _ := publishFixture(t)
	before, _ := os.ReadFile(filepath.Join(dir, "first.md"))
	old := time.Date(2020, 3, 4, 8, 30, 0, 0, time.UTC)
	first := publishForTest(t, a, "first.md", &old)
	index := publicGet(a, "/")
	if index.Code != 200 || !strings.Contains(index.Body.String(), first.URL) || !strings.Contains(index.Body.String(), "2020-03-04") || strings.Contains(index.Body.String(), "Second article") {
		t.Fatalf("index: %s", index.Body.String())
	}
	w := publicGet(a, first.URL)
	for _, forbidden := range []string{"PRIVATE_BODY", "PRIVATE_TITLE", "YAML_NOT_PUBLIC", "COMMENT_NOT_PUBLIC", "PRIVATE_IMAGE", "/admin/?", "/admin/api/asset", "<script>", "更新于"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("public copy contains %q: %s", forbidden, w.Body.String())
		}
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Article ending.") {
		t.Fatalf("full article: %d %s", w.Code, w.Body.String())
	}
	article := a.publications.Snapshot()["first.md"]
	if len(article.Assets) != 1 {
		t.Fatalf("frontmatter-only attachments copied: %+v", article.Assets)
	}
	assetURL := "/media/" + first.ID + "/" + article.Assets["photo.svg"]
	image := publicGet(a, assetURL)
	if image.Code != 200 || !strings.Contains(image.Body.String(), "PUBLIC_IMAGE") {
		t.Fatal("copied attachment unavailable")
	}
	if err := os.WriteFile(filepath.Join(dir, "photo.svg"), []byte("NEW_LOCAL_IMAGE"), 0600); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(publicGet(a, assetURL).Body.String(), "PUBLIC_IMAGE") {
		t.Fatal("attachment is not an independent copy")
	}
	r := httptest.NewRequest("GET", assetURL, nil)
	r.Header.Set("Range", "bytes=0-3")
	rangeResult := httptest.NewRecorder()
	a.Handler().ServeHTTP(rangeResult, r)
	if rangeResult.Code != 206 || rangeResult.Body.String() != "<svg" {
		t.Fatal("public attachment ranges unsupported")
	}
	second := publishForTest(t, a, "second.md", nil)
	index = publicGet(a, "/")
	if strings.Index(index.Body.String(), second.URL) > strings.Index(index.Body.String(), first.URL) {
		t.Fatal("article list is not sorted by publication date")
	}
	w = publicGet(a, first.URL)
	if !strings.Contains(w.Body.String(), `href="`+second.URL+`"`) || !strings.Contains(w.Body.String(), second.URL+"#section") {
		t.Fatal("published note links not remapped")
	}
	w = editRequest(testHandler(t, a), "PUT", "/admin/api/publication", map[string]any{"path": "second.md", "enabled": false})
	if w.Code != 200 || publicGet(a, second.URL).Code != 404 || strings.Contains(publicGet(a, first.URL).Body.String(), `href="`+second.URL) {
		t.Fatal("withdrawn note remains linked or accessible")
	}
	w = editRequest(testHandler(t, a), "PUT", "/admin/api/publication", map[string]any{"path": "first.md", "enabled": false})
	if w.Code != 200 || publicGet(a, first.URL).Code != 404 || publicGet(a, assetURL).Code != 404 {
		t.Fatal("withdrawn article/attachment remains accessible")
	}
	republished := publishForTest(t, a, "first.md", nil)
	if republished.ID != first.ID || !republished.PublishedAt.Equal(old) {
		t.Fatal("re-publication changed identity or date")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "first.md"))
	if string(before) != string(after) {
		t.Fatal("publication modified original Markdown")
	}
}

func TestPublicationRefreshDatesRestartAndOfflineCopies(t *testing.T) {
	a, dir, data := publishFixture(t)
	first := publishForTest(t, a, "first.md", nil)
	original := a.publications.Snapshot()["first.md"]
	h := testHandler(t, a)
	for i := 0; i < 2; i++ {
		w := editRequest(h, "POST", "/admin/api/publications/refresh", nil)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if !a.publications.Snapshot()["first.md"].UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatal("no-op refresh changed update time")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "first.md"))
	propertyEdit := strings.Replace(string(raw), "YAML_NOT_PUBLIC", "NEW_PRIVATE_PROPERTY", 1)
	if err := os.WriteFile(filepath.Join(dir, "first.md"), []byte(propertyEdit), 0600); err != nil {
		t.Fatal(err)
	}
	editRequest(h, "POST", "/admin/api/publications/refresh", nil)
	if !a.publications.Snapshot()["first.md"].UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatal("private property changed public update time")
	}
	commentEdit := strings.Replace(propertyEdit, "COMMENT_NOT_PUBLIC", "CHANGED_PRIVATE_COMMENT", 1)
	if err := os.WriteFile(filepath.Join(dir, "first.md"), []byte(commentEdit), 0600); err != nil {
		t.Fatal(err)
	}
	editRequest(h, "POST", "/admin/api/publications/refresh", nil)
	if !a.publications.Snapshot()["first.md"].UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatal("private comment changed public update time")
	}
	newSource := strings.Replace(propertyEdit, "Complete article body.", "UPDATED_PUBLIC_BODY", 1)
	if err := os.WriteFile(filepath.Join(dir, "first.md"), []byte(newSource), 0600); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(publicGet(a, first.URL).Body.String(), "UPDATED_PUBLIC_BODY") {
		t.Fatal("public route read live vault")
	}
	w := editRequest(h, "POST", "/admin/api/publications/refresh", nil)
	updated := a.publications.Snapshot()["first.md"]
	if w.Code != 200 || !updated.PublishedAt.Equal(*first.PublishedAt) || !updated.UpdatedAt.After(original.UpdatedAt) || !updated.Revised {
		t.Fatal("public timestamps incorrect")
	}
	if !strings.Contains(publicGet(a, first.URL).Body.String(), "更新于") {
		t.Fatal("actual content update date not shown")
	}
	restarted, err := NewWithOptions(dir, Options{Password: testPassword, DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if !strings.Contains(publicGet(restarted, first.URL).Body.String(), "UPDATED_PUBLIC_BODY") {
		t.Fatal("published copies not restored after restart")
	}
	if err := os.Rename(dir, dir+"-offline"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(dir+"-offline", dir)
	if w := publicGet(restarted, first.URL); w.Code != 200 || !strings.Contains(w.Body.String(), "UPDATED_PUBLIC_BODY") {
		t.Fatal("public rendering depends on live vault")
	}
	store, err := publication.Open(data, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(publication.Published(store.Snapshot())) != 1 {
		t.Fatal("copies lost while source unavailable")
	}
}

func TestPublicationValidationAndFailedCommit(t *testing.T) {
	a, _, data := publishFixture(t)
	h := testHandler(t, a)
	for _, body := range []any{
		map[string]any{"path": "first.md", "enabled": true, "revision": "stale"},
		map[string]any{"path": "first.md", "enabled": true, "publishedAt": time.Now().Add(24 * time.Hour)},
		map[string]any{"path": "../first.md", "enabled": true},
		map[string]any{"path": "first.md"},
	} {
		w := editRequest(h, "PUT", "/admin/api/publication", body)
		if w.Code != 400 && w.Code != 409 {
			t.Fatalf("invalid publication accepted: %d %s", w.Code, w.Body.String())
		}
	}
	w := editRequest(a.Handler(), "PUT", "/admin/api/publication", map[string]any{"path": "first.md", "enabled": true})
	if w.Code != 401 {
		t.Fatal("anonymous publication accepted")
	}
	first := publishForTest(t, a, "first.md", nil)
	// Force metadata rename failure without relying on filesystem permissions/root.
	matches, err := filepath.Glob(filepath.Join(data, "publications", "*", "publications.json"))
	if err != nil || len(matches) != 1 {
		t.Fatal("publication metadata missing")
	}
	metadata := matches[0]
	if err := os.Rename(metadata, metadata+".backup"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(metadata, 0700); err != nil {
		t.Fatal(err)
	}
	w = editRequest(h, "PUT", "/admin/api/publication", map[string]any{"path": "first.md", "enabled": false})
	if w.Code != 500 || publicGet(a, first.URL).Code != 200 {
		t.Fatal("failed persistence mutated active publication")
	}
	if err := os.Remove(metadata); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(metadata+".backup", metadata); err != nil {
		t.Fatal(err)
	}
	// Concurrent readers must see either complete old or complete new snapshots.
	var group sync.WaitGroup
	for i := 0; i < 6; i++ {
		group.Go(func() {
			for j := 0; j < 8; j++ {
				if w := publicGet(a, first.URL); w.Code != 200 {
					t.Errorf("concurrent public read: %d", w.Code)
				}
			}
		})
	}
	for i := 0; i < 3; i++ {
		editRequest(h, "POST", "/admin/api/publications/refresh", nil)
	}
	group.Wait()
	if w := publicGet(a, "/media/"+first.ID+"/..%2Fconfig.json"); w.Code == 200 {
		t.Fatal("unexpected public file access")
	}
	if w := publicGet(a, "/posts/"+url.PathEscape("first.md")); w.Code != 404 {
		t.Fatal("source paths treated as public IDs")
	}
}

func TestPublishedManagedRecoveryAndVaultSwitch(t *testing.T) {
	rt, data := runtimeForTest(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("# Published before recovery"), 0600); err != nil {
		t.Fatal(err)
	}
	w := runtimeRequest(rt, "POST", "/install/api/configure", nil, setupInput{Mode: "existing", Vault: dir, Password: testPassword, Confirmation: testPassword})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	first := publishForTest(t, rt.app, "note.md", nil)
	if err := os.Rename(dir, dir+"-missing"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(dir+"-missing", dir)
	recovered, err := OpenRuntime(data)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	w = runtimeRequest(recovered, "GET", first.URL, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Published before recovery") {
		t.Fatal("recovery mode lost public copies")
	}
	cookie := runtimeLogin(t, rt, testPassword)
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "note.md"), []byte("# Different private vault"), 0600); err != nil {
		t.Fatal(err)
	}
	w = runtimeRequest(rt, "POST", "/admin/api/settings", cookie, setupInput{Mode: "existing", Vault: other})
	if w.Code != 200 || runtimeRequest(rt, "GET", first.URL, nil, nil).Code != 404 {
		t.Fatal("vault switch reused old publication mapping")
	}
	if strings.Contains(runtimeRequest(rt, "GET", "/", nil, nil).Body.String(), "Different private vault") {
		t.Fatal("same-path note in another vault was published")
	}
}

func TestPublishedGitPullRenameSaveAndDeletion(t *testing.T) {
	a, dir, _, device := gitApp(t)
	date := time.Date(2021, 2, 3, 0, 0, 0, 0, time.UTC)
	first := publishForTest(t, a, "note.md", &date)
	h := testHandler(t, a)
	gitCmd(t, device, "mv", "note.md", "moved.md")
	if err := os.WriteFile(filepath.Join(device, "private.md"), []byte("NOT_FOR_PUBLIC"), 0600); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, device, "add", ".")
	gitCmd(t, device, "commit", "-m", "Move article")
	gitCmd(t, device, "push")
	if publicGet(a, first.URL).Code != 200 {
		t.Fatal("remote changes affected unsynced copy")
	}
	w := editRequest(h, "POST", "/admin/api/git/pull", nil)
	article := a.publications.Snapshot()["moved.md"]
	if w.Code != 200 || article == nil || article.ID != first.ID || !article.PublishedAt.Equal(date) || len(publication.Published(a.publications.Snapshot())) != 1 {
		t.Fatalf("Git rename/publication: %d %s %+v", w.Code, w.Body.String(), article)
	}
	head := gitCmd(t, dir, "rev-parse", "HEAD")
	w = editRequest(h, "PUT", "/admin/api/note", noteInput{Path: "moved.md", Source: "# Web published\n\nNew content.", Revision: vault.Revision([]byte("# Initial\n")), GitHead: head})
	if w.Code != 200 || !strings.Contains(publicGet(a, first.URL).Body.String(), "Web published") {
		t.Fatalf("web save did not update copy: %d %s", w.Code, w.Body.String())
	}
	gitCmd(t, device, "pull", "--ff-only")
	gitCmd(t, device, "rm", "moved.md")
	gitCmd(t, device, "commit", "-m", "Remove article")
	gitCmd(t, device, "push")
	w = editRequest(h, "POST", "/admin/api/git/pull", nil)
	if w.Code != 200 || publicGet(a, first.URL).Code != 404 {
		t.Fatal("deleted note remained published")
	}
}

func TestPublicationFailedGitPushDoesNotPublishDraft(t *testing.T) {
	a, dir, remote, _ := gitApp(t)
	first := publishForTest(t, a, "note.md", nil)
	head := gitCmd(t, dir, "rev-parse", "HEAD")
	gitCmd(t, dir, "config", "remote.origin.pushurl", filepath.Join(t.TempDir(), "unavailable.git"))
	h := testHandler(t, a)
	w := editRequest(h, "PUT", "/admin/api/note", noteInput{Path: "note.md", Source: "# NOT_SYNCED\n", Revision: vault.Revision([]byte("# Initial\n")), GitHead: head})
	if w.Code != 200 || strings.Contains(publicGet(a, first.URL).Body.String(), "NOT_SYNCED") {
		t.Fatal("un-pushed draft was published")
	}
	w = editRequest(h, "POST", "/admin/api/publications/refresh", nil)
	if w.Code != 409 {
		t.Fatal("refresh bypassed pending Git state")
	}
	gitCmd(t, dir, "config", "--unset", "remote.origin.pushurl")
	w = editRequest(h, "POST", "/admin/api/git/push", nil)
	if w.Code != http.StatusOK || !strings.Contains(publicGet(a, first.URL).Body.String(), "NOT_SYNCED") || gitCmd(t, remote, "rev-parse", "main") == head {
		t.Fatal("successful retry did not update published copy")
	}
}

func TestPublishedRenameRetryAfterStorageFailure(t *testing.T) {
	a, dir, _, device := gitApp(t)
	first := publishForTest(t, a, "note.md", nil)
	h := testHandler(t, a)
	gitCmd(t, device, "mv", "note.md", "renamed.md")
	gitCmd(t, device, "commit", "-m", "Rename")
	gitCmd(t, device, "push")
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(dir), "data", "publications", "*", "publications.json"))
	if err != nil || len(matches) != 1 {
		t.Fatal("metadata missing")
	}
	metadata := matches[0]
	if err := os.Rename(metadata, metadata+".backup"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(metadata, 0700); err != nil {
		t.Fatal(err)
	}
	w := editRequest(h, "POST", "/admin/api/git/pull", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"warning"`) || publicGet(a, first.URL).Code != 200 {
		t.Fatal("partial publication failure lost previous copy")
	}
	if err := os.Remove(metadata); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(metadata+".backup", metadata); err != nil {
		t.Fatal(err)
	}
	w = editRequest(h, "POST", "/admin/api/publications/refresh", nil)
	article := a.publications.Snapshot()["renamed.md"]
	if w.Code != 200 || article == nil || article.ID != first.ID || !article.PublishedAt.Equal(*first.PublishedAt) {
		t.Fatal("retry lost Git rename identity/date")
	}
}

func TestReadOnlyVaultCanPublishAndWithdrawOffline(t *testing.T) {
	a, _, remote, _ := gitApp(t)
	a.writable = false
	first := publishForTest(t, a, "note.md", nil)
	if err := os.Rename(remote, remote+"-offline"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(remote+"-offline", remote)
	w := editRequest(testHandler(t, a), "PUT", "/admin/api/publication", map[string]any{"path": "note.md", "enabled": false})
	if w.Code != 200 || publicGet(a, first.URL).Code != 404 {
		t.Fatal("withdrawal incorrectly requires writable/online Git")
	}
}
