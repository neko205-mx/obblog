package math

import (
	"strings"

	"github.com/go-latex/latex/ast"
)

// walker renders AST nodes into MathML.
//
// The library AST is flat: a script appears as a sibling *after* the node it
// applies to, rather than as a child. renderList therefore looks ahead and
// attaches Sub and Sup nodes to the preceding element, which is the only place
// that grouping has to happen.
type walker struct {
	out *strings.Builder
	// depth guards the recursive descent against pathological nesting.
	depth int
}

const maxDepth = 32

func (w *walker) renderList(nodes []ast.Node) {
	for i := 0; i < len(nodes); i++ {
		node := nodes[i]
		switch n := node.(type) {
		case *ast.Sub, *ast.Sup:
			// A script with no base: emit the marker on its own so the author
			// sees something rather than a silently dropped token.
			marker := "_"
			if _, ok := node.(*ast.Sup); ok {
				marker = "^"
			}
			w.writeOperator(marker)
		case *ast.Macro:
			w.renderMacro(n, nodes, &i)
		default:
			// A plain leaf may still be followed by scripts; collect them here
			// so a superscript never becomes a dangling marker.
			base := w.leaf(n)
			sub, sup := takeScripts(nodes, &i)
			w.writeBase(w.wrapScripts(base, sub, sup))
		}
	}
}

// takeScripts consumes any Sub or Sup nodes directly after index and returns
// their bodies. The library keeps scripts as siblings, so every element needs
// this lookahead.
func takeScripts(nodes []ast.Node, index *int) (sub, sup ast.Node) {
	for *index+1 < len(nodes) {
		switch next := nodes[*index+1].(type) {
		case *ast.Sub:
			if sub != nil {
				return sub, sup
			}
			sub = next.Node
			*index++
		case *ast.Sup:
			if sup != nil {
				return sub, sup
			}
			sup = next.Node
			*index++
		default:
			return sub, sup
		}
	}
	return sub, sup
}

// renderMacro handles a command plus any scripts that follow it.
func (w *walker) renderMacro(macro *ast.Macro, siblings []ast.Node, index *int) {
	name := identName(macro.Name)
	args := macro.Args

	sub, sup := takeScripts(siblings, index)

	switch name {
	case "frac", "dfrac", "tfrac", "cfrac":
		if len(args) < 2 {
			w.writeRaw(`\` + name)
			return
		}
		w.writeBase(w.wrapScripts(w.fraction(args[0], args[1], false), sub, sup))
		return
	case "binom", "dbinom", "tbinom":
		if len(args) < 2 {
			w.writeRaw(`\` + name)
			return
		}
		w.writeBase(w.wrapScripts(w.fraction(args[0], args[1], true), sub, sup))
		return
	case "sqrt":
		w.writeBase(w.wrapScripts(w.root(args), sub, sup))
		return
	case "overline", "bar":
		if len(args) < 1 {
			w.writeRaw(`\` + name)
			return
		}
		w.writeRaw(`<mover>`)
		w.renderNodes(listOf(args[0]))
		w.writeRaw(`<mo stretchy="true">¯</mo></mover>`)
		return
	case "underline":
		if len(args) < 1 {
			w.writeRaw(`\` + name)
			return
		}
		w.writeRaw(`<munder>`)
		w.renderNodes(listOf(args[0]))
		w.writeRaw(`<mo stretchy="true">_</mo></munder>`)
		return
	case "mathbf", "textbf", "boldsymbol", "bm":
		if len(args) < 1 {
			w.writeRaw(`\` + name)
			return
		}
		w.writeRaw(`<mstyle mathvariant="bold">`)
		w.renderNodes(listOf(args[0]))
		w.writeRaw(`</mstyle>`)
		return
	case "mathit", "textit":
		if len(args) < 1 {
			w.writeRaw(`\` + name)
			return
		}
		w.writeRaw(`<mstyle mathvariant="italic">`)
		w.renderNodes(listOf(args[0]))
		w.writeRaw(`</mstyle>`)
		return
	case "mathrm", "operatorname":
		if len(args) < 1 {
			w.writeRaw(`\` + name)
			return
		}
		w.writeRaw(`<mi mathvariant="normal">`)
		w.renderPlainText(args[0])
		w.writeRaw(`</mi>`)
		return
	}

	if sym, ok := commands[name]; ok {
		switch sym.tag {
		case "mspace":
			w.writeRaw(`<mspace width="` + sym.value + `"></mspace>`)
			return
		case "mtext":
			w.writeRaw(`<mtext>` + escape(sym.value) + `</mtext>`)
			return
		}
		// The symbol table decides the element type: letters are identifiers,
		// everything else is an operator.
		var base node
		if sym.tag == "mi" {
			base = &identifier{name: sym.value}
		} else {
			// Big operators stack their limits above and below.
			base = &operator{value: sym.value, stretchy: sym.stretchy, fence: sym.fence, limit: sym.stretchy}
		}
		w.writeBase(w.wrapScripts(base, sub, sup))
		return
	}

	if functions[name] {
		// Function names are set upright and take scripts to the side, except
		// for limit-style names such as \lim.
		base := &operator{value: name}
		if limitFunctions[name] {
			base.limit = true
		}
		w.writeBase(w.wrapScripts(base, sub, sup))
		return
	}

	// An unknown macro: show it literally so the author notices the typo.
	w.writeRaw(`<mtext>` + escape(`\`+name) + `</mtext>`)
	if sub != nil || sup != nil {
		w.writeBase(w.wrapScripts(&row{}, sub, sup))
	}
}

// fraction renders \frac or \binom.
func (w *walker) fraction(numerator, denominator ast.Node, asBinomial bool) node {
	num := &parsedNodes{nodes: listOf(numerator)}
	den := &parsedNodes{nodes: listOf(denominator)}
	if asBinomial {
		return &binomial{numerator: num, denominator: den}
	}
	return &fraction{numerator: num, denominator: den}
}

// root renders \sqrt with an optional index.
func (w *walker) root(args ast.List) node {
	var index, body ast.Node
	for _, arg := range args {
		switch a := arg.(type) {
		case *ast.OptArg:
			index = a.List
		case *ast.Arg:
			body = a.List
		}
	}
	if body == nil && index == nil {
		return &row{}
	}
	// A nil index must stay nil: an empty parsedNodes is a non-nil interface,
	// which would render \sqrt as an indexed root.
	rad := &radical{body: &parsedNodes{nodes: listOf(body)}}
	if index != nil {
		rad.index = &parsedNodes{nodes: listOf(index)}
	}
	return rad
}

// wrapScripts attaches scripts to a base, choosing the MathML element by which
// scripts are present and whether the base takes limits.
func (w *walker) wrapScripts(base node, sub, sup ast.Node) node {
	if sub == nil && sup == nil {
		return base
	}
	result := &script{base: base}
	if sub != nil {
		result.sub = &parsedNodes{nodes: listOf(sub)}
	}
	if sup != nil {
		result.sup = &parsedNodes{nodes: listOf(sup)}
	}
	if op, ok := base.(*operator); ok && op.limit {
		result.under = true
	}
	return result
}

func (w *walker) leaf(node ast.Node) node {
	switch n := node.(type) {
	case *ast.Word:
		return w.word(n.Text)
	case *ast.Literal:
		return &number{value: n.Text}
	case *ast.Symbol:
		return &operator{value: n.Text, fence: isFence(n.Text)}
	case *ast.Ident:
		return &identifier{name: n.Name}
	case ast.List:
		return &parsedNodes{nodes: []ast.Node(n)}
	}
	return &row{}
}

// word splits a run of Latin letters into single variables, which is what LaTeX
// math mode does; a known function name stays whole.
func (w *walker) word(text string) node {
	if functions[text] {
		return &operator{value: text}
	}
	if strings.TrimSpace(text) == "" {
		return &row{}
	}
	runes := []rune(text)
	if len(runes) == 1 {
		return &identifier{name: text}
	}
	// A multi-letter run becomes a group, so a following script applies to the
	// whole run rather than only its last character. "mc^2" must superscript
	// the pair, not just the c.
	result := &group{}
	for _, r := range runes {
		// Digits inside a word keep their numeric styling.
		if r >= '0' && r <= '9' {
			result.children = append(result.children, &number{value: string(r)})
			continue
		}
		result.children = append(result.children, &identifier{name: string(r)})
	}
	return result
}

func (w *walker) renderNodes(nodes []ast.Node) {
	saved := w.depth
	if w.depth > maxDepth {
		w.writeRaw(`<mtext>…</mtext>`)
		return
	}
	w.depth++
	w.renderList(nodes)
	w.depth = saved
}

func (w *walker) writeBase(n node) {
	if n == nil {
		return
	}
	n.writeTo(w.out)
}

func (w *walker) writeRaw(value string) { w.out.WriteString(value) }

func (w *walker) writeOperator(value string) { w.writeRaw(`<mo>` + escape(value) + `</mo>`) }

// renderPlainText writes an argument's characters without math spacing.
func (w *walker) renderPlainText(node ast.Node) {
	var text strings.Builder
	collectText(node, &text)
	w.writeRaw(escape(text.String()))
}

// collectText flattens an argument to its literal characters.
func collectText(node ast.Node, out *strings.Builder) {
	switch n := node.(type) {
	case ast.List:
		for _, child := range n {
			collectText(child, out)
		}
	case *ast.Arg:
		collectText(n.List, out)
	case *ast.OptArg:
		collectText(n.List, out)
	case *ast.Word:
		out.WriteString(n.Text)
	case *ast.Literal:
		out.WriteString(n.Text)
	case *ast.Symbol:
		out.WriteString(n.Text)
	case *ast.Macro:
		if sym, ok := commands[identName(n.Name)]; ok {
			out.WriteString(sym.value)
			return
		}
		out.WriteString(strings.TrimPrefix(n.Name.Name, "\\"))
	}
}

// parsedNodes renders a node list captured from an argument.
type parsedNodes struct{ nodes []ast.Node }

func (p *parsedNodes) writeTo(b *strings.Builder) {
	// More than one node needs a container: <msub> takes exactly two children,
	// so a group such as {i=1} must not spread its parts across the element.
	if len(p.nodes) > 1 {
		b.WriteString("<mrow>")
		defer b.WriteString("</mrow>")
	}
	w := &walker{out: b}
	w.renderList(p.nodes)
}
