// Package openai implements the provider.Provider interface against the
// OpenAI (and compatible) Chat Completions API using only the standard library.
// Its presence proves the provider interface is genuinely vendor-neutral.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/defomok-max/Ties/internal/provider"
)

const (
	defaultBaseURL = "https://api.openai.com"
	defaultMaxTok  = 4096
)

func init() {
	provider.Register("openai", func(o provider.Options) (provider.Provider, error) {
		base := o.BaseURL
		if base == "" {
			base = defaultBaseURL
		}
		return &client{apiKey: o.APIKey, baseURL: strings.TrimRight(base, "/"), http: &http.Client{}}, nil
	})
}

type client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

func (c *client) Name() string { return "openai" }

type wireFunc struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type wireToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function wireFunc `json:"function"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wireRequest struct {
	Model         string         `json:"model"`
	Messages      []wireMessage  `json:"messages"`
	Tools         []wireTool     `json:"tools,omitempty"`
	Temperature   float64        `json:"temperature,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions map[string]any `json:"stream_options,omitempty"`
}

// Stream implements provider.Provider.
func (c *client) Stream(ctx context.Context, req provider.Request) (<-chan provider.StreamEvent, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("openai: missing API key")
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTok
	}
	wreq := wireRequest{
		Model:         req.Model,
		Messages:      toWireMessages(req.System, req.Messages),
		Tools:         toWireTools(req.Tools),
		Temperature:   req.Temperature,
		MaxTokens:     maxTok,
		Stream:        true,
		StreamOptions: map[string]any{"include_usage": true},
	}
	body, err := json.Marshal(wreq)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, &APIError{Status: resp.StatusCode, Body: string(data)}
	}
	out := make(chan provider.StreamEvent)
	go parseSSE(resp.Body, out)
	return out, nil
}

func toWireTools(tools []provider.ToolDefinition) []wireTool {
	if len(tools) == 0 {
		return nil
	}
	w := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		var wt wireTool
		wt.Type = "function"
		wt.Function.Name = t.Name
		wt.Function.Description = t.Description
		wt.Function.Parameters = t.Parameters
		if len(wt.Function.Parameters) == 0 {
			wt.Function.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		w = append(w, wt)
	}
	return w
}

func toWireMessages(system string, msgs []provider.Message) []wireMessage {
	var out []wireMessage
	if system != "" {
		out = append(out, wireMessage{Role: "system", Content: system})
	}
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleUser:
			out = append(out, wireMessage{Role: "user", Content: m.Content})
		case provider.RoleAssistant:
			wm := wireMessage{Role: "assistant", Content: m.Content}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
					ID: tc.ID, Type: "function", Function: wireFunc{Name: tc.Name, Arguments: args},
				})
			}
			out = append(out, wm)
		case provider.RoleTool:
			out = append(out, wireMessage{Role: "tool", ToolCallID: m.ToolCallID, Content: m.Content})
		case provider.RoleSystem:
			out = append(out, wireMessage{Role: "system", Content: m.Content})
		}
	}
	return out
}

type sseChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type tcAccum struct {
	id   string
	name string
	args strings.Builder
}

func parseSSE(body io.ReadCloser, out chan<- provider.StreamEvent) {
	defer close(out)
	defer func() { _ = body.Close() }()

	calls := map[int]*tcAccum{}
	usage := provider.Usage{}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		var ck sseChunk
		if err := json.Unmarshal([]byte(payload), &ck); err != nil {
			continue
		}
		if ck.Usage != nil {
			usage.InputTokens = ck.Usage.PromptTokens
			usage.OutputTokens = ck.Usage.CompletionTokens
		}
		for _, ch := range ck.Choices {
			if ch.Delta.Content != "" {
				out <- provider.StreamEvent{Type: provider.EventTextDelta, Text: ch.Delta.Content}
			}
			for _, tc := range ch.Delta.ToolCalls {
				acc := calls[tc.Index]
				if acc == nil {
					acc = &tcAccum{}
					calls[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				acc.args.WriteString(tc.Function.Arguments)
			}
			if ch.FinishReason != nil {
				flushCalls(calls, out)
			}
		}
	}
	flushCalls(calls, out)
	if err := scanner.Err(); err != nil {
		out <- provider.StreamEvent{Type: provider.EventError, Err: err}
		return
	}
	out <- provider.StreamEvent{Type: provider.EventUsage, Usage: &usage}
	out <- provider.StreamEvent{Type: provider.EventDone}
}

func flushCalls(calls map[int]*tcAccum, out chan<- provider.StreamEvent) {
	if len(calls) == 0 {
		return
	}
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		acc := calls[i]
		args := acc.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		out <- provider.StreamEvent{Type: provider.EventToolCall, ToolCall: &provider.ToolCall{
			ID: acc.id, Name: acc.name, Arguments: json.RawMessage(args),
		}}
		delete(calls, i)
	}
}

// APIError is a non-2xx HTTP response from the OpenAI API.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("openai API error: status %d: %s", e.Status, e.Body)
}

// Retryable reports whether the request may succeed if retried.
func (e *APIError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || (e.Status >= 500 && e.Status <= 599)
}
