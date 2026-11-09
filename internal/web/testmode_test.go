package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"obblog/internal/config"
	"obblog/internal/gitsync"
	"obblog/internal/vault"
)

func testRuntime(t *testing.T, source string) *Runtime {
	t.Helper()
	rt, err := OpenTestRuntime(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := rt.Close(); err != nil {
			t.Error(err)
		}
	})
	return rt
}

func installTestRuntime(t *testing.T, rt *Runtime, mode, source string) {
	t.Helper()
	w := runtimeRequest(rt, "POST", "/install/api/configure", nil, setupInput{Mode: mode, Vault: source, Password: testPassword, Confirmation: testPassword})
	if w.Code != 200 {
		t.Fatalf("test installation: %d %s", w.Code, w.Body.String())
	}
}

func TestDisposableRuntimeCleansAllFilesAndStartsFresh(t *testing.T) {
	rt := testRuntime(t, "")
	dir := rt.dir
	installTestRuntime(t, rt, "empty", "")
	cookie := runtimeLogin(t, rt, testPassword)
	if cookie.Name != "obvault_test_session" || cookie.MaxAge != 0 {
		t.Fatal("test login used a persistent or production cookie")
	}
	w := runtimeRequest(rt, "POST", "/admin/api/note", cookie, noteInput{Path: "draft.md", Source: "# Temporary article\n"})
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	article := publishForTest(t, rt.app, "draft.md", nil)
	if runtimeRequest(rt, "GET", article.URL, nil, nil).Code != 200 {
		t.Fatal("test publication unavailable")
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("test data remained after shutdown")
	}
	if rt.app != nil || rt.settings != nil || rt.handler != nil {
		t.Fatal("runtime retained installation state")
	}
	if w := runtimeRequest(rt, "POST", "/install/api/configure", nil, setupInput{Mode: "empty", Password: testPassword, Confirmation: testPassword}); w.Code != 503 {
		t.Fatal("closed runtime reopened installation")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("request recreated cleaned test directory")
	}
	again := testRuntime(t, "")
	if again.dir == dir || again.app != nil {
		t.Fatal("test restart reused data")
	}
	w = runtimeRequest(again, "GET", "/install/api/state", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"installed":false`) || !strings.Contains(w.Body.String(), `"testMode":true`) {
		t.Fatal("new test is not a clean installation")
	}
}

func TestTestModeCopiesSourceWithoutChangingIt(t *testing.T) {
	source := t.TempDir()
	const original = "---\r\nprivate: preserved\r\n---\r\n# Original\r\n"
	if err := os.WriteFile(filepath.Join(source, "original.md"), []byte(original), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "photo.svg"), []byte("<svg/>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "empty-folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".private-config"), []byte("not imported"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("original.md", filepath.Join(source, "alias.md")); err != nil {
		t.Fatal(err)
	}
	rt := testRuntime(t, source)
	w := runtimeRequest(rt, "GET", "/install/api/state", nil, nil)
	if !strings.Contains(w.Body.String(), source) {
		t.Fatal("-vault source not suggested during test installation")
	}
	installTestRuntime(t, rt, "existing", source)
	if rt.app.VaultPath() == source || !strings.HasPrefix(rt.app.VaultPath(), rt.dir+string(filepath.Separator)) {
		t.Fatal("test runtime opened original directory")
	}
	cookie := runtimeLogin(t, rt, testPassword)
	w = runtimeRequest(rt, "PUT", "/admin/api/note", cookie, noteInput{Path: "original.md", Source: "# Edited copy\n", Revision: vault.Revision([]byte(original))})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	copyPath := rt.app.VaultPath()
	for _, name := range []string{"photo.svg", "empty-folder"} {
		if _, err := os.Stat(filepath.Join(copyPath, name)); err != nil {
			t.Fatal("missing copied attachment/folder")
		}
	}
	for _, name := range []string{"alias.md", ".private-config"} {
		if _, err := os.Lstat(filepath.Join(copyPath, name)); !os.IsNotExist(err) {
			t.Fatal("unexpected hidden/symlink import")
		}
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(source, "original.md"))
	if err != nil || string(raw) != original {
		t.Fatal("test editing or cleanup changed original notes")
	}
	info, err := os.Stat(filepath.Join(source, "original.md"))
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatal("test changed original file permissions")
	}
}

func TestTestGitPushesOnlyToDisposableRemote(t *testing.T) {
	_, source, originalRemote, _ := gitApp(t)
	originalHead := gitCmd(t, source, "rev-parse", "HEAD")
	rt := testRuntime(t, "")
	installTestRuntime(t, rt, "existing", source)
	copyPath := rt.app.VaultPath()
	origin := gitCmd(t, copyPath, "remote", "get-url", "origin")
	if !strings.HasPrefix(origin, rt.dir+string(filepath.Separator)) || origin == originalRemote {
		t.Fatal("test retained original Git remote")
	}
	cookie := runtimeLogin(t, rt, testPassword)
	w := runtimeRequest(rt, "PUT", "/admin/api/note", cookie, noteInput{Path: "note.md", Source: "# Disposable commit\n", Revision: vault.Revision([]byte("# Initial\n")), GitHead: gitCmd(t, copyPath, "rev-parse", "HEAD")})
	var saved gitsync.Result
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || !saved.Synced {
		t.Fatalf("test Git save: %d %s", w.Code, w.Body.String())
	}
	if gitCmd(t, origin, "rev-parse", "main") != saved.GitHead {
		t.Fatal("test Git push did not reach local bare remote")
	}
	if gitCmd(t, source, "rev-parse", "HEAD") != originalHead || gitCmd(t, originalRemote, "rev-parse", "main") != originalHead || gitCmd(t, source, "status", "--porcelain") != "" {
		t.Fatal("test Git affected source checkout/remote")
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(origin); !os.IsNotExist(err) {
		t.Fatal("test Git remote survived cleanup")
	}
}

func TestTestCloneDiscardsCredentialsAndRealRemote(t *testing.T) {
	_, _, originalRemote, _ := gitApp(t)
	before := gitCmd(t, originalRemote, "rev-parse", "main")
	rt := testRuntime(t, "")
	rt.clone = func(ctx context.Context, remote, branch, dest string, credentials gitsync.Credentials) error {
		if credentials.Token != "temporary-fixture-token" {
			t.Fatal("clone did not receive credentials")
		}
		return gitsync.Clone(ctx, originalRemote, branch, dest, gitsync.Credentials{})
	}
	w := runtimeRequest(rt, "POST", "/install/api/configure", nil, setupInput{Mode: "clone", Repository: "https://github.com/fixture/vault.git", Token: "temporary-fixture-token", Password: testPassword, Confirmation: testPassword})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	c, err := config.Load(rt.dir)
	if err != nil || c.Credentials.Token != "" || !c.Git {
		t.Fatal("test clone persisted credentials or lost Git functionality")
	}
	if remote := gitCmd(t, c.Vault, "remote", "get-url", "origin"); !strings.HasPrefix(remote, rt.dir+string(filepath.Separator)) {
		t.Fatal("cloned test vault retained original origin")
	}
	if gitCmd(t, originalRemote, "rev-parse", "main") != before {
		t.Fatal("test clone modified original remote")
	}
}

func TestEndTestEndpointAndRegularRuntimeIsolation(t *testing.T) {
	normal, normalData := runtimeForTest(t)
	w := runtimeRequest(normal, "POST", "/admin/api/test/stop", nil, nil)
	if w.Code != 404 {
		t.Fatal("test stop endpoint enabled in normal mode")
	}
	rt := testRuntime(t, "")
	installTestRuntime(t, rt, "empty", "")
	w = runtimeRequest(rt, "POST", "/admin/api/test/stop", nil, nil)
	if w.Code != 401 {
		t.Fatal("anonymous user stopped an installed test")
	}
	cookie := runtimeLogin(t, rt, testPassword)
	r := httptest.NewRequest("POST", "/admin/api/test/stop", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site request ended test")
	}
	w = runtimeRequest(rt, "POST", "/admin/api/test/stop", cookie, nil)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("ending test did not expire its session cookie")
	}
	select {
	case <-rt.Done():
	default:
		t.Fatal("stop signal missing")
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(normalData); err != nil {
		t.Fatal("test cleanup removed normal data directory")
	}
	installTestRuntime(t, normal, "empty", "")
	if err := normal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(normalData, "config.json")); err != nil {
		t.Fatal("normal shutdown removed persistent configuration")
	}
}
