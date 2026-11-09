package markdown

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractBodyDoesNotPublishProperties(t *testing.T) {
	raw := []byte("---\ncover: '[[private.svg]]'\n---\n---\n[[body.svg]]\n---\n")
	metadata := ExtractBody(raw)
	if len(metadata.Links) != 1 || metadata.Links[0].Target != "body.svg" {
		t.Fatalf("unexpected public references: %+v", metadata.Links)
	}
}

func TestExtractReferences(t *testing.T) {
	raw := `---
tags: [知识/图谱, test]
related: "[[属性链接#章节|属性]]"
aliases: ["[[不是引用]]"]
---
# 标题
[[笔记#章节|显示别名]] [[笔记#^block]] ![[嵌入]] ![[photo.png|320]]
[[#本页章节]] [本页](#section) [其他](folder/Some%20Note.md#section)
[外部](https://example.com/note.md) [邮箱](mailto:reader@example.com)
![图片](assets/image.png) [引用][ref]

[ref]: ../Ref.md

正文 #内联标签 #知识/图谱 #123

` + "`[[inline-code]] #不算标签 %%`\n\n```mermaid\n[[fenced]] #code\n```\n\n    [[indented]]\n\n" + `%% [[隐藏]]
[[跨行隐藏]] #隐藏标签 %%

<!-- [[HTML注释]] -->

[[末尾链接]]
`
	metadata := Extract([]byte(raw))
	var targets []string
	for _, ref := range metadata.Links {
		targets = append(targets, ref.Target)
	}
	want := []string{"笔记", "笔记", "嵌入", "photo.png", "folder/Some Note.md", "assets/image.png", "../Ref.md", "末尾链接", "属性链接"}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("got links %q, want %q", targets, want)
	}
	if metadata.InvalidYAML || !reflect.DeepEqual(metadata.Tags, []string{"test", "内联标签", "知识/图谱"}) {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	if !metadata.Links[2].Embed || !metadata.Links[0].Wiki || metadata.Links[4].Wiki {
		t.Fatalf("incorrect reference kind: %+v", metadata.Links)
	}
}

func TestCommentMaskPreservesCodeAndSource(t *testing.T) {
	source := []byte("# Title\n\n`%% preserved %%`\n\n```text\n%% [[code]] %%\n```\n\nvisible %% [[secret]]\nsecret %% [[Link]]\n")
	original := string(source)
	masked := maskComments(source)
	if len(masked) != len(source) || strings.Count(string(masked), "\n") != strings.Count(original, "\n") || string(source) != original {
		t.Fatal("mask must preserve offsets and not modify the input")
	}
	if strings.Contains(string(masked), "secret") || !strings.Contains(string(masked), "%% preserved %%") || !strings.Contains(string(masked), "%% [[code]] %%") {
		t.Fatalf("incorrect mask: %s", masked)
	}
	result, err := Render(source, "欢迎.md", snapshot())
	if err != nil || strings.Contains(result.HTML, "secret") {
		t.Fatalf("comments must not render: %v, %+v", err, result)
	}
}

func TestExtractMalformedProperties(t *testing.T) {
	metadata := Extract([]byte("---\ntags: [broken\n---\n[[Link]]\n"))
	if !metadata.InvalidYAML || len(metadata.Links) != 1 || metadata.Links[0].Target != "Link" {
		t.Fatalf("invalid YAML should not prevent body indexing: %+v", metadata)
	}
}

func TestEscapedLinksAndTableAliases(t *testing.T) {
	metadata := Extract([]byte("\\[[escaped]]\n\n| 链接 |\n| --- |\n| [[Link\\|别名]] |\n"))
	if len(metadata.Links) != 1 || metadata.Links[0].Target != "Link" {
		t.Fatalf("incorrect escaped/table links: %+v", metadata.Links)
	}
}
