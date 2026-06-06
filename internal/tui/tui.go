// Package tui implements an optional full-screen terminal interface for the
// ties chat command. It uses the alternate screen buffer and an immediate-mode
// repaint model (no raw keyboard handling), so it stays robust and
// dependency-free while providing a fixed header, a scrolling transcript with
// syntax highlighting, and a live status bar with token/cost metering.
package tui

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/defomok-max/Ties/internal/ui"
)

const (
	altScreenOn  = "\x1b[?1049h"
	altScreenOff = "\x1b[?1049l"
	cursorHome   = "\x1b[H"
	hideCursor   = "\x1b[?25l"
	showCursor   = "\x1b[?25h"
	clearLineEOL = "\x1b[K"

	minWidth  = 20
	minHeight = 8
)

// entryKind classifies a transcript entry.
type entryKind int

const (
	entryUser entryKind = iota
	entryAssistant
	entryTool
	entryError
	entryNote
)

type entry struct {
	kind   entryKind
	text   string
	detail string
}

// Model is the pure, testable state of the screen. Render turns it into a
// frame string; it performs no I/O.
type Model struct {
	theme   ui.Theme
	color   bool
	width   int
	height  int
	entries []entry
	// live is the in-progress assistant message (streamed token by token).
	live    string
	hasLive bool

	model   string
	session string
	tokIn   int
	tokOut  int
	cost    float64
	hasCost bool

	working bool
	spinner int

	// scroll is the number of body lines scrolled up from the bottom.
	scroll int
}

// NewModel builds a Model with the given theme and dimensions.
func NewModel(theme ui.Theme, color bool, width, height int) *Model {
	return &Model{theme: theme, color: color, width: clampMin(width, minWidth), height: clampMin(height, minHeight)}
}

func (m *Model) sgr(code, s string) string { return ui.SGR(code, s, m.color) }

// Resize updates the dimensions.
func (m *Model) Resize(width, height int) {
	m.width = clampMin(width, minWidth)
	m.height = clampMin(height, minHeight)
}

// SetMeta sets the header model/session identifiers.
func (m *Model) SetMeta(model, session string) { m.model, m.session = model, session }

// SetUsage updates the status-bar token/cost figures.
func (m *Model) SetUsage(in, out int, cost float64, hasCost bool) {
	m.tokIn, m.tokOut, m.cost, m.hasCost = in, out, cost, hasCost
}

// SetWorking toggles the spinner state.
func (m *Model) SetWorking(on bool) { m.working = on }

// Tick advances the spinner frame.
func (m *Model) Tick() { m.spinner++ }

// AddUser appends a user turn.
func (m *Model) AddUser(s string) { m.append(entry{kind: entryUser, text: s}) }

// AddTool appends a tool-invocation line.
func (m *Model) AddTool(name, detail string) {
	m.append(entry{kind: entryTool, text: name, detail: detail})
}

// AddError appends an error line.
func (m *Model) AddError(s string) { m.append(entry{kind: entryError, text: s}) }

// AddNote appends a dim informational line.
func (m *Model) AddNote(s string) { m.append(entry{kind: entryNote, text: s}) }

// AppendAssistant adds a streamed delta to the live assistant message.
func (m *Model) AppendAssistant(delta string) {
	m.live += delta
	m.hasLive = true
	m.scroll = 0
}

// EndAssistant commits the live assistant message to the transcript.
func (m *Model) EndAssistant() {
	if m.hasLive {
		if strings.TrimSpace(m.live) != "" {
			m.append(entry{kind: entryAssistant, text: m.live})
		}
		m.live = ""
		m.hasLive = false
	}
}

// Clear empties the transcript and any in-progress assistant message.
func (m *Model) Clear() {
	m.entries = nil
	m.live = ""
	m.hasLive = false
	m.scroll = 0
}

// LiveEmpty reports whether the in-progress assistant message has no
// meaningful content yet.
func (m *Model) LiveEmpty() bool { return strings.TrimSpace(m.live) == "" }

func (m *Model) append(e entry) {
	m.entries = append(m.entries, e)
	m.scroll = 0 // jump to bottom on new content
}

// ScrollUp/ScrollDown move the viewport within the scrollback.
func (m *Model) ScrollUp(n int) { m.scroll += n }
func (m *Model) ScrollDown(n int) {
	m.scroll -= n
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// bodyLines renders every transcript entry to styled, wrapped display lines.
func (m *Model) bodyLines() []string {
	hl := newHighlighter(m.theme, m.color)
	var lines []string
	for _, e := range m.entries {
		lines = append(lines, m.renderEntry(e, hl)...)
	}
	if m.hasLive {
		lines = append(lines, m.renderEntry(entry{kind: entryAssistant, text: m.live}, hl)...)
	}
	return lines
}

// renderEntry returns the styled, wrapped display lines for one entry.
func (m *Model) renderEntry(e entry, hl highlighter) []string {
	var out []string
	add := func(s string) {
		out = append(out, wrap(s, m.width)...)
	}
	switch e.kind {
	case entryUser:
		add(m.sgr(m.theme.User, "❯ ") + e.text)
	case entryAssistant:
		styled := hl.Render(e.text)
		for _, ln := range strings.Split(styled, "\n") {
			out = append(out, wrap(ln, m.width)...)
		}
	case entryTool:
		line := m.sgr(m.theme.Tool, ui.ToolIcon(e.text)+" "+e.text)
		if e.detail != "" {
			line += m.sgr(m.theme.Dim, "  "+e.detail)
		}
		add(line)
	case entryError:
		add(m.sgr(m.theme.Error, "✗ "+e.text))
	case entryNote:
		add(m.sgr(m.theme.Dim, e.text))
	}
	return out
}

// Render returns the full frame: header, viewport, status bar and prompt. The
// cursor is left at the prompt so cooked-mode input echoes in place.
func (m *Model) Render() string {
	rows := make([]string, m.height)

	// Header (rows 0-2).
	title := m.sgr(m.theme.Accent, "ties") + m.sgr(m.theme.Dim, "  ·  terminal AI coding agent")
	meta := m.sgr(m.theme.Heading, "model ") + m.model
	if m.session != "" {
		meta += m.sgr(m.theme.Dim, "   session ") + short(m.session)
	}
	rows[0] = title
	rows[1] = meta
	rows[2] = m.sgr(m.theme.Dim, strings.Repeat("─", m.width))

	bodyTop := 3
	statusRow := m.height - 2
	inputRow := m.height - 1
	bodyHeight := statusRow - bodyTop
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	body := m.bodyLines()
	// Apply scrollback offset, clamped so we never scroll past the top.
	maxScroll := maxInt(0, len(body)-bodyHeight)
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	end := len(body) - m.scroll
	start := maxInt(0, end-bodyHeight)
	window := body[start:end]

	// Bottom-align the window within the body region.
	pad := bodyHeight - len(window)
	for i := 0; i < bodyHeight; i++ {
		row := bodyTop + i
		if i < pad {
			rows[row] = ""
		} else {
			rows[row] = window[i-pad]
		}
	}

	rows[statusRow] = m.statusBar()
	rows[inputRow] = m.sgr(m.theme.Accent, "❯ ")

	var b strings.Builder
	b.WriteString(cursorHome)
	for i, r := range rows {
		b.WriteString(padTo(r, m.width))
		b.WriteString(clearLineEOL)
		if i < len(rows)-1 {
			b.WriteString("\r\n")
		}
	}
	// Leave the cursor just after the prompt on the input row.
	b.WriteString(fmt.Sprintf("\x1b[%d;3H", inputRow+1))
	return b.String()
}

// statusBar renders the bottom metering line, including the spinner.
func (m *Model) statusBar() string {
	left := ""
	if m.working {
		frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		left = m.sgr(m.theme.Accent, string(frames[m.spinner%len(frames)])) + " " + m.sgr(m.theme.Dim, "working…")
	} else {
		left = m.sgr(m.theme.Dim, "ready")
	}

	tok := fmt.Sprintf("%s in / %s out", group(m.tokIn), group(m.tokOut))
	if m.hasCost {
		tok += fmt.Sprintf("  ·  $%.4f", m.cost)
	}
	hint := "PgUp/PgDn scroll · /help · /exit"

	right := m.sgr(m.theme.Dim, tok+"   "+hint)
	gap := m.width - ui.DisplayWidth(left) - ui.DisplayWidth(right)
	if gap < 1 {
		// Drop the hint when cramped.
		right = m.sgr(m.theme.Dim, tok)
		gap = m.width - ui.DisplayWidth(left) - ui.DisplayWidth(right)
	}
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// --- Screen: the thin I/O wrapper around Model ------------------------------

// Screen drives a Model against a terminal, owning the alternate-screen
// lifecycle, size detection and a repaint spinner goroutine.
type Screen struct {
	m   *Model
	out io.Writer
	tty *os.File

	mu      sync.Mutex
	started bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewScreen builds a Screen writing to out, sizing from tty (which may be nil,
// in which case defaults/env are used).
func NewScreen(out io.Writer, tty *os.File, theme ui.Theme, color bool) *Screen {
	w, h := resolveSize(tty)
	return &Screen{m: NewModel(theme, color, w, h), out: out, tty: tty}
}

// Model exposes the underlying model for the caller to push events.
func (s *Screen) Model() *Model { return s.m }

// Start enters the alternate screen and begins the spinner repaint loop.
func (s *Screen) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	s.started = true
	io.WriteString(s.out, altScreenOn+hideCursor) //nolint:errcheck
	s.repaintLocked()
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	go s.spin()
}

// Stop leaves the alternate screen and restores the cursor.
func (s *Screen) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	close(s.stopCh)
	done := s.doneCh
	s.mu.Unlock()
	<-done
	io.WriteString(s.out, showCursor+altScreenOff) //nolint:errcheck
}

func (s *Screen) spin() {
	defer close(s.doneCh)
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			s.mu.Lock()
			if s.m.working {
				s.m.Tick()
				s.repaintLocked()
			}
			s.mu.Unlock()
		}
	}
}

// Update mutates the model under the screen lock and repaints, so callers on
// other goroutines (agent callbacks) never race the spinner's render.
func (s *Screen) Update(fn func(*Model)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.m)
	s.repaintLocked()
}

// Repaint refreshes the frame (after resizing or pushing events).
func (s *Screen) Repaint() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, h := resolveSize(s.tty); w != s.m.width || h != s.m.height {
		s.m.Resize(w, h)
	}
	s.repaintLocked()
}

func (s *Screen) repaintLocked() {
	if !s.started {
		return
	}
	io.WriteString(s.out, s.m.Render()) //nolint:errcheck
}

// --- helpers ----------------------------------------------------------------

// resolveSize returns the terminal dimensions, falling back to COLUMNS/LINES
// then a sane 80x24 default.
func resolveSize(f *os.File) (int, int) {
	if c, r, ok := terminalSize(f); ok {
		return c, r
	}
	c := envInt("COLUMNS", 80)
	r := envInt("LINES", 24)
	return c, r
}

func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// group inserts thousands separators into a non-negative integer.
func group(n int) string {
	s := strconv.Itoa(n)
	if n < 1000 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",")
}

func clampMin(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
