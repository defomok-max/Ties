package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/defomok-max/Ties/internal/permission"
	"github.com/defomok-max/Ties/internal/provider"
	"github.com/defomok-max/Ties/internal/tool"
)

// usageProvider always asks for a tool and reports a fixed usage per turn.
type usageProvider struct{ in, out int }

func (m *usageProvider) Name() string { return "usage" }
func (m *usageProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.EventToolCall, ToolCall: &provider.ToolCall{ID: "1", Name: "ping", Arguments: json.RawMessage(`{}`)}}
		ch <- provider.StreamEvent{Type: provider.EventUsage, Usage: &provider.Usage{InputTokens: m.in, OutputTokens: m.out}}
		ch <- provider.StreamEvent{Type: provider.EventDone}
	}()
	return ch, nil
}

func TestTokenBudgetStopsRun(t *testing.T) {
	ran := false
	reg := tool.NewRegistry()
	reg.Register(&pingTool{ran: &ran})

	ag := &Agent{
		Provider: &usageProvider{in: 100, out: 100},
		Model:    "mock",
		Tools:    reg,
		Perm:     permission.New(map[string]string{"*": "allow"}),
		MaxSteps: 20,
		Budget:   Budget{MaxTokens: 150},
	}
	err := ag.Run(context.Background(), "go")
	if err == nil || !strings.Contains(err.Error(), "token budget") {
		t.Fatalf("expected token budget error, got %v", err)
	}
	_, tokens := ag.Spent()
	if tokens < 150 {
		t.Fatalf("expected >=150 tokens spent, got %d", tokens)
	}
}

func TestCostBudgetStopsRun(t *testing.T) {
	reg := tool.NewRegistry()
	ran := false
	reg.Register(&pingTool{ran: &ran})

	ag := &Agent{
		Provider: &usageProvider{in: 1_000_000, out: 0},
		Model:    "test-model",
		Tools:    reg,
		Perm:     permission.New(map[string]string{"*": "allow"}),
		MaxSteps: 20,
		Budget:   Budget{MaxUSD: 1.50},
		// $2 per 1M input tokens => one turn = $2, over the $1.50 cap.
		EstimateCost: func(_ string, in, _ int) (float64, bool) {
			return float64(in) / 1_000_000 * 2.0, true
		},
	}
	err := ag.Run(context.Background(), "go")
	if err == nil || !strings.Contains(err.Error(), "cost budget") {
		t.Fatalf("expected cost budget error, got %v", err)
	}
	usd, _ := ag.Spent()
	if usd < 1.50 {
		t.Fatalf("expected >=$1.50 spent, got %.4f", usd)
	}
}

func TestNoBudgetRunsToCompletion(t *testing.T) {
	reg := tool.NewRegistry()
	mp := &mockProvider{turns: [][]provider.StreamEvent{
		{{Type: provider.EventUsage, Usage: &provider.Usage{InputTokens: 5, OutputTokens: 5}}},
	}}
	ag := &Agent{Provider: mp, Model: "m", Tools: reg, Perm: permission.New(map[string]string{"*": "allow"}), MaxSteps: 5}
	if err := ag.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
