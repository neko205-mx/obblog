package markdown

import (
	"strings"
	"testing"

	"obblog/internal/vault"
)

// snapshotFixture builds a minimal snapshot so link resolution has something to
// work with.
func snapshotFixture() *vault.Snapshot {
	return &vault.Snapshot{
		Name: "vault",
		Files: map[string]*vault.Entry{
			"other.md": {Name: "other.md", Path: "other.md", Kind: "note"},
		},
	}
}

func renderSource(t *testing.T, source string) string {
	t.Helper()
	result, err := Render([]byte(source), "note.md", snapshotFixture())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return result.HTML
}

func TestMathInline(t *testing.T) {
	html := renderSource(t, "Euler: $e^{i\\pi}+1=0$ is famous.")
	if !strings.Contains(html, "<math") {
		t.Fatalf("inline formula not rendered: %s", html)
	}
	if !strings.Contains(html, "<msup>") {
		t.Errorf("superscript missing: %s", html)
	}
	// The surrounding prose survives.
	if !strings.Contains(html, "Euler:") || !strings.Contains(html, "is famous.") {
		t.Errorf("surrounding text lost: %s", html)
	}
}

func TestMathBlock(t *testing.T) {
	html := renderSource(t, "Before\n\n$$\n\\frac{a}{b}\n$$\n\nAfter")
	if !strings.Contains(html, `class="math-block"`) {
		t.Fatalf("block formula not rendered: %s", html)
	}
	if !strings.Contains(html, "<mfrac>") {
		t.Errorf("fraction missing: %s", html)
	}
	if !strings.Contains(html, `display="block"`) {
		t.Errorf("block formula should use display mode: %s", html)
	}
	if !strings.Contains(html, "Before") || !strings.Contains(html, "After") {
		t.Errorf("surrounding paragraphs lost: %s", html)
	}
}

func TestMathBlockSingleLine(t *testing.T) {
	html := renderSource(t, "$$a^2+b^2=c^2$$")
	if !strings.Contains(html, "<math") {
		t.Fatalf("single-line block not rendered: %s", html)
	}
	if !strings.Contains(html, "<msup>") {
		t.Errorf("superscript missing: %s", html)
	}
}

// A dollar sign in prose must not be treated as a formula delimiter.
func TestMathDoesNotEatCurrency(t *testing.T) {
	html := renderSource(t, "The price is $5 and the other is $6 today.")
	if strings.Contains(html, "<math") {
		t.Errorf("currency should not become a formula: %s", html)
	}
	if !strings.Contains(html, "$5") || !strings.Contains(html, "$6") {
		t.Errorf("dollar amounts lost: %s", html)
	}
}

func TestMathDoesNotPairCurrencyWithInlineCode(t *testing.T) {
	html := renderSource(t, "Price $5 and $6; code `$x^2$`.")
	if strings.Contains(html, "<math") || strings.Contains(html, "math-error") {
		t.Fatalf("currency paired with dollar inside code: %s", html)
	}
	for _, expected := range []string{"$5", "$6", "<code>$x^2$</code>"} {
		if !strings.Contains(html, expected) {
			t.Errorf("missing %q: %s", expected, html)
		}
	}
}

func TestMathIgnoresUnclosedDelimiter(t *testing.T) {
	html := renderSource(t, "An unclosed $formula stays text.")
	if strings.Contains(html, "<math") {
		t.Errorf("unclosed delimiter must not render: %s", html)
	}
	if !strings.Contains(html, "$formula") {
		t.Errorf("source should remain visible: %s", html)
	}
}

// A formula inside a fenced code block is shown literally.
func TestMathInsideCodeBlockIsLiteral(t *testing.T) {
	html := renderSource(t, "```\n$x^2$\n```")
	if strings.Contains(html, "<math") {
		t.Errorf("code block must not render math: %s", html)
	}
	if !strings.Contains(html, "$x^2$") {
		t.Errorf("code content lost: %s", html)
	}
}

func TestMathInsideInlineCodeIsLiteral(t *testing.T) {
	html := renderSource(t, "Use `$x$` to write a formula.")
	if strings.Contains(html, "<math") {
		t.Errorf("inline code must not render math: %s", html)
	}
}

func TestMathEscapedDollarIsLiteral(t *testing.T) {
	html := renderSource(t, `Costs \$5 total.`)
	if strings.Contains(html, "<math") {
		t.Errorf("escaped dollar must not start a formula: %s", html)
	}
	if !strings.Contains(html, "$5") {
		t.Errorf("escaped dollar should render as a literal: %s", html)
	}
}

// A broken formula shows its source instead of disappearing.
func TestMathErrorShowsSource(t *testing.T) {
	html := renderSource(t, "Broken $\\frac{a}{b$ here.")
	if !strings.Contains(html, "math-error") {
		t.Fatalf("expected an error marker: %s", html)
	}
	if !strings.Contains(html, `\frac{a}{b`) {
		t.Errorf("source should stay visible: %s", html)
	}
}

func TestMathWithWikiLinkCoexists(t *testing.T) {
	html := renderSource(t, "Formula $x^2$ and link [[other]] together.")
	if !strings.Contains(html, "<math") {
		t.Errorf("formula missing: %s", html)
	}
	if !strings.Contains(html, `class="internal-link"`) {
		t.Errorf("wiki link missing: %s", html)
	}
}

func TestMathInList(t *testing.T) {
	html := renderSource(t, "- First $a$\n- Second $b$")
	if strings.Count(html, "<math") != 2 {
		t.Errorf("both list formulas should render: %s", html)
	}
	if !strings.Contains(html, "<li>") {
		t.Errorf("list structure lost: %s", html)
	}
}

func TestMathExtractsNothingIntoGraph(t *testing.T) {
	// A formula must not be scanned for wiki links or tags.
	metadata := Extract([]byte("$\\text{[[notalink]] #nottag}$"))
	if len(metadata.Links) != 0 {
		t.Errorf("formula produced links: %+v", metadata.Links)
	}
	for _, tag := range metadata.Tags {
		if tag == "nottag" {
			t.Errorf("formula produced a tag: %v", metadata.Tags)
		}
	}
}

func TestMathHTMLIsEscaped(t *testing.T) {
	html := renderSource(t, `$\text{<script>alert(1)</script>}$`)
	if strings.Contains(html, "<script>") {
		t.Fatalf("script tag must be escaped: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("expected escaped output: %s", html)
	}
}
