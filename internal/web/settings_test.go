package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"obblog/internal/config"
)

func TestSettingsAndInstallerElementReferences(t *testing.T) {
	ids := regexp.MustCompile(`\$\("([^"]+)"\)`)
	for _, page := range []string{"settings", "install"} {
		html, err := assets.ReadFile("static/" + page + ".html")
		if err != nil {
			t.Fatal(err)
		}
		js, err := assets.ReadFile("static/" + page + ".js")
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range ids.FindAllStringSubmatch(string(js), -1) {
			if !strings.Contains(string(html), `id="`+match[1]+`"`) {
				t.Errorf("%s.js references missing element %s", page, match[1])
			}
		}
	}
}

func TestSettingsPageIsSeparateFromInstallationWizard(t *testing.T) {
	rt, _ := runtimeForTest(t)
	installTestRuntime(t, rt, "empty", "")
	w := runtimeRequest(rt, "GET", "/admin/settings", nil, nil)
	if w.Code != 303 || w.Header().Get("Location") != "/admin/login" {
		t.Fatal("settings page must require login")
	}
	cookie := runtimeLogin(t, rt, testPassword)
	w = runtimeRequest(rt, "GET", "/admin/settings", cookie, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	body := w.Body.String()
	for _, required := range []string{`id="settings-form"`, `id="knowledge"`, `id="credentials"`, `id="password"`, `form="settings-form"`, `/admin/settings.js`, `/admin/settings.css`} {
		if !strings.Contains(body, required) {
			t.Errorf("missing settings page element: %s", required)
		}
	}
	for _, forbidden := range []string{"下一步", "上一步", "setup-steps", "account-step", "/install/install.js"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("settings page still uses installation flow: %s", forbidden)
		}
	}
	for _, endpoint := range []string{"/admin/settings.js", "/admin/settings.css"} {
		if w := runtimeRequest(rt, "GET", endpoint, cookie, nil); w.Code != 200 {
			t.Fatalf("settings asset %s: %d", endpoint, w.Code)
		}
		if w := runtimeRequest(rt, "GET", endpoint, nil, nil); w.Code != 401 {
			t.Fatal("settings assets lost session gate")
		}
	}
	installer, _ := assets.ReadFile("static/install.html")
	installerJS, _ := assets.ReadFile("static/install.js")
	if !strings.Contains(string(installer), "下一步") || strings.Contains(string(installerJS), "/admin/api/settings") || strings.Contains(string(installerJS), "settingsMode") {
		t.Fatal("installer and settings are not independent")
	}
}

func TestSettingsCanSaveRepositoryAndTokenWithoutPasswordStep(t *testing.T) {
	rt, data := runtimeForTest(t)
	installTestRuntime(t, rt, "empty", "")
	before, err := config.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	const source = "# Existing note\n\nUnchanged content.\n"
	if err := os.WriteFile(filepath.Join(before.Vault, "note.md"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	cookie := runtimeLogin(t, rt, testPassword)
	w := runtimeRequest(rt, "POST", "/admin/api/settings", cookie, setupInput{Mode: "existing", Vault: before.Vault, Username: "fixture-user", Token: "fixture-token-only"})
	if w.Code != 200 {
		t.Fatalf("save without password: %d %s", w.Code, w.Body.String())
	}
	after, err := config.Load(data)
	if err != nil || after.PasswordHash != before.PasswordHash || after.Vault != before.Vault || after.Credentials.Token != "fixture-token-only" {
		t.Fatal("settings save changed blank password or lost credentials")
	}
	cookie = runtimeLogin(t, rt, testPassword)
	w = runtimeRequest(rt, "POST", "/admin/api/settings", cookie, setupInput{Mode: "existing", Vault: after.Vault})
	if w.Code != 200 {
		t.Fatal("saving current settings with all optional fields blank failed")
	}
	after, err = config.Load(data)
	if err != nil || after.Credentials.Token != "fixture-token-only" || after.PasswordHash != before.PasswordHash {
		t.Fatal("blank settings fields did not retain existing values")
	}
	raw, err := os.ReadFile(filepath.Join(after.Vault, "note.md"))
	if err != nil || string(raw) != source {
		t.Fatal("saving settings changed note contents")
	}
}
