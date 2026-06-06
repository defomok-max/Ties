// Package ui provides a small, dependency-free terminal styling toolkit:
// themes, color detection, and rendering helpers (banner, headings, tool
// lines, spinner, boxes, diffs) used by the ties CLI to look good without
// pulling in any third-party library.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const esc = "\x1b["

// Theme is a named palette of ANSI SGR codes (without the surrounding escape).
type Theme struct {
	Name      string
	Heading   string
	Accent    string
	User      string
	Assistant string
	Tool      string
	Dim       string
	Error     string
	Success   string
	Warn      string
}

// themes holds the built-in palettes.
var themes = map[string]Theme{
	"dark": {
		Name: "dark", Heading: "1;38;5;81", Accent: "38;5;213", User: "1;38;5;75",
		Assistant: "0", Tool: "38;5;245", Dim: "2;38;5;245", Error: "1;38;5;203",
		Success: "38;5;78", Warn: "38;5;221",
	},
	"light": {
		Name: "light", Heading: "1;38;5;25", Accent: "38;5;128", User: "1;38;5;26",
		Assistant: "0", Tool: "38;5;240", Dim: "2;38;5;240", Error: "1;38;5;160",
		Success: "38;5;28", Warn: "38;5;130",
	},
	"mono": {
		Name: "mono", Heading: "1", Accent: "1", User: "1", Assistant: "0",
		Tool: "2", Dim: "2", Error: "1", Success: "1", Warn: "1",
	},
}

// ResolveTheme returns the theme for name, defaulting to dark. "auto" maps to
// dark (we cannot reliably detect terminal background without extra deps).
func ResolveTheme(name string) Theme {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "light":
		return themes["light"]
	case "mono", "none", "plain":
		return themes["mono"]
	default:
		return themes["dark"]
	}
}

// ColorEnabled reports whether ANSI color should be emitted on f, honoring
// NO_COLOR, FORCE_COLOR / CLICOLOR_FORCE and TTY detection.
func ColorEnabled(f *os.File) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" || os.Getenv("CLICOLOR_FORCE") != "" {
		return true
	}
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Printer renders styled output to a writer.
type Printer struct {
	w     io.Writer
	theme Theme
	color bool
}

// New builds a Printer for writer w using the named theme. forceColor of nil
// means auto-detect from w when it is an *os.File.
func New(w io.Writer, themeName string, color bool) *Printer {
	return &Printer{w: w, theme: ResolveTheme(themeName), color: color}
}

// Theme returns the active theme.
func (p *Printer) Theme() Theme { return p.theme }

// ColorOn reports whether color is enabled.
func (p *Printer) ColorOn() bool { return p.color }

// style wraps s in the SGR code unless color is disabled or code is empty.
func (p *Printer) style(code, s string) string {
	return SGR(code, s, p.color)
}

// SGR wraps s in an ANSI SGR sequence when on is true and code is non-empty.
// It is the package-level form of the Printer's styling, reused by the tui
// package so both render identically.
func SGR(code, s string, on bool) string {
	if !on || code == "" || code == "0" {
		return s
	}
	return esc + code + "m" + s + esc + "0m"
}

// DisplayWidth returns the printable width of s, ignoring ANSI escape
// sequences. Exposed for layout code in other packages.
func DisplayWidth(s string) int { return displayWidth(s) }

// ToolIcon returns the glyph used to mark a tool invocation.
func ToolIcon(name string) string { return toolIcon(name) }

// Sprint helpers return styled strings.
func (p *Printer) Heading(s string) string { return p.style(p.theme.Heading, s) }
func (p *Printer) Accent(s string) string  { return p.style(p.theme.Accent, s) }
func (p *Printer) Dim(s string) string     { return p.style(p.theme.Dim, s) }
func (p *Printer) Err(s string) string     { return p.style(p.theme.Error, s) }
func (p *Printer) Success(s string) string { return p.style(p.theme.Success, s) }
func (p *Printer) Warn(s string) string    { return p.style(p.theme.Warn, s) }
func (p *Printer) Tool(s string) string    { return p.style(p.theme.Tool, s) }

// Print/Printf/Println write to the underlying writer.
func (p *Printer) Print(s string)            { _, _ = io.WriteString(p.w, s) }
func (p *Printer) Printf(f string, a ...any) { _, _ = fmt.Fprintf(p.w, f, a...) }
func (p *Printer) Println(s string)          { _, _ = io.WriteString(p.w, s+"\n") }

// Banner prints the ties wordmark and a subtitle.
func (p *Printer) Banner(subtitle string) {
	art := "" +
		"  _   _           \n" +
		" | |_(_) ___  ___ \n" +
		" | __| |/ _ \\/ __|\n" +
		" | |_| |  __/\\__ \\\n" +
		"  \\__|_|\\___||___/\n"
	p.Print(p.Accent(art))
	if subtitle != "" {
		p.Println(" " + p.Dim(subtitle))
	}
}

// Rule prints a horizontal divider of the given width (default 48).
func (p *Printer) Rule(width int) {
	if width <= 0 {
		width = 48
	}
	p.Println(p.Dim(strings.Repeat("─", width)))
}

// ToolLine renders a tool invocation line, e.g. "· bash  go build ./...".
func (p *Printer) ToolLine(name, detail string) {
	icon := toolIcon(name)
	line := fmt.Sprintf("%s %s", icon, name)
	if detail != "" {
		line += "  " + detail
	}
	p.Println(p.Tool(line))
}

// ErrorLine renders a failed tool/result line.
func (p *Printer) ErrorLine(s string) { p.Println(p.Err("  ✗ " + s)) }

// Box draws a rounded box around the lines of body under an optional title.
func (p *Printer) Box(title, body string) {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	width := len(title)
	for _, l := range lines {
		if w := displayWidth(l); w > width {
			width = w
		}
	}
	width += 2
	top := "╭" + strings.Repeat("─", width) + "╮"
	if title != "" {
		t := " " + title + " "
		top = "╭─" + t + strings.Repeat("─", maxInt(0, width-len(t)-1)) + "╮"
	}
	p.Println(p.Dim(top))
	for _, l := range lines {
		pad := width - 1 - displayWidth(l)
		if pad < 0 {
			pad = 0
		}
		p.Println(p.Dim("│") + " " + l + strings.Repeat(" ", pad) + p.Dim("│"))
	}
	p.Println(p.Dim("╰" + strings.Repeat("─", width) + "╯"))
}

// Diff renders a minimal red/green line diff of old vs new.
func (p *Printer) Diff(oldText, newText string) {
	for _, l := range strings.Split(strings.TrimRight(oldText, "\n"), "\n") {
		if l == "" {
			continue
		}
		p.Println(p.Err("- " + l))
	}
	for _, l := range strings.Split(strings.TrimRight(newText, "\n"), "\n") {
		if l == "" {
			continue
		}
		p.Println(p.Success("+ " + l))
	}
}

// Spinner is a lightweight braille spinner driven by a goroutine.
type Spinner struct {
	p      *Printer
	label  string
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	active bool
}

// StartSpinner begins an animated spinner with the given label. When color is
// off (non-TTY) it is a no-op so logs stay clean.
func (p *Printer) StartSpinner(label string) *Spinner {
	s := &Spinner{p: p, label: label, stop: make(chan struct{}), done: make(chan struct{})}
	if !p.color {
		close(s.done)
		return s
	}
	s.active = true
	go s.run()
	return s
}

func (s *Spinner) run() {
	defer close(s.done)
	frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	t := time.NewTicker(90 * time.Millisecond)
	defer t.Stop()
	i := 0
	for {
		select {
		case <-s.stop:
			s.p.Print("\r\x1b[K")
			return
		case <-t.C:
			frame := string(frames[i%len(frames)])
			s.p.Printf("\r%s %s", s.p.Accent(frame), s.p.Dim(s.label))
			i++
		}
	}
}

// Stop ends the spinner and clears its line.
func (s *Spinner) Stop() {
	s.once.Do(func() {
		if s.active {
			close(s.stop)
		}
		<-s.done
	})
}

func toolIcon(name string) string {
	switch name {
	case "bash":
		return "❯"
	case "read", "list", "glob", "grep":
		return "○"
	case "write", "edit":
		return "✎"
	case "skill":
		return "★"
	default:
		return "·"
	}
}

// displayWidth approximates printable width, ignoring ANSI sequences.
func displayWidth(s string) int {
	n, inEsc := 0, false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case inEsc:
			// skip
		default:
			n++
		}
	}
	return n
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
