package web

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

func TestUIOmitsBoilerplateCopy(t *testing.T) {
	forbidden := []string{
		"单用户", "12 小时会话", "私有内容不对外提供",
		"笔记、连接与编辑，只在后台", "登录不会自动拉取",
		"安装不会将笔记公开", "安装时不会公开笔记",
		"不会自动成为公开文章", "公开博客将使用独立发布副本",
		"未执行 Git 同步", "未写入文件", "草稿未丢失",
		"想法的下一站", "思考留在文件里", "让想法，在这里连接",
		"给知识，", "让笔记留在你的掌握中", "让文件之间的连接，浮出水面",
		"YOUR SECOND BRAIN", "PRIVATE BY DEFAULT", "THE SHAPE OF YOUR KNOWLEDGE",
		"YOUR FILES. YOUR SPACE.", "A NOTE FROM YOUR VAULT", "MVP 0.3",
	}
	err := fs.WalkDir(assets, "static", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || (path.Ext(name) != ".html" && path.Ext(name) != ".js") {
			return nil
		}
		data, err := assets.ReadFile(name)
		if err != nil {
			return err
		}
		for _, phrase := range forbidden {
			if strings.Contains(string(data), phrase) {
				t.Errorf("%s contains redundant UI copy: %q", name, phrase)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestEditorChromeIsSingleRow keeps the editor header from growing back into
// three stacked bands. The reading toolbar above already names the vault, so
// the editor row carries the path, status and actions together.
func TestEditorChromeIsSingleRow(t *testing.T) {
	html, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(html)

	start := strings.Index(page, `id="editor-view"`)
	if start < 0 {
		t.Fatal("editor view missing")
	}
	end := strings.Index(page, `id="graph-view"`)
	if end < start {
		t.Fatal("graph view must follow the editor")
	}
	editor := page[start:end]

	// The separate status band and its redundant label are gone.
	// The visible label above the input is gone; the accessibility label on the
	// input itself stays, so only the standalone text is checked.
	for _, gone := range []string{`class="editor-info"`, `>文件路径<`, `<kbd>Ctrl / ⌘ S</kbd>`} {
		if strings.Contains(editor, gone) {
			t.Errorf("editor should not render %q", gone)
		}
	}
	// Everything the editor needs is inside the single toolbar.
	for _, want := range []string{`class="editor-toolbar"`, `id="editor-path"`, `id="editor-status"`, `id="editor-character-count"`, `id="save-note"`, `id="close-editor"`} {
		if !strings.Contains(editor, want) {
			t.Errorf("editor toolbar missing %q", want)
		}
	}
	css, err := assets.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(css), ".editor-info {") {
		t.Error("editor-info styles must be removed with the element")
	}
}
