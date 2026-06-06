package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolveTheme(t *testing.T) {
	if ResolveTheme("light").Name != "light" {
		t.Error("light")
	}
	if ResolveTheme("mono").Name != "mono" {
		t.Error("mono")
	}
	if ResolveTheme("auto").Name != "dark" {
		t.Error("auto should map to dark")
	}
	if ResolveTheme("nonsense").Name != "dark" {
		t.Error("default dark")
	}
}

func TestStyleColorToggle(t *testing.T) {
	var buf bytes.Buffer
	on := New(&buf, "dark", true)
	if !strings.Contains(on.Heading("hi"), "\x1b[") {
		t.Error("expected ANSI codes when color on")
	}
	off := New(&buf, "dark", false)
	if off.Heading("hi") != "hi" {
		t.Errorf("expected plain text when color off, got %q", off.Heading("hi"))
	}
}

func TestToolLineAndDisplayWidth(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, "dark", false)
	p.ToolLine("bash", "go build")
	out := buf.String()
	if !strings.Contains(out, "bash") || !strings.Contains(out, "go build") {
		t.Errorf("tool line missing content: %q", out)
	}
	// ANSI codes must not count toward display width.
	styled := "\x1b[1mhello\x1b[0m"
	if displayWidth(styled) != 5 {
		t.Errorf("displayWidth = %d want 5", displayWidth(styled))
	}
}

func TestBoxRenders(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, "mono", false)
	p.Box("title", "line one\nlonger line two")
	out := buf.String()
	if !strings.Contains(out, "title") || !strings.Contains(out, "longer line two") {
		t.Errorf("box missing content: %q", out)
	}
}

func TestSpinnerNoColorIsNoop(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, "dark", false)
	s := p.StartSpinner("x")
	s.Stop() // must not hang or panic
	if buf.Len() != 0 {
		t.Errorf("spinner wrote output when color off: %q", buf.String())
	}
}
