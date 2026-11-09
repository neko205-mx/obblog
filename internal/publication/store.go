// Package publication persists explicit public copies separately from the vault.
package publication

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"obblog/internal/markdown"
	"obblog/internal/vault"
)

type Article struct {
	ID          string                  `json:"id"`
	Path        string                  `json:"path"`
	Title       string                  `json:"title"`
	Enabled     bool                    `json:"enabled"`
	PublishedAt time.Time               `json:"publishedAt"`
	UpdatedAt   time.Time               `json:"updatedAt"`
	Revised     bool                    `json:"revised"`
	Source      string                  `json:"source"`
	References  map[string]*vault.Entry `json:"references"`
	Assets      map[string]string       `json:"assets"`
}

type diskState struct {
	Version  int                 `json:"version"`
	GitHead  string              `json:"gitHead,omitempty"`
	Articles map[string]*Article `json:"articles"`
}

type Store struct {
	mu       sync.RWMutex
	dir      string
	articles map[string]*Article
	gitHead  string
}

func Open(data, source string) (*Store, error) {
	abs, err := filepath.Abs(data)
	if err != nil {
		return nil, err
	}
	// Resolving the nearest existing ancestor also handles a not-yet-created data directory.
	canonical := func(name string) string {
		parent, err := filepath.EvalSymlinks(name)
		if err == nil {
			return parent
		}
		if filepath.Dir(name) == name {
			return name
		}
		parts := []string{filepath.Base(name)}
		for name = filepath.Dir(name); ; name = filepath.Dir(name) {
			if parent, err = filepath.EvalSymlinks(name); err == nil {
				for i := len(parts) - 1; i >= 0; i-- {
					parent = filepath.Join(parent, parts[i])
				}
				return parent
			}
			if filepath.Dir(name) == name {
				return name
			}
			parts = append(parts, filepath.Base(name))
		}
	}
	abs = canonical(abs)
	source, err = filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	source = canonical(source)
	rel, err := filepath.Rel(source, abs)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return nil, errors.New("发布数据目录不能位于笔记库内，请调整 -data")
	}
	hash := sha256.Sum256([]byte(source))
	s := &Store{dir: filepath.Join(abs, "publications", hex.EncodeToString(hash[:16])), articles: map[string]*Article{}}
	b, err := os.ReadFile(filepath.Join(s.dir, "publications.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var state diskState
	if err = json.Unmarshal(b, &state); err != nil {
		return nil, fmt.Errorf("read publications: %w", err)
	}
	if state.Version != 1 || state.Articles == nil {
		return nil, errors.New("无效的发布记录")
	}
	if state.GitHead != "" {
		if _, err := hex.DecodeString(state.GitHead); err != nil || (len(state.GitHead) != 40 && len(state.GitHead) != 64) {
			return nil, errors.New("无效的发布同步版本")
		}
	}
	ids := map[string]bool{}
	for name, article := range state.Articles {
		if article == nil || article.Path != name || !vault.ValidPath(name) || vault.Kind(name) != "note" || !validID(article.ID) || ids[article.ID] || article.PublishedAt.IsZero() || article.UpdatedAt.IsZero() || len(article.Source) > vault.MaxNoteSize {
			return nil, errors.New("无效的文章发布记录")
		}
		ids[article.ID] = true
		for name, blob := range article.Assets {
			if !vault.ValidPath(name) || vault.Kind(name) != "asset" || !validBlob(blob) {
				return nil, errors.New("无效的发布附件记录")
			}
		}
		for _, entry := range article.References {
			if entry == nil || !vault.ValidPath(entry.Path) || vault.Kind(entry.Path) != entry.Kind {
				return nil, errors.New("无效的发布链接记录")
			}
		}
	}
	s.articles = state.Articles
	s.gitHead = state.GitHead
	return s, nil
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && id == strings.ToLower(id)
}

func validBlob(name string) bool {
	base := strings.TrimSuffix(name, path.Ext(name))
	if len(base) != 64 || vault.Kind(name) != "asset" || path.Base(name) != name {
		return false
	}
	_, err := hex.DecodeString(base)
	return err == nil
}

// Returned articles and maps are immutable; writers replace the entire state.
func (s *Store) Snapshot() map[string]*Article {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMap(s.articles)
}

func cloneMap(source map[string]*Article) map[string]*Article {
	result := make(map[string]*Article, len(source))
	for name, article := range source {
		result[name] = article
	}
	return result
}

func Published(articles map[string]*Article) []*Article {
	list := []*Article{}
	for _, article := range articles {
		if article.Enabled {
			list = append(list, article)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].PublishedAt.Equal(list[j].PublishedAt) {
			return list[i].ID < list[j].ID
		}
		return list[i].PublishedAt.After(list[j].PublishedAt)
	})
	return list
}

func URL(id string) string { return "/posts/" + id }

func Render(article *Article, articles map[string]*Article) (*markdown.Result, error) {
	return markdown.RenderWithOptions([]byte(article.Source), article.Path, nil, markdown.RenderOptions{
		Resolve: func(from, target string, wiki bool) (*vault.Entry, error) {
			if target == "" {
				return &vault.Entry{Path: article.Path, Kind: "note"}, nil
			}
			if entry := article.References[referenceKey(target, wiki)]; entry != nil {
				return entry, nil
			}
			return nil, fs.ErrNotExist
		},
		NoteURL: func(name, fragment string) string {
			target := articles[name]
			if target == nil || !target.Enabled {
				return ""
			}
			href := URL(target.ID)
			if fragment != "" {
				href += "#" + url.PathEscape(markdown.Slug(fragment))
			}
			return href
		},
		AssetURL: func(name string) string {
			if blob := article.Assets[name]; blob != "" {
				return "/media/" + article.ID + "/" + blob
			}
			return "/media/missing"
		},
	})
}

func referenceKey(target string, wiki bool) string {
	if wiki {
		return "wiki:" + target
	}
	return "markdown:" + target
}

// Set toggles one copy or changes its date. Repeating an enabled toggle does not
// silently refresh the body; Refresh is tied to explicit sync/save operations.
func (s *Store) Set(v *vault.Vault, name string, enabled bool, date *time.Time, head string) (*Article, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneMap(s.articles)
	previous := next[name]
	if !vault.ValidPath(name) || vault.Kind(name) != "note" {
		return nil, fs.ErrNotExist
	}
	if previous == nil && !enabled {
		return nil, fs.ErrNotExist
	}
	if date != nil && (date.IsZero() || date.Year() < 1 || date.Year() > 9999 || date.After(time.Now().Add(time.Minute))) {
		return nil, errors.New("发布时间不能晚于当前时间")
	}
	var article Article
	if previous != nil {
		article = *previous
	} else {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		article = Article{ID: hex.EncodeToString(id), Path: name, PublishedAt: now, UpdatedAt: now}
	}
	if enabled && (previous == nil || !previous.Enabled) {
		snapshot, err := v.Scan()
		if err != nil {
			return nil, err
		}
		copy, err := s.copy(v, snapshot, &article)
		if err != nil {
			return nil, err
		}
		article = *copy
	}
	article.Enabled = enabled
	if date != nil {
		article.PublishedAt = date.UTC()
	}
	next[name] = &article
	if s.gitHead != "" {
		head = s.gitHead
	}
	if err := s.persist(next, head); err != nil {
		return nil, err
	}
	return &article, nil
}

func (s *Store) copy(v *vault.Vault, snapshot *vault.Snapshot, previous *Article) (*Article, error) {
	raw, _, err := v.ReadNote(previous.Path)
	if err != nil {
		return nil, err
	}
	article := *previous
	article.Source = string(raw)
	article.Title = strings.TrimSuffix(path.Base(article.Path), path.Ext(article.Path))
	article.References = map[string]*vault.Entry{}
	article.Assets = map[string]string{}
	for _, ref := range markdown.ExtractBody(raw).Links {
		var entry *vault.Entry
		if ref.Wiki {
			entry, err = snapshot.ResolveWiki(article.Path, ref.Target)
		} else {
			entry, err = snapshot.Resolve(article.Path, ref.Target)
		}
		if err != nil {
			continue
		}
		article.References[referenceKey(ref.Target, ref.Wiki)] = entry
		if entry.Kind != "asset" {
			continue
		}
		if _, exists := article.Assets[entry.Path]; exists {
			continue
		}
		blob, err := s.copyAsset(v, entry.Path)
		if err != nil {
			return nil, fmt.Errorf("copy attachment %s: %w", entry.Path, err)
		}
		article.Assets[entry.Path] = blob
	}
	article.Enabled = true
	rendered, err := Render(&article, map[string]*Article{article.Path: &article})
	if err != nil {
		return nil, err
	}
	if len(rendered.Headings) > 0 && rendered.Headings[0].Level == 1 {
		article.Title = rendered.Headings[0].Text
	}
	oldAssets, _ := json.Marshal(previous.Assets)
	newAssets, _ := json.Marshal(article.Assets)
	old := *previous
	old.Enabled = true
	oldRendered, err := Render(&old, map[string]*Article{old.Path: &old})
	if err != nil {
		return nil, err
	}
	if previous.References != nil && (oldRendered.HTML != rendered.HTML || previous.Title != article.Title || string(oldAssets) != string(newAssets)) {
		article.UpdatedAt = time.Now().UTC()
		article.Revised = true
	}
	return &article, nil
}

func (s *Store) copyAsset(v *vault.Vault, name string) (string, error) {
	source, err := v.OpenFile(name)
	if err != nil {
		return "", err
	}
	defer source.Close()
	dir := filepath.Join(s.dir, "blobs")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".copy-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(io.MultiWriter(f, hash), source); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	blob := hex.EncodeToString(hash.Sum(nil)) + strings.ToLower(path.Ext(name))
	if err = os.Rename(f.Name(), filepath.Join(dir, blob)); err != nil {
		return "", err
	}
	return blob, nil
}

// Refresh publishes only enabled notes. Removed sources are withdrawn; metadata
// is retained so a later re-publication keeps the original ID and date.
func (s *Store) Refresh(v *vault.Vault, renames map[string]string, head string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.articles) == 0 {
		return nil
	}
	snapshot, err := v.Scan()
	if err != nil {
		return err
	}
	next := cloneMap(s.articles)
	for old, name := range renames {
		if article := next[old]; article != nil && next[name] == nil && vault.ValidPath(name) && vault.Kind(name) == "note" {
			moved := *article
			moved.Path = name
			delete(next, old)
			next[name] = &moved
		}
	}
	for name, previous := range next {
		if !previous.Enabled {
			continue
		}
		if snapshot.Files[name] == nil {
			removed := *previous
			removed.Enabled = false
			next[name] = &removed
			continue
		}
		article, err := s.copy(v, snapshot, previous)
		if err != nil {
			return err
		}
		next[name] = article
	}
	return s.persist(next, head)
}

func (s *Store) Head() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gitHead
}

func (s *Store) persist(next map[string]*Article, head string) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".publications-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = json.NewEncoder(f).Encode(diskState{Version: 1, Articles: next, GitHead: head}); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(s.dir, "publications.json")); err != nil {
		return err
	}
	s.articles = next
	s.gitHead = head
	return nil
}

func (s *Store) OpenAsset(id, blob string) (*os.File, string, error) {
	if !validID(id) || !validBlob(blob) {
		return nil, "", fs.ErrNotExist
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, article := range s.articles {
		if article.ID != id || !article.Enabled {
			continue
		}
		for name, stored := range article.Assets {
			if stored == blob {
				f, err := os.Open(filepath.Join(s.dir, "blobs", blob))
				return f, path.Base(name), err
			}
		}
	}
	return nil, "", fs.ErrNotExist
}
