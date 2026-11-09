package markdown

import (
	"bytes"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

type Reference struct {
	Target string
	Wiki   bool
	Embed  bool
}

type Metadata struct {
	Links       []Reference
	Tags        []string
	InvalidYAML bool
}

// Mask comments only outside code and raw HTML, retaining source offsets/newlines.
func maskComments(source []byte) []byte {
	if !bytes.Contains(source, []byte("%%")) {
		return source
	}
	protected := make([]bool, len(source))
	protect := func(start, stop int) {
		for i := max(0, start); i < min(len(protected), stop); i++ {
			protected[i] = true
		}
	}
	doc := newMarkdown().Parser().Parse(text.NewReader(source))
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.CodeSpan:
			for child := node.FirstChild(); child != nil; child = child.NextSibling() {
				if t, ok := child.(*ast.Text); ok {
					protect(t.Segment.Start, t.Segment.Stop)
				}
			}
		case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.HTMLBlock:
			for i := 0; i < n.Lines().Len(); i++ {
				segment := n.Lines().At(i)
				protect(segment.Start, segment.Stop)
			}
			if block, ok := node.(*ast.HTMLBlock); ok && block.HasClosure() {
				protect(block.ClosureLine.Start, block.ClosureLine.Stop)
			}
		case *ast.RawHTML:
			for i := 0; i < node.Segments.Len(); i++ {
				segment := node.Segments.At(i)
				protect(segment.Start, segment.Stop)
			}
		}
		return ast.WalkContinue, nil
	})
	out := bytes.Clone(source)
	inComment := false
	for i := 0; i < len(source); i++ {
		if !inComment && protected[i] {
			continue
		}
		if source[i] == '%' && i+1 < len(source) && source[i+1] == '%' {
			inComment = !inComment
			out[i], out[i+1] = ' ', ' '
			i++
		} else if inComment && source[i] != '\n' && source[i] != '\r' {
			out[i] = ' '
		}
	}
	return out
}

var tagPattern = regexp.MustCompile(`(?:^|[\s(（])#([\p{L}\p{N}_/-]+)`)

func Extract(raw []byte) Metadata {
	return extract(raw, true)
}

// ExtractBody excludes YAML properties from public attachment/link discovery.
func ExtractBody(raw []byte) Metadata {
	return extract(raw, false)
}

func extract(raw []byte, includeProperties bool) Metadata {
	body, properties := splitFrontmatter(normalize(raw))
	body = maskComments(body)
	result := Metadata{Links: []Reference{}, Tags: []string{}}
	tags := map[string]bool{}
	addTag := func(tag string) {
		tag = strings.TrimPrefix(strings.TrimSpace(tag), "#")
		if tag == "" || strings.HasPrefix(tag, "/") || strings.HasSuffix(tag, "/") || strings.Contains(tag, "//") {
			return
		}
		nonDigit := false
		for _, c := range tag {
			if !unicode.IsLetter(c) && !unicode.IsNumber(c) && c != '_' && c != '-' && c != '/' {
				return
			}
			if !unicode.IsNumber(c) {
				nonDigit = true
			}
		}
		if nonDigit {
			tags[tag] = true
		}
	}
	var collect func([]byte, bool)
	collect = func(source []byte, frontmatter bool) {
		doc := newMarkdown().Parser().Parse(text.NewReader(source))
		ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			switch node := n.(type) {
			case *ast.CodeSpan, *ast.CodeBlock, *ast.FencedCodeBlock, *ast.HTMLBlock, *ast.RawHTML:
				return ast.WalkSkipChildren, nil
			case *wikiNode:
				target, _, _ := strings.Cut(node.target, "#")
				if target != "" {
					result.Links = append(result.Links, Reference{Target: target, Wiki: true, Embed: node.embed})
				}
				return ast.WalkSkipChildren, nil
			case *ast.Link:
				if !frontmatter {
					appendMarkdownReference(&result, node.Destination, false)
				}
				return ast.WalkSkipChildren, nil
			case *ast.Image:
				if !frontmatter {
					appendMarkdownReference(&result, node.Destination, true)
				}
				return ast.WalkSkipChildren, nil
			case *ast.Text:
				if !frontmatter {
					for _, match := range tagPattern.FindAllStringSubmatch(string(node.Value(source)), -1) {
						addTag(match[1])
					}
				}
			}
			return ast.WalkContinue, nil
		})
	}
	collect(body, false)
	if includeProperties && properties != "" {
		var root yaml.Node
		if err := yaml.Unmarshal([]byte(properties), &root); err != nil {
			result.InvalidYAML = true
		} else {
			var visit func(*yaml.Node, string)
			visit = func(n *yaml.Node, key string) {
				switch n.Kind {
				case yaml.DocumentNode, yaml.SequenceNode:
					for _, child := range n.Content {
						visit(child, key)
					}
				case yaml.MappingNode:
					for i := 0; i+1 < len(n.Content); i += 2 {
						visit(n.Content[i+1], n.Content[i].Value)
					}
				case yaml.ScalarNode:
					if key == "tags" || key == "tag" {
						for _, tag := range strings.FieldsFunc(n.Value, func(c rune) bool { return unicode.IsSpace(c) || c == ',' }) {
							addTag(tag)
						}
					} else if key != "aliases" && key != "alias" && n.Tag == "!!str" {
						collect([]byte(n.Value), true)
					}
				}
			}
			visit(&root, "")
		}
	}
	for tag := range tags {
		result.Tags = append(result.Tags, tag)
	}
	sort.Strings(result.Tags)
	return result
}

func appendMarkdownReference(result *Metadata, destination []byte, embed bool) {
	u, err := url.Parse(string(destination))
	if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" {
		return
	}
	result.Links = append(result.Links, Reference{Target: u.Path, Embed: embed})
}
