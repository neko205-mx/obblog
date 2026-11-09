package gitsync

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGitHubURLAndCredentialEnvironment(t *testing.T) {
	for _, raw := range []string{"file:///tmp/repo", "https://evil.example/a/b", "https://github.com.evil/a/b", "https://name:secret@github.com/a/b", "https://github.com/a/b?token=secret", "https://github.com/a/b#x", "https://github.com/a/b/c", "https://github.com:443/a/b", "https://github.com/a/../b"} {
		if _, err := GitHubURL(raw); err == nil {
			t.Errorf("accepted unsafe URL %q", raw)
		}
	}
	if u, err := GitHubURL("https://github.com/fixture/notes.git"); err != nil || u != "https://github.com/fixture/notes.git" {
		t.Fatal("valid repo rejected")
	}
	env := strings.Join((Credentials{Token: "fixture-only-token"}).environment(), "\n")
	if !strings.Contains(env, "http.https://github.com/.extraHeader") || !strings.Contains(env, base64.StdEncoding.EncodeToString([]byte("x-access-token:fixture-only-token"))) {
		t.Fatal("scoped credentials missing")
	}
}
