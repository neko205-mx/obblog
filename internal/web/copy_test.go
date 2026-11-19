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

// TestEditorControlsReuseDocumentToolbar ensures editing uses the existing
// notes breadcrumb row instead of adding another band above the source pane.
func TestEditorControlsReuseDocumentToolbar(t *testing.T) {
	html, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(html)
	start := strings.Index(page, `<header class="document-toolbar">`)
	if start < 0 {
		t.Fatal("document toolbar missing")
	}
	end := strings.Index(page[start:], `</header>`)
	if end < 0 {
		t.Fatal("document toolbar is unclosed")
	}
	toolbar := page[start : start+end]
	for _, want := range []string{`id="breadcrumbs"`, `id="editor-path-control"`, `id="editor-path"`, `id="editor-meta"`, `id="editor-status"`, `id="editor-character-count"`, `id="editor-actions"`, `id="editor-preview-toggle"`, `id="save-note"`, `id="close-editor"`} {
		if !strings.Contains(toolbar, want) {
			t.Errorf("shared document toolbar missing %q", want)
		}
	}
	// Paths still have an accessible label and remain editable for new notes.
	if !strings.Contains(toolbar, `aria-label="编辑文件路径"`) {
		t.Error("path input has no accessible name")
	}
	editorStart := strings.Index(page, `id="editor-view"`)
	editorEnd := strings.Index(page, `id="graph-view"`)
	if editorStart < 0 || editorEnd < editorStart {
		t.Fatal("editor view missing")
	}
	editor := page[editorStart:editorEnd]
	for _, gone := range []string{`class="editor-toolbar"`, `class="editor-info"`, `id="editor-path"`, `id="save-note"`, `<kbd>Ctrl / ⌘ S</kbd>`} {
		if strings.Contains(editor, gone) {
			t.Errorf("editor pane must not add a second row containing %q", gone)
		}
	}
	css, err := assets.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(css), ".editor-toolbar {") || strings.Contains(string(css), ".editor-info {") {
		t.Error("removed editor rows should not retain CSS rules")
	}
	js, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), "setEditorControls(true)") || strings.Count(string(js), "setEditorControls(false)") < 2 {
		t.Error("editor controls must be shown when editing and hidden when reading or showing graph")
	}
}
