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
