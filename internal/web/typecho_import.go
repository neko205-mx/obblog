package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"obblog/internal/publication"
	"obblog/internal/typecho"
	"obblog/internal/vault"
)

const maxTypechoSQL = 32 << 20

var errTypechoUpload = errors.New("请上传一份 UTF-8 的 .sql 文件")

type typechoImportRow struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Path        string    `json:"path"`
	Slug        string    `json:"slug"`
	PublishedAt time.Time `json:"publishedAt"`
	Tags        []string  `json:"tags"`
	Categories  []string  `json:"categories"`
	Status      string    `json:"status"`
	URL         string    `json:"url,omitempty"`
	source      []byte
	exists      bool
}

type typechoImportSummary struct {
	Ready     int `json:"ready"`
	Existing  int `json:"existing"`
	Conflicts int `json:"conflicts"`
	Skipped   int `json:"skipped"`
	Imported  int `json:"imported"`
}

type typechoImportPlan struct {
	Checksum       string               `json:"checksum"`
	Summary        typechoImportSummary `json:"summary"`
	Posts          []typechoImportRow   `json:"posts"`
	SkippedReasons map[string]int       `json:"skippedReasons"`
	Blocked        string               `json:"blocked,omitempty"`
	Message        string               `json:"message,omitempty"`
}

func readTypechoUpload(w http.ResponseWriter, r *http.Request, apply bool) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTypechoSQL+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, "", errTypechoUpload
	}
	var raw []byte
	var checksum string
	for seenFile, seenChecksum := false, false; ; {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", err
		}
		switch part.FormName() {
		case "file":
			if seenFile || !strings.EqualFold(path.Ext(part.FileName()), ".sql") {
				return nil, "", errTypechoUpload
			}
			seenFile = true
			raw, err = io.ReadAll(io.LimitReader(part, maxTypechoSQL+1))
			if len(raw) > maxTypechoSQL {
				return nil, "", &http.MaxBytesError{Limit: maxTypechoSQL}
			}
		case "checksum":
			if !apply || seenChecksum || part.FileName() != "" {
				return nil, "", errTypechoUpload
			}
			seenChecksum = true
			var value []byte
			value, err = io.ReadAll(io.LimitReader(part, 2*sha256.Size+1))
			checksum = string(value)
			if err == nil && !vault.ValidRevision(checksum) {
				return nil, "", errTypechoUpload
			}
		default:
			return nil, "", errTypechoUpload
		}
		if err != nil {
			return nil, "", err
		}
		if err := part.Close(); err != nil {
			return nil, "", err
		}
	}
	if len(raw) == 0 || apply && checksum == "" {
		return nil, "", errTypechoUpload
	}
	return raw, checksum, nil
}

func importLabels(start []string, values []string, tags bool) []string {
	seen := map[string]bool{}
	list := []string{}
	for _, value := range append(start, values...) {
		name := strings.Join(strings.Fields(value), " ")
		if tags {
			name = strings.TrimSpace(strings.TrimPrefix(name, "#"))
		}
		key := strings.ToLower(name)
		if len(name) == 0 || len(name) > 256 || seen[key] {
			continue
		}
		list = append(list, name)
		seen[key] = true
		if len(list) == 64 {
			break
		}
	}
	return list
}

func makeTypechoNote(post typecho.Post, tags, categories []string) ([]byte, error) {
	meta := struct {
		Title      string   `yaml:"title"`
		Date       string   `yaml:"date"`
		Tags       []string `yaml:"tags,flow"`
		Categories []string `yaml:"categories,flow,omitempty"`
		ID         int64    `yaml:"typecho_id"`
		Slug       string   `yaml:"typecho_slug,omitempty"`
	}{post.Title, post.PublishedAt.UTC().Format(time.RFC3339), tags, categories, post.ID, post.Slug}
	header, err := yaml.Marshal(meta)
	if err != nil {
		return nil, err
	}
	return bytes.Join([][]byte{[]byte("---\n"), header, []byte("---\n\n"), []byte(post.Markdown)}, nil), nil
}

func (a *App) planTypechoImport(raw []byte, backup typecho.Backup) (typechoImportPlan, error) {
	plan := typechoImportPlan{Posts: []typechoImportRow{}, SkippedReasons: backup.Skipped}
	if plan.SkippedReasons == nil {
		plan.SkippedReasons = map[string]int{}
	}
	for _, count := range plan.SkippedReasons {
		plan.Summary.Skipped += count
	}
	if err := a.vault.CheckTypechoDirectory(false); err != nil {
		plan.Blocked = "知识库根目录的 typecho 不是可用目录，或没有访问权限"
	}
	published := a.publications.Snapshot()
	hash := sha256.New()
	// Include the anticipated target states and import dates. A file that
	// became eligible (for example a future post) requires another preview.
	fmt.Fprintf(hash, "%x\n", sha256.Sum256(raw))
	for _, post := range backup.Posts {
		row := typechoImportRow{
			ID: post.ID, Title: post.Title, Slug: post.Slug, PublishedAt: post.PublishedAt,
			Path: fmt.Sprintf("typecho/%d.md", post.ID), Tags: importLabels([]string{"typecho"}, post.Tags, true),
			Categories: importLabels(nil, post.Categories, false), Status: "ready",
		}
		var err error
		row.source, err = makeTypechoNote(post, row.Tags, row.Categories)
		if err != nil {
			return typechoImportPlan{}, err
		}
		if len(row.source) > vault.MaxNoteSize {
			plan.Summary.Skipped++
			plan.SkippedReasons["tooLarge"]++
			continue
		}
		if plan.Blocked == "" {
			row.exists, err = a.vault.CheckNoteDestination(row.Path)
			if err != nil {
				row.Status = "conflict"
			}
			if row.exists && err == nil {
				source, _, err := a.vault.ReadNote(row.Path)
				if err != nil || !bytes.Equal(source, row.source) {
					row.Status = "conflict"
				}
			}
			if old := published[row.Path]; old != nil {
				if row.Status == "ready" && row.exists && old.Enabled && old.PublishedAt.Equal(post.PublishedAt) && old.Source == string(row.source) {
					row.Status, row.URL = "existing", publication.URL(old.ID)
				} else {
					row.Status = "conflict"
				}
			}
		} else {
			row.Status = "conflict"
		}
		switch row.Status {
		case "ready":
			plan.Summary.Ready++
		case "existing":
			plan.Summary.Existing++
		case "conflict":
			plan.Summary.Conflicts++
		}
		fmt.Fprintf(hash, "%d:%s:%t:%s\n", row.ID, row.Status, row.exists, row.URL)
		plan.Posts = append(plan.Posts, row)
	}
	plan.Checksum = hex.EncodeToString(hash.Sum(nil))
	return plan, nil
}

func (a *App) previewTypechoImport(w http.ResponseWriter, r *http.Request) {
	a.typechoImport(w, r, false)
}
func (a *App) applyTypechoImport(w http.ResponseWriter, r *http.Request) { a.typechoImport(w, r, true) }

func (a *App) typechoImport(w http.ResponseWriter, r *http.Request, apply bool) {
	if apply && !a.writable {
		fail(w, 403, "知识库为只读模式，不能导入")
		return
	}
	raw, expected, err := readTypechoUpload(w, r, apply)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			fail(w, 413, "SQL 文件不能超过 32 MiB")
		} else {
			fail(w, 400, errTypechoUpload.Error())
		}
		return
	}
	backup, err := typecho.Parse(raw, time.Now())
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	plan, err := a.planTypechoImport(raw, backup)
	if err != nil {
		fail(w, 500, "无法生成导入预览")
		return
	}
	if !a.writable {
		plan.Blocked = "知识库为只读模式，不能导入"
	}
	if !apply {
		if plan.Blocked == "" && plan.Summary.Ready > 0 && a.git != nil {
			status := a.git.Status(r.Context())
			if !status.CanWrite {
				plan.Blocked = status.Message
			}
		}
		respond(w, plan)
		return
	}
	if plan.Checksum != expected {
		fail(w, 409, "备份或导入目标已发生变化，请重新预览")
		return
	}
	if plan.Blocked != "" {
		fail(w, 409, plan.Blocked)
		return
	}
	if plan.Summary.Conflicts > 0 {
		fail(w, 409, "存在同名笔记或不匹配的发布记录，未导入任何文章")
		return
	}
	if plan.Summary.Ready == 0 {
		respond(w, plan)
		return
	}
	var newPaths []string
	jobs := make([]publication.ImportNote, 0, plan.Summary.Ready)
	for _, row := range plan.Posts {
		if row.Status != "ready" {
			continue
		}
		if !row.exists {
			newPaths = append(newPaths, row.Path)
		}
		jobs = append(jobs, publication.ImportNote{Path: row.Path, Revision: vault.Revision(row.source), PublishedAt: row.PublishedAt})
	}
	write := func() error {
		if len(newPaths) == 0 {
			return nil
		}
		if err := a.vault.CheckTypechoDirectory(true); err != nil {
			return errors.New("无法创建或写入 typecho 目录")
		}
		for _, row := range plan.Posts {
			if row.Status != "ready" || row.exists {
				continue
			}
			if err := r.Context().Err(); err != nil {
				return errors.New("导入已取消，已写入的笔记尚未发布")
			}
			if _, err := a.vault.SaveNote(row.Path, row.source, "", true); err != nil {
				return fmt.Errorf("无法写入 %s；没有发布任何文章，已写入的笔记保留", row.Path)
			}
		}
		return nil
	}
	if a.git != nil {
		err = a.git.ImportNotes(r.Context(), newPaths, write)
	} else {
		err = write()
	}
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	created, err := a.publications.ImportExternalNotes(a.vault, jobs)
	if err != nil {
		if errors.Is(err, vault.ErrConflict) || errors.Is(err, fs.ErrNotExist) {
			fail(w, 409, "导入笔记已变化，未公开发布；请重新预览")
			return
		}
		log.Printf("publish Typecho import: %v", err)
		fail(w, 500, "笔记已写入，但公开副本保存失败；可用同一备份重新预览并发布")
		return
	}
	byPath := map[string]*publication.Article{}
	for _, article := range created {
		byPath[article.Path] = article
	}
	for i := range plan.Posts {
		if article := byPath[plan.Posts[i].Path]; article != nil {
			plan.Posts[i].Status, plan.Posts[i].URL = "imported", publication.URL(article.ID)
		}
	}
	plan.Summary.Ready, plan.Summary.Imported = 0, len(created)
	plan.Message = fmt.Sprintf("已导入并发布 %d 篇文章，保留 Typecho 原发布时间。", len(created))
	respond(w, plan)
}
