package markdown

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExtractPublicationFrontMatter(t *testing.T) {
	raw := []byte("\ufeff" + strings.ReplaceAll(`---
title: " 中文标题 "
date: 2025-11-23
tags: [Go, "#知识管理", go, 2025, null, {ignore: 私有标签}]
categories: 公开 分类
notes:
  tags: [嵌套标签]
  categories: 嵌套分类
  title: 错误标题
---
# 这是正文

#正文标签
`, "\n", "\r\n"))
	got := ExtractPublicationFrontMatter(raw)
	want := PublicationFrontMatter{
		Title: "中文标题", Date: time.Date(2025, 11, 22, 16, 0, 0, 0, time.UTC),
		Tags: []string{"Go", "知识管理", "2025"}, Categories: []string{"公开 分类"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestPublicationFrontMatterBoundsLabels(t *testing.T) {
	var source strings.Builder
	source.WriteString("---\ntags:\n  - ")
	source.WriteString(strings.Repeat("长", 86)) // 258 UTF-8 bytes.
	for i := range 65 {
		fmt.Fprintf(&source, "\n  - 标签%d", i)
	}
	source.WriteString("\n---\n")
	got := ExtractPublicationFrontMatter([]byte(source.String())).Tags
	if len(got) != 64 || got[0] != "标签0" || got[63] != "标签63" {
		t.Fatalf("expected bounded labels in source order; got %v", got)
	}
}

func TestPublicationFrontMatterIgnoresUnexpectedFields(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"malformed", `title: [`},
		{"not a mapping", `[title, tags, date]`},
		{"nested or aliases", `private: &private {title: 私有标题, tags: [私有]}
<<: *private
privateList: &items [保密]
tags: *items
categories: [*items]
title: [不是标量]
date: {day: 23}
cover: '[[私有图片.png]]'`},
		{"duplicates", "tags: [a]\ntags: [b]\ntags: [c]\ntitle: one\ntitle: two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractPublicationFrontMatter([]byte("---\n" + tc.yaml + "\n---\n#正文标签"))
			if !reflect.DeepEqual(got, PublicationFrontMatter{}) {
				t.Fatalf("unexpected public properties: %+v", got)
			}
		})
	}
}

func TestPublicationFrontMatterDates(t *testing.T) {
	for _, tc := range []struct{ date, want string }{
		{"2025-7-3", "2025-07-02T16:00:00Z"},
		{`"2025-07-03T09:30:12.25+08:00"`, "2025-07-03T01:30:12.25Z"},
		{"2024-02-29T23:00:00Z", "2024-02-29T23:00:00Z"},
		{"2023-02-29", ""},
		{"2025-07-03T09:30:12", ""},
		{"2025-07-03T09:30:12+08:60", ""},
		{"2025-07-03T09:30:12+24:00", ""},
		{"2025", ""},
	} {
		t.Run(tc.date, func(t *testing.T) {
			got := ExtractPublicationFrontMatter([]byte("---\ndate: " + tc.date + "\n---\n")).Date
			if tc.want == "" {
				if !got.IsZero() {
					t.Fatalf("accepted an invalid date: %s", got)
				}
			} else if got.Format(time.RFC3339Nano) != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestWordCountMatchesRender(t *testing.T) {
	raw := []byte("\ufeff---\r\ntitle: Not counted\r\n---\r\nGo 和中文\r\n\r\n%% private %%")
	result, err := Render(raw, "article.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count := WordCount(raw); count != result.Words || count != 4 {
		t.Fatalf("count=%d render=%d", count, result.Words)
	}
}
