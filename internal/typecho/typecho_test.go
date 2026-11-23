package typecho

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

var fixtureNow = time.Unix(1704067300, 0).UTC()

func TestParseMySQLBackup(t *testing.T) {
	raw := "\ufeff" + `-- Typecho backup, UTF-8
/*!40101 SET NAMES utf8mb4 */;
DROP TABLE IF EXISTS legacy_contents;
CREATE TABLE IF NOT EXISTS legacy_contents (
  cid int unsigned NOT NULL,
  ` + "`title`" + ` varchar(200) DEFAULT NULL,
  slug varchar(200),
  created int,
  text longtext,
  type enum('post','page'),
  status varchar(16),
  password varchar(32),
  PRIMARY KEY (cid),
  KEY idx (slug(32))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
# Relationships can precede the rows they reference.
INSERT INTO legacy_relationships VALUES (1,7),(1,8),(1,9),(2,10);
INSERT IGNORE INTO legacy_contents VALUES
  (8,'另一个公开标题','eight','1704067205','<!--markdown-->Another body','post','publish',''),
  (1,'公开标题 # 1','a',1704067200,'<!--markdown-->你好\r\n第二行; /*文字*/ # -- it\'s a \\path, doubled '' apostrophe','post','publish',NULL),
  (2,'PRIVATE_PAGE','page',1704067201,'<!--markdown-->PRIVATE_PAGE_BODY','page','publish',NULL),
  (3,'PRIVATE_DRAFT','draft',1704067201,'<!--markdown-->PRIVATE_DRAFT_BODY','post','draft',NULL),
  (4,'PRIVATE_PROTECTED','pass',1704067201,'<!--markdown-->PRIVATE_PROTECTED_BODY','post','publish','PRIVATE_PASSWORD'),
  (5,'PRIVATE_FUTURE','future',1704067301,'<!--markdown-->PRIVATE_FUTURE_BODY','post','publish',NULL),
  (6,'PRIVATE_HTML','html',1704067201,'<p>PRIVATE_HTML_BODY</p>','post','publish',NULL),
  (7,'PRIVATE_UNMARKED','unmarked',1704067201,'# PRIVATE_UNMARKED_BODY','post','publish',NULL),
  (9,'PRIVATE_BAD_TIME','invalid',0,'<!--markdown-->PRIVATE_BAD_TIME_BODY','post','publish',NULL),
  (10,'','empty-title',1704067201,'<!--markdown-->PRIVATE_INVALID_BODY','post','publish',NULL);
INSERT INTO legacy_metas (type,name,mid) VALUES ('category','分类',7),('tag',' Go ',8),('tag','go',9),('tag','PRIVATE_PAGE_TAG',10);
INSERT INTO ignored_users VALUES (1,'PRIVATE_OTHER_TABLE');`
	got, err := Parse([]byte(raw), fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	want := []Post{
		{ID: 1, Title: "公开标题 # 1", Slug: "a", PublishedAt: time.Unix(1704067200, 0).UTC(), Markdown: "你好\n第二行; /*文字*/ # -- it's a \\path, doubled ' apostrophe", Tags: []string{"Go"}, Categories: []string{"分类"}},
		{ID: 8, Title: "另一个公开标题", Slug: "eight", PublishedAt: time.Unix(1704067205, 0).UTC(), Markdown: "Another body"},
	}
	if !reflect.DeepEqual(got.Posts, want) {
		t.Fatalf("posts: %#v", got.Posts)
	}
	if want := (map[string]int{"nonPost": 1, "nonPublic": 2, "invalidDate": 2, "nonMarkdown": 2, "invalidData": 1}); !reflect.DeepEqual(got.Skipped, want) {
		t.Fatalf("skipped: %+v", got.Skipped)
	}
	serialized, err := json.Marshal(got)
	if err != nil || strings.Contains(string(serialized), "PRIVATE_") {
		t.Fatal("skipped details escaped the parser")
	}
}

func TestParseFallbackSchemaAndQualifiedTable(t *testing.T) {
	raw := "USE `blog`; INSERT INTO `blog`.`contents` VALUES (17,\"标题 \"\"!\"\"\",NULL,1704067300,1704067300,'\ufeff<!--markdown-->正文',0,1,NULL,'post','publish',NULL,0,1,1,1,0);" +
		"INSERT INTO metas VALUES (3,'标签','tag','tag',NULL,1,0,0); INSERT INTO relationships VALUES (17,3);"
	got, err := Parse([]byte(raw), fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Posts) != 1 || got.Posts[0].Title != `标题 "!"` || got.Posts[0].Slug != "" || got.Posts[0].Markdown != "正文" || !got.Posts[0].PublishedAt.Equal(fixtureNow) || !reflect.DeepEqual(got.Posts[0].Tags, []string{"标签"}) {
		t.Fatalf("bad fallback record: %+v", got)
	}
}

const fixtureInsert = "INSERT INTO blog_contents (cid,title,created,text,type,status,password) VALUES "
const fixtureRow = "(1,'PRIVATE_FIXTURE_TITLE',1704067200,'<!--markdown-->PRIVATE_FIXTURE_BODY','post','publish',NULL)"

func TestParseFailsClosed(t *testing.T) {
	for _, tt := range []struct {
		name, raw string
		err       error
	}{
		{"ambiguous prefixes", fixtureInsert + fixtureRow + "; CREATE TABLE other_contents (cid int);", errTable},
		{"ambiguous databases", "USE a; " + fixtureInsert + fixtureRow + "; USE b; " + fixtureInsert + fixtureRow + ";", errTable},
		{"duplicate across statements", fixtureInsert + fixtureRow + "; " + fixtureInsert + fixtureRow + ";", errIdentity},
		{"duplicate even private", fixtureInsert + fixtureRow + ",(1,'foo',1,'body','page','draft',NULL);", errIdentity},
		{"invalid cid", fixtureInsert + strings.Replace(fixtureRow, "(1,", "(0,", 1) + ";", errIdentity},
		{"truncated tuple", fixtureInsert + fixtureRow[:len(fixtureRow)-1] + ";", errSyntax},
		{"truncated later ignored statement", fixtureInsert + fixtureRow + "; INSERT INTO users VALUES ('PRIVATE_UNCLOSED", errSyntax},
		{"unterminated comment", fixtureInsert + fixtureRow + "; /* PRIVATE_comment", errSyntax},
		{"unterminated final statement", fixtureInsert + fixtureRow, errSyntax},
		{"absolute unknown expression", fixtureInsert + strings.Replace(fixtureRow, "1704067200", "NOW()", 1) + ";", errSyntax},
		{"missing visibility column", "INSERT INTO blog_contents (cid,title,created,text,type,status) VALUES (1,'x',1704067200,'<!--markdown-->x','post','publish');", errSyntax},
		{"unexpected tuple columns", fixtureInsert + fixtureRow[:len(fixtureRow)-1] + ",7);", errSyntax},
		{"ambiguous duplicate column", "INSERT INTO blog_contents (cid,title,created,text,type,status,password,PASSWORD) VALUES (1,'x',1,'x','post','publish',NULL,'x');", errSyntax},
		{"backslash mode", "/*!40101 SET SQL_MODE='NO_BACKSLASH_ESCAPES' */; " + fixtureInsert + fixtureRow + ";", errMode},
		{"proxy dynamic update", fixtureInsert + fixtureRow + "; UPDATE blog_contents SET status='private';", errMode},
		{"versioned dynamic update", fixtureInsert + fixtureRow + "; /*!40101 UPDATE blog_contents SET status='private' */;", errMode},
		{"duplicate DDL column", "CREATE TABLE blog_contents (cid int, CID int);", errSyntax},
		{"select instead of values", "INSERT INTO blog_contents SELECT * FROM source_contents;", errSyntax},
		{"bad utf8", fixtureInsert + fixtureRow + ";\xff", errEncoding},
		{"literal NUL", fixtureInsert + fixtureRow + ";\x00", errEncoding},
		{"duplicate taxonomies", fixtureInsert + fixtureRow + "; INSERT INTO blog_metas (mid,name,type) VALUES (1,'x','tag'),(1,'y','tag');", errIdentity},
		{"duplicate links", fixtureInsert + fixtureRow + "; INSERT INTO blog_relationships VALUES (1,2),(1,2);", errIdentity},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.raw), fixtureNow)
			if !errors.Is(err, tt.err) || got.Posts != nil || got.Skipped != nil {
				t.Fatalf("unexpected result: %v / %+v", err, got)
			}
			if strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatal("error includes input text")
			}
		})
	}
}

func TestParseLimitsPublicCandidatesEvenWithoutMarker(t *testing.T) {
	var sql strings.Builder
	sql.WriteString(fixtureInsert)
	for id := 1; id <= maxPublicCandidates+1; id++ {
		if id > 1 {
			sql.WriteByte(',')
		}
		fmt.Fprintf(&sql, "(%d,'title',1704067200,'not-markdown','post','publish',NULL)", id)
	}
	sql.WriteByte(';')
	got, err := Parse([]byte(sql.String()), fixtureNow)
	if !errors.Is(err, errLimit) || got.Posts != nil {
		t.Fatalf("limit not enforced: %v", err)
	}
}
