package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandMentions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := expandMentions(root, "please look at @main.go and fix it")
	if !strings.Contains(out, "package main") {
		t.Errorf("file contents not inlined:\n%s", out)
	}
	if !strings.Contains(out, "Referenced files:") {
		t.Errorf("missing reference header:\n%s", out)
	}
	if !strings.Contains(out, "@main.go") {
		t.Errorf("original mention should be preserved:\n%s", out)
	}
}

func TestExpandMentionsTrailingPunct(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("AAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := expandMentions(root, "see @a.txt.")
	if !strings.Contains(out, "AAA") {
		t.Errorf("trailing dot should not break resolution:\n%s", out)
	}
}

func TestExpandMentionsIgnoresUnknownAndEscapes(t *testing.T) {
	root := t.TempDir()
	// non-existent file → unchanged
	in := "ping @nope.go now"
	if got := expandMentions(root, in); got != in {
		t.Errorf("unknown mention should be unchanged, got %q", got)
	}
	// escaping the root is rejected
	if got := expandMentions(root, "read @../../etc/passwd"); strings.Contains(got, "Referenced files") {
		t.Errorf("path escape should not be inlined: %q", got)
	}
	// an email-like token at start without whitespace is not a mention
	if got := expandMentions(root, "user@example.com"); got != "user@example.com" {
		t.Errorf("email should be untouched, got %q", got)
	}
}

func TestExpandMentionsDedup(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("ONLYONCE"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := expandMentions(root, "@x.txt and again @x.txt")
	if strings.Count(out, "ONLYONCE") != 1 {
		t.Errorf("duplicate mention should inline once:\n%s", out)
	}
}
