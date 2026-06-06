package tui

import (
	"strings"
	"testing"

	"github.com/defomok-max/Ties/internal/ui"
)

func newTestModel() *Model {
	return NewModel(ui.ResolveTheme("dark"), false, 40, 12)
}

// frameRows splits a rendered frame into its display rows (ignoring the leading
// cursor-home and the trailing cursor-position escape).
func frameRows(frame string) []string {
	frame = strings.TrimPrefix(frame, cursorHome)
	// drop the trailing "\x1b[..;3H" cursor move
	if i := strings.LastIndex(frame, "\x1b["); i >= 0 {
		frame = frame[:i]
	}
	rows := strings.Split(frame, "\r\n")
	for i, r := range rows {
		rows[i] = strings.ReplaceAll(r, clearLineEOL, "")
	}
	return rows
}

func TestRenderHasFixedHeight(t *testing.T) {
	m := newTestModel()
	rows := frameRows(m.Render())
	if len(rows) != 12 {
		t.Fatalf("expected 12 rows, got %d", len(rows))
	}
}

func TestRenderHeaderAndStatus(t *testing.T) {
	m := newTestModel()
	m.SetMeta("claude-x", "sess-1234567890ab")
	m.SetUsage(1500, 250, 0.0021, true)
	rows := frameRows(m.Render())
	if !strings.Contains(rows[0], "ties") {
		t.Fatalf("header row missing title: %q", rows[0])
	}
	if !strings.Contains(rows[1], "claude-x") {
		t.Fatalf("meta row missing model: %q", rows[1])
	}
	status := rows[len(rows)-2]
	if !strings.Contains(status, "1,500 in / 250 out") {
		t.Fatalf("status missing grouped tokens: %q", status)
	}
	if !strings.Contains(status, "$0.0021") {
		t.Fatalf("status missing cost: %q", status)
	}
}

func TestRenderShowsTranscriptBottomAligned(t *testing.T) {
	m := newTestModel()
	m.AddUser("hello there")
	m.AppendAssistant("hi! ")
	m.AppendAssistant("how can I help?")
	rows := frameRows(m.Render())
	body := strings.Join(rows, "\n")
	if !strings.Contains(body, "hello there") {
		t.Fatalf("user line missing: %q", body)
	}
	if !strings.Contains(body, "how can I help?") {
		t.Fatalf("assistant live text missing: %q", body)
	}
}

func TestEndAssistantCommitsLive(t *testing.T) {
	m := newTestModel()
	m.AppendAssistant("partial answer")
	if m.LiveEmpty() {
		t.Fatal("live should not be empty")
	}
	m.EndAssistant()
	if !m.LiveEmpty() {
		t.Fatal("live should be empty after EndAssistant")
	}
	if len(m.entries) != 1 || m.entries[0].kind != entryAssistant {
		t.Fatalf("assistant entry not committed: %+v", m.entries)
	}
}

func TestScrollClampsAndShowsOlderLines(t *testing.T) {
	m := NewModel(ui.ResolveTheme("dark"), false, 40, 10)
	for i := 0; i < 30; i++ {
		m.AddNote("line-" + string(rune('A'+i%26)) + "-" + itoa(i))
	}
	// Scroll far past the top; it should clamp without panicking.
	m.ScrollUp(1000)
	_ = m.Render() // clamps m.scroll
	rows := frameRows(m.Render())
	if len(rows) != 10 {
		t.Fatalf("rows = %d", len(rows))
	}
	body := strings.Join(rows, "\n")
	if !strings.Contains(body, "line-A-0") {
		t.Fatalf("scrolling to top should reveal the first line: %q", body)
	}
	// Scrolling back down to the bottom shows the latest line.
	m.ScrollDown(1000)
	rows = frameRows(m.Render())
	if !strings.Contains(strings.Join(rows, "\n"), itoa(29)) {
		t.Fatal("bottom should show the most recent line")
	}
}

func TestClearEmptiesTranscript(t *testing.T) {
	m := newTestModel()
	m.AddUser("x")
	m.AppendAssistant("y")
	m.Clear()
	if len(m.entries) != 0 || !m.LiveEmpty() {
		t.Fatal("Clear did not reset state")
	}
}

func TestGroupThousands(t *testing.T) {
	cases := map[int]string{0: "0", 42: "42", 1000: "1,000", 1234567: "1,234,567"}
	for in, want := range cases {
		if got := group(in); got != want {
			t.Fatalf("group(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveSizeFallback(t *testing.T) {
	// nil tty → env/default path; we just assert sane positive numbers.
	w, h := resolveSize(nil)
	if w < minWidth || h < 1 {
		t.Fatalf("resolveSize fallback = %dx%d", w, h)
	}
}

// itoa avoids importing strconv in the test for a tiny helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
