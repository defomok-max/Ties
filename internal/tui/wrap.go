package tui

import (
	"strings"

	"github.com/defomok-max/Ties/internal/ui"
)

// wrap breaks a single logical line into display lines no wider than width
// printable columns, preserving ANSI escape sequences and breaking on word
// boundaries where possible (falling back to hard breaks for long words). A
// width <= 0 disables wrapping.
func wrap(line string, width int) []string {
	if width <= 0 || ui.DisplayWidth(line) <= width {
		return []string{line}
	}

	var out []string
	var cur strings.Builder
	curW := 0
	flush := func() {
		out = append(out, cur.String())
		cur.Reset()
		curW = 0
	}

	for _, word := range splitKeepSpace(line) {
		ww := ui.DisplayWidth(word)
		switch {
		case curW == 0 && ww > width:
			// A single token wider than the line: hard-break it.
			for _, piece := range hardBreak(word, width) {
				if curW > 0 {
					flush()
				}
				cur.WriteString(piece)
				curW = ui.DisplayWidth(piece)
				if curW >= width {
					flush()
				}
			}
		case curW+ww > width:
			flush()
			if strings.TrimSpace(word) == "" {
				continue // don't start a wrapped line with spaces
			}
			cur.WriteString(word)
			curW = ww
		default:
			cur.WriteString(word)
			curW += ww
		}
	}
	if cur.Len() > 0 || len(out) == 0 {
		flush()
	}
	return out
}

// splitKeepSpace splits s into alternating word / whitespace runs so that the
// wrapper can keep interior spacing intact.
func splitKeepSpace(s string) []string {
	var parts []string
	var b strings.Builder
	var inSpace bool
	started := false
	for _, r := range s {
		sp := r == ' ' || r == '\t'
		if started && sp != inSpace {
			parts = append(parts, b.String())
			b.Reset()
		}
		b.WriteRune(r)
		inSpace = sp
		started = true
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

// hardBreak splits a token with no break opportunities into width-sized chunks,
// honoring ANSI escapes (which have zero printable width).
func hardBreak(s string, width int) []string {
	var out []string
	var b strings.Builder
	w := 0
	inEsc := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc = true
			b.WriteRune(r)
		case inEsc:
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
		default:
			if w >= width {
				out = append(out, b.String())
				b.Reset()
				w = 0
			}
			b.WriteRune(r)
			w++
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// padTo right-pads s with spaces to exactly width printable columns, or
// truncates (preserving ANSI) when it is too long.
func padTo(s string, width int) string {
	w := ui.DisplayWidth(s)
	if w == width {
		return s
	}
	if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return truncate(s, width)
}

// truncate shortens s to at most width printable columns, keeping ANSI escapes
// and appending a reset so styling never leaks past the cut.
func truncate(s string, width int) string {
	if ui.DisplayWidth(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	inEsc := false
	sawEsc := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc = true
			sawEsc = true
			b.WriteRune(r)
		case inEsc:
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
		default:
			if w >= width {
				if sawEsc {
					b.WriteString("\x1b[0m")
				}
				return b.String()
			}
			b.WriteRune(r)
			w++
		}
	}
	return b.String()
}
