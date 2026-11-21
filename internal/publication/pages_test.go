package publication

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPagesAndMetadataWritesPreserveEachOtherAndDiskFormat(t *testing.T) {
	data, source := t.TempDir(), t.TempDir()
	store, err := Open(data, source)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(store.dir, "publications.json")
	if err := os.MkdirAll(store.dir, 0700); err != nil {
		t.Fatal(err)
	}
	const legacy = `{"version":1,"articles":{},"blog":{"title":"Existing site","theme":"light","pages":[{"slug":"old-page","title":"Existing page","source":"legacy body","createdAt":"2024-01-02T03:04:05Z","updatedAt":"2024-01-02T03:04:05Z"}]}}`
	if err := os.WriteFile(name, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	store, err = Open(data, source)
	if err != nil {
		t.Fatal(err)
	}
	old := store.Page("old-page")
	if old == nil || old.Source != "legacy body" || old.CreatedAt.IsZero() || old.Summary().Revision == "" {
		t.Fatalf("legacy page not preserved: %+v", old)
	}

	// Exercise the store lock directly, not the serial HTTP write middleware.
	metadata := store.Metadata()
	metadata.Title, metadata.Theme = "New site name", "dark"
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := store.SetBlog(metadata)
		results <- err
	}()
	go func() {
		<-start
		_, err := store.UpdatePage("old-page", PageInput{Slug: "renamed", Title: "New page name", Source: "new body"}, old.Summary().Revision)
		results <- err
	}()
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	written := store.Page("renamed")
	loaded, err := Open(data, source)
	if err != nil {
		t.Fatal(err)
	}
	page := loaded.Page("renamed")
	if page == nil || written == nil || page.Source != "new body" || !page.CreatedAt.Equal(old.CreatedAt) || page.Summary().Revision != written.Summary().Revision || loaded.Metadata() != metadata {
		t.Fatalf("writes did not persist independently: page=%+v metadata=%+v", page, loaded.Metadata())
	}

	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var disk struct {
		Version int `json:"version"`
		Blog    struct {
			Title string                       `json:"title"`
			Pages []map[string]json.RawMessage `json:"pages"`
		} `json:"blog"`
	}
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	if disk.Version != 1 || disk.Blog.Title != "New site name" || len(disk.Blog.Pages) != 1 {
		t.Fatalf("disk layout changed: %s", raw)
	}
	for _, field := range []string{"slug", "title", "source", "createdAt", "updatedAt"} {
		if disk.Blog.Pages[0][field] == nil {
			t.Errorf("legacy field %s no longer on disk", field)
		}
	}
	if len(disk.Blog.Pages[0]) != 5 {
		t.Fatalf("derived API fields unneeded on disk: %s", raw)
	}
}

func TestFailedStandalonePageWriteLeavesStoreUnchanged(t *testing.T) {
	store, err := Open(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store.dir, "publications.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePage(PageInput{Slug: "about", Title: "About", Source: "body"}); err == nil {
		t.Fatal("rename over directory unexpectedly succeeded")
	}
	if pages, _ := store.Pages(); len(pages) != 0 {
		t.Fatalf("failed write exposed an in-memory page: %v", pages)
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".publications-") {
			t.Errorf("temporary file not cleaned up: %s", entry.Name())
		}
	}
}
