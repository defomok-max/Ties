package tool

import (
	"strings"
	"testing"
)

func TestTodoSetAndList(t *testing.T) {
	var seen []TodoItem
	tl := newTodoTool(func(items []TodoItem) { seen = items })

	res := runTool(t, tl, map[string]any{
		"action": "set",
		"items": []map[string]any{
			{"text": "design", "status": "done"},
			{"text": "build", "status": "in_progress"},
			{"text": "test"},
		},
	})
	if res.IsError {
		t.Fatalf("set failed: %s", res.Content)
	}
	if len(seen) != 3 {
		t.Fatalf("onChange got %d items", len(seen))
	}
	if seen[2].Status != "pending" {
		t.Fatalf("default status not applied: %q", seen[2].Status)
	}
	if !strings.Contains(res.Content, "[x] design") || !strings.Contains(res.Content, "1/3 done") {
		t.Fatalf("render wrong: %s", res.Content)
	}

	list := runTool(t, tl, map[string]any{"action": "list"})
	if !strings.Contains(list.Content, "[~] build") {
		t.Fatalf("list render wrong: %s", list.Content)
	}
}

func TestTodoUnknownAction(t *testing.T) {
	tl := newTodoTool(nil)
	res := runTool(t, tl, map[string]any{"action": "frobnicate"})
	if !res.IsError {
		t.Fatal("expected error for unknown action")
	}
}
