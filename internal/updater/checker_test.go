package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"obblog/internal/version"
)

// releaseServer serves a canned GitHub payload. Using a local server keeps the
// check tests hermetic.
func releaseServer(t *testing.T, status int, body string) *Checker {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/repos/owner/repo/releases/latest"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "obblog/") {
			t.Errorf("missing user agent: %q", ua)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	checker := NewChecker("owner/repo")
	checker.APIBase = server.URL
	return checker
}

func releaseJSON(tag string, assets ...string) string {
	var list strings.Builder
	for i, name := range assets {
		if i > 0 {
			list.WriteString(",")
		}
		list.WriteString(`{"name":"` + name + `","browser_download_url":"https://example.test/` + name + `","size":1024}`)
	}
	return `{"tag_name":"` + tag + `","body":"notes here","html_url":"https://example.test/release","published_at":"2026-01-02T03:04:05Z","draft":false,"prerelease":false,"assets":[` + list.String() + `]}`
}

func withVersion(t *testing.T, value string) {
	t.Helper()
	original := version.Version
	version.Version = value
	t.Cleanup(func() { version.Version = original })
}

func TestStatusReportsUpgrade(t *testing.T) {
	withVersion(t, "v1.0.0")
	want := AssetName()
	checker := releaseServer(t, 200, releaseJSON("v1.1.0", want, "SHA256SUMS", "SHA256SUMS.sig"))

	status := checker.Status(context.Background())
	if status.Error != "" {
		t.Fatalf("unexpected error: %s", status.Error)
	}
	if !status.Available {
		t.Fatalf("v1.1.0 should be available for v1.0.0: %+v", status)
	}
	if status.Latest != "v1.1.0" || status.Notes != "notes here" {
		t.Errorf("unexpected status: %+v", status)
	}
	if status.PublishedAt == nil {
		t.Error("published time should be reported")
	}
}

func TestStatusIgnoresSameOrOlder(t *testing.T) {
	want := AssetName()
	for _, tag := range []string{"v1.0.0", "v0.9.0"} {
		withVersion(t, "v1.0.0")
		checker := releaseServer(t, 200, releaseJSON(tag, want, "SHA256SUMS", "SHA256SUMS.sig"))
		status := checker.Status(context.Background())
		if status.Available {
			t.Errorf("%s must not be offered over v1.0.0", tag)
		}
	}
}

// A release without a signed checksum cannot be trusted, so it must not be
// advertised as installable even when the version is newer.
func TestStatusRejectsReleaseWithoutSignature(t *testing.T) {
	withVersion(t, "v1.0.0")
	checker := releaseServer(t, 200, releaseJSON("v2.0.0", AssetName(), "SHA256SUMS"))
	status := checker.Status(context.Background())
	if status.Available {
		t.Error("unsigned release must not be offered")
	}
	if !strings.Contains(status.Error, "签名") {
		t.Errorf("error should explain the missing signature: %q", status.Error)
	}
}

func TestStatusRejectsReleaseWithoutBinaryForPlatform(t *testing.T) {
	withVersion(t, "v1.0.0")
	checker := releaseServer(t, 200, releaseJSON("v2.0.0", "obblog-plan9-mips", "SHA256SUMS", "SHA256SUMS.sig"))
	status := checker.Status(context.Background())
	if status.Available {
		t.Error("release without a matching binary must not be offered")
	}
}

// Network and protocol failures surface as Error, never as a 500 for the panel.
func TestStatusReportsFailures(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
		want string
	}{
		{"not found", 404, `{}`, "还没有发布"},
		{"server error", 500, `{}`, "GitHub 返回 500"},
		{"rate limited", 403, `{}`, "403"},
		{"bad json", 200, `{`, "无法解析"},
		{"draft", 200, `{"tag_name":"v9.0.0","draft":true}`, "草稿"},
		{"prerelease", 200, `{"tag_name":"v9.0.0","prerelease":true}`, "预发布"},
		{"bad tag", 200, `{"tag_name":"nightly"}`, "标签格式"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			withVersion(t, "v1.0.0")
			checker := releaseServer(t, testCase.code, testCase.body)
			status := checker.Status(context.Background())
			if status.Available {
				t.Error("failed check must not advertise an update")
			}
			if !strings.Contains(status.Error, testCase.want) {
				t.Errorf("error = %q, want contains %q", status.Error, testCase.want)
			}
		})
	}
}

func TestStatusHandlesUnreachableServer(t *testing.T) {
	withVersion(t, "v1.0.0")
	checker := NewChecker("owner/repo")
	checker.APIBase = "http://127.0.0.1:1" // nothing listens here
	status := checker.Status(context.Background())
	if status.Available {
		t.Error("unreachable server must not advertise an update")
	}
	if !strings.Contains(status.Error, "无法连接") {
		t.Errorf("error = %q", status.Error)
	}
}

// A dev build has no comparable version, so it should accept any release.
func TestStatusAllowsDevBuildToUpgrade(t *testing.T) {
	withVersion(t, "dev")
	checker := releaseServer(t, 200, releaseJSON("v1.0.0", AssetName(), "SHA256SUMS", "SHA256SUMS.sig"))
	if status := checker.Status(context.Background()); !status.Available {
		t.Fatalf("dev build should see v1.0.0 as an upgrade: %+v", status)
	}
}

func TestAssetNameMatchesPlatform(t *testing.T) {
	name := AssetName()
	if !strings.HasPrefix(name, "obblog-") {
		t.Errorf("unexpected asset name: %s", name)
	}
	if !strings.Contains(name, "-linux-") && !strings.Contains(name, "-darwin-") && !strings.Contains(name, "-windows-") {
		t.Errorf("asset name should embed the OS: %s", name)
	}
}
