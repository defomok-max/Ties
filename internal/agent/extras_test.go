package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/defomok-max/Ties/internal/permission"
	"github.com/defomok-max/Ties/internal/provider"
	"github.com/defomok-max/Ties/internal/tool"
)

// callOnceProvider asks for the named tool on the first turn, then stops.
type callOnceProvider struct {
	toolName string
	calls    int
}

func (m *callOnceProvider) Name() string { return "callonce" }
func (m *callOnceProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	turn := m.calls
	m.calls++
	go func() {
		defer close(ch)
		if turn == 0 {
			ch <- provider.StreamEvent{Type: provider.EventToolCall, ToolCall: &provider.ToolCall{
				ID: "1", Name: m.toolName, Arguments: json.RawMessage(`{"path":"x"}`)}}
		} else {
			ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "done"}
		}
		ch <- provider.StreamEvent{Type: provider.EventDone}
	}()
	return ch, nil
}

func TestPlanModeBlocksMutatingTool(t *testing.T) {
	ran := false
	reg := tool.NewRegistry()
	reg.Register(&namedTool{name: "edit", ran: &ran})

	var toolResult tool.Result
	ag := &Agent{
		Provider:  &callOnceProvider{toolName: "edit"},
		Model:     "m",
		Tools:     reg,
		Perm:      permission.New(map[string]string{"*": "allow"}),
		MaxSteps:  5,
		DenyTools: map[string]bool{"edit": true},
		OnToolResult: func(_ string, res tool.Result) {
			toolResult = res
		},
	}
	if err := ag.Run(context.Background(), "go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if ran {
		t.Fatal("edit tool ran despite plan mode")
	}
	if !toolResult.IsError || !strings.Contains(toolResult.Content, "plan mode") {
		t.Fatalf("expected plan-mode error result, got %+v", toolResult)
	}
}

func TestToolTimeout(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&slowTool{})
	var got tool.Result
	ag := &Agent{
		Provider:     &callOnceProvider{toolName: "slow"},
		Model:        "m",
		Tools:        reg,
		Perm:         permission.New(map[string]string{"*": "allow"}),
		MaxSteps:     5,
		ToolTimeout:  20 * time.Millisecond,
		OnToolResult: func(_ string, res tool.Result) { got = res },
	}
	if err := ag.Run(context.Background(), "go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !got.IsError || !strings.Contains(got.Content, "timed out") {
		t.Fatalf("expected timeout error, got %+v", got)
	}
}

func TestRemainingBudgetAndAddSpent(t *testing.T) {
	ag := &Agent{Budget: Budget{MaxUSD: 1.0, MaxTokens: 100}}
	ag.AddSpent(0.4, 30)
	rb := ag.RemainingBudget()
	if rb.MaxUSD < 0.59 || rb.MaxUSD > 0.61 {
		t.Fatalf("remaining usd = %.4f, want ~0.6", rb.MaxUSD)
	}
	if rb.MaxTokens != 70 {
		t.Fatalf("remaining tokens = %d, want 70", rb.MaxTokens)
	}
	// Exhausted dimensions clamp to an effectively-zero limit, not unlimited.
	ag.AddSpent(2.0, 200)
	rb = ag.RemainingBudget()
	if rb.MaxTokens != 1 || rb.MaxUSD <= 0 {
		t.Fatalf("exhausted budget should clamp, got %+v", rb)
	}
	if rb.Empty() {
		t.Fatal("clamped budget must not report Empty (would mean unlimited)")
	}
}

// namedTool is a configurable tool that records execution.
type namedTool struct {
	name string
	ran  *bool
}

func (n *namedTool) Name() string            { return n.name }
func (n *namedTool) Description() string     { return "test" }
func (n *namedTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (n *namedTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	if n.ran != nil {
		*n.ran = true
	}
	return tool.Result{Content: "ok"}, nil
}

// slowTool blocks until its context is cancelled.
type slowTool struct{}

func (s *slowTool) Name() string            { return "slow" }
func (s *slowTool) Description() string     { return "test" }
func (s *slowTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *slowTool) Run(ctx context.Context, _ json.RawMessage) (tool.Result, error) {
	select {
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	case <-time.After(2 * time.Second):
		return tool.Result{Content: "slept"}, nil
	}
}
