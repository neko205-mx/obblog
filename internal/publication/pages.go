package publication

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	PageLimit     = 12
	MaxPageSource = 2 << 20
	maxPageSlug   = 64
)

var (
	pageSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	ErrInvalidPage  = errors.New("无效的独立页面")
	ErrPageTooLarge = errors.New("独立页面内容超过 2 MiB")
	ErrPageExists   = errors.New("该独立页面短地址已存在")
	ErrPageConflict = errors.New("独立页面已被修改，请重新加载")
	ErrPageLimit    = errors.New("独立页面数量超出限制")
)

// BlogPage is the on-disk page format. Revisions and URLs are constructed from
// the record and are intentionally not written to publications.json.
type BlogPage struct {
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// PageInput contains the user-editable fields, not timestamps or revision.
type PageInput struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Source string `json:"source"`
}

// PageSummary excludes source for listing, but carries the revision needed to
// delete a page without fetching its contents first.
type PageSummary struct {
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Revision  string    `json:"revision"`
	URL       string    `json:"url"`
}

func (p BlogPage) Summary() PageSummary {
	return PageSummary{p.Slug, p.Title, p.CreatedAt, p.UpdatedAt, p.revision(), PageURL(p.Slug)}
}

func (p BlogPage) revision() string {
	// Include all editable fields and the server-generated timestamps so even an
	// edit reverted to its old content doesn't make an earlier revision current.
	h := sha256.New()
	for _, field := range []string{p.Slug, p.Title, p.Source, p.CreatedAt.UTC().Format(time.RFC3339Nano), p.UpdatedAt.UTC().Format(time.RFC3339Nano)} {
		fmt.Fprintf(h, "%d:%s", len(field), field)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func validPageSlug(slug string) bool {
	return len(slug) <= maxPageSlug && pageSlugPattern.MatchString(slug)
}

func validatePage(input PageInput) (PageInput, error) {
	input.Slug = strings.TrimSpace(input.Slug)
	input.Title = strings.TrimSpace(input.Title)
	if !validPageSlug(input.Slug) {
		return PageInput{}, fmt.Errorf("%w：短地址必须为小写字母、数字和连字符，且不超过 64 字符", ErrInvalidPage)
	}
	if input.Title == "" || len(input.Title) > maxBlogTitle || !utf8.ValidString(input.Title) {
		return PageInput{}, fmt.Errorf("%w：标题不能为空或超过 %d 字节", ErrInvalidPage, maxBlogTitle)
	}
	if len(input.Source) > MaxPageSource {
		return PageInput{}, ErrPageTooLarge
	}
	if !utf8.ValidString(input.Source) {
		return PageInput{}, fmt.Errorf("%w：正文不是 UTF-8", ErrInvalidPage)
	}
	return input, nil
}

// PageURL is the canonical public URL of a validated slug.
func PageURL(slug string) string { return "/pages/" + slug }

func pageIndex(pages []*BlogPage, slug string) int {
	for i, page := range pages {
		if page.Slug == slug {
			return i
		}
	}
	return -1
}

// Pages returns summaries in the same order as the public nav. Article and page
// changes are serialized under mu; the list and the linkable post paths thus
// represent the same store snapshot.
func (s *Store) Pages() ([]PageSummary, []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pages := make([]PageSummary, 0, len(s.blog.Pages))
	paths := make([]string, 0, len(s.articles))
	for _, page := range s.blog.Pages {
		pages = append(pages, page.Summary())
	}
	for path, article := range s.articles {
		if article.Enabled {
			paths = append(paths, path)
		}
	}
	return pages, paths
}

// Page returns a copy of a standalone page, or nil if it no longer exists.
func (s *Store) Page(slug string) *BlogPage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if i := pageIndex(s.blog.Pages, slug); i >= 0 {
		page := *s.blog.Pages[i]
		return &page
	}
	return nil
}

func (s *Store) CreatePage(input PageInput) (BlogPage, error) {
	input, err := validatePage(input)
	if err != nil {
		return BlogPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if pageIndex(s.blog.Pages, input.Slug) >= 0 {
		return BlogPage{}, ErrPageExists
	}
	if len(s.blog.Pages) >= PageLimit {
		return BlogPage{}, ErrPageLimit
	}
	now := time.Now().UTC()
	page := BlogPage{input.Slug, input.Title, input.Source, now, now}
	next := cloneBlog(s.blog)
	next.Pages = append(next.Pages, &page)
	if err := s.persist(s.articles, next, s.gitHead); err != nil {
		return BlogPage{}, err
	}
	return page, nil
}

func (s *Store) UpdatePage(original string, input PageInput, revision string) (BlogPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := pageIndex(s.blog.Pages, original)
	if i < 0 {
		return BlogPage{}, fs.ErrNotExist
	}
	previous := s.blog.Pages[i]
	if previous.revision() != revision {
		return BlogPage{}, ErrPageConflict
	}
	input, err := validatePage(input)
	if err != nil {
		return BlogPage{}, err
	}
	if other := pageIndex(s.blog.Pages, input.Slug); other >= 0 && other != i {
		return BlogPage{}, ErrPageExists
	}
	now := time.Now().UTC()
	if !now.After(previous.UpdatedAt) {
		now = previous.UpdatedAt.UTC().Add(time.Nanosecond)
	}
	page := BlogPage{input.Slug, input.Title, input.Source, previous.CreatedAt, now}
	next := cloneBlog(s.blog)
	next.Pages[i] = &page
	if err := s.persist(s.articles, next, s.gitHead); err != nil {
		return BlogPage{}, err
	}
	return page, nil
}

func (s *Store) DeletePage(slug, revision string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := pageIndex(s.blog.Pages, slug)
	if i < 0 {
		return fs.ErrNotExist
	}
	if s.blog.Pages[i].revision() != revision {
		return ErrPageConflict
	}
	next := cloneBlog(s.blog)
	next.Pages = append(next.Pages[:i], next.Pages[i+1:]...)
	return s.persist(s.articles, next, s.gitHead)
}
