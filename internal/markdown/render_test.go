package markdown

import (
	"strings"
	"testing"

	"obblog/internal/vault"
)

func snapshot() *vault.Snapshot {
	files := map[string]*vault.Entry{}
	for _, name := range []string{"欢迎.md", "notes/Link.md", "assets/photo.png", "notes/z.v1.md"} {
		parts := strings.Split(name, "/")
		files[name] = &vault.Entry{Path: name, Name: parts[len(parts)-1], Kind: vault.Kind(name)}
	}
	return &vault.Snapshot{Files: files}
}

func TestRenderObsidian(t *testing.T) {
	raw := "---\r\ntags: [test]\r\n---\r\n# 你好，世界\r\n\r\n[[Link|跳转]] [[欢迎#你好，世界]] ![[photo.png|240]] [[missing]]\r\n\r\n[普通链接](notes/Link.md)\r\n\r\n> [!tip] 小提示\r\n> 内容\r\n\r\n| A | B |\r\n| - | - |\r\n| x | y |\r\n"
	result, err := Render([]byte(raw), "欢迎.md", snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="你好世界"`, `class="internal-link"`, `note=notes%2FLink.md`, `>跳转</a>`, `width="240"`, `class="unresolved-link"`, `<table>`, `callout-title">小提示`, `内容`} {
		if !strings.Contains(result.HTML, want) {
			t.Errorf("missing %q in %s", want, result.HTML)
		}
	}
	if result.Properties != "tags: [test]" || len(result.Headings) != 1 || result.Headings[0].Text != "你好，世界" {
		t.Fatalf("unexpected metadata: %+v", result)
	}
}

func TestCodeAndHTMLStaySafe(t *testing.T) {
	source := "`[[Link]]`\n\n```md\n[[Link]]\n```\n\n<script>alert(1)</script>\n\n[bad](javascript:alert%281%29)\n\n[[Link|<img src=x onerror=alert(1)>]]\n"
	result, err := Render([]byte(source), "欢迎.md", snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"<script>", `href="javascript:`, "<img src=x"} {
		if strings.Contains(result.HTML, forbidden) {
			t.Errorf("unsafe content %q: %s", forbidden, result.HTML)
		}
	}
	if !strings.Contains(result.HTML, "<code>[[Link]]</code>") || strings.Count(result.HTML, `class="internal-link"`) != 1 {
		t.Fatalf("code should not be converted into links: %s", result.HTML)
	}
}

func TestHeadingIDsAndLinks(t *testing.T) {
	result, err := Render([]byte("# 标题\n\n## 标题\n\n[[#标题|本页]]\n\n[章节](#标题)\n"), "欢迎.md", snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if result.Headings[0].ID != "标题" || result.Headings[1].ID != "标题-2" {
		t.Fatalf("unexpected headings: %+v", result.Headings)
	}
	if !strings.Contains(result.HTML, `note=%E6%AC%A2%E8%BF%8E.md#`) {
		t.Fatalf("missing same-file wiki link: %s", result.HTML)
	}
}

func TestHeadingIDCollisions(t *testing.T) {
	result, err := Render([]byte("# a\n\n## a\n\n## a-2\n"), "欢迎.md", snapshot())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, h := range result.Headings {
		if seen[h.ID] {
			t.Fatalf("duplicate heading ID: %s", h.ID)
		}
		seen[h.ID] = true
	}
}

func TestFrontmatter(t *testing.T) {
	for _, tc := range []struct{ source, body, props string }{
		{"---\na: b\n---\ntext", "text", "a: b"},
		{"---\na: b\n---", "", "a: b"},
		{"---\ntext", "---\ntext", ""},
		{"hello", "hello", ""},
	} {
		body, props := splitFrontmatter([]byte(tc.source))
		if string(body) != tc.body || props != tc.props {
			t.Errorf("split %q: %q, %q", tc.source, body, props)
		}
	}
}
