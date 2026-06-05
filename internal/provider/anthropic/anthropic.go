// Package anthropic implements the provider.Provider interface against the
// Anthropic Messages API using only the standard library.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/defomok-max/Ties/internal/provider"
)

const (
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
	defaultMaxTok  = 4096
)

func init() {
	provider.Register("anthropic", func(o provider.Options) (provider.Provider, error) {
		base := o.BaseURL
		if base == "" {
			base = defaultBaseURL
		}
		return &client{apiKey: o.APIKey, baseURL: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 0}}, nil
	})
}

type client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

func (c *client) Name() string { return "anthropic" }

// --- wire types -------------------------------------------------------------

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type wireBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

type wireRequest struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	System      string        `json:"system,omitempty"`
	Messages    []wireMessage `json:"messages"`
	Tools       []wireTool    `json:"tools,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
}

// Stream implements provider.Provider.
func (c *client) Stream(ctx context.Context, req provider.Request) (<-chan provider.StreamEvent, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("anthropic: missing API key")
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTok
	}
	wreq := wireRequest{
		Model:       req.Model,
		MaxTokens:   maxTok,
		System:      req.System,
		Messages:    toWireMessages(req.Messages),
		Tools:       toWireTools(req.Tools),
		Temperature: req.Temperature,
		Stream:      true,
	}
	body, err := json.Marshal(wreq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

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

// --- conversion -------------------------------------------------------------

func toWireTools(tools []provider.ToolDefinition) []wireTool {
	if len(tools) == 0 {
		return nil
	}
	w := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		schema := t.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		w = append(w, wireTool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return w
}

func toWireMessages(msgs []provider.Message) []wireMessage {
	var out []wireMessage
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleUser:
			out = append(out, wireMessage{Role: "user", Content: []wireBlock{{Type: "text", Text: m.Content}}})
		case provider.RoleAssistant:
			var blocks []wireBlock
			if m.Content != "" {
				blocks = append(blocks, wireBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := tc.Arguments
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, wireBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
			out = append(out, wireMessage{Role: "assistant", Content: blocks})
		case provider.RoleTool:
			block := wireBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content, IsError: m.IsError}
			// Merge consecutive tool results into the previous user message.
			if n := len(out); n > 0 && out[n-1].Role == "user" && isToolResultMsg(out[n-1]) {
				out[n-1].Content = append(out[n-1].Content, block)
			} else {
				out = append(out, wireMessage{Role: "user", Content: []wireBlock{block}})
			}
		case provider.RoleSystem:
			// System handled out-of-band via Request.System; ignore here.
		}
	}
	return out
}

func isToolResultMsg(m wireMessage) bool {
	for _, b := range m.Content {
		if b.Type != "tool_result" {
			return false
		}
	}
	return len(m.Content) > 0
}

// --- SSE parsing ------------------------------------------------------------

type sseEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type blockState struct {
	kind string // "text" or "tool_use"
	id   string
	name string
	json strings.Builder
}

func parseSSE(body io.ReadCloser, out chan<- provider.StreamEvent) {
	defer close(out)
	defer func() { _ = body.Close() }()

	blocks := map[int]*blockState{}
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
		var ev sseEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			usage.InputTokens = ev.Message.Usage.InputTokens
		case "content_block_start":
			blocks[ev.Index] = &blockState{kind: ev.ContentBlock.Type, id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
		case "content_block_delta":
			b := blocks[ev.Index]
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					out <- provider.StreamEvent{Type: provider.EventTextDelta, Text: ev.Delta.Text}
				}
			case "input_json_delta":
				if b != nil {
					b.json.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "content_block_stop":
			if b := blocks[ev.Index]; b != nil && b.kind == "tool_use" {
				args := b.json.String()
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				out <- provider.StreamEvent{Type: provider.EventToolCall, ToolCall: &provider.ToolCall{
					ID: b.id, Name: b.name, Arguments: json.RawMessage(args),
				}}
			}
			delete(blocks, ev.Index)
		case "message_delta":
			if ev.Usage.OutputTokens > 0 {
				usage.OutputTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			out <- provider.StreamEvent{Type: provider.EventUsage, Usage: &usage}
			out <- provider.StreamEvent{Type: provider.EventDone}
			return
		case "error":
			out <- provider.StreamEvent{Type: provider.EventError, Err: fmt.Errorf("anthropic stream error: %s", payload)}
			return
		}
	}
	if err := scanner.Err(); err != nil {
		out <- provider.StreamEvent{Type: provider.EventError, Err: err}
		return
	}
	out <- provider.StreamEvent{Type: provider.EventUsage, Usage: &usage}
	out <- provider.StreamEvent{Type: provider.EventDone}
}

// --- errors -----------------------------------------------------------------

// APIError is a non-2xx HTTP response from the Anthropic API.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("anthropic API error: status %d: %s", e.Status, e.Body)
}

// Retryable reports whether the request may succeed if retried.
func (e *APIError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || (e.Status >= 500 && e.Status <= 599)
}

// RetryAfter is a best-effort hint for how long to wait before retrying.
func (e *APIError) RetryAfter() time.Duration {
	return 2 * time.Second
}
