package tui

import "github.com/defomok-max/Ties/internal/ui"

// Wrap breaks a single logical line into display lines no wider than width
// printable columns, preserving ANSI escapes and word boundaries. It is the
// exported entry point to the package's internal line wrapper so other
// packages (the interactive chat screen) can reuse it.
func Wrap(line string, width int) []string { return wrap(line, width) }

// PadTo pads (or truncates) s to exactly width printable columns.
func PadTo(s string, width int) string { return padTo(s, width) }

// Truncate shortens s to at most width printable columns, keeping ANSI escapes.
func Truncate(s string, width int) string { return truncate(s, width) }

// RenderMarkdown applies lightweight markdown/code syntax highlighting to text
// using the given theme. It is the exported entry point to the package's
// internal highlighter.
func RenderMarkdown(theme ui.Theme, color bool, text string) string {
	return newHighlighter(theme, color).Render(text)
}
