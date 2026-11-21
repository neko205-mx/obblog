package web

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"obblog/internal/publication"
)

func TestBuiltInFriendLinksTemplate(t *testing.T) {
	a, _, _ := publishFixture(t)
	h := testHandler(t, a)
	catalog := decodePageResponse[struct{ Templates []pageTemplate }](t, editRequest(h, "GET", pagesAPI, nil), 200)
	if len(catalog.Templates) != 1 || catalog.Templates[0].ID != friendsTemplateID || catalog.Templates[0].Source == "" {
		t.Fatalf("invalid template catalog: %+v", catalog)
	}
	source := "## 朋友\n\n- [A blog](https://example.org/ \"可选简介\") ![头像](https://example.org/avatar.png)\n\n正文不变。"
	input := map[string]string{"slug": "friends", "title": "友情链接", "source": source, "template": friendsTemplateID}
	preview := decodePageResponse[struct{ HTML, SandboxHTML, TemplateWarning string }](t, editRequest(h, "POST", pagesAPI+"/preview", input), 200)
	for _, token := range []string{"friend-link-grid", "friend-link-card", "可选简介", "https://example.org/avatar.png", "正文不变。"} {
		if !strings.Contains(preview.HTML, token) {
			t.Fatalf("preview missing %s: %s", token, preview.HTML)
		}
	}
	if preview.SandboxHTML != "" || preview.TemplateWarning != "" {
		t.Fatal("built-in safe preview should render directly")
	}
	if w := publicGet(a, "/pages/friends"); w.Code != 404 {
		t.Fatal("preview published the page")
	}
	created := decodePageResponse[pageDetail](t, editRequest(h, "POST", pagesAPI, input), 201)
	if created.Template != friendsTemplateID || created.Source != source {
		t.Fatalf("source/template changed: %+v", created)
	}
	home := publicGet(a, "/")
	if !strings.Contains(home.Body.String(), `href="/pages/friends"`) {
		t.Fatal("friends page missing in the navigation")
	}
	public := publicGet(a, created.URL)
	if public.Code != 200 || !strings.Contains(public.Body.String(), preview.HTML) || !strings.Contains(public.Body.String(), `href="/page-templates.css"`) {
		t.Fatalf("preview and public page diverged: %d %s", public.Code, public.Body.String())
	}
	if w := publicGet(a, "/page-templates.css"); w.Code != 200 || !strings.Contains(w.Body.String(), "grid-template-columns") {
		t.Fatal("card stylesheet not served")
	}
	input["template"] = "default/../profile"
	if w := editRequest(h, "POST", pagesAPI+"/preview", input); w.Code != 400 {
		t.Fatal("invalid template ID accepted")
	}
	input["template"] = "default/missing"
	if w := editRequest(h, "POST", pagesAPI, input); w.Code != 400 {
		t.Fatal("missing template accepted on create")
	}
}

func TestThemePageTemplatePreviewAndFallback(t *testing.T) {
	a, vault, data := publishFixture(t)
	h := testHandler(t, a)
	const id = "plain-journal"
	if err := os.CopyFS(filepath.Join(data, "themes", id), os.DirFS("../../examples/themes/"+id)); err != nil {
		t.Fatal(err)
	}
	if w := editRequest(h, "POST", "/admin/api/themes", map[string]string{"id": id}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	list := decodePageResponse[struct{ Templates []pageTemplate }](t, editRequest(h, "GET", pagesAPI, nil), 200)
	if len(list.Templates) != 2 || list.Templates[1].ID != "plain-journal/profile" || !strings.Contains(list.Templates[1].Source, "个人主页") {
		t.Fatalf("theme starter missing: %+v", list)
	}
	input := map[string]string{"slug": "about", "title": "关于我", "source": "## 自我介绍\n\n这里是简介。", "template": list.Templates[1].ID}
	preview := decodePageResponse[struct{ HTML, SandboxHTML, TemplateWarning string }](t, editRequest(h, "POST", pagesAPI+"/preview", input), 200)
	if !strings.Contains(preview.SandboxHTML, `class="journal-profile"`) || !strings.Contains(preview.SandboxHTML, `/theme-assets/plain-journal/profile.css`) || strings.Contains(preview.HTML, `class="journal-profile"`) {
		t.Fatalf("theme markup not separated from admin preview: %+v", preview)
	}
	created := decodePageResponse[pageDetail](t, editRequest(h, "POST", pagesAPI, input), 201)
	public := publicGet(a, created.URL)
	if public.Code != 200 || !strings.Contains(public.Body.String(), `class="journal-profile"`) || public.Header().Get("Content-Security-Policy") != externalThemeCSP {
		t.Fatalf("custom page missing or has wrong policy: %d %s", public.Code, public.Body.String())
	}
	if w := editRequest(h, "POST", "/admin/api/themes", map[string]string{"id": defaultPublicTheme}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	preview = decodePageResponse[struct{ HTML, SandboxHTML, TemplateWarning string }](t, editRequest(h, "POST", pagesAPI+"/preview", input), 200)
	if preview.SandboxHTML != "" || preview.TemplateWarning == "" || !strings.Contains(preview.HTML, "这里是简介。") {
		t.Fatalf("expected ordinary Markdown fallback: %+v", preview)
	}
	if w := publicGet(a, created.URL); w.Code != 200 || strings.Contains(w.Body.String(), "journal-profile") || !strings.Contains(w.Body.String(), preview.HTML) {
		t.Fatal("plain fallback lost response content")
	}
	input["source"], input["revision"] = "修改后仍保留模板。", created.Revision
	saved := decodePageResponse[pageDetail](t, editRequest(h, "PUT", pagesAPI+"/about", input), 200)
	if saved.Template != created.Template {
		t.Fatal("missing theme erased the saved template ID")
	}
	reopened, err := publication.Open(data, vault)
	if err != nil {
		t.Fatal(err)
	}
	if page := reopened.Page("about"); page == nil || page.Template != saved.Template || page.Source != input["source"] {
		t.Fatalf("page did not persist: %+v", page)
	}
	// Previewing the original theme also restores its page layout without enabling it.
	themePreview := "/admin/theme-preview/" + id + "?" + url.Values{"path": {saved.URL}}.Encode()
	if w := editRequest(h, "GET", themePreview, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `class="journal-profile"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if a.publications.PublicTheme() != defaultPublicTheme {
		t.Fatal("theme preview activated the theme")
	}
}
