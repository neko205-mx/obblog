package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"obblog/internal/gitsync"
	"obblog/internal/vault"
)

// Every Git mutation in these tests targets a newly created temporary checkout
// and local bare remote. Never consult OBBLOG_TEST_VAULT or the real notes.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	base := []string{"-c", "user.name=Fixture", "-c", "user.email=fixture@localhost", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "-C", dir}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git %v: %s (%v)", args, b, err)
	}
	return strings.TrimSpace(string(b))
}

func gitApp(t *testing.T) (*App, string, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git unavailable")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	dir := filepath.Join(root, "vault")
	device := filepath.Join(root, "device")
	gitCmd(t, root, "init", "--bare", "--initial-branch=main", remote)
	gitCmd(t, root, "clone", remote, dir)
	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("# Initial\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "note.md")
	gitCmd(t, dir, "commit", "-m", "Initial")
	gitCmd(t, dir, "push", "-u", "origin", "main")
	gitCmd(t, root, "clone", remote, device)
	a, err := NewWithOptions(dir, Options{Password: testPassword, Writable: true, Git: true, DataDir: filepath.Join(root, "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, dir, remote, device
}

func TestGitManualPullAndWebSave(t *testing.T) {
	a, dir, remote, device := gitApp(t)
	h := testHandler(t, a)
	initial := gitCmd(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(device, "note.md"), []byte("# Device\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, device, "add", "note.md")
	gitCmd(t, device, "commit", "-m", "Device")
	gitCmd(t, device, "push")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/git", nil))
	var status gitsync.Status
	if json.Unmarshal(w.Body.Bytes(), &status) != nil || status.State != "changed" || status.CanWrite {
		t.Fatalf("status: %s", w.Body.String())
	}
	if gitCmd(t, dir, "rev-parse", "HEAD") != initial {
		t.Fatal("status must not automatically pull")
	}
	w = editRequest(h, "PUT", "/admin/api/note", noteInput{Path: "note.md", Source: "unsafe", Revision: vault.Revision([]byte("# Initial\n")), GitHead: initial})
	if w.Code != 409 {
		t.Fatalf("stale save: %d %s", w.Code, w.Body.String())
	}
	w = editRequest(h, "POST", "/admin/api/git/pull", nil)
	if w.Code != 200 {
		t.Fatalf("pull: %d %s", w.Code, w.Body.String())
	}
	head := gitCmd(t, dir, "rev-parse", "HEAD")
	if head == initial || head != gitCmd(t, device, "rev-parse", "HEAD") {
		t.Fatal("fast forward failed")
	}
	w = editRequest(h, "PUT", "/admin/api/note", noteInput{Path: "note.md", Source: "# Web\r\n", Revision: vault.Revision([]byte("# Device\n")), GitHead: initial})
	if w.Code != 409 {
		t.Fatal("old repository generation accepted")
	}
	w = editRequest(h, "PUT", "/admin/api/note", noteInput{Path: "note.md", Source: "# Web\r\n", Revision: vault.Revision([]byte("# Device\n")), GitHead: head})
	var saved gitsync.Result
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || !saved.Synced || saved.Warning != "" {
		t.Fatalf("save/push: %d %s", w.Code, w.Body.String())
	}
	if gitCmd(t, remote, "rev-parse", "main") != saved.GitHead || gitCmd(t, dir, "status", "--porcelain") != "" {
		t.Fatal("web save did not create a clean pushed commit")
	}
	b, err := os.ReadFile(filepath.Join(dir, "note.md"))
	if err != nil || string(b) != "# Web\r\n" {
		t.Fatal("raw Markdown was changed")
	}
}

func TestGitFailedPushRetainsCommitAndRetry(t *testing.T) {
	a, dir, remote, _ := gitApp(t)
	h := testHandler(t, a)
	head := gitCmd(t, dir, "rev-parse", "HEAD")
	gitCmd(t, dir, "config", "remote.origin.pushurl", filepath.Join(t.TempDir(), "unavailable.git"))
	w := editRequest(h, "PUT", "/admin/api/note", noteInput{Path: "note.md", Source: "# Pending\n", Revision: vault.Revision([]byte("# Initial\n")), GitHead: head})
	var saved gitsync.Result
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.Synced || saved.Warning == "" || saved.Revision != vault.Revision([]byte("# Pending\n")) {
		t.Fatalf("partial success: %d %s", w.Code, w.Body.String())
	}
	if gitCmd(t, dir, "rev-parse", "HEAD") == head || gitCmd(t, remote, "rev-parse", "main") != head {
		t.Fatal("pending commit lost or false push success")
	}
	gitCmd(t, dir, "config", "--unset", "remote.origin.pushurl")
	w = editRequest(h, "POST", "/admin/api/git/push", nil)
	if w.Code != 200 || gitCmd(t, remote, "rev-parse", "main") != saved.GitHead {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
}

func TestGitOfflineDirtyAndDivergence(t *testing.T) {
	a, dir, remote, device := gitApp(t)
	h := testHandler(t, a)
	head := gitCmd(t, dir, "rev-parse", "HEAD")
	if err := os.Rename(remote, remote+"-offline"); err != nil {
		t.Fatal(err)
	}
	w := editRequest(h, "PUT", "/admin/api/note", noteInput{Path: "note.md", Source: "offline", Revision: vault.Revision([]byte("# Initial\n")), GitHead: head})
	if w.Code != 409 {
		t.Fatal("offline save accepted")
	}
	if err := os.Rename(remote+"-offline", remote); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.md"), []byte("Do not touch"), 0600); err != nil {
		t.Fatal(err)
	}
	w = editRequest(h, "POST", "/admin/api/git/pull", nil)
	if w.Code != 409 {
		t.Fatal("dirty checkout sync accepted")
	}
	w = editRequest(h, "PUT", "/admin/api/note", noteInput{Path: "note.md", Source: "dirty", Revision: vault.Revision([]byte("# Initial\n")), GitHead: head})
	if w.Code != 409 {
		t.Fatal("dirty checkout save accepted")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "note.md"))
	if string(b) != "# Initial\n" {
		t.Fatal("blocked writes changed source")
	}
	gitCmd(t, dir, "add", "untracked.md")
	gitCmd(t, dir, "commit", "-m", "Local")
	if err := os.WriteFile(filepath.Join(device, "remote.md"), []byte("Remote"), 0600); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, device, "add", "remote.md")
	gitCmd(t, device, "commit", "-m", "Remote")
	gitCmd(t, device, "push")
	local := gitCmd(t, dir, "rev-parse", "HEAD")
	w = editRequest(h, "POST", "/admin/api/git/pull", nil)
	if w.Code != 409 || gitCmd(t, dir, "rev-parse", "HEAD") != local {
		t.Fatalf("divergence should stop: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "remote.md")); !os.IsNotExist(err) {
		t.Fatal("divergence wrote remote files")
	}
}

func TestGitLiteralPathsAndIndexGuards(t *testing.T) {
	a, dir, _, _ := gitApp(t)
	h := testHandler(t, a)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.md\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", ".gitignore")
	gitCmd(t, dir, "commit", "-m", "Ignore fixture")
	gitCmd(t, dir, "push")
	head := gitCmd(t, dir, "rev-parse", "HEAD")
	w := editRequest(h, "POST", "/admin/api/note", noteInput{Path: "ignored.md", Source: "must not write", GitHead: head})
	if w.Code != 409 {
		t.Fatalf("ignored note accepted: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "ignored.md")); !os.IsNotExist(err) {
		t.Fatal("ignored note written")
	}
	gitCmd(t, dir, "update-index", "--assume-unchanged", "note.md")
	w = editRequest(h, "PUT", "/admin/api/note", noteInput{Path: "note.md", Source: "must not write", Revision: vault.Revision([]byte("# Initial\n")), GitHead: head})
	if w.Code != 409 {
		t.Fatal("assume-unchanged note accepted")
	}
	gitCmd(t, dir, "update-index", "--no-assume-unchanged", "note.md")
	w = editRequest(h, "POST", "/admin/api/note", noteInput{Path: "literal[1].md", Source: "# Literal\n", GitHead: head})
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"synced":true`) {
		t.Fatalf("literal path create failed: %d %s", w.Code, w.Body.String())
	}
	if gitCmd(t, dir, "status", "--porcelain") != "" {
		t.Fatal("literal file not committed")
	}
}

func TestGitStageFailureAcknowledgesLocalSave(t *testing.T) {
	a, dir, _, _ := gitApp(t)
	h := testHandler(t, a)
	head := gitCmd(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, ".git", "index.lock"), []byte("fixture lock"), 0600); err != nil {
		t.Fatal(err)
	}
	w := editRequest(h, "PUT", "/admin/api/note", noteInput{Path: "note.md", Source: "# Locally saved\n", Revision: vault.Revision([]byte("# Initial\n")), GitHead: head})
	var saved gitsync.Result
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.Warning == "" || saved.Synced || saved.Revision != vault.Revision([]byte("# Locally saved\n")) {
		t.Fatalf("local save must be acknowledged: %d %s", w.Code, w.Body.String())
	}
	b, _ := os.ReadFile(filepath.Join(dir, "note.md"))
	if string(b) != "# Locally saved\n" || gitCmd(t, dir, "rev-parse", "HEAD") != head {
		t.Fatal("stage failure lost source or changed HEAD")
	}
}
