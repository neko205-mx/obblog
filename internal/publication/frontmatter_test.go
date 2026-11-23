package publication

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"obblog/internal/markdown"
	"obblog/internal/vault"
)

func metadataFixture(t *testing.T) (*Store, *vault.Vault, string, string) {
	t.Helper()
	root := t.TempDir()
	notes, data := filepath.Join(root, "notes"), filepath.Join(root, "data")
	if err := os.Mkdir(notes, 0700); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(notes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	s, err := Open(data, notes)
	if err != nil {
		t.Fatal(err)
	}
	return s, v, notes, data
}

func writeMetadataNote(t *testing.T, notes, name, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(notes, name), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationFrontMatterCopiesSelectedFields(t *testing.T) {
	s, v, notes, _ := metadataFixture(t)
	const name = "note.md"
	const body = "# 正文题目\n\nHello 中文 #正文标签\n\n%% PRIVATE_COMMENT %%\n"
	const source = "---\ntitle: YAML 标题\ndate: 2025-11-23\ntags: [Go, 中文]\ncategories: 技术\ncover: '[[private.png]]'\nsecret: PRIVATE_PROPERTY\n---\n" + body
	writeMetadataNote(t, notes, name, source)
	// Even an existing attachment isn't copied just because a YAML property links it.
	writeMetadataNote(t, notes, "private.png", "not public")
	a, err := s.Set(v, name, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Title != "YAML 标题" || a.Words != markdown.WordCount([]byte(body)) || !a.PublishedAt.Equal(time.Date(2025, 11, 22, 16, 0, 0, 0, time.UTC)) || !reflect.DeepEqual(a.Tags, []string{"Go", "中文"}) || !reflect.DeepEqual(a.Categories, []string{"技术"}) {
		t.Fatalf("bad published fields: %+v", a)
	}
	if len(a.Assets) != 0 || len(a.References) != 0 || a.Source != source {
		t.Fatalf("unrelated properties caused publication side effects: %+v", a)
	}
	rendered, err := Render(a, s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Properties != "" || strings.Contains(rendered.HTML, "PRIVATE_") || !strings.Contains(rendered.HTML, "正文题目") || rendered.Words != a.Words {
		t.Fatalf("public render mismatch: %+v", rendered)
	}
	if disk, err := os.ReadFile(filepath.Join(notes, name)); err != nil || string(disk) != source {
		t.Fatalf("original changed: %v", err)
	}

	changed := strings.NewReplacer("YAML 标题", "新标题", "[Go, 中文]", "[测试]", "categories: 技术", "categories: [生活]", "PRIVATE_PROPERTY", "OTHER_PRIVATE_PROPERTY").Replace(source)
	writeMetadataNote(t, notes, name, changed)
	if got := s.Snapshot()[name]; got.Title != a.Title || !reflect.DeepEqual(got.Tags, a.Tags) {
		t.Fatal("live vault edit altered a published snapshot before refresh")
	}
	if err := s.Refresh(v, nil, ""); err != nil {
		t.Fatal(err)
	}
	next := s.Snapshot()[name]
	if !next.Revised || !next.UpdatedAt.After(a.UpdatedAt) || next.Title != "新标题" || !reflect.DeepEqual(next.Tags, []string{"测试"}) || !reflect.DeepEqual(next.Categories, []string{"生活"}) || next.ID != a.ID {
		t.Fatalf("refresh did not publish metadata changes: %+v", next)
	}
	if a.Tags[0] != "Go" || a.Categories[0] != "技术" {
		t.Fatal("refresh mutated an older snapshot")
	}
	writeMetadataNote(t, notes, name, strings.Replace(changed, "OTHER_PRIVATE_PROPERTY", "PRIVATE_ONLY_UPDATE", 1))
	if err := s.Refresh(v, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot()[name]; got.UpdatedAt != next.UpdatedAt {
		t.Fatal("private metadata changed the public updated timestamp")
	}
	// Removing only a taxonomy changes the public snapshot too.
	writeMetadataNote(t, notes, name, strings.Replace(changed, "categories: [生活]", "categories: []", 1))
	if err := s.Refresh(v, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot()[name]; len(got.Categories) != 0 || !got.UpdatedAt.After(next.UpdatedAt) {
		t.Fatalf("taxonomy-only change did not revise the article: %+v", got)
	}
}

func TestFrontMatterPublishedDatePrecedenceAndFallback(t *testing.T) {
	s, v, notes, data := metadataFixture(t)
	const name = "文章.md"
	writeDate := func(date string) { writeMetadataNote(t, notes, name, "---\ndate: "+date+"\n---\n# 正文标题\n") }
	writeDate("2025-02-30")
	start := time.Now()
	a, err := s.Set(v, name, true, nil, "")
	if err != nil || a.PublishedAt.Before(start) || a.PublishedAt.After(time.Now()) || a.Title != "正文标题" {
		t.Fatalf("invalid date or title did not fall back: %v %+v", err, a)
	}
	for _, date := range []string{"2999-05-05", "not-a-date"} {
		writeDate(date)
		if err := s.Refresh(v, nil, ""); err != nil {
			t.Fatal(err)
		}
		if got := s.Snapshot()[name]; !got.PublishedAt.Equal(a.PublishedAt) || got.Revised {
			t.Fatalf("refresh with invalid/future date unexpectedly changed publication: %+v", got)
		}
	}
	writeDate("2020-05-01T07:30:00+08:00")
	if err := s.Refresh(v, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot()[name]; got.PublishedAt.Format(time.RFC3339) != "2020-04-30T23:30:00Z" {
		t.Fatalf("valid changed date was not applied: %v", got.PublishedAt)
	}
	admin := time.Date(2019, 1, 2, 3, 4, 0, 0, time.UTC)
	if a, err := s.Set(v, name, true, &admin, ""); err != nil || !a.PublishedAt.Equal(admin) || a.UseFrontMatterDate {
		t.Fatalf("could not persist admin override: %v %+v", err, a)
	}
	writeDate("2018-07-07")
	reopened, err := Open(data, notes)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Refresh(v, nil, ""); err != nil {
		t.Fatal(err)
	}
	if !reopened.Snapshot()[name].PublishedAt.Equal(admin) {
		t.Fatal("admin override was lost on refresh after reopen")
	}
	if _, err := reopened.Set(v, name, false, nil, ""); err != nil {
		t.Fatal(err)
	}
	if a, err := reopened.Set(v, name, true, nil, ""); err != nil || !a.PublishedAt.Equal(admin) {
		t.Fatalf("republication lost the override: %v %+v", err, a)
	}
	writeMetadataNote(t, notes, "filename.md", "---\ntitle: [invalid]\ndate: 2020-06-06\n---\nno heading")
	if a, err := reopened.Set(v, "filename.md", true, &admin, ""); err != nil || !a.PublishedAt.Equal(admin) || a.Title != "filename" {
		t.Fatalf("first publish ignored admin date or filename fallback: %v %+v", err, a)
	}
}

func TestLegacyPublicationBackfillDoesNotChangeDate(t *testing.T) {
	s, v, notes, data := metadataFixture(t)
	const name = "legacy.md"
	const source = "---\ntitle: 新标题\ndate: 2020-05-01\ntags: [旧站]\ncategories: 技术\n---\n# 旧标题\n\n中文内容"
	writeMetadataNote(t, notes, name, source)
	oldDate := time.Date(2023, 5, 10, 13, 21, 0, 0, time.UTC)
	old := &Article{ID: "1234567890abcdef1234567890abcdef", Path: name, Title: "旧标题", Enabled: true, PublishedAt: oldDate, UpdatedAt: oldDate, Source: source, References: map[string]*vault.Entry{}}
	if err := s.persist(map[string]*Article{name: old}, s.blog, ""); err != nil {
		t.Fatal(err)
	}
	writeMetadataNote(t, notes, name, source+"\n\nUnpublished draft words.")
	reopened, err := Open(data, notes)
	if err != nil {
		t.Fatal(err)
	}
	if a := reopened.Snapshot()[name]; a.Title != old.Title || len(a.Tags) != 0 || len(a.Categories) != 0 || a.Words != markdown.WordCount([]byte(source)) || !a.PublishedAt.Equal(oldDate) {
		t.Fatalf("start unexpectedly re-published a legacy article: %+v", a)
	}
	if err := reopened.Refresh(v, nil, ""); err != nil {
		t.Fatal(err)
	}
	if a := reopened.Snapshot()[name]; a.Title != "新标题" || len(a.Tags) != 1 || len(a.Categories) != 1 || !a.PublishedAt.Equal(oldDate) || a.ID != old.ID || !a.Revised {
		t.Fatalf("legacy refresh failed to backfill only allowed fields: %+v", a)
	}
}
