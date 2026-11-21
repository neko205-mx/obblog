package markdown

import (
	"fmt"
	"html"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/util"
)

// FriendLink describes a public site. All fields are plain strings, not HTML.
// Avatar is empty unless the source explicitly supplied a valid HTTPS image URL.
type FriendLink struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Avatar      string `json:"avatar"`
	Initial     string `json:"initial"`
}

var friendLinkGridKind = ast.NewNodeKind("FriendLinkGrid")

type friendLinkGrid struct {
	ast.BaseBlock
	links []FriendLink
}

func (n *friendLinkGrid) Kind() ast.NodeKind { return friendLinkGridKind }
func (n *friendLinkGrid) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// Only entire top-level blocks are eligible. In particular, an unordered list
// with any regular item is preserved intact rather than moving part of it.
func prepareFriendLinkGrids(doc ast.Node, source []byte) []FriendLink {
	var links []FriendLink
	var grid *friendLinkGrid
	for n := doc.FirstChild(); n != nil; {
		next := n.NextSibling()
		entries := friendLinksInBlock(n, source)
		if len(entries) == 0 {
			grid = nil
		} else {
			if grid == nil {
				grid = &friendLinkGrid{}
				doc.InsertBefore(doc, n, grid)
			}
			grid.links = append(grid.links, entries...)
			links = append(links, entries...)
			doc.RemoveChild(doc, n)
		}
		n = next
	}
	return links
}

func friendLinksInBlock(n ast.Node, source []byte) []FriendLink {
	switch n := n.(type) {
	case *ast.Paragraph:
		if link, ok := readFriendLink(n, source); ok {
			return []FriendLink{link}
		}
	case *ast.List:
		if n.IsOrdered() {
			return nil
		}
		var entries []FriendLink
		for item := n.FirstChild(); item != nil; item = item.NextSibling() {
			body := item.FirstChild()
			if body == nil || body.NextSibling() != nil || (body.Kind() != ast.KindParagraph && body.Kind() != ast.KindTextBlock) {
				return nil
			}
			entry, ok := readFriendLink(body, source)
			if !ok {
				return nil
			}
			entries = append(entries, entry)
		}
		return entries
	}
	return nil
}

func readFriendLink(body ast.Node, source []byte) (FriendLink, bool) {
	link, ok := body.FirstChild().(*ast.Link)
	if !ok {
		return FriendLink{}, false
	}
	href := friendWebURL(link.Destination, false)
	var label strings.Builder
	for n := link.FirstChild(); n != nil; n = n.NextSibling() {
		if !writeFriendText(&label, n, source) {
			return FriendLink{}, false
		}
	}
	title := strings.Join(strings.Fields(label.String()), " ")
	if title == "" || href == "" {
		return FriendLink{}, false
	}
	var description strings.Builder
	var avatar string
	seenAvatar := false
	for n := link.NextSibling(); n != nil; n = n.NextSibling() {
		if image, ok := n.(*ast.Image); ok {
			if seenAvatar {
				return FriendLink{}, false
			}
			seenAvatar = true
			avatar = friendWebURL(image.Destination, true)
			continue
		}
		if seenAvatar {
			text, ok := n.(*ast.Text)
			if !ok || strings.TrimSpace(string(text.Value(source))) != "" {
				return FriendLink{}, false
			}
			continue
		}
		if !writeFriendText(&description, n, source) {
			return FriendLink{}, false
		}
	}
	desc := strings.TrimSpace(description.String())
	if desc != "" {
		// Require an explicit separator to avoid converting ordinary prose
		// that happens to start with a link. Typographer also converts -- to –.
		rest, found := strings.CutPrefix(desc, "—")
		if !found {
			rest, found = strings.CutPrefix(desc, "–")
		}
		if !found || rest == "" {
			return FriendLink{}, false
		}
		desc = rest
	} else {
		desc = string(friendMarkdownValue(link.Title))
	}
	initial := "?"
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			initial = string(unicode.ToUpper(r))
			break
		}
	}
	return FriendLink{Title: title, URL: href, Description: strings.Join(strings.Fields(desc), " "), Avatar: avatar, Initial: initial}, true
}

func friendMarkdownValue(raw []byte) []byte {
	return util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(raw)))
}

// Flatten simple formatting, not raw HTML, images, secondary links or math.
func writeFriendText(b *strings.Builder, n ast.Node, source []byte) bool {
	switch n := n.(type) {
	case *ast.Text:
		b.Write(friendMarkdownValue(n.Value(source)))
		if n.HardLineBreak() || n.SoftLineBreak() {
			b.WriteByte(' ')
		}
	case *ast.String:
		b.Write(friendMarkdownValue(n.Value))
	case *ast.Emphasis, *extast.Strikethrough:
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			if !writeFriendText(b, child, source) {
				return false
			}
		}
	default:
		return false
	}
	return true
}

func friendWebURL(raw []byte, avatar bool) string {
	value := string(friendMarkdownValue(raw))
	if strings.Contains(value, `\`) || strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || u.Opaque != "" || u.User != nil || u.Hostname() == "" {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "https" && (avatar || u.Scheme != "http") {
		return ""
	}
	if strings.Contains(u.Path, `\`) || strings.ContainsFunc(u.Path, unicode.IsControl) {
		return ""
	}
	// Reject private app endpoints even when written as absolute URLs. Clean
	// decoded paths to also cover escapes and redundant separators.
	clean := strings.ToLower(path.Clean(u.Path))
	if clean == "/admin/api" || strings.HasPrefix(clean, "/admin/api/") || clean == "/api" || strings.HasPrefix(clean, "/api/") {
		return ""
	}
	return u.String()
}

func renderFriendLinkGrid(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	var b strings.Builder
	b.WriteString("<div class=\"friend-link-grid\">\n")
	for _, entry := range n.(*friendLinkGrid).links {
		fmt.Fprintf(&b, `<a class="friend-link-card" href="%s" target="_blank" rel="noopener noreferrer"><span class="friend-link-avatar" aria-hidden="true"><span class="friend-link-initial">%s</span>`, html.EscapeString(entry.URL), html.EscapeString(entry.Initial))
		if entry.Avatar != "" {
			fmt.Fprintf(&b, `<img class="friend-link-image" src="%s" alt="" loading="lazy" decoding="async" referrerpolicy="no-referrer">`, html.EscapeString(entry.Avatar))
		}
		fmt.Fprintf(&b, `</span><span class="friend-link-content"><span class="friend-link-title">%s</span>`, html.EscapeString(entry.Title))
		if entry.Description != "" {
			fmt.Fprintf(&b, `<span class="friend-link-description">%s</span>`, html.EscapeString(entry.Description))
		}
		b.WriteString("</span></a>\n")
	}
	b.WriteString("</div>\n")
	_, err := w.WriteString(b.String())
	return ast.WalkContinue, err
}
