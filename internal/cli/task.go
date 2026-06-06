package cli

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/defomok-max/Ties/internal/tool"
)

// taskTool delegates a self-contained subtask to a fresh sub-agent. The actual
// spawning logic is injected per run (it needs the live provider, model and
// parent budget), so the tool can still be listed by `ties tools` when no agent
// is active.
type taskTool struct {
	spawn func(ctx context.Context, description, prompt string) (string, error)
}

func newTaskTool() *taskTool { return &taskTool{} }

func (t *taskTool) Name() string { return "task" }

func (t *taskTool) Description() string {
	return "Delegate a focused, self-contained subtask to a sub-agent that shares your tools but keeps its own short transcript. Use it to investigate or implement a chunk without cluttering the main context; it returns the sub-agent's final answer. The sub-agent cannot spawn further tasks."
}

func (t *taskTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"description":{"type":"string","description":"Short label for the subtask"},` +
		`"prompt":{"type":"string","description":"The full instruction handed to the sub-agent"}` +
		`},"required":["prompt"]}`)
}

func (t *taskTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	if t.spawn == nil {
		return tool.Result{Content: "sub-agents are not available in this context", IsError: true}, nil
	}
	var a struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}
	if strings.TrimSpace(a.Prompt) == "" {
		return tool.Result{Content: "task requires a non-empty prompt", IsError: true}, nil
	}
	out, err := t.spawn(ctx, a.Description, a.Prompt)
	if err != nil {
		// Return whatever the sub-agent produced plus the error, as a tool error.
		msg := "sub-agent stopped: " + err.Error()
		if strings.TrimSpace(out) != "" {
			msg = out + "\n\n[" + msg + "]"
		}
		return tool.Result{Content: msg, IsError: true}, nil
	}
	if strings.TrimSpace(out) == "" {
		out = "(sub-agent finished without a final message)"
	}
	return tool.Result{Content: out}, nil
}

const subAgentNote = "\n\nYou are a SUB-AGENT handling one delegated subtask. Work autonomously, " +
	"then end with a concise final message summarizing what you found or did. " +
	"You cannot delegate further."

const planModeNote = "\n\nPLAN MODE (read-only): editing and shell tools are disabled. " +
	"Investigate using read-only tools, then present a concrete, step-by-step " +
	"implementation plan for the user to approve. Do not attempt to modify files."
