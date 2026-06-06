package cli

import (
	"strings"
	"testing"

	"github.com/defomok-max/Ties/internal/screen"
	"github.com/defomok-max/Ties/internal/ui"
)

func newTestChat() *chatUI {
	return &chatUI{theme: ui.ResolveTheme("dark"), color: false, width: 80, height: 24, model: "anthropic/claude-3-5-sonnet"}
}

func key(k screen.Key) screen.Event { return screen.Event{Kind: screen.EventKey, Key: k} }
func rkey(r rune) screen.Event {
	return screen.Event{Kind: screen.EventKey, Key: screen.KeyRune, Rune: r}
}

func feed(c *chatUI, evs ...screen.Event) (submit bool, line string) {
	for _, e := range evs {
		submit, line = c.handleKeyLocked(e)
	}
	return
}

func TestChatTypingAndSubmit(t *testing.T) {
	c := newTestChat()
	feed(c, rkey('h'), rkey('i'))
	if got := string(c.input); got != "hi" {
		t.Fatalf("input = %q, want hi", got)
	}
	if c.cur != 2 {
		t.Fatalf("cursor = %d, want 2", c.cur)
	}
	submit, line := feed(c, key(screen.KeyEnter))
	if !submit || line != "hi" {
		t.Fatalf("submit=%v line=%q, want true/hi", submit, line)
	}
	if len(c.input) != 0 || c.cur != 0 {
		t.Fatalf("input not cleared after submit: %q cur=%d", string(c.input), c.cur)
	}
}

func TestChatCursorEditing(t *testing.T) {
	c := newTestChat()
	feed(c, rkey('a'), rkey('c')) // "ac"
	feed(c, key(screen.KeyLeft))  // cursor between a and c
	feed(c, rkey('b'))            // "abc"
	if got := string(c.input); got != "abc" {
		t.Fatalf("input = %q, want abc", got)
	}
	feed(c, key(screen.KeyHome), key(screen.KeyDelete)) // delete 'a' -> "bc"
	if got := string(c.input); got != "bc" {
		t.Fatalf("after delete = %q, want bc", got)
	}
	feed(c, key(screen.KeyEnd), key(screen.KeyBackspace)) // -> "b"
	if got := string(c.input); got != "b" {
		t.Fatalf("after backspace = %q, want b", got)
	}
}

func TestChatSlashPopup(t *testing.T) {
	c := newTestChat()
	feed(c, rkey('/'))
	if !c.popup || len(c.popMatch) == 0 {
		t.Fatalf("popup should open after '/'")
	}
	feed(c, rkey('m')) // "/m" -> /model
	found := false
	for _, m := range c.popMatch {
		if m.name == "/model" {
			found = true
		}
	}
	if !found {
		t.Fatalf("/m should match /model, got %+v", c.popMatch)
	}
	// Tab completes the selected command and closes the popup.
	c.popupSel = 0
	want := c.popMatch[0].name + " "
	feed(c, key(screen.KeyTab))
	if string(c.input) != want {
		t.Fatalf("after tab input = %q, want %q", string(c.input), want)
	}
	if c.popup {
		t.Fatalf("popup should close after completion")
	}
	// A space in the buffer disables the popup.
	feed(c, rkey('x'))
	if c.popup {
		t.Fatalf("popup should stay closed once args are typed")
	}
}

func TestChatEscClearsInput(t *testing.T) {
	c := newTestChat()
	feed(c, rkey('a'), rkey('b'))
	feed(c, key(screen.KeyEsc))
	if len(c.input) != 0 {
		t.Fatalf("esc should clear input, got %q", string(c.input))
	}
}

func TestChatWelcomeWhenEmpty(t *testing.T) {
	c := newTestChat()
	lines := c.bodyLines(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Welcome to Ties") {
		t.Fatalf("empty transcript should show welcome, got:\n%s", joined)
	}
}

func TestChatRenderEntries(t *testing.T) {
	c := newTestChat()
	c.entries = []chatEntry{
		{kind: ckUser, text: "build me a thing"},
		{kind: ckAssistant, text: "Sure, here is `code`."},
		{kind: ckTool, text: "bash", detail: "go build ./..."},
	}
	lines := c.bodyLines(80)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"You", "build me a thing", "Ties", "bash"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("body missing %q in:\n%s", want, joined)
		}
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
