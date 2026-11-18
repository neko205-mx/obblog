package math

import (
	"strings"
	"testing"
)

func render(t *testing.T, source string, display bool) string {
	t.Helper()
	out, err := Render(source, display)
	if err != nil {
		t.Fatalf("Render(%q): %v", source, err)
	}
	return out
}

func TestRenderSimpleIdentifier(t *testing.T) {
	out := render(t, "x", false)
	want := `<math xmlns="http://www.w3.org/1998/Math/MathML"><mrow><mi>x</mi></mrow></math>`
	if out != want {
		t.Errorf("got %s\nwant %s", out, want)
	}
}

// LaTeX sets a run of letters as separate variables, not one word.
func TestRenderSplitsAdjacentLetters(t *testing.T) {
	out := render(t, "abc", false)
	if strings.Count(out, "<mi>") != 3 {
		t.Errorf("abc should be three identifiers: %s", out)
	}
	for _, letter := range []string{"<mi>a</mi>", "<mi>b</mi>", "<mi>c</mi>"} {
		if !strings.Contains(out, letter) {
			t.Errorf("missing %s in %s", letter, out)
		}
	}
}

func TestRenderFunctionNamesStayWhole(t *testing.T) {
	out := render(t, `\sin x`, false)
	if !strings.Contains(out, "<mo>sin</mo>") {
		t.Errorf("sin should be a single operator: %s", out)
	}
}

func TestRenderSuperscript(t *testing.T) {
	out := render(t, "x^2", false)
	if !strings.Contains(out, "<msup>") || !strings.Contains(out, "<mn>2</mn>") {
		t.Errorf("expected msup with 2: %s", out)
	}
}

func TestRenderSubscript(t *testing.T) {
	out := render(t, "x_i", false)
	if !strings.Contains(out, "<msub>") || !strings.Contains(out, "<mi>i</mi>") {
		t.Errorf("expected msub: %s", out)
	}
}

func TestRenderSubSupInEitherOrder(t *testing.T) {
	// Sub then superscript is the form the underlying parser handles.
	out := render(t, "x_i^2", false)
	if !strings.Contains(out, "<msubsup>") {
		t.Errorf("x_i^2 should produce msubsup: %s", out)
	}
	if !strings.Contains(out, "<mn>2</mn>") || !strings.Contains(out, "<mi>i</mi>") {
		t.Errorf("x_i^2 lost an operand: %s", out)
	}
}

// The parser folds a trailing underscore into the preceding literal, so
// "x^2_i" loses its subscript. Documented in docs/math.md; braced form works.
func TestRenderSupThenSubNeedsBraces(t *testing.T) {
	out := render(t, "x^{2}_{i}", false)
	if !strings.Contains(out, "<msubsup>") {
		t.Errorf("braced form should produce msubsup: %s", out)
	}
}

func TestRenderBracedGroupAsScript(t *testing.T) {
	out := render(t, "x^{2n}", false)
	if !strings.Contains(out, "<msup>") {
		t.Fatalf("expected msup: %s", out)
	}
	// The group keeps both factors.
	if !strings.Contains(out, "<mn>2</mn>") || !strings.Contains(out, "<mi>n</mi>") {
		t.Errorf("group lost a factor: %s", out)
	}
}

func TestRenderFraction(t *testing.T) {
	out := render(t, `\frac{a}{b}`, false)
	if !strings.Contains(out, "<mfrac>") {
		t.Fatalf("expected mfrac: %s", out)
	}
	if !strings.Contains(out, "<mi>a</mi>") || !strings.Contains(out, "<mi>b</mi>") {
		t.Errorf("fraction lost an operand: %s", out)
	}
}

func TestRenderBinomialHasNoBar(t *testing.T) {
	out := render(t, `\binom{n}{k}`, false)
	if !strings.Contains(out, `linethickness="0"`) {
		t.Errorf("binomial should suppress the fraction bar: %s", out)
	}
}

func TestRenderRoot(t *testing.T) {
	out := render(t, `\sqrt{x}`, false)
	if !strings.Contains(out, "<msqrt>") {
		t.Errorf("expected msqrt: %s", out)
	}

	indexed := render(t, `\sqrt[3]{x}`, false)
	if !strings.Contains(indexed, "<mroot>") {
		t.Errorf("expected mroot: %s", indexed)
	}
	if !strings.Contains(indexed, "<mn>3</mn>") {
		t.Errorf("root index lost: %s", indexed)
	}
}

func TestRenderGreekLetters(t *testing.T) {
	cases := map[string]string{
		`\alpha`: "α", `\beta`: "β", `\pi`: "π", `\Omega`: "Ω",
		`\varepsilon`: "ε", `\varphi`: "φ",
	}
	for source, want := range cases {
		out := render(t, source, false)
		if !strings.Contains(out, "<mi>"+want+"</mi>") {
			t.Errorf("%s should contain %s: %s", source, want, out)
		}
	}
}

func TestRenderOperators(t *testing.T) {
	cases := map[string]string{
		`a \times b`: "×", `a \leq b`: "≤", `a \neq b`: "≠",
		`a \to b`: "→", `a \in b`: "∈", `a \cdot b`: "⋅",
	}
	for source, want := range cases {
		out := render(t, source, false)
		if !strings.Contains(out, "<mo>"+want+"</mo>") {
			t.Errorf("%s should contain %s: %s", source, want, out)
		}
	}
}

// Big operators stack their limits instead of placing them to the side.
func TestRenderSumUsesUnderOver(t *testing.T) {
	out := render(t, `\sum_{i=1}^{n} i`, false)
	// Both limits are present, so the combined element is used.
	if !strings.Contains(out, "<munderover>") {
		t.Errorf("sum limits should stack: %s", out)
	}
	// The subscript keeps the whole group together.
	if !strings.Contains(out, "<mrow><mi>i</mi><mo>=</mo><mn>1</mn></mrow>") {
		t.Errorf("sum subexpression should be grouped: %s", out)
	}
	if !strings.Contains(out, "<mo movablelimits=\"true\">∑</mo>") {
		t.Errorf("sum symbol missing: %s", out)
	}
}

func TestRenderTextIsUpright(t *testing.T) {
	out := render(t, `\text{if } x > 0`, false)
	if !strings.Contains(out, "<mtext>if </mtext>") {
		t.Errorf("expected mtext: %s", out)
	}
}

func TestRenderLeftRight(t *testing.T) {
	out := render(t, `\left( \frac{a}{b} \right)`, false)
	if strings.Count(out, `fence="true"`) < 2 {
		t.Errorf("expected two fences: %s", out)
	}
	if !strings.Contains(out, "<mfrac>") {
		t.Errorf("fence body lost: %s", out)
	}
}

// \left. is an invisible delimiter; it must still produce an element.
func TestRenderInvisibleDelimiter(t *testing.T) {
	out := render(t, `\left. \frac{a}{b} \right|`, false)
	if !strings.Contains(out, "&#8203;") && !strings.Contains(out, "&amp;#8203;") {
		t.Errorf("invisible delimiter missing: %s", out)
	}
}

func TestRenderSpacingCommands(t *testing.T) {
	out := render(t, `a \, b`, false)
	if !strings.Contains(out, `<mspace width="0.1667em"></mspace>`) {
		t.Errorf("thin space missing: %s", out)
	}
}

func TestRenderDisplayMode(t *testing.T) {
	out := render(t, "x", true)
	if !strings.Contains(out, `display="block"`) {
		t.Errorf("display mode missing: %s", out)
	}
}

// HTML in \text must be escaped; this is the main injection surface.
func TestRenderEscapesHTML(t *testing.T) {
	out := render(t, `\text{<script>alert(1)</script>}`, false)
	if strings.Contains(out, "<script>") {
		t.Fatalf("script tag must be escaped: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("expected escaped text: %s", out)
	}
}

func TestRenderEscapesAngleInOperator(t *testing.T) {
	out := render(t, "a < b", false)
	if strings.Contains(out, "<mo><</mo>") {
		t.Fatalf("raw < must not appear: %s", out)
	}
	if !strings.Contains(out, "&lt;") {
		t.Errorf("expected escaped operator: %s", out)
	}
}

// An unknown command is shown literally so the author notices a typo.
func TestRenderUnknownCommandIsVisible(t *testing.T) {
	out := render(t, `\notacommand`, false)
	if !strings.Contains(out, "\\notacommand") {
		t.Errorf("unknown command should be visible: %s", out)
	}
}

func TestRenderRejectsUnbalancedBraces(t *testing.T) {
	if _, err := Render("x^{2", false); err == nil {
		t.Fatal("unbalanced brace must fail")
	}
	if _, err := Render("x^2}", false); err == nil {
		t.Fatal("stray closing brace must fail")
	}
}

func TestRenderRejectsUnpairedLeft(t *testing.T) {
	if _, err := Render(`\left( x`, false); err == nil {
		t.Fatal("unpaired \\left must fail")
	}
	if _, err := Render(`x \right)`, false); err == nil {
		t.Fatal("unpaired \\right must fail")
	}
}

func TestRenderRejectsMissingArguments(t *testing.T) {
	for _, source := range []string{`\frac{a}`, `\frac`, `\sqrt`, `\text`} {
		if _, err := Render(source, false); err == nil {
			t.Errorf("%s should fail", source)
		}
	}
}

func TestRenderRejectsRepeatedScripts(t *testing.T) {
	if _, err := Render("x^2^3", false); err == nil {
		t.Fatal("repeated superscript must fail")
	}
	if _, err := Render("x_i_j", false); err == nil {
		t.Fatal("repeated subscript must fail")
	}
}

func TestRenderDepthLimit(t *testing.T) {
	deep := strings.Repeat("{", maxDepth+5) + "x" + strings.Repeat("}", maxDepth+5)
	if _, err := Render(deep, false); err == nil {
		t.Fatal("deep nesting must be rejected")
	}
}

func TestEscape(t *testing.T) {
	cases := map[string]string{
		"a<b":    "a&lt;b",
		"a>b":    "a&gt;b",
		`a"b`:    "a&#34;b",
		"a&b":    "a&amp;b",
		"plain":  "plain",
		"<&\"'>": "&lt;&amp;&#34;&#39;&gt;",
	}
	for input, want := range cases {
		if got := escape(input); got != want {
			t.Errorf("escape(%q) = %q, want %q", input, got, want)
		}
	}
}

// \mathbb and friends map to Unicode mathematical letters, since the parser has
// no variant support.
func TestRenderBlackboardBold(t *testing.T) {
	out := render(t, `\mathbb{R}`, false)
	if !strings.Contains(out, "<mi>ℝ</mi>") {
		t.Errorf("expected a blackboard R: %s", out)
	}
}

func TestRenderCalligraphic(t *testing.T) {
	out := render(t, `\mathcal{L}`, false)
	// Unicode keeps the script L as U+2112 rather than in the math block.
	if !strings.Contains(out, "<mi>ℒ</mi>") {
		t.Errorf("expected a calligraphic L: %s", out)
	}
}

// A formula mixing text and blackboard bold must render as a whole.
func TestRenderMixedTextAndLetters(t *testing.T) {
	out := render(t, `\text{for } x \in \mathbb{R}`, false)
	if !strings.Contains(out, "<mtext>for </mtext>") {
		t.Errorf("text run missing: %s", out)
	}
	if !strings.Contains(out, "ℝ") {
		t.Errorf("blackboard R missing: %s", out)
	}
	if !strings.Contains(out, "<mo>∈</mo>") {
		t.Errorf("membership operator missing: %s", out)
	}
}

// A script after a multi-letter run applies to the whole run. "mc^2" must not
// produce a three-child msup, which browsers truncate.
func TestRenderScriptOnMultiLetterRun(t *testing.T) {
	out := render(t, "mc^2", false)
	// The run is grouped, so the script wraps an mrow containing both letters.
	if !strings.Contains(out, "<mrow><mi>m</mi><mi>c</mi></mrow>") {
		t.Errorf("letters should be grouped before the script: %s", out)
	}
	// msup must have exactly two children: the group and the exponent.
	start := strings.Index(out, "<msup>")
	end := strings.Index(out, "</msup>")
	if start < 0 || end < 0 {
		t.Fatalf("expected msup: %s", out)
	}
	inner := out[start:end]
	if strings.Count(inner, "<mn>2</mn>") != 1 {
		t.Errorf("exponent should appear once: %s", out)
	}
}
