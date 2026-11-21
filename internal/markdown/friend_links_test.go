package markdown

import (
	"reflect"
	"strings"
	"testing"
)

func renderFriendLinksForTest(t *testing.T, source string, enable bool) *Result {
	t.Helper()
	result, err := RenderWithOptions([]byte(source), "欢迎.md", snapshot(), RenderOptions{FriendLinks: enable})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestFriendLinksCardsAndTypedData(t *testing.T) {
	source := `# 友情链接

- [站点](https://blog.example) — 简介 ![头像](https://blog.example/avatar.png)
- [**Blog &amp; co**](http://blog2.example "简介 &amp; other")

[另外一个站点](https://another.example/)

## 更多

[有描述](https://desc.example/) -- Typographer dash
`
	got := renderFriendLinksForTest(t, source, true)
	want := []FriendLink{
		{Title: "站点", URL: "https://blog.example", Description: "简介", Avatar: "https://blog.example/avatar.png", Initial: "站"},
		{Title: "Blog & co", URL: "http://blog2.example", Description: "简介 & other", Initial: "B"},
		{Title: "另外一个站点", URL: "https://another.example/", Initial: "另"},
		{Title: "有描述", URL: "https://desc.example/", Description: "Typographer dash", Initial: "有"},
	}
	if !reflect.DeepEqual(got.FriendLinks, want) {
		t.Fatalf("cards: %+v, want %+v; HTML: %s", got.FriendLinks, want, got.HTML)
	}
	if strings.Count(got.HTML, `class="friend-link-grid"`) != 2 || strings.Count(got.HTML, `class="friend-link-card"`) != len(want) || len(got.Headings) != 2 {
		t.Fatalf("unexpected markup or headings: %s (%+v)", got.HTML, got.Headings)
	}
	for _, part := range []string{`class="friend-link-image" src="https://blog.example/avatar.png"`, `referrerpolicy="no-referrer"`, `class="friend-link-initial">站`, `class="friend-link-description">简介 &amp; other`, `id="友情链接"`, `id="更多"`} {
		if !strings.Contains(got.HTML, part) {
			t.Errorf("missing %q in %s", part, got.HTML)
		}
	}
}

func TestFriendLinksKeepsNonCardMarkdown(t *testing.T) {
	source := "[炫酷](https://a.example) is an ordinary sentence.\n\n- [链接](https://a.example)\n- Just prose\n\n- [Parent](https://p.example)\n  - [Nested](https://n.example)\n\n1. [Ordered](https://o.example)\n\n> [Quoted](https://q.example)\n\n```md\n- [Code](https://example.org)\n```\n\n[one](https://example.org) [two](https://example.net)\n"
	normal := renderFriendLinksForTest(t, source, false)
	enabled := renderFriendLinksForTest(t, source, true)
	if !reflect.DeepEqual(enabled, normal) {
		t.Fatalf("unrelated Markdown changed\nNormal: %s\nCards: %s", normal.HTML, enabled.HTML)
	}
	withCard := renderFriendLinksForTest(t, source+"\n[Card](https://card.example)", false)
	if len(withCard.FriendLinks) != 0 || strings.Contains(withCard.HTML, "friend-link-card") {
		t.Fatal("normal renderer must not emit cards")
	}
}

func TestFriendLinksSafeAvatarAndEscaping(t *testing.T) {
	source := `- [&#60;站&amp;点&#62;](https://blog.example/?a=1&amp;b=2 "&lt;script&gt;") ![bad](/admin/api/asset?path=private.png)
- [No HTTP image](https://blog.example) ![bad](http://blog.example/face.png)
- [No private image](https://blog.example) ![bad](https://blog.example/ab/../admin%2fapi/asset?path=private.jpg)
- [No image credentials](https://blog.example) ![bad](https://reader:pass@blog.example/image.jpg)
- [No empty image](https://blog.example) ![bad]()

[<i>Not card HTML</i>](https://blog.example)

<img src="https://example.net" onerror="alert(1)">
`
	got := renderFriendLinksForTest(t, source, true)
	if len(got.FriendLinks) != 5 || got.FriendLinks[0].Title != "<站&点>" || got.FriendLinks[0].Description != "<script>" || got.FriendLinks[0].URL != "https://blog.example/?a=1&b=2" {
		t.Fatalf("unexpected friend data or cards: %+v\n%s", got.FriendLinks, got.HTML)
	}
	for _, entry := range got.FriendLinks {
		if entry.Avatar != "" || entry.Initial == "" {
			t.Fatalf("expected an initial without an avatar: %+v", entry)
		}
	}
	for _, forbidden := range []string{"<script>", "onerror=", "private.png", "private.jpg", `class="friend-link-image"`} {
		if strings.Contains(got.HTML, forbidden) {
			t.Errorf("unsafe markup %q: %s", forbidden, got.HTML)
		}
	}
	for _, want := range []string{"&lt;站&amp;点&gt;", "&lt;script&gt;", "a=1&amp;b=2"} {
		if !strings.Contains(got.HTML, want) {
			t.Errorf("expected escape %q: %s", want, got.HTML)
		}
	}
}

func TestFriendLinksRejectsUntrustedLinkDestinations(t *testing.T) {
	for _, href := range []string{
		"javascript:alert%281%29", "mailto:user@example.com", "https://site.example@evil.example", "https://site.example%0a.example",
		"https:///nohost", "https:opaque", "//blog.example", "/admin/api/asset?path=private.png", "https://blog.example/admin/api/asset?path=private.png",
	} {
		t.Run(href, func(t *testing.T) {
			result := renderFriendLinksForTest(t, "[Site]("+href+")", true)
			if len(result.FriendLinks) != 0 || strings.Contains(result.HTML, `class="friend-link-card"`) {
				t.Fatalf("unsafe destination accepted as card: %s", result.HTML)
			}
		})
	}
}
