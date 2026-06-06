package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// TodoItem is a single planned task.
type TodoItem struct {
	Text   string `json:"text"`
	Status string `json:"status"` // pending | in_progress | done
}

// todoTool holds an in-memory task list for the current run so the model can
// plan multi-step work and track progress. State is process-local (not
// persisted): the model rewrites the whole list each time.
type todoTool struct {
	mu    sync.Mutex
	items []TodoItem
	// onChange is invoked after every mutation so the UI can render the list.
	onChange func([]TodoItem)
}

func newTodoTool(onChange func([]TodoItem)) *todoTool {
	return &todoTool{onChange: onChange}
}

func (t *todoTool) Name() string { return "todo" }
func (t *todoTool) Description() string {
	return "Maintain a short task list for the current goal. action=set replaces the whole list; action=list returns it. Use it to plan multi-step work and mark items done as you go."
}
func (t *todoTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["set","list"],"description":"set replaces the list; list returns it"},"items":{"type":"array","items":{"type":"object","properties":{"text":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","done"]}},"required":["text"]}}},"required":["action"]}`)
}

func (t *todoTool) Run(_ context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		Action string     `json:"action"`
		Items  []TodoItem `json:"items"`
	}
	if err := decode(args, &a); err != nil {
		return Result{}, err
	}
	t.mu.Lock()
	switch a.Action {
	case "set":
		norm := make([]TodoItem, 0, len(a.Items))
		for _, it := range a.Items {
			if strings.TrimSpace(it.Text) == "" {
				continue
			}
			if it.Status == "" {
				it.Status = "pending"
			}
			norm = append(norm, it)
		}
		t.items = norm
	case "list", "":
		// no mutation
	default:
		t.mu.Unlock()
		return Result{Content: "unknown action: " + a.Action, IsError: true}, nil
	}
	items := append([]TodoItem{}, t.items...)
	onChange := t.onChange
	t.mu.Unlock()

	if a.Action == "set" && onChange != nil {
		onChange(items)
	}
	return Result{Content: renderTodos(items)}, nil
}

func renderTodos(items []TodoItem) string {
	if len(items) == 0 {
		return "(todo list is empty)"
	}
	var b strings.Builder
	done := 0
	for _, it := range items {
		mark := "[ ]"
		switch it.Status {
		case "done":
			mark = "[x]"
			done++
		case "in_progress":
			mark = "[~]"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, it.Text)
	}
	fmt.Fprintf(&b, "(%d/%d done)", done, len(items))
	return b.String()
}
