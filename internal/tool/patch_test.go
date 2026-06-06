package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func runTool(t *testing.T, tl Tool, args any) Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	res, err := tl.Run(context.Background(), json.RawMessage(raw))
	if err != nil {
		t.Fatalf("%s run: %v", tl.Name(), err)
	}
	return res
}

func TestMultiedit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "f.txt")
	if err := os.WriteFile(path, []byte("alpha beta gamma"), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := newMultieditTool(root)
	res := runTool(t, tl, map[string]any{
		"path": "f.txt",
		"edits": []map[string]any{
			{"old": "alpha", "new": "ALPHA"},
			{"old": "gamma", "new": "GAMMA"},
		},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "ALPHA beta GAMMA" {
		t.Fatalf("got %q", got)
	}
}

func TestMultieditAtomicOnFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "f.txt")
	orig := "one two three"
	_ = os.WriteFile(path, []byte(orig), 0o644)
	tl := newMultieditTool(root)
	res := runTool(t, tl, map[string]any{
		"path": "f.txt",
		"edits": []map[string]any{
			{"old": "one", "new": "ONE"},
			{"old": "MISSING", "new": "X"},
		},
	})
	if !res.IsError {
		t.Fatal("expected error for missing edit")
	}
	got, _ := os.ReadFile(path)
	if string(got) != orig {
		t.Fatalf("file mutated despite failure: %q", got)
	}
}

func TestPatchApply(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "greet.txt")
	_ = os.WriteFile(path, []byte("hello\nworld\nbye\n"), 0o644)
	diff := "--- a/greet.txt\n+++ b/greet.txt\n@@ -1,3 +1,3 @@\n hello\n-world\n+WORLD\n bye\n"
	tl := newPatchTool(root)
	res := runTool(t, tl, map[string]any{"diff": diff})
	if res.IsError {
		t.Fatalf("patch failed: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello\nWORLD\nbye\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPatchAddAndDeleteLines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "list.txt")
	_ = os.WriteFile(path, []byte("a\nb\nc\n"), 0o644)
	diff := "--- a/list.txt\n+++ b/list.txt\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n+b2\n c\n"
	res := runTool(t, newPatchTool(root), map[string]any{"diff": diff})
	if res.IsError {
		t.Fatalf("patch failed: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "a\nB\nb2\nc\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPatchCreateFile(t *testing.T) {
	root := t.TempDir()
	diff := "--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1,2 @@\n+line1\n+line2\n"
	res := runTool(t, newPatchTool(root), map[string]any{"diff": diff})
	if res.IsError {
		t.Fatalf("patch create failed: %s", res.Content)
	}
	got, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "line1\nline2\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPatchRejectsBadContext(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "x.txt")
	_ = os.WriteFile(path, []byte("real content\n"), 0o644)
	diff := "--- a/x.txt\n+++ b/x.txt\n@@ -1,1 +1,1 @@\n-does not match\n+replacement\n"
	res := runTool(t, newPatchTool(root), map[string]any{"diff": diff})
	if !res.IsError {
		t.Fatal("expected error when context does not match")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "real content\n" {
		t.Fatalf("file mutated on failed patch: %q", got)
	}
}

func TestPatchEscapesRootDenied(t *testing.T) {
	root := t.TempDir()
	diff := "--- a/../escape.txt\n+++ b/../escape.txt\n@@ -0,0 +1,1 @@\n+x\n"
	res := runTool(t, newPatchTool(root), map[string]any{"diff": diff})
	if !res.IsError {
		t.Fatal("expected path-escape to be denied")
	}
}
