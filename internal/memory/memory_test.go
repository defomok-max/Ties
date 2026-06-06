package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectWalksUpAndOrders(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "CLAUDE.md"), []byte("leaf rules"), 0o644); err != nil {
		t.Fatal(err)
	}

	docs := Collect(sub, "")
	if len(docs) != 2 {
		t.Fatalf("want 2 docs, got %d: %+v", len(docs), docs)
	}
	// Outermost (root) first, nearest (leaf) last.
	if docs[0].Content != "root rules" {
		t.Errorf("first doc should be root, got %q", docs[0].Content)
	}
	if docs[1].Content != "leaf rules" {
		t.Errorf("last doc should be leaf, got %q", docs[1].Content)
	}
}

func TestCollectFirstNameWins(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("agents"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("claude"), 0o644); err != nil {
		t.Fatal(err)
	}
	docs := Collect(root, "")
	if len(docs) != 1 || docs[0].Content != "agents" {
		t.Fatalf("AGENTS.md should win, got %+v", docs)
	}
}

func TestCollectGlobalLowestPrecedence(t *testing.T) {
	global := t.TempDir()
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(global, "AGENTS.md"), []byte("global"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "AGENTS.md"), []byte("project"), 0o644); err != nil {
		t.Fatal(err)
	}
	docs := Collect(proj, global)
	if len(docs) != 2 {
		t.Fatalf("want 2 docs, got %d", len(docs))
	}
	if docs[0].Content != "global" || docs[len(docs)-1].Content != "project" {
		t.Errorf("global should be first, project last: %+v", docs)
	}
}

func TestCollectSkipsEmptyAndMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("   \n  "), 0o644); err != nil {
		t.Fatal(err)
	}
	if docs := Collect(root, ""); len(docs) != 0 {
		t.Fatalf("blank file should be skipped, got %+v", docs)
	}
}

func TestRender(t *testing.T) {
	out := Render([]Doc{{Path: "/x/AGENTS.md", Content: "hi"}})
	if !strings.Contains(out, "/x/AGENTS.md") || !strings.Contains(out, "hi") {
		t.Fatalf("render missing parts: %q", out)
	}
	if Render(nil) != "" {
		t.Fatal("empty render should be empty")
	}
}
