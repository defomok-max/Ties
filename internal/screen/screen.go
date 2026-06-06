// Package screen provides a tiny, dependency-free terminal abstraction for
// building interactive, mouse-aware full-screen UIs (menus, pickers, settings)
// using only the Go standard library. It handles raw mode, the alternate
// screen buffer, mouse tracking and flicker-free frame painting on Linux,
// macOS and Windows. When the terminal cannot be driven interactively
// (Supported returns false), callers should fall back to a line-based UI.
package screen

import (
	"bufio"
	"os"
)

// Control sequences. Mouse tracking uses normal tracking (1000) + any-motion
// (1003, for hover) with SGR extended coordinates (1006) so clicks and moves
// beyond column 223 are reported correctly.
const (
	altScreenOn  = "\x1b[?1049h"
	altScreenOff = "\x1b[?1049l"
	hideCursor   = "\x1b[?25l"
	showCursor   = "\x1b[?25h"
	cursorHome   = "\x1b[H"
	clearBelow   = "\x1b[J"
	clearLineEOL = "\x1b[K"
	mouseOn      = "\x1b[?1000h\x1b[?1003h\x1b[?1006h"
	mouseOff     = "\x1b[?1006l\x1b[?1003l\x1b[?1000l"
)

// rawRestorer restores the terminal mode captured when raw mode was enabled.
type rawRestorer interface{ restore() error }

// Screen owns an interactive terminal session.
type Screen struct {
	in     *os.File
	out    *os.File
	w      *bufio.Writer
	raw    rawRestorer
	width  int
	height int
	rbuf   []byte
}

// New returns a Screen bound to the given input/output terminals.
func New(in, out *os.File) *Screen {
	return &Screen{in: in, out: out, w: bufio.NewWriter(out), rbuf: make([]byte, 0, 128)}
}

// Supported reports whether both files are real terminals so an interactive
// screen can be started. It does not allocate the screen.
func Supported(in, out *os.File) bool {
	return isTerminal(in) && isTerminal(out)
}

// Start enables raw mode, switches to the alternate screen and turns on mouse
// tracking. Call Stop (typically via defer) to restore the terminal.
func (s *Screen) Start() error {
	rr, err := startRaw(s.in, s.out)
	if err != nil {
		return err
	}
	s.raw = rr
	s.width, s.height = termSize(s.out)
	s.writeStr(altScreenOn + hideCursor + mouseOn)
	s.Flush()
	return nil
}

// Stop turns off mouse tracking, leaves the alternate screen, restores the
// cursor and returns the terminal to its previous mode. It is safe to call
// more than once.
func (s *Screen) Stop() {
	if s.raw == nil {
		return
	}
	s.writeStr(mouseOff + showCursor + altScreenOff)
	s.Flush()
	_ = s.raw.restore()
	s.raw = nil
}

// Size returns the last known terminal dimensions (cols, rows).
func (s *Screen) Size() (int, int) { return s.width, s.height }

// Refresh re-queries the terminal size; returns true if it changed.
func (s *Screen) Refresh() bool {
	w, h := termSize(s.out)
	if w != s.width || h != s.height {
		s.width, s.height = w, h
		return true
	}
	return false
}

// BeginFrame moves the cursor home so a fresh frame can be painted over the
// previous one without a full clear (which would flicker).
func (s *Screen) BeginFrame() { s.writeStr(cursorHome) }

// WriteLine writes one line of the current frame, clearing any leftover
// characters from the previous frame on that row.
func (s *Screen) WriteLine(text string) { s.writeStr(text + clearLineEOL + "\r\n") }

// EndFrame clears everything below the cursor (rows the new frame did not
// reach) and flushes the buffer to the terminal.
func (s *Screen) EndFrame() {
	s.writeStr(clearBelow)
	s.Flush()
}

// Flush writes any buffered output to the terminal.
func (s *Screen) Flush() { _ = s.w.Flush() }

func (s *Screen) writeStr(str string) { _, _ = s.w.WriteString(str) }

// isTerminal reports whether f is attached to a character device (a TTY/console).
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Read blocks for the next batch of input and decodes it into events. A single
// read may yield several events (e.g. a burst of mouse-move reports).
func (s *Screen) Read() ([]Event, error) {
	buf := make([]byte, 128)
	n, err := s.in.Read(buf)
	if err != nil {
		return nil, err
	}
	// Stitch any partial sequence left over from the previous read.
	s.rbuf = append(s.rbuf, buf[:n]...)
	events, consumed := parseEvents(s.rbuf)
	if consumed >= len(s.rbuf) {
		s.rbuf = s.rbuf[:0]
	} else {
		s.rbuf = append(s.rbuf[:0], s.rbuf[consumed:]...)
	}
	return events, nil
}
