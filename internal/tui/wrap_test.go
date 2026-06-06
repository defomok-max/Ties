package tui

import (
	"strings"
	"testing"

	"github.com/defomok-max/Ties/internal/ui"
)

func TestWrapShortLineUnchanged(t *testing.T) {
	got := wrap("hello world", 40)
	if len(got) != 1 || got[0] != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestWrapBreaksOnWords(t *testing.T) {
	got := wrap("the quick brown fox jumps", 10)
	for _, l := range got {
		if ui.DisplayWidth(l) > 10 {
			t.Fatalf("line %q exceeds width 10", l)
		}
	}
	if strings.Join(got, " ") != "the quick brown fox jumps" {
		// joining wrapped pieces should reconstruct the words (spaces may differ)
		joined := strings.Join(got, "")
		if !strings.Contains(joined, "quick") || !strings.Contains(joined, "jumps") {
			t.Fatalf("lost content: %q", got)
		}
	}
}

func TestWrapHardBreaksLongWord(t *testing.T) {
	got := wrap("supercalifragilistic", 6)
	if len(got) < 3 {
		t.Fatalf("expected multiple chunks, got %q", got)
	}
	for _, l := range got {
		if ui.DisplayWidth(l) > 6 {
			t.Fatalf("chunk %q exceeds width", l)
		}
	}
	if strings.Join(got, "") != "supercalifragilistic" {
		t.Fatalf("hard break lost characters: %q", got)
	}
}

func TestWrapPreservesANSIWidth(t *testing.T) {
	styled := ui.SGR("1;38;5;81", "colored text here", true)
	got := wrap(styled, 8)
	for _, l := range got {
		if ui.DisplayWidth(l) > 8 {
			t.Fatalf("ANSI line %q printable width exceeds 8", l)
		}
	}
}

func TestPadTo(t *testing.T) {
	if got := padTo("hi", 5); got != "hi   " {
		t.Fatalf("padTo = %q", got)
	}
	if got := padTo("hello world", 5); ui.DisplayWidth(got) != 5 {
		t.Fatalf("padTo truncate width = %d", ui.DisplayWidth(got))
	}
}

func TestTruncateKeepsANSIAndResets(t *testing.T) {
	styled := ui.SGR("31", "abcdefgh", true)
	got := truncate(styled, 4)
	if ui.DisplayWidth(got) != 4 {
		t.Fatalf("width = %d (%q)", ui.DisplayWidth(got), got)
	}
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("truncated styled string must end with reset: %q", got)
	}
}
