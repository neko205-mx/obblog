package typecho

import (
	"bytes"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var (
	errSyntax  = errors.New("Typecho SQL 格式无效或备份不完整")
	errMode    = errors.New("不支持包含特殊 SQL_MODE 或动态操作的 Typecho SQL 备份")
	modeName   = regexp.MustCompile(`(?i)\bSQL_MODE\b`)
	badMode    = regexp.MustCompile(`(?i)\b(NO_BACKSLASH_ESCAPES|ANSI_QUOTES)\b`)
	dynamicSQL = regexp.MustCompile(`(?i)^[\t\n\r ]*[0-9]*[\t\n\r ]*(INSERT|REPLACE|UPDATE|DELETE|TRUNCATE|RENAME|LOAD)\b`)
)

// These are tokens for a data-file reader, not tokens for an SQL interpreter.
type token struct {
	kind byte // 'w' for bare words, quote character for quoted data, otherwise punctuation
	raw  []byte
}

func (t token) is(word string) bool { return t.kind == 'w' && bytes.EqualFold(t.raw, []byte(word)) }

type lexer struct {
	raw []byte
	pos int
}

func space(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}
func wordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '$' || b >= 128
}

func (l *lexer) next() (token, error) {
	s := l.raw
	for l.pos < len(s) {
		i := l.pos
		b := s[i]
		switch {
		case space(b):
			l.pos++
		case b == '#' || b == '-' && i+2 < len(s) && s[i+1] == '-' && s[i+2] <= ' ':
			if end := bytes.IndexByte(s[i:], '\n'); end >= 0 {
				l.pos = i + end + 1
			} else {
				l.pos = len(s)
			}
		case b == '/' && i+1 < len(s) && s[i+1] == '*':
			end := bytes.Index(s[i+2:], []byte("*/"))
			if end < 0 {
				return token{}, errSyntax
			}
			body := s[i+2 : i+2+end]
			if bytes.HasPrefix(body, []byte("!")) || bytes.HasPrefix(body, []byte("M!")) {
				body = bytes.TrimPrefix(bytes.TrimPrefix(body, []byte("M")), []byte("!"))
				if modeName.Match(body) && badMode.Match(body) || dynamicSQL.Match(body) {
					return token{}, errMode
				}
			}
			l.pos = i + end + 4
		case b == '\'' || b == '"' || b == '`':
			for j := i + 1; j < len(s); j++ {
				if s[j] == '\\' && b != '`' {
					j++
					continue
				}
				if s[j] != b {
					continue
				}
				if j+1 < len(s) && s[j+1] == b {
					j++
					continue
				}
				l.pos = j + 1
				return token{b, s[i+1 : j]}, nil
			}
			return token{}, errSyntax
		case wordByte(b):
			l.pos++
			for l.pos < len(s) && wordByte(s[l.pos]) {
				l.pos++
			}
			return token{'w', s[i:l.pos]}, nil
		default:
			l.pos++
			return token{b, s[i:l.pos]}, nil
		}
	}
	return token{}, nil
}

func decodeQuoted(raw []byte, quote byte) string {
	if !bytes.ContainsAny(raw, string([]byte{'\\', quote})) {
		return string(raw)
	}
	var out strings.Builder
	out.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == quote && i+1 < len(raw) && raw[i+1] == quote {
			i++
		} else if c == '\\' && quote != '`' && i+1 < len(raw) {
			i++
			c = raw[i]
			switch c {
			case '0':
				c = 0
			case 'b':
				c = '\b'
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'Z':
				c = 26
			case '%', '_':
				out.WriteByte('\\')
			}
		}
		out.WriteByte(c)
	}
	return out.String()
}

func identifier(t token) (string, bool) {
	if (t.kind != 'w' && t.kind != '`') || len(t.raw) == 0 || len(t.raw) > 512 || bytes.IndexByte(t.raw, 0) >= 0 {
		return "", false
	}
	if t.kind == '`' {
		return decodeQuoted(t.raw, '`'), true
	}
	return string(t.raw), true
}

type tableID struct{ database, name string }

type statement struct {
	create bool
	table  tableID
	tail   []byte // Tokens after the table identifier (without the terminating semicolon).
}

// The full scan verifies quoting, comments and termination before any records
// can be returned. Every callback error causes Parse to discard its results.
func walkStatements(raw []byte, visit func(statement) error) error {
	l := lexer{raw: raw}
	var database string
	for {
		first, err := l.next()
		if err != nil {
			return err
		}
		if first.kind == 0 {
			return nil
		}
		if first.kind == ';' {
			continue
		}
		// Only unquoted statement keywords below use this span.
		start := l.pos - len(first.raw)
		for {
			end, err := l.next()
			if err != nil {
				return err
			}
			if end.kind == 0 {
				return errSyntax
			}
			if end.kind != ';' {
				continue
			}
			s := raw[start : l.pos-1]
			if first.is("SET") {
				if modeName.Match(s) && badMode.Match(s) {
					return errMode
				}
			} else if first.is("USE") {
				cursor := lexer{raw: s}
				_, _ = cursor.next()
				t, _ := cursor.next()
				name, ok := identifier(t)
				rest, _ := cursor.next()
				if !ok || rest.kind != 0 {
					return errSyntax
				}
				database = name
			} else if first.is("INSERT") || first.is("REPLACE") || first.is("CREATE") {
				head, ok, err := tableStatement(s, database)
				if err != nil {
					return err
				}
				if ok {
					if err := visit(head); err != nil {
						return err
					}
				}
			} else if first.is("UPDATE") || first.is("DELETE") || first.is("TRUNCATE") || first.is("RENAME") || first.is("LOAD") || first.is("DELIMITER") {
				// A backup is a snapshot represented by literal rows, not an
				// incremental script that changes the visibility of those rows.
				return errMode
			}
			break
		}
	}
}

func tableStatement(raw []byte, database string) (statement, bool, error) {
	l := lexer{raw: raw}
	first, _ := l.next() // walkStatements has verified the token stream.
	t, _ := l.next()
	create := first.is("CREATE")
	if create {
		if !t.is("TABLE") {
			return statement{}, false, nil
		}
		t, _ = l.next()
		if t.is("IF") {
			not, _ := l.next()
			exists, _ := l.next()
			if !not.is("NOT") || !exists.is("EXISTS") {
				return statement{}, false, errSyntax
			}
			t, _ = l.next()
		}
	} else {
		for t.is("IGNORE") || t.is("LOW_PRIORITY") || t.is("HIGH_PRIORITY") || t.is("DELAYED") {
			t, _ = l.next()
		}
		if !t.is("INTO") {
			return statement{}, false, errSyntax
		}
		t, _ = l.next()
	}
	name, ok := identifier(t)
	if !ok {
		return statement{}, false, errSyntax
	}
	end := l.pos
	dot, _ := l.next()
	if dot.kind == '.' {
		database = name
		t, _ := l.next()
		name, ok = identifier(t)
		if !ok {
			return statement{}, false, errSyntax
		}
		end = l.pos
	}
	return statement{create, tableID{database, name}, raw[end:]}, true, nil
}

const maxColumns = 128

// createColumns reads only the first identifier of each column definition.
// Nested parentheses and commas in types or expressions cannot add columns.
func createColumns(raw []byte) ([]string, error) {
	l := lexer{raw: raw}
	open, _ := l.next()
	if open.kind != '(' {
		return nil, errSyntax
	}
	var cols []string
	seen := map[string]bool{}
	for {
		first, _ := l.next()
		name, ok := identifier(first)
		if !ok {
			return nil, errSyntax
		}
		constraint := first.is("PRIMARY") || first.is("UNIQUE") || first.is("KEY") || first.is("INDEX") || first.is("CONSTRAINT") || first.is("CHECK") || first.is("FOREIGN") || first.is("FULLTEXT") || first.is("SPATIAL")
		if !constraint {
			name = strings.ToLower(name)
			if len(cols) >= maxColumns || seen[name] {
				return nil, errSyntax
			}
			seen[name] = true
			cols = append(cols, name)
		}
		depth := 0
		for {
			t, _ := l.next()
			switch t.kind {
			case 0:
				return nil, errSyntax
			case '(':
				depth++
			case ')':
				if depth == 0 {
					if len(cols) == 0 {
						return nil, errSyntax
					}
					return cols, nil
				}
				depth--
			}
			if depth == 0 && t.kind == ',' {
				break
			}
		}
	}
}

// literal retains a view of the raw SQL token. Private title and text fields
// are never decoded or copied unless the record passes public eligibility.
type literal struct {
	token
	sign byte
}

func (v literal) text() (string, bool) {
	if v.kind != '\'' && v.kind != '"' {
		return "", false
	}
	return decodeQuoted(v.raw, v.kind), true
}
func (v literal) integer() (int64, bool) {
	if len(v.raw) > 64 {
		return 0, false
	}
	var s string
	if v.kind == 'w' {
		s = string(v.raw)
	} else if text, ok := v.text(); ok {
		s = text
	} else {
		return 0, false
	}
	if v.sign != 0 {
		s = string(v.sign) + s
	}
	value, err := strconv.ParseInt(s, 10, 64)
	return value, err == nil
}
func (v literal) absent() bool {
	return v.is("NULL") || (v.kind == '\'' || v.kind == '"') && len(v.raw) == 0
}

func readLiteral(l *lexer) (literal, error) {
	t, _ := l.next()
	if t.kind == '\'' || t.kind == '"' || t.is("NULL") {
		return literal{token: t}, nil
	}
	var sign byte
	if t.kind == '+' || t.kind == '-' {
		sign = t.kind
		t, _ = l.next()
	}
	if t.kind != 'w' || len(t.raw) == 0 {
		return literal{}, errSyntax
	}
	for _, b := range t.raw {
		if b < '0' || b > '9' {
			return literal{}, errSyntax
		}
	}
	return literal{t, sign}, nil
}

func walkRows(raw []byte, schema, required []string, visit func(row []literal, index map[string]int) error) error {
	l := lexer{raw: raw}
	t, _ := l.next()
	cols := schema
	if t.kind == '(' {
		cols = nil
		for {
			t, _ := l.next()
			name, ok := identifier(t)
			if !ok || len(cols) >= maxColumns {
				return errSyntax
			}
			cols = append(cols, strings.ToLower(name))
			sep, _ := l.next()
			if sep.kind == ')' {
				break
			}
			if sep.kind != ',' {
				return errSyntax
			}
		}
		t, _ = l.next()
	}
	if !t.is("VALUES") && !t.is("VALUE") {
		return errSyntax
	}
	index := make(map[string]int, len(cols))
	for i, col := range cols {
		if _, exists := index[col]; exists {
			return errSyntax
		}
		index[col] = i
	}
	for _, col := range required {
		if _, exists := index[col]; !exists {
			return errSyntax
		}
	}
	row := make([]literal, len(cols))
	for {
		open, _ := l.next()
		if open.kind != '(' {
			return errSyntax
		}
		for i := range row {
			value, err := readLiteral(&l)
			if err != nil {
				return err
			}
			row[i] = value
			sep, _ := l.next()
			if i+1 == len(row) && sep.kind != ')' || i+1 < len(row) && sep.kind != ',' {
				return errSyntax
			}
		}
		if err := visit(row, index); err != nil {
			return err
		}
		sep, _ := l.next()
		if sep.kind == 0 {
			return nil
		}
		if sep.kind != ',' {
			return errSyntax
		}
	}
}
