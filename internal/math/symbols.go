package math

// symbol is one MathML element for a LaTeX command or character.
type symbol struct {
	// tag is the MathML element, e.g. "mi", "mo", "mn".
	tag string
	// value is the text content, already unescaped.
	value string
	// stretchy marks large operators that take limits above and below.
	stretchy bool
	// fence marks delimiters that scale with \left and \right.
	fence bool
}

// commands maps a LaTeX command (without the backslash) to its MathML output.
// The subset follows what Obsidian exposes through MathJax for everyday notes;
// anything absent is rendered as its literal source.
var commands = map[string]symbol{
	// Greek lower case.
	"alpha": {"mi", "α", false, false}, "beta": {"mi", "β", false, false},
	"gamma": {"mi", "γ", false, false}, "delta": {"mi", "δ", false, false},
	"epsilon": {"mi", "ϵ", false, false}, "varepsilon": {"mi", "ε", false, false},
	"zeta": {"mi", "ζ", false, false}, "eta": {"mi", "η", false, false},
	"theta": {"mi", "θ", false, false}, "vartheta": {"mi", "ϑ", false, false},
	"iota": {"mi", "ι", false, false}, "kappa": {"mi", "κ", false, false},
	"lambda": {"mi", "λ", false, false}, "mu": {"mi", "μ", false, false},
	"nu": {"mi", "ν", false, false}, "xi": {"mi", "ξ", false, false},
	"pi": {"mi", "π", false, false}, "varpi": {"mi", "ϖ", false, false},
	"rho": {"mi", "ρ", false, false}, "sigma": {"mi", "σ", false, false},
	"tau": {"mi", "τ", false, false}, "upsilon": {"mi", "υ", false, false},
	"phi": {"mi", "ϕ", false, false}, "varphi": {"mi", "φ", false, false},
	"chi": {"mi", "χ", false, false}, "psi": {"mi", "ψ", false, false},
	"omega": {"mi", "ω", false, false},

	// Greek upper case.
	"Gamma": {"mi", "Γ", false, false}, "Delta": {"mi", "Δ", false, false},
	"Theta": {"mi", "Θ", false, false}, "Lambda": {"mi", "Λ", false, false},
	"Xi": {"mi", "Ξ", false, false}, "Pi": {"mi", "Π", false, false},
	"Sigma": {"mi", "Σ", false, false}, "Upsilon": {"mi", "Υ", false, false},
	"Phi": {"mi", "Φ", false, false}, "Psi": {"mi", "Ψ", false, false},
	"Omega": {"mi", "Ω", false, false},

	// Binary operators.
	"times": {"mo", "×", false, false}, "div": {"mo", "÷", false, false},
	"cdot": {"mo", "⋅", false, false}, "pm": {"mo", "±", false, false},
	"mp": {"mo", "∓", false, false}, "ast": {"mo", "∗", false, false},
	"star": {"mo", "⋆", false, false}, "circ": {"mo", "∘", false, false},
	"bullet": {"mo", "∙", false, false}, "oplus": {"mo", "⊕", false, false},
	"otimes": {"mo", "⊗", false, false}, "cup": {"mo", "∪", false, false},
	"cap": {"mo", "∩", false, false}, "setminus": {"mo", "∖", false, false},
	"wedge": {"mo", "∧", false, false}, "vee": {"mo", "∨", false, false},
	"land": {"mo", "∧", false, false}, "lor": {"mo", "∨", false, false},

	// Relations.
	"leq": {"mo", "≤", false, false}, "le": {"mo", "≤", false, false},
	"geq": {"mo", "≥", false, false}, "ge": {"mo", "≥", false, false},
	"neq": {"mo", "≠", false, false}, "ne": {"mo", "≠", false, false},
	"approx": {"mo", "≈", false, false}, "equiv": {"mo", "≡", false, false},
	"sim": {"mo", "∼", false, false}, "simeq": {"mo", "≃", false, false},
	"cong": {"mo", "≅", false, false}, "propto": {"mo", "∝", false, false},
	"ll": {"mo", "≪", false, false}, "gg": {"mo", "≫", false, false},
	"subset": {"mo", "⊂", false, false}, "supset": {"mo", "⊃", false, false},
	"subseteq": {"mo", "⊆", false, false}, "supseteq": {"mo", "⊇", false, false},
	"in": {"mo", "∈", false, false}, "notin": {"mo", "∉", false, false},

	// Arrows.
	"to": {"mo", "→", false, false}, "rightarrow": {"mo", "→", false, false},
	"leftarrow": {"mo", "←", false, false}, "gets": {"mo", "←", false, false},
	"leftrightarrow": {"mo", "↔", false, false},
	"Rightarrow":     {"mo", "⇒", false, false}, "Leftarrow": {"mo", "⇐", false, false},
	"Leftrightarrow": {"mo", "⇔", false, false},
	"mapsto":         {"mo", "↦", false, false}, "implies": {"mo", "⟹", false, false},
	"iff": {"mo", "⟺", false, false},

	// Big operators; these take limits above and below.
	"sum": {"mo", "∑", true, false}, "prod": {"mo", "∏", true, false},
	"coprod": {"mo", "∐", true, false}, "int": {"mo", "∫", true, false},
	"iint": {"mo", "∬", true, false}, "iiint": {"mo", "∭", true, false},
	"oint": {"mo", "∮", true, false}, "bigcup": {"mo", "⋃", true, false},
	"bigcap": {"mo", "⋂", true, false},

	// Miscellaneous.
	"infty": {"mo", "∞", false, false}, "partial": {"mo", "∂", false, false},
	"nabla": {"mo", "∇", false, false}, "forall": {"mo", "∀", false, false},
	"exists": {"mo", "∃", false, false}, "emptyset": {"mo", "∅", false, false},
	"varnothing": {"mo", "∅", false, false}, "neg": {"mo", "¬", false, false},
	"lnot": {"mo", "¬", false, false}, "angle": {"mo", "∠", false, false},
	"degree": {"mo", "°", false, false}, "prime": {"mo", "′", false, false},
	"ldots": {"mo", "…", false, false}, "cdots": {"mo", "⋯", false, false},
	"dots": {"mo", "…", false, false}, "vdots": {"mo", "⋮", false, false},
	"hbar": {"mi", "ℏ", false, false}, "ell": {"mi", "ℓ", false, false},
	"Re": {"mi", "ℜ", false, false}, "Im": {"mi", "ℑ", false, false},
	"aleph":     {"mi", "ℵ", false, false},
	"therefore": {"mo", "∴", false, false}, "because": {"mo", "∵", false, false},
	"triangle": {"mo", "△", false, false},
	"mid":      {"mo", "∣", false, false}, "parallel": {"mo", "∥", false, false},
	"perp": {"mo", "⊥", false, false},

	// Delimiters used with \left and \right.
	"{": {"mo", "{", false, true}, "}": {"mo", "}", false, true},
	"lbrace": {"mo", "{", false, true}, "rbrace": {"mo", "}", false, true},
	"lbrack": {"mo", "[", false, true}, "rbrack": {"mo", "]", false, true},
	"langle": {"mo", "⟨", false, true}, "rangle": {"mo", "⟩", false, true},
	"lceil": {"mo", "⌈", false, true}, "rceil": {"mo", "⌉", false, true},
	"lfloor": {"mo", "⌊", false, true}, "rfloor": {"mo", "⌋", false, true},
	"vert": {"mo", "|", false, true}, "Vert": {"mo", "‖", false, true},
	"|": {"mo", "‖", false, true},

	// Spacing commands, emitted as spacing elements.
	",":     {"mspace", "0.1667em", false, false},
	":":     {"mspace", "0.2222em", false, false},
	";":     {"mspace", "0.2778em", false, false},
	"!":     {"mspace", "-0.1667em", false, false},
	"quad":  {"mspace", "1em", false, false},
	"qquad": {"mspace", "2em", false, false},
}

// functions are LaTeX commands that take a name argument and set it upright,
// such as \sin. They are rendered as an operator so spacing is natural.
var functions = map[string]bool{
	"sin": true, "cos": true, "tan": true, "cot": true, "sec": true, "csc": true,
	"arcsin": true, "arccos": true, "arctan": true,
	"sinh": true, "cosh": true, "tanh": true,
	"log": true, "ln": true, "exp": true, "lg": true,
	"lim": true, "max": true, "min": true, "sup": true, "inf": true,
	"det": true, "dim": true, "gcd": true, "ker": true, "hom": true,
	"arg": true, "deg": true, "mod": true, "bmod": true,
}

// limitFunctions take their subscripts underneath rather than to the side.
var limitFunctions = map[string]bool{
	"lim": true, "max": true, "min": true, "sup": true, "inf": true,
	"det": true, "gcd": true, "limsup": true, "liminf": true,
}

// singleCharSymbols maps ASCII punctuation that MathJax renders as an operator.
var singleCharSymbols = map[string]bool{
	"+": true, "-": true, "=": true, "<": true, ">": true,
	"/": true, "*": true, ":": true, "|": true,
	"(": true, ")": true, "[": true, "]": true,
	"!": true, "?": true, "%": true, "#": true, "&": true,
	";": true, ",": true, ".": true, "@": true,
}
