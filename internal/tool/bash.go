package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

type bashTool struct{ root string }

func newBashTool(root string) Tool { return &bashTool{root: root} }
func (t *bashTool) Name() string   { return "bash" }
func (t *bashTool) Description() string {
	return "Run a shell command in the workspace and return combined stdout/stderr. Use for builds, tests and git. Destructive by default; gated by permissions."
}
func (t *bashTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeoutSeconds":{"type":"integer","description":"Max seconds before the command is killed (default 120)"}},"required":["command"]}`)
}

func (t *bashTool) Run(ctx context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeoutSeconds"`
	}
	if err := decode(args, &a); err != nil {
		return Result{}, err
	}
	if a.Command == "" {
		return Result{Content: "command is required", IsError: true}, nil
	}
	timeout := time.Duration(a.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "sh", "-c", a.Command)
	cmd.Dir = t.root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if cctx.Err() == context.DeadlineExceeded {
		return Result{Content: fmt.Sprintf("command timed out after %s\n%s", timeout, out), IsError: true}, nil
	}
	if err != nil {
		return Result{Content: fmt.Sprintf("%s\n(exit error: %v)", out, err), IsError: true}, nil
	}
	if out == "" {
		out = "(no output)"
	}
	return Result{Content: out}, nil
}
