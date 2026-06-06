package tui

import (
	"strings"

	"github.com/defomok-max/Ties/internal/ui"
)

// highlighter applies lightweight, dependency-free syntax coloring to assistant
// text. It recognizes fenced code blocks (``` … ```) and inline `code` spans,
// and within code blocks colors comments, strings, numbers and a small set of
// language keywords. It is intentionally heuristic — good enough to make code
// readable, never a full parser.
type highlighter struct {
	theme ui.Theme
	color bool
}

func newHighlighter(theme ui.Theme, color bool) highlighter {
	return highlighter{theme: theme, color: color}
}

// Render returns text with ANSI styling applied. The result may contain
// newlines; callers wrap it afterwards.
func (h highlighter) Render(text string) string {
	if !h.color {
		return text
	}
	lines := strings.Split(text, "\n")
	var out []string
	inFence := false
	lang := ""
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				inFence = true
				lang = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "```")))
			} else {
				inFence = false
				lang = ""
			}
			out = append(out, h.dim(ln))
			continue
		}
		if inFence {
			out = append(out, h.code(ln, lang))
		} else {
			out = append(out, h.prose(ln))
		}
	}
	return strings.Join(out, "\n")
}

func (h highlighter) sgr(code, s string) string { return ui.SGR(code, s, h.color) }
func (h highlighter) dim(s string) string       { return h.sgr(h.theme.Dim, s) }

// prose styles non-code lines: markdown headings, list bullets and inline
// `code` spans get a touch of color.
func (h highlighter) prose(ln string) string {
	trimmed := strings.TrimLeft(ln, " ")
	indent := ln[:len(ln)-len(trimmed)]
	switch {
	case strings.HasPrefix(trimmed, "#"):
		return indent + h.sgr(h.theme.Heading, trimmed)
	case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "):
		return indent + h.sgr(h.theme.Accent, trimmed[:1]) + trimmed[1:]
	}
	return h.inlineCode(ln)
}

// inlineCode colors `backtick` spans within a prose line.
func (h highlighter) inlineCode(ln string) string {
	if !strings.Contains(ln, "`") {
		return ln
	}
	var b strings.Builder
	parts := strings.Split(ln, "`")
	for i, p := range parts {
		if i%2 == 1 && i < len(parts) { // inside a span
			b.WriteString(h.sgr(h.theme.Accent, "`"+p+"`"))
		} else {
			b.WriteString(p)
		}
	}
	// Odd number of backticks: the split above already balanced; if the final
	// segment was an unterminated span we leave it as-is.
	return b.String()
}

var keywords = map[string]map[string]bool{
	"go": set("func", "package", "import", "var", "const", "type", "struct", "interface",
		"return", "if", "else", "for", "range", "switch", "case", "default", "go", "defer",
		"chan", "map", "select", "break", "continue", "nil", "true", "false"),
	"python": set("def", "class", "import", "from", "return", "if", "elif", "else", "for",
		"while", "in", "with", "as", "try", "except", "finally", "lambda", "yield", "pass",
		"None", "True", "False", "and", "or", "not"),
	"js": set("function", "const", "let", "var", "return", "if", "else", "for", "while",
		"class", "import", "export", "from", "async", "await", "new", "this", "null",
		"undefined", "true", "false"),
	"bash": set("if", "then", "else", "elif", "fi", "for", "in", "do", "done", "while",
		"case", "esac", "function", "echo", "export", "local", "return"),
}

func langKeywords(lang string) map[string]bool {
	switch lang {
	case "go", "golang":
		return keywords["go"]
	case "py", "python":
		return keywords["python"]
	case "js", "javascript", "ts", "typescript", "jsx", "tsx":
		return keywords["js"]
	case "sh", "bash", "shell", "zsh":
		return keywords["bash"]
	default:
		return nil
	}
}

// code styles a single line inside a fenced block.
func (h highlighter) code(ln, lang string) string {
	// Comments first — once a comment starts, the rest of the line is dim.
	if idx := commentStart(ln, lang); idx >= 0 {
		return h.tokens(ln[:idx], lang) + h.dim(ln[idx:])
	}
	return h.tokens(ln, lang)
}

// tokens colors keywords, string literals and numbers within a code fragment.
func (h highlighter) tokens(s string, lang string) string {
	kw := langKeywords(lang)
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '"' || c == '\'' || c == '`':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' && j+1 < len(s) {
					j++
				}
				j++
			}
			if j < len(s) {
				j++ // include closing quote
			}
			b.WriteString(h.sgr(h.theme.Success, s[i:j]))
			i = j
		case isIdentStart(c):
			j := i
			for j < len(s) && isIdent(s[j]) {
				j++
			}
			word := s[i:j]
			if kw != nil && kw[word] {
				b.WriteString(h.sgr(h.theme.Heading, word))
			} else {
				b.WriteString(word)
			}
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && (isDigit(s[j]) || s[j] == '.' || s[j] == 'x' || (s[j] >= 'a' && s[j] <= 'f') || (s[j] >= 'A' && s[j] <= 'F')) {
				j++
			}
			b.WriteString(h.sgr(h.theme.Accent, s[i:j]))
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// commentStart returns the byte index where a line comment begins, or -1.
func commentStart(ln, lang string) int {
	switch lang {
	case "go", "golang", "js", "javascript", "ts", "typescript", "jsx", "tsx":
		return outsideString(ln, "//")
	case "py", "python", "sh", "bash", "shell", "zsh", "yaml", "yml", "toml":
		return outsideString(ln, "#")
	default:
		return -1
	}
}

// outsideString finds marker in ln but only when it is not inside a quoted
// string, returning its index or -1.
func outsideString(ln, marker string) int {
	var quote byte
	for i := 0; i < len(ln); i++ {
		c := ln[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if strings.HasPrefix(ln[i:], marker) {
			return i
		}
	}
	return -1
}

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isIdent(c byte) bool      { return isIdentStart(c) || isDigit(c) }
