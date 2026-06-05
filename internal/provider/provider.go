// Package provider defines a vendor-neutral interface for LLM providers with
// streaming, tool-calling and usage accounting, plus a registry so providers
// can be plugged in without the rest of the system knowing their identity.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Role identifies the author of a message.
type Role string

// Message roles.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall is a request from the model to invoke a tool.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Message is a single turn in a conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`
	// ToolCalls is set on assistant messages that request tool execution.
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	// ToolCallID links a RoleTool message back to the ToolCall it answers.
	ToolCallID string `json:"toolCallId,omitempty"`
	// IsError marks a tool result that failed.
	IsError bool `json:"isError,omitempty"`
}

// ToolDefinition describes a tool exposed to the model. Parameters is a JSON
// Schema object.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Usage reports token accounting for a completion.
type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// Request is a single completion request.
type Request struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolDefinition
	MaxTokens   int
	Temperature float64
}

// EventType classifies a streaming event.
type EventType string

// Stream event types.
const (
	EventTextDelta EventType = "text"
	EventToolCall  EventType = "tool_call"
	EventUsage     EventType = "usage"
	EventDone      EventType = "done"
	EventError     EventType = "error"
)

// StreamEvent is one item in a provider's streaming response.
type StreamEvent struct {
	Type     EventType
	Text     string
	ToolCall *ToolCall
	Usage    *Usage
	Err      error
}

// Provider is implemented by every model backend.
type Provider interface {
	// Name returns the canonical provider id (e.g. "anthropic").
	Name() string
	// Stream issues a streaming completion. The returned channel is closed
	// after an EventDone or EventError event.
	Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)
}

// Factory builds a Provider from its configuration.
type Factory func(cfg Options) (Provider, error)

// Options carries provider construction parameters.
type Options struct {
	APIKey  string
	BaseURL string
}

var registry = map[string]Factory{}

// Register makes a provider factory available under name. It panics on a
// duplicate registration, which can only happen at init time.
func Register(name string, f Factory) {
	if _, dup := registry[name]; dup {
		panic("provider: duplicate registration for " + name)
	}
	registry[name] = f
}

// New constructs the named provider.
func New(name string, opts Options) (Provider, error) {
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q (available: %s)", name, strings.Join(Available(), ", "))
	}
	return f(opts)
}

// Available returns the sorted list of registered provider names.
func Available() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SplitModel parses a "provider/model" string. If no slash is present the
// whole string is treated as the model and the provider is empty.
func SplitModel(s string) (provider, model string) {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}
