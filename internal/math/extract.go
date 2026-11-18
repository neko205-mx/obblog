package math

import (
	"errors"
	"strings"
)

// The go-latex parser panics on \text and on matrix environments. Both are
// common enough to matter — \text is how Chinese notes label a formula — so
// they are lifted out of the source before parsing and rendered separately.
//
// The same pass replaces macros the library rejects with literal text, because
// one typo should not blank out the rest of the formula.

// extract splits source into renderable fragments.
func extract(source string) ([]fragment, error) {
	// The library does not validate brace balance or repeated scripts, so a
	// malformed formula would silently render as something else.
	if err := validate(source); err != nil {
		return nil, err
	}

	var fragments []fragment
	var pending strings.Builder

	flushPending := func() error {
		text := pending.String()
		pending.Reset()
		if strings.TrimSpace(text) == "" {
			return nil
		}
		nodes, err := parseExpr(text)
		if err != nil {
			return err
		}
		fragments = append(fragments, &parsed{nodes: nodes})
		return nil
	}

	for i := 0; i < len(source); {
		rest := source[i:]
		switch {
		case strings.HasPrefix(rest, `\text`), strings.HasPrefix(rest, `\textrm`),
			strings.HasPrefix(rest, `\mbox`), strings.HasPrefix(rest, `\hbox`):
			body, width, err := readTextCommand(rest)
			if err != nil {
				return nil, err
			}
			if err := flushPending(); err != nil {
				return nil, err
			}
			fragments = append(fragments, &textNode{value: body})
			i += width

		case strings.HasPrefix(rest, `\mathbb{`), strings.HasPrefix(rest, `\mathcal{`),
			strings.HasPrefix(rest, `\mathfrak{`):
			styled, width, err := readStyled(rest)
			if err != nil {
				return nil, err
			}
			if err := flushPending(); err != nil {
				return nil, err
			}
			fragments = append(fragments, styled)
			i += width

		case strings.HasPrefix(rest, `\left`):
			fenced, width, err := readFenced(rest)
			if err != nil {
				return nil, err
			}
			if err := flushPending(); err != nil {
				return nil, err
			}
			fragments = append(fragments, fenced)
			i += width

		case strings.HasPrefix(rest, `\begin{matrix}`), strings.HasPrefix(rest, `\begin{pmatrix}`),
			strings.HasPrefix(rest, `\begin{bmatrix}`):
			matrix, width, err := readMatrix(rest)
			if err != nil {
				return nil, err
			}
			if err := flushPending(); err != nil {
				return nil, err
			}
			fragments = append(fragments, matrix)
			i += width

		case strings.HasPrefix(rest, `\`) && len(rest) > 1 && isASCIILetter(rest[1]):
			// A command: keep it when the library knows it, otherwise show it as
			// literal text so one typo does not abort the whole formula.
			end := 1
			for end < len(rest) && isASCIILetter(rest[end]) {
				end++
			}
			name := rest[1:end]
			if knownMacro(name) {
				pending.WriteString(rest[:end])
				i += end
				continue
			}
			if err := flushPending(); err != nil {
				return nil, err
			}
			fragments = append(fragments, &textNode{value: `\` + name})
			i += end

		default:
			pending.WriteByte(source[i])
			i++
		}
	}
	if err := flushPending(); err != nil {
		return nil, err
	}
	if len(fragments) == 0 {
		return nil, errors.New("公式为空")
	}
	return fragments, nil
}

// readTextCommand reads \text{...} and returns the literal body and the bytes
// consumed.
func readTextCommand(source string) (body string, consumed int, err error) {
	brace := strings.IndexByte(source, '{')
	if brace < 0 {
		return "", 0, errors.New(`\text 需要花括号参数`)
	}
	depth := 0
	var out strings.Builder
	for i := brace; i < len(source); i++ {
		switch source[i] {
		case '\\':
			// Escaped brace inside the body: keep the character, drop the slash.
			if i+1 < len(source) && (source[i+1] == '{' || source[i+1] == '}') {
				out.WriteByte(source[i+1])
				i++
				continue
			}
			out.WriteByte(source[i])
		case '{':
			depth++
			if depth > 1 {
				out.WriteByte('{')
			}
		case '}':
			depth--
			if depth == 0 {
				return out.String(), i + 1, nil
			}
			out.WriteByte('}')
		default:
			out.WriteByte(source[i])
		}
	}
	return "", 0, errors.New(`\text 缺少右括号`)
}

// styled is a letter set in blackboard bold, calligraphic or fraktur style.
type styled struct{ value string }

func (s *styled) writeTo(b *strings.Builder) {
	b.WriteString("<mi>")
	b.WriteString(escape(s.value))
	b.WriteString("</mi>")
}

// readStyled converts \mathbb{R} and friends into a Unicode letter.
func readStyled(source string) (*styled, int, error) {
	open := strings.IndexByte(source, '{')
	if open < 0 {
		return nil, 0, errors.New("字体命令缺少参数")
	}
	closing := strings.IndexByte(source[open:], '}')
	if closing < 0 {
		return nil, 0, errors.New("字体命令缺少右括号")
	}
	inner := source[open+1 : open+closing]
	total := open + closing + 1

	kind := "frak"
	switch {
	case strings.HasPrefix(source, `\mathbb`):
		kind = "bb"
	case strings.HasPrefix(source, `\mathcal`):
		kind = "cal"
	}
	return &styled{value: convertLetters(inner, kind)}, total, nil
}

// convertLetters maps ASCII letters into a Unicode mathematical alphabet.
//
// The block has historical gaps: 𝔹 is followed by ℂ at a different offset, and
// several script letters were already encoded as symbols. A lookup table is
// therefore required; arithmetic on the block base yields the wrong character.
func convertLetters(value, kind string) string {
	alphabet, ok := mathAlphabets[kind]
	if !ok {
		return value
	}
	var out strings.Builder
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z':
			if repl := alphabet.upper[r-'A']; repl != 0 {
				out.WriteRune(repl)
			} else {
				out.WriteRune(r)
			}
		case r >= 'a' && r <= 'z':
			if repl := alphabet.lower[r-'a']; repl != 0 {
				out.WriteRune(repl)
			} else {
				out.WriteRune(r)
			}
		case r >= '0' && r <= '9' && alphabet.hasDigits:
			if repl := alphabet.digits[r-'0']; repl != 0 {
				out.WriteRune(repl)
			} else {
				out.WriteRune(r)
			}
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

type mathAlphabet struct {
	upper     [26]rune
	lower     [26]rune
	digits    [10]rune
	hasDigits bool
}

// mathAlphabets lists the letters that differ from the block's first run.
// A zero entry falls back to the plain ASCII character, which is what LaTeX
// does for a missing glyph.
var mathAlphabets = map[string]mathAlphabet{
	"bb": {
		upper: [26]rune{
			'𝔸', '𝔹', 'ℂ', '𝔻', '𝔼', '𝔽', '𝔾', 'ℍ', '𝕀', '𝕁', '𝕂', '𝕃', '𝕄',
			'ℕ', '𝕆', 'ℙ', 'ℚ', 'ℝ', '𝕊', '𝕋', '𝕌', '𝕍', '𝕎', '𝕏', '𝕐', 'ℤ',
		},
		lower: [26]rune{
			'𝕒', '𝕓', '𝕔', '𝕕', '𝕖', '𝕗', '𝕘', '𝕙', '𝕚', '𝕛', '𝕜', '𝕝', '𝕞',
			'𝕟', '𝕠', '𝕡', '𝕢', '𝕣', '𝕤', '𝕥', '𝕦', '𝕧', '𝕨', '𝕩', '𝕪', '𝕫',
		},
		digits:    [10]rune{'𝟘', '𝟙', '𝟚', '𝟛', '𝟜', '𝟝', '𝟞', '𝟟', '𝟠', '𝟡'},
		hasDigits: true,
	},
	"cal": {
		upper: [26]rune{
			'𝒜', 'ℬ', '𝒞', '𝒟', 'ℰ', 'ℱ', '𝒢', 'ℋ', 'ℐ', '𝒥', '𝒦', 'ℒ', 'ℳ',
			'𝒩', '𝒪', '𝒫', '𝒬', 'ℛ', '𝒮', '𝒯', '𝒰', '𝒱', '𝒲', '𝒳', '𝒴', '𝒵',
		},
	},
	"frak": {
		upper: [26]rune{
			'𝔄', '𝔅', 'ℭ', '𝔇', '𝔈', '𝔉', '𝔊', 'ℌ', 'ℑ', '𝔍', '𝔎', '𝔏', '𝔐',
			'𝔑', '𝔒', '𝔓', '𝔔', 'ℜ', '𝔖', '𝔗', '𝔘', '𝔙', '𝔚', '𝔛', '𝔜', 'ℨ',
		},
		lower: [26]rune{
			'𝔞', '𝔟', '𝔠', '𝔡', '𝔢', '𝔣', '𝔤', '𝔥', '𝔦', '𝔧', '𝔨', '𝔩', '𝔪',
			'𝔫', '𝔬', '𝔭', '𝔮', '𝔯', '𝔰', '𝔱', '𝔲', '𝔳', '𝔴', '𝔵', '𝔶', '𝔷',
		},
	},
}

// fenced is a \left ... \right group with scalable delimiters.
type fenced struct {
	open  string
	close string
	body  string
}

func (f *fenced) writeTo(b *strings.Builder) {
	b.WriteString("<mrow>")
	writeFence(b, f.open, true)
	nodes, err := parseExpr(f.body)
	if err != nil {
		b.WriteString("<mtext>")
		b.WriteString(escape(f.body))
		b.WriteString("</mtext>")
	} else {
		w := &walker{out: b}
		w.renderList(nodes)
	}
	writeFence(b, f.close, false)
	b.WriteString("</mrow>")
}

// writeFence emits a scalable delimiter; "." is LaTeX's invisible delimiter.
func writeFence(b *strings.Builder, value string, open bool) {
	if value == "" || value == "." {
		b.WriteString(`<mo fence="true" stretchy="true">&#8203;</mo>`)
		return
	}
	b.WriteString(`<mo fence="true" stretchy="true"`)
	if open {
		b.WriteString(` form="prefix"`)
	} else {
		b.WriteString(` form="postfix"`)
	}
	b.WriteString(">")
	b.WriteString(escape(value))
	b.WriteString("</mo>")
}

// readFenced reads a \left<delim> ... \right<delim> group. Nested groups are
// balanced, so an inner \left does not end the outer one.
func readFenced(source string) (*fenced, int, error) {
	open, width, err := readDelimiter(source[len(`\left`):])
	if err != nil {
		return nil, 0, err
	}
	start := len(`\left`) + width
	depth := 1
	for i := start; i < len(source); {
		rest := source[i:]
		switch {
		case strings.HasPrefix(rest, `\left`):
			depth++
			_, w, err := readDelimiter(rest[len(`\left`):])
			if err != nil {
				return nil, 0, err
			}
			i += len(`\left`) + w
		case strings.HasPrefix(rest, `\right`):
			depth--
			if depth == 0 {
				closing, w, err := readDelimiter(rest[len(`\right`):])
				if err != nil {
					return nil, 0, err
				}
				return &fenced{open: open, close: closing, body: source[start:i]},
					i + len(`\right`) + w, nil
			}
			i += len(`\right`)
		default:
			i++
		}
	}
	return nil, 0, errors.New(`\left 缺少对应的 \right`)
}

// readDelimiter reads one delimiter and returns it with the bytes consumed.
func readDelimiter(source string) (string, int, error) {
	j := 0
	for j < len(source) && (source[j] == ' ' || source[j] == '\t') {
		j++
	}
	if j >= len(source) {
		return "", 0, errors.New(`\left 或 \right 缺少括号`)
	}
	if source[j] != '\\' {
		return string(source[j]), j + 1, nil
	}
	k := j + 1
	if k < len(source) && !isASCIILetter(source[k]) {
		return string(source[k]), k + 1, nil
	}
	for k < len(source) && isASCIILetter(source[k]) {
		k++
	}
	name := source[j+1 : k]
	if sym, ok := commands[name]; ok {
		return sym.value, k, nil
	}
	return "", 0, errors.New(`不支持的分隔符 \` + name)
}

// matrix is a rendered matrix environment.
type matrix struct {
	rows   [][]string
	fenced string // "p" for ( ), "b" for [ ], "" for none
}

func (m *matrix) writeTo(b *strings.Builder) {
	if m.fenced != "" {
		b.WriteString("<mrow>")
		if m.fenced == "p" {
			b.WriteString(`<mo fence="true">(</mo>`)
		} else {
			b.WriteString(`<mo fence="true">[</mo>`)
		}
	}
	b.WriteString("<mtable>")
	for _, row := range m.rows {
		b.WriteString("<mtr>")
		for _, cell := range row {
			b.WriteString("<mtd>")
			if nodes, err := parseExpr(cell); err == nil {
				w := &walker{out: b}
				w.renderList(nodes)
			} else {
				b.WriteString("<mtext>")
				b.WriteString(escape(cell))
				b.WriteString("</mtext>")
			}
			b.WriteString("</mtd>")
		}
		b.WriteString("</mtr>")
	}
	b.WriteString("</mtable>")
	if m.fenced != "" {
		if m.fenced == "p" {
			b.WriteString(`<mo fence="true">)</mo>`)
		} else {
			b.WriteString(`<mo fence="true">]</mo>`)
		}
		b.WriteString("</mrow>")
	}
}

// readMatrix reads a matrix environment and returns the rendered form plus the
// bytes consumed.
func readMatrix(source string) (*matrix, int, error) {
	open := strings.Index(source, `\begin{`)
	if open < 0 {
		return nil, 0, errors.New("矩阵语法无效")
	}
	closeBrace := strings.IndexByte(source[open:], '}')
	if closeBrace < 0 {
		return nil, 0, errors.New("矩阵语法无效")
	}
	env := source[open+len(`\begin{`) : open+closeBrace]
	end := strings.Index(source, `\end{`+env+`}`)
	if end < 0 {
		return nil, 0, errors.New(`矩阵缺少 \end`)
	}
	body := source[open+closeBrace+1 : end]
	total := end + len(`\end{`+env+`}`)

	fenced := ""
	switch env {
	case "pmatrix":
		fenced = "p"
	case "bmatrix":
		fenced = "b"
	}

	var rows [][]string
	for _, line := range strings.Split(body, `\\`) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cells := strings.Split(line, "&")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return nil, 0, errors.New("矩阵为空")
	}
	return &matrix{rows: rows, fenced: fenced}, total, nil
}

// validate rejects input the library would otherwise accept silently:
// unbalanced braces and a repeated script on the same base.
func validate(source string) error {
	depth := 0
	for i := 0; i < len(source); i++ {
		switch source[i] {
		case '\\':
			i++ // an escaped character is never a delimiter
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return errors.New("公式有多余的右括号")
			}
		}
	}
	if depth != 0 {
		return errors.New("公式缺少右括号")
	}

	// Reject "x^2^3" and "x_i_j". A script attaches to the base before it, and
	// the characters making up that script must not count as a new base, or the
	// repeat would go unnoticed.
	seen := map[byte]bool{}
	inScript := false
	scriptDepth := 0
	for i := 0; i < len(source); i++ {
		ch := source[i]
		switch {
		case ch == '\\':
			i++
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
		case ch == '^' || ch == '_':
			if seen[ch] && !inScript {
				if ch == '^' {
					return errors.New("公式有重复的上标")
				}
				return errors.New("公式有重复的下标")
			}
			if !inScript {
				seen[ch] = true
			}
			inScript = true
			scriptDepth = 0
		case ch == '{':
			if inScript {
				scriptDepth++
			} else {
				seen = map[byte]bool{}
			}
		case ch == '}':
			if inScript && scriptDepth > 0 {
				scriptDepth--
				if scriptDepth == 0 {
					inScript = false
				}
			} else {
				seen = map[byte]bool{}
				inScript = false
			}
		case ch == '+' || ch == '-' || ch == '=' || ch == '(' || ch == ')' ||
			ch == ',' || ch == ';' || ch == '&' || ch == '|':
			seen = map[byte]bool{}
			inScript = false
		default:
			if !inScript {
				seen = map[byte]bool{}
			}
			if inScript && scriptDepth == 0 {
				inScript = false
			}
		}
	}
	return nil
}

// knownMacro reports whether the library or this package can render a command.
func knownMacro(name string) bool {
	switch name {
	case "text", "textrm", "mbox", "hbox", "left", "right", "begin", "end",
		"matrix", "pmatrix", "bmatrix", "frac", "dfrac", "tfrac", "cfrac",
		"binom", "dbinom", "tbinom", "sqrt", "overline", "bar", "underline",
		"mathbf", "textbf", "boldsymbol", "bm", "mathit", "textit", "mathrm",
		"operatorname", "limits", "nolimits",
		"mathbb", "mathcal", "mathfrak":
		return true
	}
	if _, ok := commands[name]; ok {
		return true
	}
	return functions[name]
}

func isASCIILetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
