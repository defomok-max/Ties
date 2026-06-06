package tui

import (
	"strings"
	"testing"

	"github.com/defomok-max/Ties/internal/ui"
)

func TestHighlightNoColorIsIdentity(t *testing.T) {
	h := newHighlighter(ui.ResolveTheme("dark"), false)
	in := "```go\nfunc main() {}\n```"
	if got := h.Render(in); got != in {
		t.Fatalf("no-color render altered text: %q", got)
	}
}

func TestHighlightColorsKeywordsInFence(t *testing.T) {
	h := newHighlighter(ui.ResolveTheme("dark"), true)
	out := h.Render("```go\nfunc main() { return }\n```")
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("expected ANSI codes in highlighted code")
	}
	// The fence markers should be present and dimmed, code line colored.
	if !strings.Contains(out, "```") {
		t.Fatal("fence markers should be preserved")
	}
}

func TestHighlightProseUntouchedOutsideFence(t *testing.T) {
	h := newHighlighter(ui.ResolveTheme("dark"), true)
	out := h.Render("just some prose with the word func in it")
	// "func" outside a fence must NOT be keyword-colored.
	if strings.Contains(out, ui.SGR(h.theme.Heading, "func", true)) {
		t.Fatal("keyword coloring leaked outside code fence")
	}
}

func TestHighlightInlineCode(t *testing.T) {
	h := newHighlighter(ui.ResolveTheme("dark"), true)
	out := h.Render("use the `read` tool")
	if !strings.Contains(out, "`read`") {
		t.Fatalf("inline code span lost: %q", out)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("inline code should be styled")
	}
}

func TestOutsideString(t *testing.T) {
	// // inside a string is not a comment.
	if got := outsideString(`x := "a // b"`, "//"); got != -1 {
		t.Fatalf("found false comment at %d", got)
	}
	// real comment is found.
	if got := outsideString(`x := 1 // note`, "//"); got != 7 {
		t.Fatalf("comment index = %d", got)
	}
}

func TestCommentStartLanguages(t *testing.T) {
	if commentStart("y = 1 # c", "python") != 6 {
		t.Fatal("python comment not detected")
	}
	if commentStart("x := 1 // c", "go") != 7 {
		t.Fatal("go comment not detected")
	}
	if commentStart("plain text", "text") != -1 {
		t.Fatal("unknown language should have no comment marker")
	}
}

func TestTokensColorsStrings(t *testing.T) {
	h := newHighlighter(ui.ResolveTheme("dark"), true)
	out := h.tokens(`x := "hello"`, "go")
	if !strings.Contains(out, ui.SGR(h.theme.Success, `"hello"`, true)) {
		t.Fatalf("string literal not colored: %q", out)
	}
}
