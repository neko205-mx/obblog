package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"obblog/internal/config"
	"obblog/internal/gitsync"
)

func runtimeForTest(t *testing.T) (*Runtime, string) {
	t.Helper()
	dir := t.TempDir()
	rt, err := OpenRuntime(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	return rt, dir
}
func runtimeRequest(rt *Runtime, method, path string, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, r)
	return w
}
func runtimeLogin(t *testing.T, rt *Runtime, password string) *http.Cookie {
	t.Helper()
	w := runtimeRequest(rt, "POST", "/admin/api/login", nil, map[string]string{"password": password})
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}

func TestWebInstallationPersistenceAndSettings(t *testing.T) {
	rt, data := runtimeForTest(t)
	vaultDir := t.TempDir()
	const note = "---\r\ncustom: preserved\r\n---\r\n# Fixture\r\n"
	if err := os.WriteFile(filepath.Join(vaultDir, "test.md"), []byte(note), 0600); err != nil {
		t.Fatal(err)
	}
	w := runtimeRequest(rt, "GET", "/install/api/state", nil, nil)
	if strings.Contains(w.Body.String(), data) {
		t.Fatal("unexpected data path leak")
	}
	w = runtimeRequest(rt, "GET", "/install", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "设置密码") || strings.Contains(w.Body.String(), "一次性安装码") {
		t.Fatal("installer not served")
	}
	input := setupInput{Mode: "existing", Vault: vaultDir, Password: testPassword, Confirmation: testPassword, Writable: true, Token: "fixture-pat-not-for-network"}
	w = runtimeRequest(rt, "POST", "/install/api/configure", nil, input)
	if w.Code != 200 {
		t.Fatalf("installation: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(data, "install.key")); !os.IsNotExist(err) {
		t.Fatal("installation must not create a code file")
	}
	info, err := os.Stat(filepath.Join(data, "config.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("configuration must be private")
	}
	raw, _ := os.ReadFile(filepath.Join(data, "config.json"))
	if bytes.Contains(raw, []byte(testPassword)) || !bytes.Contains(raw, []byte("pbkdf2-sha256")) {
		t.Fatal("password not stored as salted hash")
	}
	w = runtimeRequest(rt, "POST", "/install/api/configure", nil, input)
	if w.Code != 410 {
		t.Fatal("installer not closed")
	}
	w = runtimeRequest(rt, "GET", "/admin/api/settings", nil, nil)
	if w.Code != 401 {
		t.Fatal("settings unprotected")
	}
	cookie := runtimeLogin(t, rt, testPassword)
	w = runtimeRequest(rt, "GET", "/admin/api/settings", cookie, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "fixture-pat") || strings.Contains(w.Body.String(), "pbkdf2") || !strings.Contains(w.Body.String(), `"tokenConfigured":true`) {
		t.Fatalf("settings secret leak: %s", w.Body.String())
	}
	w = runtimeRequest(rt, "GET", "/admin/api/note?path=test.md", cookie, nil)
	var rendered struct {
		Source string `json:"source"`
	}
	json.Unmarshal(w.Body.Bytes(), &rendered)
	if w.Code != 200 || rendered.Source != note {
		t.Fatal("existing Markdown changed")
	}
	input.Password = "replacement-password-123"
	input.Confirmation = input.Password
	input.Token = ""
	input.Writable = false
	w = runtimeRequest(rt, "POST", "/admin/api/settings", cookie, input)
	if w.Code != 200 {
		t.Fatalf("settings save: %d %s", w.Code, w.Body.String())
	}
	w = runtimeRequest(rt, "GET", "/admin/api/vault", cookie, nil)
	if w.Code != 401 {
		t.Fatal("previous session survived settings change")
	}
	w = runtimeRequest(rt, "POST", "/admin/api/login", nil, map[string]string{"password": testPassword})
	if w.Code != 401 {
		t.Fatal("old password still active")
	}
	rt.Close()
	again, err := OpenRuntime(data)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if runtimeRequest(again, "GET", "/install/api/directories", nil, nil).Code != 410 {
		t.Fatal("restart reopened installer")
	}
	cookie = runtimeLogin(t, again, input.Password)
	w = runtimeRequest(again, "GET", "/admin/api/vault", cookie, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"writable":true`) || !strings.Contains(w.Body.String(), `"managed":true`) {
		t.Fatal("settings not restored on restart")
	}
	b, _ := os.ReadFile(filepath.Join(vaultDir, "test.md"))
	if string(b) != note {
		t.Fatal("installation/settings modified existing note")
	}
}

func TestDefaultInstallationEnablesEditingAndUpgradesOldFlags(t *testing.T) {
	rt, data := runtimeForTest(t)
	w := runtimeRequest(rt, "POST", "/install/api/configure", nil, setupInput{Mode: "empty", Password: testPassword, Confirmation: testPassword})
	if w.Code != 200 {
		t.Fatalf("install without feature flags: %d %s", w.Code, w.Body.String())
	}
	cookie := runtimeLogin(t, rt, testPassword)
	w = runtimeRequest(rt, "POST", "/admin/api/note", cookie, noteInput{Path: "default.md", Source: "# Editing works by default\n"})
	if w.Code != 201 {
		t.Fatalf("default editing disabled: %d %s", w.Code, w.Body.String())
	}
	c, err := config.Load(data)
	if err != nil || !c.Writable || c.Git {
		t.Fatalf("local defaults: %+v %v", c, err)
	}
	rt.Close()
	c.Writable, c.Git = false, false
	if err := config.Save(data, c, false); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenRuntime(data)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	cookie = runtimeLogin(t, restarted, testPassword)
	w = runtimeRequest(restarted, "GET", "/admin/api/vault", cookie, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"writable":true`) || !strings.Contains(w.Body.String(), `"saveMode":"local"`) {
		t.Fatal("old disabled flags blocked full defaults")
	}
	w = runtimeRequest(restarted, "POST", "/admin/api/note", cookie, noteInput{Path: "after-restart.md", Source: "# Still editable\n"})
	if w.Code != 201 {
		t.Fatal("editing not enabled after restart")
	}
}

func TestExistingRepositoryAutomaticallyEnablesGitWithoutPulling(t *testing.T) {
	_, dir, _, device := gitApp(t)
	before := gitCmd(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(device, "note.md"), []byte("# New device version\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, device, "add", ".")
	gitCmd(t, device, "commit", "-m", "Device update")
	gitCmd(t, device, "push")
	rt, data := runtimeForTest(t)
	w := runtimeRequest(rt, "POST", "/install/api/configure", nil, setupInput{Mode: "existing", Vault: dir, Password: testPassword, Confirmation: testPassword})
	if w.Code != 200 {
		t.Fatalf("automatic Git install: %d %s", w.Code, w.Body.String())
	}
	if gitCmd(t, dir, "rev-parse", "HEAD") != before {
		t.Fatal("installation automatically pulled")
	}
	c, err := config.Load(data)
	if err != nil || !c.Git || !c.Writable {
		t.Fatal("automatic Git/editing defaults not persisted")
	}
	cookie := runtimeLogin(t, rt, testPassword)
	w = runtimeRequest(rt, "GET", "/admin/api/git", cookie, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"changed"`) {
		t.Fatalf("Git not enabled: %s", w.Body.String())
	}
	rt.Close()
	c.Git = false
	if err := config.Save(data, c, false); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenRuntime(data)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.app.git == nil || !restarted.app.writable {
		t.Fatal("old configuration disabled available features")
	}
}

func TestFeatureGateCheckboxesRemoved(t *testing.T) {
	for _, name := range []string{"install.html", "install.js"} {
		data, err := assets.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"enable-write", "enable-git", "secure-cookie"} {
			if strings.Contains(string(data), id) {
				t.Errorf("%s still depends on removed checkbox %s", name, id)
			}
		}
	}
}

func TestConfiguredRepositoryDoesNotSilentlyBecomeLocal(t *testing.T) {
	_, dir, _, _ := gitApp(t)
	rt, data := runtimeForTest(t)
	w := runtimeRequest(rt, "POST", "/install/api/configure", nil, setupInput{Mode: "existing", Vault: dir, Password: testPassword, Confirmation: testPassword})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	rt.Close()
	if err := os.Rename(filepath.Join(dir, ".git"), filepath.Join(dir, ".git-unavailable")); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenRuntime(data)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.problem == "" || restarted.app.vault != nil {
		t.Fatal("configured Git vault silently fell back to local editing")
	}
	cookie := runtimeLogin(t, restarted, testPassword)
	if w := runtimeRequest(restarted, "GET", "/admin/api/settings", cookie, nil); w.Code != 200 {
		t.Fatal("repository recovery settings unavailable")
	}
}

func TestInstallValidationAndFailureCleanup(t *testing.T) {
	rt, data := runtimeForTest(t)
	for _, input := range []setupInput{
		{Mode: "empty", Password: "short", Confirmation: "short"},
		{Mode: "empty", Password: testPassword, Confirmation: "different"},
		{Mode: "existing", Vault: "/nonexistent/fixture/path", Password: testPassword, Confirmation: testPassword},
		{Mode: "existing", Vault: data, Password: testPassword, Confirmation: testPassword},
		{Mode: "clone", Repository: "file:///tmp/unsafe", Password: testPassword, Confirmation: testPassword},
	} {
		w := runtimeRequest(rt, "POST", "/install/api/configure", nil, input)
		if w.Code != 400 {
			t.Fatalf("invalid setup: %d %s", w.Code, w.Body.String())
		}
		if _, err := os.Stat(filepath.Join(data, "config.json")); !os.IsNotExist(err) {
			t.Fatal("invalid setup wrote config")
		}
	}
	rt.clone = func(context.Context, string, string, string, gitsync.Credentials) error {
		return errors.New("fixture clone failure")
	}
	w := runtimeRequest(rt, "POST", "/install/api/configure", nil, setupInput{Mode: "clone", Repository: "https://github.com/fixture/vault.git", Password: testPassword, Confirmation: testPassword})
	if w.Code != 400 {
		t.Fatal("failed clone installed configuration")
	}
	entries, _ := os.ReadDir(data)
	if len(entries) != 0 {
		t.Fatal("failed setup left temporary files")
	}
	r := httptest.NewRequest("POST", "/install/api/configure", strings.NewReader(`{}`))
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin setup accepted")
	}
}

func TestInstallCloneAndConcurrentClaim(t *testing.T) {
	_, _, remote, _ := gitApp(t)
	rt, data := runtimeForTest(t)
	started := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	rt.clone = func(ctx context.Context, repo, branch, dest string, credentials gitsync.Credentials) error {
		close(started)
		<-release
		if repo != "https://github.com/fixture/vault.git" || credentials.Token != "fake-private-token" {
			return errors.New("unexpected clone configuration")
		}
		if err := gitsync.Clone(ctx, remote, branch, dest, gitsync.Credentials{}); err != nil {
			return err
		}
		return exec.CommandContext(ctx, "git", "-C", dest, "remote", "set-url", "origin", repo).Run()
	}
	input := setupInput{Mode: "clone", Repository: "https://github.com/fixture/vault.git", Branch: "main", Token: "fake-private-token", Password: testPassword, Confirmation: testPassword}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- runtimeRequest(rt, "POST", "/install/api/configure", nil, input) }()
	<-started
	w := runtimeRequest(rt, "POST", "/install/api/configure", nil, input)
	if w.Code != 409 {
		t.Fatal("concurrent installer not rejected")
	}
	close(release)
	w = <-result
	if w.Code != 200 {
		t.Fatalf("local-remote clone fixture: %d %s", w.Code, w.Body.String())
	}
	c, err := config.Load(data)
	if err != nil || !c.Git || c.Credentials.Token != input.Token {
		t.Fatal("clone configuration missing")
	}
	if strings.Contains(gitCmd(t, c.Vault, "remote", "get-url", "origin"), input.Token) {
		t.Fatal("Token written to remote URL")
	}
	if _, err := os.Stat(filepath.Join(c.Vault, "note.md")); err != nil {
		t.Fatal("cloned note missing")
	}
	if gitCmd(t, c.Vault, "status", "--porcelain") != "" {
		t.Fatal("installer changed cloned content")
	}
}

func TestDirectoryPickerTrustsFirstVisitAndListsHidden(t *testing.T) {
	rt, _ := runtimeForTest(t)
	parent := t.TempDir()
	os.Mkdir(filepath.Join(parent, ".hidden-vault"), 0700)
	os.WriteFile(filepath.Join(parent, "private.md"), []byte("never return note contents"), 0600)
	endpoint := "/install/api/directories?path=" + url.QueryEscape(parent)
	w := runtimeRequest(rt, "GET", endpoint, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), ".hidden-vault") || strings.Contains(w.Body.String(), "private.md") {
		t.Fatalf("directory listing: %d %s", w.Code, w.Body.String())
	}
}

func TestConfigCorruptionDoesNotReopenInstaller(t *testing.T) {
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, "config.json"), []byte("broken JSON"), 0600)
	if rt, err := OpenRuntime(data); err == nil {
		rt.Close()
		t.Fatal("corrupt config reopened installation")
	}
	if _, err := os.Stat(filepath.Join(data, "install.key")); !os.IsNotExist(err) {
		t.Fatal("new installer key created for corrupt config")
	}
}

func TestObsoleteInstallKeyDoesNotBlockFirstVisit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "install.key"), []byte("obsolete, invalid code"), 0600); err != nil {
		t.Fatal(err)
	}
	rt, err := OpenRuntime(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	w := runtimeRequest(rt, "GET", "/install/api/state", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"installed":false`) {
		t.Fatal("obsolete code blocked installation")
	}
	w = runtimeRequest(rt, "GET", "/install/api/unlock", nil, nil)
	if w.Code != 404 {
		t.Fatal("obsolete unlock endpoint still exists")
	}
}

func TestMissingVaultAuthenticatedRecovery(t *testing.T) {
	rt, data := runtimeForTest(t)
	w := runtimeRequest(rt, "POST", "/install/api/configure", nil, setupInput{Mode: "empty", Password: testPassword, Confirmation: testPassword})
	if w.Code != 200 {
		t.Fatalf("empty install: %s", w.Body.String())
	}
	c, _ := config.Load(data)
	rt.Close()
	if err := os.Rename(c.Vault, c.Vault+"-unmounted"); err != nil {
		t.Fatal(err)
	}
	recovery, err := OpenRuntime(data)
	if err != nil {
		t.Fatal(err)
	}
	defer recovery.Close()
	if runtimeRequest(recovery, "GET", "/install/api/directories", nil, nil).Code != 410 {
		t.Fatal("missing vault reopened first-time setup")
	}
	cookie := runtimeLogin(t, recovery, testPassword)
	w = runtimeRequest(recovery, "GET", "/admin/api/settings", cookie, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "知识库暂时不可用") {
		t.Fatal("recovery settings unavailable")
	}
	newVault := t.TempDir()
	w = runtimeRequest(recovery, "POST", "/admin/api/settings", cookie, setupInput{Mode: "existing", Vault: newVault})
	if w.Code != 200 {
		t.Fatalf("recovery save failed: %d %s", w.Code, w.Body.String())
	}
	cookie = runtimeLogin(t, recovery, testPassword)
	w = runtimeRequest(recovery, "GET", "/admin/api/vault", cookie, nil)
	if w.Code != 200 {
		t.Fatal("vault not restored after web recovery")
	}
}

func TestFailedSettingsChangePreservesWorkingConfiguration(t *testing.T) {
	rt, data := runtimeForTest(t)
	w := runtimeRequest(rt, "POST", "/install/api/configure", nil, setupInput{Mode: "empty", Password: testPassword, Confirmation: testPassword})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cookie := runtimeLogin(t, rt, testPassword)
	before, _ := os.ReadFile(filepath.Join(data, "config.json"))
	rt.clone = func(context.Context, string, string, string, gitsync.Credentials) error {
		return errors.New("fixture clone failure")
	}
	w = runtimeRequest(rt, "POST", "/admin/api/settings", cookie, setupInput{Mode: "clone", Repository: "https://github.com/fixture/public-notes.git"})
	if w.Code != 400 {
		t.Fatalf("failed settings clone: %d %s", w.Code, w.Body.String())
	}
	after, _ := os.ReadFile(filepath.Join(data, "config.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("failed change replaced config")
	}
	w = runtimeRequest(rt, "GET", "/admin/api/vault", cookie, nil)
	if w.Code != 200 {
		t.Fatal("failed change revoked working session")
	}
	r := httptest.NewRequest("POST", "/admin/api/settings", strings.NewReader(`{}`))
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site settings accepted")
	}
	w = runtimeRequest(rt, "GET", "/admin/api/settings/directories", nil, nil)
	if w.Code != 401 {
		t.Fatal("anonymous settings directory listing accepted")
	}
}
