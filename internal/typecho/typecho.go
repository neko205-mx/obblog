// Package typecho extracts public Markdown posts from a literal Typecho MySQL
// backup. It never connects to a database or executes SQL.
package typecho

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxPublicCandidates = 1000
	maxTables           = 4096
	maxContentsRows     = 200000
	maxMetaRows         = 100000
	maxRelationships    = 300000
	markdownMarker      = "<!--markdown-->"
	maxUnixTime         = 253402300799 // 9999-12-31T23:59:59Z
)

var (
	errEncoding = errors.New("Typecho 备份必须是 UTF-8 的 MySQL SQL 文件")
	errTable    = errors.New("无法唯一确定 Typecho contents 表")
	errIdentity = errors.New("Typecho 备份含无效或重复的记录标识")
	errLimit    = errors.New("Typecho 备份记录过多，请分批导入（每批最多 1000 篇公开候选文章）")
)

// Backup contains eligible posts (sorted by Typecho ID) and counts of skipped
// contents rows. Skipped records never contribute titles, bodies, or taxonomy.
type Backup struct {
	Posts   []Post
	Skipped map[string]int
}

// Post is the retained subset of one public Typecho contents row.
type Post struct {
	ID                    int64
	Title, Slug, Markdown string
	PublishedAt           time.Time
	Tags, Categories      []string
}

var (
	contentsSchema = []string{"cid", "title", "slug", "created", "modified", "text", "order", "authorid", "template", "type", "status", "password", "commentsnum", "allowcomment", "allowping", "allowfeed", "parent"}
	metaSchema     = []string{"mid", "name", "slug", "type", "description", "count", "order", "parent"}
	relationSchema = []string{"cid", "mid"}
	contentsFields = []string{"cid", "title", "created", "text", "type", "status", "password"}
	metaFields     = []string{"mid", "name", "type"}
	relationFields = []string{"cid", "mid"}
)

type tableDefinition struct {
	ddl       []byte
	duplicate bool
}

// Parse reads MySQL UTF-8 backup statements delimited by semicolons. Unknown
// tables are not imported; Typecho taxonomy tables are optional. Invalid dump
// structure, ambiguous table prefixes and duplicate IDs yield a zero Backup.
// Individual non-eligible articles are counted without exposing their data.
func Parse(raw []byte, now time.Time) (Backup, error) {
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return Backup{}, errEncoding
	}
	raw = bytes.TrimPrefix(raw, []byte("\uFEFF"))
	tables := map[tableID]tableDefinition{}
	if err := walkStatements(raw, func(stmt statement) error {
		if !strings.HasSuffix(stmt.table.name, "contents") && !strings.HasSuffix(stmt.table.name, "metas") && !strings.HasSuffix(stmt.table.name, "relationships") {
			return nil
		}
		definition, found := tables[stmt.table]
		if !found && len(tables) >= maxTables {
			return errLimit
		}
		if stmt.create {
			definition.duplicate = definition.ddl != nil
			definition.ddl = stmt.tail
		}
		tables[stmt.table] = definition
		return nil
	}); err != nil {
		return Backup{}, err
	}
	var contents tableID
	for table := range tables {
		if strings.HasSuffix(table.name, "contents") {
			if contents.name != "" {
				return Backup{}, errTable
			}
			contents = table
		}
	}
	if contents.name == "" {
		return Backup{}, errTable
	}
	prefix := strings.TrimSuffix(contents.name, "contents")
	metas, relationships := tableID{contents.database, prefix + "metas"}, tableID{contents.database, prefix + "relationships"}
	columns := func(id tableID, schema []string) ([]string, error) {
		definition := tables[id]
		if definition.duplicate {
			return nil, errIdentity
		}
		if definition.ddl != nil {
			return createColumns(definition.ddl)
		}
		return schema, nil
	}
	cc, err := columns(contents, contentsSchema)
	if err != nil {
		return Backup{}, err
	}
	mc, err := columns(metas, metaSchema)
	if err != nil {
		return Backup{}, err
	}
	rc, err := columns(relationships, relationSchema)
	if err != nil {
		return Backup{}, err
	}

	out := Backup{Posts: []Post{}, Skipped: map[string]int{}}
	ids := map[int64]bool{}
	metaByID := map[int64]taxonomy{}
	candidates := 0
	if err := walkStatements(raw, func(stmt statement) error {
		if stmt.create {
			return nil
		}
		switch stmt.table {
		case contents:
			return walkRows(stmt.tail, cc, contentsFields, func(row []literal, fields map[string]int) error {
				if len(ids) >= maxContentsRows {
					return errLimit
				}
				id, ok := row[fields["cid"]].integer()
				if !ok || id <= 0 || ids[id] {
					return errIdentity
				}
				ids[id] = true
				p, skipped, candidate := parsePost(id, row, fields, now)
				if candidate {
					candidates++
					if candidates > maxPublicCandidates {
						return errLimit
					}
				}
				if skipped != "" {
					out.Skipped[skipped]++
				} else {
					out.Posts = append(out.Posts, p)
				}
				return nil
			})
		case metas:
			return walkRows(stmt.tail, mc, metaFields, func(row []literal, fields map[string]int) error {
				if len(metaByID) >= maxMetaRows {
					return errLimit
				}
				id, ok := row[fields["mid"]].integer()
				if _, found := metaByID[id]; !ok || id <= 0 || found {
					return errIdentity
				}
				typ, _ := row[fields["type"]].text()
				name, _ := row[fields["name"]].text()
				name = strings.TrimSpace(name)
				if len(name) > 256 || !cleanField(name) {
					name = ""
				}
				metaByID[id] = taxonomy{typ, name}
				return nil
			})
		}
		return nil
	}); err != nil {
		return Backup{}, err
	}

	if _, found := tables[relationships]; found {
		posts := make(map[int64]int, len(out.Posts))
		for i, p := range out.Posts {
			posts[p.ID] = i
		}
		links := map[[2]int64]bool{}
		if err := walkStatements(raw, func(stmt statement) error {
			if stmt.create || stmt.table != relationships {
				return nil
			}
			return walkRows(stmt.tail, rc, relationFields, func(row []literal, fields map[string]int) error {
				cid, cidOK := row[fields["cid"]].integer()
				mid, midOK := row[fields["mid"]].integer()
				key := [2]int64{cid, mid}
				if !cidOK || !midOK || cid <= 0 || mid <= 0 || links[key] {
					return errIdentity
				}
				if len(links) >= maxRelationships {
					return errLimit
				}
				links[key] = true
				i, found := posts[cid]
				meta := metaByID[mid]
				if !found || meta.name == "" {
					return nil
				}
				var list *[]string
				switch meta.kind {
				case "tag":
					list = &out.Posts[i].Tags
				case "category":
					list = &out.Posts[i].Categories
				default:
					return nil
				}
				// Keep one name per case-insensitive label, as the public archive does.
				for _, name := range *list {
					if strings.EqualFold(name, meta.name) {
						return nil
					}
				}
				if len(*list) >= 64 {
					return errLimit
				}
				*list = append(*list, meta.name)
				return nil
			})
		}); err != nil {
			return Backup{}, err
		}
	}
	for i := range out.Posts {
		sort.Strings(out.Posts[i].Tags)
		sort.Strings(out.Posts[i].Categories)
	}
	sort.Slice(out.Posts, func(i, j int) bool { return out.Posts[i].ID < out.Posts[j].ID })
	return out, nil
}

type taxonomy struct{ kind, name string }

func cleanField(s string) bool { return !strings.ContainsFunc(s, unicode.IsControl) }

func parsePost(id int64, row []literal, fields map[string]int, now time.Time) (post Post, skipped string, candidate bool) {
	typ, _ := row[fields["type"]].text()
	if typ != "post" {
		return Post{}, "nonPost", false
	}
	status, _ := row[fields["status"]].text()
	if status != "publish" || !row[fields["password"]].absent() {
		return Post{}, "nonPublic", false
	}
	stamp, ok := row[fields["created"]].integer()
	if !ok || stamp <= 0 || stamp > maxUnixTime || stamp > now.Unix() {
		return Post{}, "invalidDate", false
	}
	body, ok := row[fields["text"]].text()
	body = strings.TrimPrefix(body, "\uFEFF")
	if !ok || !strings.HasPrefix(body, markdownMarker) {
		return Post{}, "nonMarkdown", true
	}
	body = strings.TrimPrefix(body, markdownMarker)
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	title, _ := row[fields["title"]].text()
	title = strings.TrimSpace(title)
	var slug string
	if i, found := fields["slug"]; found && !row[i].absent() {
		slug, ok = row[i].text()
		if !ok {
			return Post{}, "invalidData", true
		}
	}
	if title == "" || !cleanField(title) || !cleanField(slug) || strings.TrimSpace(body) == "" || strings.IndexByte(body, 0) >= 0 {
		return Post{}, "invalidData", true
	}
	return Post{ID: id, Title: title, Slug: slug, Markdown: body, PublishedAt: time.Unix(stamp, 0).UTC()}, "", true
}
