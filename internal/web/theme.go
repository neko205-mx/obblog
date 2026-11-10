package web

import (
	"fmt"
	"strings"

	"obblog/internal/publication"
)

// fontVariables is shared by every page. They live in theme.css because the
// per-page sheets only reference the stacks; a missing definition invalidates
// the whole `font:` shorthand that uses it.
const fontVariables = `  --serif: "Noto Serif CJK SC", "Source Han Serif SC", "Songti SC", "STSong", Georgia, serif;
  --sans: "LXGW WenKai", "Noto Sans CJK SC", "Source Han Sans SC", "PingFang SC", "Microsoft YaHei", sans-serif;
  --mono: "JetBrains Mono", "Cascadia Code", "Noto Sans Mono", Consolas, monospace;
`

// themeCSS renders the shared palette. Every page — the admin workspace, the
// login and installer screens, the settings page and the public blog — links
// this one stylesheet, so the accent and light/dark choice saved in the panel
// apply everywhere instead of each surface keeping its own fixed colors.
//
// The values are emitted as plain CSS custom properties. Accent input is
// restricted to the fixed palette by publication.SetBlog, so nothing here can
// produce arbitrary CSS.
func themeCSS(blog *publication.Blog) string {
	if blog == nil {
		blog = publication.DefaultBlog()
	}
	accent := blog.Accent
	var b strings.Builder
	b.WriteString("/* Generated from the saved blog configuration. Do not edit. */\n")

	// Light values first, then the dark override, so a page without an explicit
	// data-theme attribute still follows the stored preference.
	light := themeVars(accent, false)
	dark := themeVars(accent, true)

	b.WriteString(":root {\n")
	b.WriteString(fontVariables)
	b.WriteString(light.css())
	b.WriteString("}\n")

	// An explicit data-theme always wins; "auto" leaves the attribute unset and
	// lets the reader's system preference decide.
	b.WriteString("html[data-theme=\"dark\"] {\n")
	b.WriteString(dark.css())
	b.WriteString("}\n")
	b.WriteString("@media (prefers-color-scheme: dark) {\n  html:not([data-theme=\"light\"]) {\n")
	for _, line := range strings.Split(strings.TrimSpace(dark.css()), "\n") {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("  }\n}\n")

	return b.String()
}

type themePalette struct {
	// Workspace surfaces (admin, login, installer, settings).
	bg, surface, sidebar, rail, raised, hover, border, line string
	text, heading, muted, faint, accent, accentBg, code     string
	// Public article surfaces reuse the same accent.
	paper, ink, accentUI, wash, tint, quote, quoteLine, selection, rule string
	dark                                                                bool
}

func (p themePalette) css() string {
	pairs := [][2]string{
		{"--bg", p.bg}, {"--surface", p.surface}, {"--sidebar", p.sidebar},
		{"--rail", p.rail}, {"--raised", p.raised}, {"--hover", p.hover},
		{"--border", p.border}, {"--line", p.line},
		{"--text", p.text}, {"--heading", p.heading}, {"--muted", p.muted}, {"--faint", p.faint},
		{"--accent", p.accent}, {"--accent-bg", p.accentBg}, {"--code", p.code},
		{"--paper", p.paper}, {"--ink", p.ink},
		{"--accent-ui", p.accentUI}, {"--wash", p.wash}, {"--tint", p.tint},
		{"--quote", p.quote}, {"--quote-line", p.quoteLine},
		{"--selection", p.selection}, {"--rule", p.rule},
	}
	var b strings.Builder
	for _, pair := range pairs {
		fmt.Fprintf(&b, "  %s: %s;\n", pair[0], pair[1])
	}
	if p.dark {
		b.WriteString("  color-scheme: dark;\n")
	} else {
		b.WriteString("  color-scheme: light;\n")
	}
	return b.String()
}

// themeVars derives every surface from one accent. The ratios were chosen so
// body text keeps its contrast against its own background in both schemes.
func themeVars(accent string, dark bool) themePalette {
	mix := func(percent int, with string) string {
		return fmt.Sprintf("color-mix(in srgb, %s %d%%, %s)", accent, percent, with)
	}
	if dark {
		return themePalette{
			bg:        mix(8, "#15181a"),
			surface:   mix(10, "#1a1e1f"),
			sidebar:   mix(7, "#141718"),
			rail:      mix(6, "#101314"),
			raised:    mix(14, "#202426"),
			hover:     mix(17, "#242829"),
			border:    mix(20, "#2b3132"),
			line:      mix(14, "#262b2c"),
			text:      mix(12, "#ced2d0"),
			heading:   mix(16, "#edf0e9"),
			muted:     mix(16, "#8b9591"),
			faint:     mix(18, "#6a7671"),
			accent:    mix(55, "#dbe4dc"),
			accentBg:  mix(30, "#1d2422"),
			code:      mix(8, "#15181a"),
			paper:     mix(8, "#15181a"),
			ink:       mix(26, "#e3e7e2"),
			accentUI:  mix(48, "#e3e7e2"),
			wash:      mix(26, "#15181a"),
			tint:      mix(16, "#15181a"),
			quote:     mix(15, "transparent"),
			quoteLine: mix(52, "#15181a"),
			selection: mix(30, "#15181a"),
			rule:      mix(32, "#262b2c"),
			dark:      true,
		}
	}
	return themePalette{
		bg:        mix(6, "#faf9f5"),
		surface:   mix(8, "#f7f6f1"),
		sidebar:   mix(10, "#f1f1ea"),
		rail:      mix(11, "#eaece4"),
		raised:    mix(13, "#eef0e7"),
		hover:     mix(16, "#e9ece2"),
		border:    mix(18, "#d9ddd1"),
		line:      mix(14, "#e5e7df"),
		text:      mix(26, "#3f4a42"),
		heading:   mix(32, "#202b24"),
		muted:     mix(18, "#6f7a70"),
		faint:     mix(20, "#87907f"),
		accent:    mix(72, "#2c4b39"),
		accentBg:  mix(20, "#dfe9db"),
		code:      mix(8, "#f2f2ea"),
		paper:     mix(4, "#faf9f5"),
		ink:       mix(34, "#101312"),
		accentUI:  mix(42, "#101312"),
		wash:      mix(22, "#faf9f5"),
		tint:      mix(13, "#faf9f5"),
		quote:     mix(13, "transparent"),
		quoteLine: mix(62, "#faf9f5"),
		selection: mix(32, "#faf9f5"),
		rule:      mix(26, "#d9ddd1"),
		dark:      false,
	}
}

// themeStylesheet is served at /theme.css and linked by every page. Caching is
// disabled for all responses, so the file always reflects the current setting.
func themeStylesheet(blog *publication.Blog) []byte {
	return []byte(themeCSS(blog))
}
