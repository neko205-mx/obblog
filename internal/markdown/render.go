package markdown

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"obblog/internal/vault"
)

type Heading struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Level int    `json:"level"`
}

type Result struct {
	HTML       string    `json:"html"`
	Headings   []Heading `json:"headings"`
	Properties string    `json:"properties"`
	Words      int       `json:"words"`
	Excerpt    string    `json:"excerpt"`
}

func splitFrontmatter(source []byte) ([]byte, string) {
	if !bytes.HasPrefix(source, []byte("---\n")) {
		return source, ""
	}
	lines := bytes.SplitAfter(source, []byte("\n"))
	offset := len(lines[0])
	for _, line := range lines[1:] {
		if strings.TrimSpace(string(line)) == "---" {
			return source[offset+len(line):], strings.TrimSpace(string(source[len(lines[0]):offset]))
		}
		offset += len(line)
	}
	return source, ""
}

func Slug(value string) string {
	var b strings.Builder
	separator := false
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' {
			if separator && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			separator = false
		} else if unicode.IsSpace(r) {
			separator = true
		}
	}
	if b.Len() == 0 {
		return "section"
	}
	return b.String()
}

func NoteURL(name, fragment string) string {
	u := "/admin/?" + url.Values{"note": {name}}.Encode()
	if fragment != "" {
		u += "#" + url.PathEscape(Slug(fragment))
	}
	return u
}

func AssetURL(name string) string {
	return "/admin/api/asset?" + url.Values{"path": {name}}.Encode()
}

func Render(raw []byte, from string, snapshot *vault.Snapshot) (*Result, error) {
	return RenderWithOptions(raw, from, snapshot, RenderOptions{})
}

// RenderOptions lets published copies resolve links without accessing the live vault.
type RenderOptions struct {
	Resolve  func(from, target string, wiki bool) (*vault.Entry, error)
	NoteURL  func(name, fragment string) string
	AssetURL func(name string) string
}

func RenderWithOptions(raw []byte, from string, snapshot *vault.Snapshot, options RenderOptions) (*Result, error) {
	source, properties := splitFrontmatter(normalize(raw))
	source = maskComments(source)
	r := &noteRenderer{from: from, snapshot: snapshot, options: options}
	md := newMarkdown(
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(r, 100))),
	)
	doc := md.Parser().Parse(text.NewReader(source))
	result := &Result{Properties: properties, Headings: []Heading{}}
	plainParts := []string{}
	ids := map[string]bool{}
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if _, paragraph := n.(*ast.Paragraph); paragraph && len(plainParts) == 0 {
			if value := strings.TrimSpace(string(n.Text(source))); value != "" {
				plainParts = append(plainParts, value)
			}
		}
		switch node := n.(type) {
		case *ast.Heading:
			label := string(node.Text(source))
			base := Slug(label)
			id := base
			for suffix := 2; ids[id]; suffix++ {
				id = base + "-" + strconv.Itoa(suffix)
			}
			ids[id] = true
			node.SetAttributeString("id", []byte(id))
			result.Headings = append(result.Headings, Heading{ID: id, Text: label, Level: node.Level})
		case *ast.Link:
			node.Destination = r.rewrite(node.Destination, false)
		case *ast.Image:
			node.Destination = r.rewrite(node.Destination, true)
		case *ast.Blockquote:
			prepareCallout(node, source)
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := md.Renderer().Render(&out, source, doc); err != nil {
		return nil, err
	}
	result.HTML = out.String()
	plain := []rune(strings.Join(strings.Fields(strings.Join(plainParts, " ")), " "))
	if len(plain) > 140 {
		plain = append(plain[:140], '…')
	}
	result.Excerpt = string(plain)
	// Count CJK characters individually, and other languages by whitespace-delimited words.
	inWord := false
	for _, c := range string(source) {
		if unicode.Is(unicode.Han, c) || unicode.Is(unicode.Hiragana, c) || unicode.Is(unicode.Katakana, c) {
			result.Words++
			inWord = false
		} else if unicode.IsLetter(c) || unicode.IsNumber(c) {
			if !inWord {
				result.Words++
			}
			inWord = true
		} else {
			inWord = false
		}
	}
	return result, nil
}

func newMarkdown(options ...goldmark.Option) goldmark.Markdown {
	defaults := []goldmark.Option{
		goldmark.WithExtensions(extension.GFM, extension.Footnote, extension.Typographer),
		goldmark.WithParserOptions(parser.WithInlineParsers(util.Prioritized(&wikiParser{}, 150))),
	}
	return goldmark.New(append(defaults, options...)...)
}

func normalize(raw []byte) []byte {
	return bytes.TrimPrefix(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")), []byte{0xef, 0xbb, 0xbf})
}

var wikiKind = ast.NewNodeKind("WikiLink")

type wikiNode struct {
	ast.BaseInline
	target string
	label  string
	embed  bool
}

func (n *wikiNode) Kind() ast.NodeKind { return wikiKind }
func (n *wikiNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"target": n.target}, nil)
}
func (n *wikiNode) Text(source []byte) []byte { return []byte(n.label) }

type wikiParser struct{}

func (*wikiParser) Trigger() []byte { return []byte{'[', '!'} }
func (*wikiParser) Parse(parent ast.Node, reader text.Reader, pc parser.Context) ast.Node {
	line, _ := reader.PeekLine()
	embed := bytes.HasPrefix(line, []byte("![["))
	start := 2
	if embed {
		start = 3
	} else if !bytes.HasPrefix(line, []byte("[[")) {
		return nil
	}
	end := bytes.Index(line[start:], []byte("]]"))
	if end < 0 {
		return nil
	}
	content := strings.ReplaceAll(string(line[start:start+end]), `\|`, "|")
	target, label, hasLabel := strings.Cut(content, "|")
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	if !hasLabel {
		label = strings.TrimSuffix(path.Base(target), ".md")
	}
	reader.Advance(start + end + 2)
	return &wikiNode{target: target, label: label, embed: embed}
}

type noteRenderer struct {
	from     string
	snapshot *vault.Snapshot
	options  RenderOptions
}

func (r *noteRenderer) resolve(target string, wiki bool) (*vault.Entry, error) {
	if r.options.Resolve != nil {
		return r.options.Resolve(r.from, target, wiki)
	}
	if wiki {
		return r.snapshot.ResolveWiki(r.from, target)
	}
	return r.snapshot.Resolve(r.from, target)
}

func (r *noteRenderer) noteURL(name, fragment string) string {
	if r.options.NoteURL != nil {
		return r.options.NoteURL(name, fragment)
	}
	return NoteURL(name, fragment)
}

func (r *noteRenderer) assetURL(name string) string {
	if r.options.AssetURL != nil {
		return r.options.AssetURL(name)
	}
	return AssetURL(name)
}

func (r *noteRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(wikiKind, r.renderWiki)
	reg.Register(ast.KindBlockquote, r.renderBlockquote)
}

func (r *noteRenderer) rewrite(destination []byte, image bool) []byte {
	u, err := url.Parse(string(destination))
	if err != nil || u.IsAbs() || u.Host != "" {
		return destination
	}
	// Root-relative links in public Markdown must not bypass the copy resolver.
	if r.options.Resolve != nil && strings.HasPrefix(u.Path, "/") {
		return []byte("#unresolved-link")
	}
	if u.Path == "" {
		if u.Fragment != "" {
			return []byte("#" + url.PathEscape(Slug(u.Fragment)))
		}
		return destination
	}
	target, err := r.resolve(u.Path, false)
	if err != nil {
		if image {
			if r.options.AssetURL != nil {
				return []byte("/media/missing")
			}
			return []byte("/admin/api/asset?path=__missing__")
		}
		return []byte("#unresolved-link")
	}
	if target.Kind == "note" && !image {
		href := r.noteURL(target.Path, u.Fragment)
		if href == "" {
			href = "#unresolved-link"
		}
		return []byte(href)
	}
	if target.Kind != "asset" {
		return []byte("#unresolved-link")
	}
	return []byte(r.assetURL(target.Path))
}

func (r *noteRenderer) renderWiki(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*wikiNode)
	name, fragment, _ := strings.Cut(n.target, "#")
	e, err := r.resolve(name, true)
	if err != nil {
		title := "笔记不存在"
		if err == vault.ErrAmbiguous {
			title = "存在同名文件，请使用完整路径"
		}
		_, err = fmt.Fprintf(w, `<span class="unresolved-link" title="%s">%s</span>`, title, html.EscapeString(n.label))
		return ast.WalkSkipChildren, err
	}
	if n.embed && e.Kind == "asset" {
		ext := strings.ToLower(path.Ext(e.Path))
		if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" || ext == ".svg" || ext == ".avif" {
			width := ""
			if v, err := strconv.Atoi(strings.Split(n.label, "x")[0]); err == nil && v > 0 && v <= 4096 {
				width = fmt.Sprintf(` width="%d"`, v)
			}
			_, err := fmt.Fprintf(w, `<img src="%s" alt="%s" loading="lazy"%s>`, html.EscapeString(r.assetURL(e.Path)), html.EscapeString(e.Name), width)
			return ast.WalkSkipChildren, err
		}
	}
	href := r.assetURL(e.Path)
	class := "internal-link"
	if e.Kind == "note" {
		href = r.noteURL(e.Path, fragment)
		if n.embed {
			class += " embedded-note"
		}
	}
	if href == "" {
		_, err := fmt.Fprintf(w, `<span class="unresolved-link">%s</span>`, html.EscapeString(n.label))
		return ast.WalkSkipChildren, err
	}
	_, err = fmt.Fprintf(w, `<a class="%s" href="%s">%s</a>`, class, html.EscapeString(href), html.EscapeString(n.label))
	return ast.WalkSkipChildren, err
}

var calloutPattern = regexp.MustCompile(`^\[!([A-Za-z-]+)\][+-]?(?:[ \t]+([^\n]*))?`)

func prepareCallout(n *ast.Blockquote, source []byte) {
	p, ok := n.FirstChild().(*ast.Paragraph)
	if !ok {
		return
	}
	if p.Lines().Len() == 0 {
		return
	}
	line := p.Lines().At(0)
	match := calloutPattern.FindSubmatch(line.Value(source))
	if match == nil {
		return
	}
	kind := strings.ToLower(string(match[1]))
	title := string(match[2])
	if title == "" {
		title = strings.ToUpper(kind[:1]) + kind[1:]
	}
	n.SetAttributeString("callout-kind", kind)
	n.SetAttributeString("callout-title", title)
	cutoff := line.Start + len(match[0])
	for child := p.FirstChild(); child != nil; {
		if child.Pos() >= cutoff {
			break
		}
		if t, ok := child.(*ast.Text); ok && t.Segment.Stop > cutoff {
			t.Segment.Start = cutoff
			break
		}
		next := child.NextSibling()
		p.RemoveChild(p, child)
		child = next
	}
}

func (*noteRenderer) renderBlockquote(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	kind, callout := node.AttributeString("callout-kind")
	if !callout {
		if entering {
			_, err := w.WriteString("<blockquote>\n")
			return ast.WalkContinue, err
		}
		_, err := w.WriteString("</blockquote>\n")
		return ast.WalkContinue, err
	}
	if entering {
		title, _ := node.AttributeString("callout-title")
		_, err := fmt.Fprintf(w, `<aside class="callout callout-%s"><div class="callout-title">%s</div>`, html.EscapeString(kind.(string)), html.EscapeString(title.(string)))
		return ast.WalkContinue, err
	}
	_, err := w.WriteString("</aside>\n")
	return ast.WalkContinue, err
}
