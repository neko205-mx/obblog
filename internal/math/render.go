// Package math converts a subset of LaTeX into MathML.
//
// Parsing is delegated to github.com/go-latex/latex. That library maps LaTeX
// onto a small AST but ships no MathML output, so this package supplies the
// rendering half: an AST walker that emits MathML elements.
//
// The library panics on macros it does not know instead of returning an error,
// which would let any note take the server down. Every call is therefore made
// through parseExpr, which recovers and reports a normal error.
//
// Two constructs the library cannot parse at all are rewritten before parsing:
// \text{...}, which is common in Chinese notes and has no AST equivalent, and
// \begin{matrix}...\end{matrix}. Both are extracted into placeholders, rendered
// separately, and spliced back into the output.
package math

import (
	"errors"
	"strings"

	"github.com/go-latex/latex"
	"github.com/go-latex/latex/ast"
)

// maxSource bounds the formula length so a pathological input cannot hang the
// renderer. Notes are capped at 8 MiB, so a single formula has no reason to be
// large.
const maxSource = 32 << 10

// Render converts a LaTeX fragment into a MathML <math> element.
//
// display selects block layout, which centres the formula and stacks limits.
// On failure the caller should show the literal source, so a typo stays visible
// instead of rendering as a blank.
func Render(source string, display bool) (string, error) {
	if len(source) > maxSource {
		return "", errors.New("公式过长")
	}
	if strings.TrimSpace(source) == "" {
		return "", errors.New("公式为空")
	}

	fragments, err := extract(source)
	if err != nil {
		return "", err
	}

	var body strings.Builder
	for _, fragment := range fragments {
		fragment.writeTo(&body)
	}

	var out strings.Builder
	out.WriteString(`<math xmlns="http://www.w3.org/1998/Math/MathML"`)
	if display {
		out.WriteString(` display="block"`)
	}
	out.WriteString(`><mrow>`)
	out.WriteString(body.String())
	out.WriteString(`</mrow></math>`)
	return out.String(), nil
}

// fragment is one piece of the rendered formula: either a parsed AST range or a
// construct the library cannot handle, such as a text run.
type fragment interface {
	writeTo(b *strings.Builder)
}

// parsed holds nodes produced by the library.
type parsed struct {
	nodes []ast.Node
}

func (p *parsed) writeTo(b *strings.Builder) {
	renderer := &walker{out: b}
	renderer.renderList(p.nodes)
}

// parseExpr wraps the library call so a panic becomes an error. Unknown macros
// are substituted before this point, so reaching the panic means the syntax is
// genuinely broken.
func parseExpr(source string) (nodes []ast.Node, err error) {
	defer func() {
		if r := recover(); r != nil {
			nodes = nil
			err = errors.New("公式含有不支持的语法")
		}
	}()
	node, parseErr := latex.ParseExpr(source)
	if parseErr != nil {
		return nil, errors.New("公式语法无效")
	}
	list, ok := node.(ast.List)
	if !ok {
		if node == nil {
			return nil, errors.New("公式为空")
		}
		return []ast.Node{node}, nil
	}
	return []ast.Node(list), nil
}

// nodeText returns the literal text of a node when it carries one.
func nodeText(node ast.Node) (string, bool) {
	switch n := node.(type) {
	case *ast.Word:
		return n.Text, true
	case *ast.Literal:
		return n.Text, true
	case *ast.Symbol:
		return n.Text, true
	case *ast.Ident:
		return n.Name, true
	}
	return "", false
}

// identName returns the macro name without the leading backslash.
func identName(id *ast.Ident) string {
	if id == nil {
		return ""
	}
	return strings.TrimPrefix(id.Name, "\\")
}

// listOf unwraps an argument into the nodes it contains.
func listOf(node ast.Node) []ast.Node {
	switch n := node.(type) {
	case ast.List:
		return []ast.Node(n)
	case *ast.Arg:
		return listOf(n.List)
	case *ast.OptArg:
		return listOf(n.List)
	}
	if node == nil {
		return nil
	}
	return []ast.Node{node}
}
