package web

import (
	"cmp"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"obblog/internal/publication"
)

const archivePath = "/archives"

// Keep archive year boundaries and visible post dates in the same time zone.
var blogTimeZone = time.FixedZone("CST", 8*60*60)

type archiveTerm struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Count    int    `json:"count,omitempty"`
	Selected bool   `json:"selected,omitempty"`
}

type archiveStats struct {
	Categories int `json:"categories"`
	Posts      int `json:"posts"`
	Tags       int `json:"tags"`
	Words      int `json:"words"`
}

type archiveYear struct {
	Year  string
	Open  bool
	Posts []themePost
}

type archiveData struct {
	Title                string
	Stats                archiveStats
	Tags, Categories     []archiveTerm
	Years                []archiveYear
	Query, Tag, Category string
	Matches              int
}

type archiveFilter struct {
	Query    string `json:"q"`
	Tag      string `json:"tag"`
	Category string `json:"category"`
}

func (f archiveFilter) url() string {
	values := url.Values{}
	if f.Query != "" {
		values.Set("q", f.Query)
	}
	if f.Tag != "" {
		values.Set("tag", f.Tag)
	}
	if f.Category != "" {
		values.Set("category", f.Category)
	}
	if len(values) == 0 {
		return archivePath
	}
	return archivePath + "?" + values.Encode()
}

func readArchiveFilter(r *http.Request) (archiveFilter, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return archiveFilter{}, false
	}
	f := archiveFilter{Query: strings.TrimSpace(values.Get("q")), Tag: strings.TrimSpace(values.Get("tag")), Category: strings.TrimSpace(values.Get("category"))}
	return f, len(f.Query) <= 512 && len(f.Tag) <= 256 && len(f.Category) <= 256
}

func termKey(s string) string { return strings.ToLower(s) }

func articleTerms(values []string, category bool) []archiveTerm {
	list := make([]archiveTerm, 0, len(values))
	for _, name := range values {
		f := archiveFilter{Tag: name}
		if category {
			f = archiveFilter{Category: name}
		}
		list = append(list, archiveTerm{Name: name, URL: f.url()})
	}
	return list
}

func addTerms(index map[string]archiveTerm, terms []string) {
	seen := map[string]bool{}
	for _, name := range terms {
		key := termKey(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		term := index[key]
		if term.Count == 0 {
			term.Name = name
		}
		term.Count++
		index[key] = term
	}
}

func archiveTerms(index map[string]archiveTerm, f archiveFilter, category bool) []archiveTerm {
	result := make([]archiveTerm, 0, len(index))
	for key, item := range index {
		next := f
		if category {
			item.Selected = key == termKey(f.Category)
			next.Category = item.Name
			if item.Selected {
				next.Category = ""
			}
		} else {
			item.Selected = key == termKey(f.Tag)
			next.Tag = item.Name
			if item.Selected {
				next.Tag = ""
			}
		}
		item.URL = next.url()
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b archiveTerm) int {
		if a.Count != b.Count {
			return cmp.Compare(b.Count, a.Count)
		}
		return strings.Compare(termKey(a.Name), termKey(b.Name))
	})
	return result
}

func buildArchive(articles map[string]*publication.Article, f archiveFilter) (archiveData, []themePost) {
	tags, categories := map[string]archiveTerm{}, map[string]archiveTerm{}
	data := archiveData{Title: "归档", Query: f.Query, Tag: f.Tag, Category: f.Category}
	matches := []themePost{}
	needle, tag, category := termKey(f.Query), termKey(f.Tag), termKey(f.Category)
	hasTerm := func(terms []string, target string) bool {
		return target == "" || slices.ContainsFunc(terms, func(term string) bool { return termKey(term) == target })
	}
	for _, article := range publication.Published(articles) {
		data.Stats.Posts++
		data.Stats.Words += article.Words
		addTerms(tags, article.Tags)
		addTerms(categories, article.Categories)
		if !hasTerm(article.Tags, tag) || !hasTerm(article.Categories, category) {
			continue
		}
		contains := func(term string) bool { return strings.Contains(termKey(term), needle) }
		if !contains(article.Title) && !slices.ContainsFunc(article.Tags, contains) && !slices.ContainsFunc(article.Categories, contains) {
			continue
		}
		matches = append(matches, postThemeSummary(article, nil))
	}
	data.Stats.Tags, data.Stats.Categories = len(tags), len(categories)
	data.Tags, data.Categories = archiveTerms(tags, f, false), archiveTerms(categories, f, true)
	data.Matches = len(matches)
	return data, matches
}

func (a *App) publicArchive(w http.ResponseWriter, r *http.Request) {
	f, ok := readArchiveFilter(r)
	if !ok {
		fail(w, http.StatusBadRequest, "无效的归档筛选条件")
		return
	}
	a.renderArchive(w, f, nil)
}

func (a *App) renderArchive(w http.ResponseWriter, f archiveFilter, preview *publicTheme) {
	archive, posts := buildArchive(a.publicSnapshot(), f)
	filtered := f != (archiveFilter{})
	for _, post := range posts {
		year := post.PublishedAt.In(blogTimeZone).Format("2006")
		if len(archive.Years) == 0 || archive.Years[len(archive.Years)-1].Year != year {
			archive.Years = append(archive.Years, archiveYear{Year: year, Open: len(archive.Years) == 0 || filtered})
		}
		group := &archive.Years[len(archive.Years)-1]
		group.Posts = append(group.Posts, post)
	}
	data := newThemeData(a.publicBlog())
	data.Archive = &archive
	a.renderThemedPage(w, "archive", data, preview)
}

// publicPosts exposes a public listing, not the persisted Article records.
func (a *App) publicPosts(w http.ResponseWriter, r *http.Request) {
	f, ok := readArchiveFilter(r)
	if !ok {
		fail(w, http.StatusBadRequest, "无效的文章筛选条件")
		return
	}
	const maxPage = 1_000_000
	positive := func(key string, fallback, maximum int) (int, bool) {
		value := r.URL.Query().Get(key)
		if value == "" {
			return fallback, true
		}
		n, err := strconv.Atoi(value)
		return n, err == nil && n > 0 && n <= maximum
	}
	page, validPage := positive("page", 1, maxPage)
	limit, validLimit := positive("limit", 20, 100)
	if !validPage || !validLimit {
		fail(w, http.StatusBadRequest, "无效的分页参数")
		return
	}
	archive, posts := buildArchive(a.publicSnapshot(), f)
	start := min(len(posts), (page-1)*limit)
	respond(w, struct {
		Filters    archiveFilter `json:"filters"`
		Stats      archiveStats  `json:"stats"`
		Tags       []archiveTerm `json:"tags"`
		Categories []archiveTerm `json:"categories"`
		Total      int           `json:"total"`
		Page       int           `json:"page"`
		Limit      int           `json:"limit"`
		Posts      []themePost   `json:"posts"`
	}{f, archive.Stats, archive.Tags, archive.Categories, len(posts), page, limit, posts[start:min(start+limit, len(posts))]})
}
