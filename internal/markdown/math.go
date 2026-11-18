package markdown

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	obmath "obblog/internal/math"
)

// mathKind identifies an inline or block formula in the AST.
var mathKind = ast.NewNodeKind("Math")

// mathNode holds the LaTeX source and whether it is a display formula.
type mathNode struct {
	ast.BaseInline
	source  string
	display bool
}

func (n *mathNode) Kind() ast.NodeKind { return mathKind }

func (n *mathNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{
		"display": "false",
		"tex":     n.source,
	}, nil)
}

func (n *mathNode) Text(source []byte) []byte { return []byte(n.source) }

// mathParser recognises $...$ and $$...$$ as inline formulas.
//
// Block formulas are handled by mathBlockParser; this parser only sees the
// inline form because goldmark tries block parsers first.
type mathParser struct{}

func (*mathParser) Trigger() []byte { return []byte{'$'} }

func (*mathParser) Parse(parent ast.Node, reader text.Reader, pc parser.Context) ast.Node {
	line, segment := reader.PeekLine()
	if len(line) < 2 || line[0] != '$' {
		return nil
	}
	// A run of two or more dollars belongs to the block parser.
	if len(line) > 1 && line[1] == '$' {
		return nil
	}
	// Opening $ must be followed by a non-space, otherwise it is a currency
	// symbol rather than a formula.
	if line[1] == ' ' || line[1] == '\t' || line[1] == '\n' {
		return nil
	}
	// Find the closing $ on the same line, skipping escaped \$.
	for i := 1; i < len(line); i++ {
		if line[i] == '\\' {
			i++ // skip the escaped character
			continue
		}
		if line[i] != '$' {
			continue
		}
		// The character before the closing $ must not be whitespace, and the
		// content must not be empty.
		if i == 1 || line[i-1] == ' ' || line[i-1] == '\t' {
			return nil
		}
		source := string(line[1:i])
		if strings.TrimSpace(source) == "" {
			return nil
		}
		reader.Advance(segment.Start + i + 1 - segment.Start)
		return &mathNode{source: source}
	}
	return nil
}

// mathBlockParser recognises $$...$$ that opens and closes a line.
type mathBlockParser struct{}

// Trigger returns the characters that start a block.
func (*mathBlockParser) Trigger() []byte { return []byte{'$'} }

// Open reports whether the line starts a display formula.
func (*mathBlockParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, segment := reader.PeekLine()
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("$$")) {
		return nil, parser.NoChildren
	}
	// Closing on the same line: $$ formula $$ with content between.
	rest := trimmed[2:]
	if end := bytes.Index(rest, []byte("$$")); end >= 0 {
		source := strings.TrimSpace(string(rest[:end]))
		if source == "" {
			return nil, parser.NoChildren
		}
		reader.Advance(segment.Len())
		return &mathBlockNode{source: source}, parser.NoChildren
	}
	// Otherwise the formula continues until a closing $$ line.
	reader.Advance(segment.Len())
	var body strings.Builder
	for {
		next, nextSegment := reader.PeekLine()
		if len(next) == 0 {
			// Unterminated formula: leave it as text by failing to open.
			return nil, parser.NoChildren
		}
		if bytes.HasPrefix(bytes.TrimSpace(next), []byte("$$")) {
			reader.Advance(nextSegment.Len())
			break
		}
		body.Write(next)
		reader.Advance(nextSegment.Len())
	}
	source := strings.TrimSpace(body.String())
	if source == "" {
		return nil, parser.NoChildren
	}
	return &mathBlockNode{source: source}, parser.NoChildren
}

func (*mathBlockParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	return parser.Close
}

func (*mathBlockParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}

func (*mathBlockParser) CanInterruptParagraph() bool { return true }

func (*mathBlockParser) CanAcceptIndentedLine() bool { return false }

// mathBlockNode is a display formula, rendered as its own block.
type mathBlockNode struct {
	ast.BaseBlock
	source string
}

func (n *mathBlockNode) Kind() ast.NodeKind { return mathKind }

func (n *mathBlockNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"tex": n.source}, nil)
}

// IsRaw marks the block so goldmark does not try to parse its children.
func (n *mathBlockNode) IsRaw() bool { return true }

// mathRenderer writes the MathML for both node types.
type mathRenderer struct{}

func (r *mathRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(mathKind, r.renderMath)
}

func (r *mathRenderer) renderMath(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	var tex string
	display := false
	switch n := node.(type) {
	case *mathNode:
		tex = n.source
	case *mathBlockNode:
		tex = n.source
		display = true
	default:
		return ast.WalkSkipChildren, nil
	}
	html, err := obmath.Render(tex, display)
	if err != nil {
		// Show the literal source so a broken formula stays visible.
		w.WriteString(`<code class="math-error" title="`)
		w.WriteString(escapeAttr(err.Error()))
		w.WriteString(`">`)
		w.WriteString(escapeHTML(tex))
		w.WriteString(`</code>`)
		return ast.WalkSkipChildren, nil
	}
	if display {
		w.WriteString(`<div class="math-block">`)
		w.WriteString(html)
		w.WriteString(`</div>`)
	} else {
		w.WriteString(html)
	}
	return ast.WalkSkipChildren, nil
}

// escapeHTML escapes text content for the fallback path.
func escapeHTML(value string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	).Replace(value)
}

// escapeAttr escapes a value placed inside an HTML attribute.
func escapeAttr(value string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
	).Replace(value)
}
