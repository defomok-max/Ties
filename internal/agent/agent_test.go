package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/defomok-max/Ties/internal/permission"
	"github.com/defomok-max/Ties/internal/provider"
	"github.com/defomok-max/Ties/internal/tool"
)

// mockProvider replays scripted turns of stream events.
type mockProvider struct {
	turns [][]provider.StreamEvent
	calls int
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	var events []provider.StreamEvent
	if m.calls < len(m.turns) {
		events = m.turns[m.calls]
	}
	m.calls++
	go func() {
		defer close(ch)
		for _, ev := range events {
			ch <- ev
		}
		ch <- provider.StreamEvent{Type: provider.EventDone}
	}()
	return ch, nil
}

// pingTool records that it ran.
type pingTool struct{ ran *bool }

func (p *pingTool) Name() string            { return "ping" }
func (p *pingTool) Description() string     { return "test" }
func (p *pingTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (p *pingTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	*p.ran = true
	return tool.Result{Content: "pong"}, nil
}

func TestAgentLoop(t *testing.T) {
	ran := false
	reg := tool.NewRegistry()
	reg.Register(&pingTool{ran: &ran})

	mp := &mockProvider{turns: [][]provider.StreamEvent{
		{{Type: provider.EventToolCall, ToolCall: &provider.ToolCall{ID: "1", Name: "ping", Arguments: json.RawMessage(`{}`)}}},
		{{Type: provider.EventTextDelta, Text: "all done"}},
	}}

	var final string
	ag := &Agent{
		Provider:        mp,
		Model:           "mock",
		Tools:           reg,
		Perm:            permission.New(map[string]string{"*": "allow"}),
		MaxSteps:        5,
		OnAssistantDone: func(s string) { final = s },
	}
	if err := ag.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("ping tool did not run")
	}
	if final != "all done" {
		t.Errorf("final = %q", final)
	}
	// user + assistant(toolcall) + tool + assistant(text) = 4
	if len(ag.local) != 4 {
		t.Errorf("transcript len = %d, want 4", len(ag.local))
	}
}

func TestAgentPermissionDeny(t *testing.T) {
	ran := false
	reg := tool.NewRegistry()
	reg.Register(&pingTool{ran: &ran})

	mp := &mockProvider{turns: [][]provider.StreamEvent{
		{{Type: provider.EventToolCall, ToolCall: &provider.ToolCall{ID: "1", Name: "ping", Arguments: json.RawMessage(`{}`)}}},
		{{Type: provider.EventTextDelta, Text: "ok"}},
	}}
	ag := &Agent{
		Provider: mp,
		Model:    "mock",
		Tools:    reg,
		Perm:     permission.New(map[string]string{"*": "deny"}),
		MaxSteps: 5,
	}
	if err := ag.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("ping tool ran despite deny policy")
	}
}
