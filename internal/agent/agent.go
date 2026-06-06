// Package agent implements the ReAct loop that connects a provider, the tool
// registry, the permission engine and a session transcript.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	// MaxToolOutput caps the characters of a tool result stored and sent back
	// to the model (0 = unlimited). Display callbacks still see the full text.
	MaxToolOutput int

	// Budget, when set, stops the run once a spend/usage ceiling is reached so
	// a runaway loop can't burn unlimited tokens or money.
	Budget Budget
	// EstimateCost converts a model turn's tokens into USD (ok=false if the
	// model's price is unknown). Used only for the cost budget.
	EstimateCost func(model string, inTok, outTok int) (usd float64, ok bool)

	// ToolTimeout caps how long a single tool call may run (0 = no limit). On
	// timeout the tool sees a cancelled context and the model gets an error.
	ToolTimeout time.Duration

	// DenyTools names tools that are hard-blocked regardless of the permission
	// engine — used by plan mode to enforce a read-only run.
	DenyTools map[string]bool

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

	// spent accumulates across the run for budget enforcement.
	spentUSD    float64
	spentTokens int
}

// Budget caps how much a single Run may consume. A zero field means "no limit"
// for that dimension.
type Budget struct {
	MaxUSD    float64 // total estimated USD across all model turns
	MaxTokens int     // total input+output tokens across all model turns
}

// Empty reports whether no budget limit is configured.
func (b Budget) Empty() bool { return b.MaxUSD <= 0 && b.MaxTokens <= 0 }

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
		if err := a.checkBudget(); err != nil {
			return err
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
				Content:    a.clamp(res.Content),
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
			if ev.Usage != nil {
				a.accountUsage(*ev.Usage)
				if a.OnUsage != nil {
					a.OnUsage(*ev.Usage)
				}
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
	if a.DenyTools[tc.Name] {
		return tool.Result{Content: fmt.Sprintf("%s is disabled in plan mode (read-only)", tc.Name), IsError: true}
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
	runCtx := ctx
	if a.ToolTimeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, a.ToolTimeout)
		defer cancel()
	}
	res, err := t.Run(runCtx, tc.Arguments)
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return tool.Result{Content: fmt.Sprintf("%s timed out after %s", tc.Name, a.ToolTimeout), IsError: true}
		}
		return tool.Result{Content: err.Error(), IsError: true}
	}
	return res
}

// RemainingBudget returns a Budget representing what is left of a.Budget after
// what has already been spent (used to bound a spawned sub-agent). Unlimited
// dimensions stay unlimited.
func (a *Agent) RemainingBudget() Budget {
	b := Budget{}
	if a.Budget.MaxUSD > 0 {
		if r := a.Budget.MaxUSD - a.spentUSD; r > 0 {
			b.MaxUSD = r
		} else {
			b.MaxUSD = 0.000001 // effectively exhausted
		}
	}
	if a.Budget.MaxTokens > 0 {
		if r := a.Budget.MaxTokens - a.spentTokens; r > 0 {
			b.MaxTokens = r
		} else {
			b.MaxTokens = 1
		}
	}
	return b
}

// clamp truncates s to MaxToolOutput characters, keeping the head and tail and
// noting how much was elided, so a huge file can't blow the context window.
func (a *Agent) clamp(s string) string {
	limit := a.MaxToolOutput
	if limit <= 0 || len(s) <= limit {
		return s
	}
	head := limit * 2 / 3
	tail := limit - head
	elided := len(s) - head - tail
	return s[:head] + fmt.Sprintf("\n\n… [%d characters truncated] …\n\n", elided) + s[len(s)-tail:]
}

// accountUsage adds a turn's tokens (and cost, if priceable) to the running
// totals used for budget enforcement.
func (a *Agent) accountUsage(u provider.Usage) {
	a.spentTokens += u.InputTokens + u.OutputTokens
	if a.EstimateCost != nil {
		if usd, ok := a.EstimateCost(a.Model, u.InputTokens, u.OutputTokens); ok {
			a.spentUSD += usd
		}
	}
}

// checkBudget returns an error when a configured spend/usage ceiling is hit.
func (a *Agent) checkBudget() error {
	if a.Budget.Empty() {
		return nil
	}
	if a.Budget.MaxTokens > 0 && a.spentTokens >= a.Budget.MaxTokens {
		return fmt.Errorf("token budget reached (%d/%d tokens) — stopping", a.spentTokens, a.Budget.MaxTokens)
	}
	if a.Budget.MaxUSD > 0 && a.spentUSD >= a.Budget.MaxUSD {
		return fmt.Errorf("cost budget reached (est. $%.4f/$%.4f) — stopping", a.spentUSD, a.Budget.MaxUSD)
	}
	return nil
}

// Spent reports the running totals accumulated during Run.
func (a *Agent) Spent() (usd float64, tokens int) { return a.spentUSD, a.spentTokens }

// AddSpent folds an external spend (e.g. a sub-agent's) into this agent's
// running totals so the parent budget accounts for delegated work.
func (a *Agent) AddSpent(usd float64, tokens int) {
	a.spentUSD += usd
	a.spentTokens += tokens
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
