// Package agent implements the ReAct loop that connects a provider, the tool
// registry, the permission engine and a session transcript.
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/defomok-max/Ties/internal/permission"
	"github.com/defomok-max/Ties/internal/provider"
	"github.com/defomok-max/Ties/internal/session"
	"github.com/defomok-max/Ties/internal/tool"
)

// Agent runs a reasoning/acting loop until the model stops requesting tools.
type Agent struct {
	Provider    provider.Provider
	Model       string
	System      string
	Tools       *tool.Registry
	Perm        *permission.Engine
	Session     *session.Session
	MaxSteps    int
	Temperature float64

	// OnText streams assistant text deltas.
	OnText func(delta string)
	// OnToolStart fires before a tool runs.
	OnToolStart func(name, args string)
	// OnToolResult fires after a tool runs.
	OnToolResult func(name string, res tool.Result)
	// OnAssistantDone fires when the model finishes a turn (text complete).
	OnAssistantDone func(text string)
	// Approve is consulted when the permission decision is "ask"; nil means deny.
	Approve func(name, target string) bool
	// OnUsage reports token usage per model turn.
	OnUsage func(provider.Usage)

	// local holds the transcript when no Session is attached.
	local []provider.Message
}

// Run executes one user turn to completion (possibly many tool steps).
func (a *Agent) Run(ctx context.Context, userInput string) error {
	if a.MaxSteps <= 0 {
		a.MaxSteps = 50
	}
	if err := a.appendMessage(provider.Message{Role: provider.RoleUser, Content: userInput}); err != nil {
		return err
	}

	for step := 0; step < a.MaxSteps; step++ {
		assistant, err := a.streamOnce(ctx)
		if err != nil {
			return err
		}
		if err := a.appendMessage(assistant); err != nil {
			return err
		}
		if a.OnAssistantDone != nil {
			a.OnAssistantDone(assistant.Content)
		}
		if len(assistant.ToolCalls) == 0 {
			return nil // model is done
		}
		for _, tc := range assistant.ToolCalls {
			res := a.runTool(ctx, tc)
			if a.OnToolResult != nil {
				a.OnToolResult(tc.Name, res)
			}
			if err := a.appendMessage(provider.Message{
				Role:       provider.RoleTool,
				Content:    res.Content,
				ToolCallID: tc.ID,
				IsError:    res.IsError,
			}); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("reached max steps (%d) without completion", a.MaxSteps)
}

func (a *Agent) streamOnce(ctx context.Context) (provider.Message, error) {
	req := provider.Request{
		Model:       a.Model,
		System:      a.System,
		Messages:    a.messages(),
		Tools:       a.Tools.Definitions(),
		Temperature: a.Temperature,
	}
	events, err := a.Provider.Stream(ctx, req)
	if err != nil {
		return provider.Message{}, err
	}
	msg := provider.Message{Role: provider.RoleAssistant}
	for ev := range events {
		switch ev.Type {
		case provider.EventTextDelta:
			msg.Content += ev.Text
			if a.OnText != nil {
				a.OnText(ev.Text)
			}
		case provider.EventToolCall:
			if ev.ToolCall != nil {
				msg.ToolCalls = append(msg.ToolCalls, *ev.ToolCall)
			}
		case provider.EventUsage:
			if ev.Usage != nil && a.OnUsage != nil {
				a.OnUsage(*ev.Usage)
			}
		case provider.EventError:
			return provider.Message{}, ev.Err
		case provider.EventDone:
			// handled by channel close
		}
	}
	return msg, nil
}

func (a *Agent) runTool(ctx context.Context, tc provider.ToolCall) tool.Result {
	t, ok := a.Tools.Get(tc.Name)
	if !ok {
		return tool.Result{Content: "unknown tool: " + tc.Name, IsError: true}
	}
	target := extractTarget(tc.Arguments)
	decision := permission.Ask
	if a.Perm != nil {
		decision = a.Perm.Evaluate(tc.Name, target)
	}
	switch decision {
	case permission.Deny:
		return tool.Result{Content: fmt.Sprintf("permission denied for %s", tc.Name), IsError: true}
	case permission.Ask:
		if a.Approve == nil || !a.Approve(tc.Name, target) {
			return tool.Result{Content: fmt.Sprintf("user declined %s", tc.Name), IsError: true}
		}
	}
	if a.OnToolStart != nil {
		a.OnToolStart(tc.Name, string(tc.Arguments))
	}
	res, err := t.Run(ctx, tc.Arguments)
	if err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}
	return res
}

func (a *Agent) messages() []provider.Message {
	if a.Session != nil {
		return a.Session.Messages
	}
	return a.local
}

func (a *Agent) appendMessage(m provider.Message) error {
	if a.Session != nil {
		return a.Session.Append(m)
	}
	a.local = append(a.local, m)
	return nil
}

// extractTarget pulls a human-meaningful detail (command/path/pattern/query)
// from tool arguments for permission matching.
func extractTarget(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	for _, key := range []string{"command", "path", "pattern", "query", "file"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}
