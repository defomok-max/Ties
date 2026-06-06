package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTreeToolBasic(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "src"))
	mustMkdir(t, filepath.Join(root, ".git"))
	mustMkdir(t, filepath.Join(root, "node_modules"))
	mustWrite(t, filepath.Join(root, "README.md"), "x")
	mustWrite(t, filepath.Join(root, "src", "main.go"), "x")

	tr := newTreeTool(root)
	res, err := tr.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	out := res.Content
	if !strings.Contains(out, "src/") || !strings.Contains(out, "main.go") {
		t.Errorf("tree missing dir/file:\n%s", out)
	}
	if !strings.Contains(out, "README.md") {
		t.Errorf("tree missing README:\n%s", out)
	}
	if strings.Contains(out, ".git") || strings.Contains(out, "node_modules") {
		t.Errorf("noise dirs should be hidden by default:\n%s", out)
	}
	// Directories should sort before files at the same level.
	if strings.Index(out, "src/") > strings.Index(out, "README.md") {
		t.Errorf("dirs should precede files:\n%s", out)
	}
}

func TestTreeToolAllAndDepth(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "a", "b"))
	mustWrite(t, filepath.Join(root, "a", "b", "deep.txt"), "x")
	mustWrite(t, filepath.Join(root, ".hidden"), "x")

	tr := newTreeTool(root)
	// depth 1 should not reach deep.txt
	res, _ := tr.Run(context.Background(), json.RawMessage(`{"depth":1}`))
	if strings.Contains(res.Content, "deep.txt") {
		t.Errorf("depth 1 should not show deep.txt:\n%s", res.Content)
	}
	// all=true reveals hidden files
	res2, _ := tr.Run(context.Background(), json.RawMessage(`{"all":true}`))
	if !strings.Contains(res2.Content, ".hidden") {
		t.Errorf("all=true should show hidden:\n%s", res2.Content)
	}
}

func TestTreeToolConfine(t *testing.T) {
	root := t.TempDir()
	tr := newTreeTool(root)
	res, _ := tr.Run(context.Background(), json.RawMessage(`{"path":"../.."}`))
	if !res.IsError {
		t.Fatal("escaping root should error")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
