package web

import (
	"errors"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"time"

	"obblog/internal/vault"
)

func (a *App) listPublications(w http.ResponseWriter, r *http.Request) {
	type row struct {
		Path     string `json:"path"`
		Editable bool   `json:"editable"`
		publicationInfo
	}
	list := []row{}
	for path, article := range a.publications.Snapshot() {
		list = append(list, row{Path: path, Editable: a.writable, publicationInfo: publicationStatus(article)})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].PublishedAt.Equal(*list[j].PublishedAt) {
			return list[i].Path < list[j].Path
		}
		return list[i].PublishedAt.After(*list[j].PublishedAt)
	})
	respond(w, struct {
		Articles []row `json:"articles"`
	}{list})
}

func (a *App) batchPublications(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Paths   []string `json:"paths"`
		Enabled *bool    `json:"enabled"`
	}
	if !decodeSettingsJSON(w, r, &input, 2<<20, "无效的批量操作") {
		return
	}
	if input.Enabled == nil || len(input.Paths) == 0 || len(input.Paths) > 1000 {
		fail(w, 400, "请选择 1 至 1000 篇文章及上线状态")
		return
	}
	seen := make(map[string]bool, len(input.Paths))
	for _, p := range input.Paths {
		if !vault.ValidPath(p) || vault.Kind(p) != "note" || seen[p] {
			fail(w, 400, "文章路径无效或重复")
			return
		}
		seen[p] = true
	}
	if *input.Enabled && !a.publicationCurrent(w, r) {
		return
	}
	changed, err := a.publications.SetPublishedBatch(a.vault, input.Paths, *input.Enabled)
	if err != nil {
		publicationManagementError(w, err)
		return
	}
	respond(w, map[string]int{"changed": changed})
}

func (a *App) changePublicationDate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path        string    `json:"path"`
		PublishedAt time.Time `json:"publishedAt"`
	}
	if !decodeSettingsJSON(w, r, &input, 16384, "无效的发布时间") {
		return
	}
	if !vault.ValidPath(input.Path) || vault.Kind(input.Path) != "note" || input.PublishedAt.IsZero() || input.PublishedAt.Year() < 1 || input.PublishedAt.Year() > 9999 || input.PublishedAt.After(time.Now().Add(time.Minute)) {
		fail(w, 400, "笔记路径或发布时间无效")
		return
	}
	article, err := a.publications.SetPublishedDate(input.Path, input.PublishedAt)
	if err != nil {
		publicationManagementError(w, err)
		return
	}
	respond(w, publicationStatus(article))
}

func publicationManagementError(w http.ResponseWriter, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		fail(w, 404, "文章记录或源笔记不存在")
		return
	}
	log.Printf("update publication records: %v", err)
	fail(w, 500, "无法保存发布记录")
}
