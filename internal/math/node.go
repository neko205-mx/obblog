package math

import (
	"strings"
)

// node is one renderable piece of the formula.
type node interface {
	writeTo(b *strings.Builder)
}

// row is a sequence of siblings with no grouping element of its own.
type row struct{ children []node }

func (r *row) writeTo(b *strings.Builder) {
	for _, child := range r.children {
		child.writeTo(b)
	}
}

// identifier is a variable, rendered as <mi>.
type identifier struct{ name string }

func (n *identifier) writeTo(b *strings.Builder) {
	b.WriteString("<mi>")
	b.WriteString(escape(n.name))
	b.WriteString("</mi>")
}

// number is a numeric literal.
type number struct{ value string }

func (n *number) writeTo(b *strings.Builder) {
	b.WriteString("<mn>")
	b.WriteString(escape(n.value))
	b.WriteString("</mn>")
}

// operator is a symbol. stretchy marks big operators that take limits,
// fence marks delimiters that scale, and limit stacks scripts vertically.
type operator struct {
	value    string
	stretchy bool
	fence    bool
	limit    bool
}

func (n *operator) writeTo(b *strings.Builder) {
	b.WriteString("<mo")
	var attrs []string
	if n.stretchy {
		attrs = append(attrs, `movablelimits="true"`)
	}
	if n.fence {
		attrs = append(attrs, `fence="true"`)
	}
	if len(attrs) > 0 {
		b.WriteString(" ")
		b.WriteString(strings.Join(attrs, " "))
	}
	b.WriteString(">")
	b.WriteString(escape(n.value))
	b.WriteString("</mo>")
}

// script attaches sub and/or superscript to a base.
type script struct {
	base  node
	sub   node
	sup   node
	under bool
}

func (n *script) writeTo(b *strings.Builder) {
	switch {
	case n.sub != nil && n.sup != nil:
		if n.under {
			b.WriteString("<munderover>")
			n.base.writeTo(b)
			n.sub.writeTo(b)
			n.sup.writeTo(b)
			b.WriteString("</munderover>")
			return
		}
		b.WriteString("<msubsup>")
		n.base.writeTo(b)
		n.sub.writeTo(b)
		n.sup.writeTo(b)
		b.WriteString("</msubsup>")
	case n.sub != nil:
		if n.under {
			b.WriteString("<munder>")
			n.base.writeTo(b)
			n.sub.writeTo(b)
			b.WriteString("</munder>")
			return
		}
		b.WriteString("<msub>")
		n.base.writeTo(b)
		n.sub.writeTo(b)
		b.WriteString("</msub>")
	case n.sup != nil:
		if n.under {
			b.WriteString("<mover>")
			n.base.writeTo(b)
			n.sup.writeTo(b)
			b.WriteString("</mover>")
			return
		}
		b.WriteString("<msup>")
		n.base.writeTo(b)
		n.sup.writeTo(b)
		b.WriteString("</msup>")
	default:
		n.base.writeTo(b)
	}
}

// fraction renders \frac.
type fraction struct {
	numerator   node
	denominator node
}

func (n *fraction) writeTo(b *strings.Builder) {
	b.WriteString("<mfrac>")
	n.numerator.writeTo(b)
	n.denominator.writeTo(b)
	b.WriteString("</mfrac>")
}

// binomial renders \binom: a stacked pair inside parentheses, without a bar.
type binomial struct {
	numerator   node
	denominator node
}

func (n *binomial) writeTo(b *strings.Builder) {
	b.WriteString(`<mrow><mo fence="true">(</mo><mfrac linethickness="0">`)
	n.numerator.writeTo(b)
	n.denominator.writeTo(b)
	b.WriteString(`</mfrac><mo fence="true">)</mo></mrow>`)
}

// radical renders \sqrt, with an optional index.
type radical struct {
	body  node
	index node
}

func (n *radical) writeTo(b *strings.Builder) {
	if n.index == nil {
		b.WriteString("<msqrt>")
		n.body.writeTo(b)
		b.WriteString("</msqrt>")
		return
	}
	b.WriteString("<mroot>")
	n.body.writeTo(b)
	n.index.writeTo(b)
	b.WriteString("</mroot>")
}

// textNode is upright literal text, used for content the library cannot parse.
type textNode struct{ value string }

func (n *textNode) writeTo(b *strings.Builder) {
	b.WriteString("<mtext>")
	b.WriteString(escape(n.value))
	b.WriteString("</mtext>")
}

// space is explicit horizontal spacing such as \, or \quad.
type space struct{ width string }

func (n *space) writeTo(b *strings.Builder) {
	b.WriteString(`<mspace width="`)
	b.WriteString(n.width)
	b.WriteString(`"></mspace>`)
}

// isFence reports whether a character should scale as a delimiter.
func isFence(value string) bool {
	switch value {
	case "(", ")", "[", "]":
		return true
	}
	return false
}

// escape converts characters that are special in HTML text content. Values are
// never written into attributes other than a fixed set of literals, so quoting
// is sufficient.
func escape(value string) string {
	if !strings.ContainsAny(value, `&<>"'`) {
		return value
	}
	var b strings.Builder
	b.Grow(len(value) + 8)
	for _, r := range value {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&#34;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// group renders its children as an explicit mrow so a script attaches to the
// whole sequence.
type group struct{ children []node }

func (g *group) writeTo(b *strings.Builder) {
	b.WriteString("<mrow>")
	for _, child := range g.children {
		child.writeTo(b)
	}
	b.WriteString("</mrow>")
}
