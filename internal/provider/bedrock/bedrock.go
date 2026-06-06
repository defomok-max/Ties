// Package bedrock implements the provider.Provider interface against AWS
// Bedrock's Anthropic Claude models using only the standard library. It signs
// requests with SigV4 and uses the non-streaming InvokeModel API, adapting the
// single JSON response into the streaming event model the agent expects.
//
// Credentials come from the standard AWS environment variables
// (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, optional AWS_SESSION_TOKEN) and the
// region from the provider's baseUrl (treated as the region) or AWS_REGION /
// AWS_DEFAULT_REGION.
package bedrock

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/defomok-max/Ties/internal/provider"
)

const (
	service       = "bedrock"
	anthropicVer  = "bedrock-2023-05-31"
	defaultMaxTok = 4096
	defaultRegion = "us-east-1"
)

func init() {
	provider.Register("bedrock", func(o provider.Options) (provider.Provider, error) {
		region := strings.TrimSpace(o.BaseURL)
		if region == "" {
			region = firstEnv("AWS_REGION", "AWS_DEFAULT_REGION")
		}
		if region == "" {
			region = defaultRegion
		}
		return &client{
			region:        region,
			creds:         credsFromEnv(),
			headers:       o.Headers,
			http:          &http.Client{Timeout: 0},
			disableStream: truthy(os.Getenv("TIES_BEDROCK_NO_STREAM")),
		}, nil
	})
}

type client struct {
	region  string
	creds   awsCreds
	headers map[string]string
	http    *http.Client
	now     func() time.Time // injectable for tests
	// endpointOverride, when set, is a fmt template ("...%s...") used instead of
	// the real Bedrock URL so tests can point at a local server.
	endpointOverride string
	// disableStream forces the non-streaming InvokeModel path (escape hatch via
	// TIES_BEDROCK_NO_STREAM=1).
	disableStream bool
}

func (c *client) Name() string { return "bedrock" }

func (c *client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *client) endpoint(model string) string {
	if c.endpointOverride != "" {
		return fmt.Sprintf(c.endpointOverride, escapeModel(model))
	}
	return fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke", c.region, escapeModel(model))
}

func (c *client) streamEndpoint(model string) string {
	if c.endpointOverride != "" {
		return fmt.Sprintf(c.endpointOverride, escapeModel(model))
	}
	return fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke-with-response-stream", c.region, escapeModel(model))
}

// escapeModel percent-encodes a model id for use as a single URL path segment.
// url.PathEscape leaves ':' (valid in a path) untouched, but Bedrock model ids
// such as "...-v1:0" must be encoded, matching the AWS SDKs.
func escapeModel(model string) string {
	return strings.ReplaceAll(url.PathEscape(model), ":", "%3A")
}

// --- wire types (Anthropic-on-Bedrock body) ---------------------------------

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
	AnthropicVersion string        `json:"anthropic_version"`
	MaxTokens        int           `json:"max_tokens"`
	System           string        `json:"system,omitempty"`
	Messages         []wireMessage `json:"messages"`
	Tools            []wireTool    `json:"tools,omitempty"`
	Temperature      float64       `json:"temperature,omitempty"`
}

type wireResponse struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Stream implements provider.Provider. By default it uses Bedrock's
// InvokeModelWithResponseStream API and decodes the binary event-stream into
// incremental events; set TIES_BEDROCK_NO_STREAM=1 to fall back to the
// buffered, non-streaming InvokeModel path.
func (c *client) Stream(ctx context.Context, req provider.Request) (<-chan provider.StreamEvent, error) {
	if c.creds.accessKeyID == "" || c.creds.secretAccessKey == "" {
		return nil, fmt.Errorf("bedrock: missing AWS credentials (set AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY)")
	}
	body, err := c.buildBody(req)
	if err != nil {
		return nil, err
	}
	if c.disableStream {
		return c.invokeOnce(ctx, req, body)
	}
	return c.invokeStream(ctx, req, body)
}

func (c *client) buildBody(req provider.Request) ([]byte, error) {
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTok
	}
	return json.Marshal(wireRequest{
		AnthropicVersion: anthropicVer,
		MaxTokens:        maxTok,
		System:           req.System,
		Messages:         toWireMessages(req.Messages),
		Tools:            toWireTools(req.Tools),
		Temperature:      req.Temperature,
	})
}

func (c *client) newRequest(ctx context.Context, url string, body []byte, accept string) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", accept)
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}
	signV4(httpReq, body, c.creds, c.region, service, c.clock())
	return httpReq, nil
}

// invokeOnce is the non-streaming InvokeModel path: one JSON response is
// buffered and converted into a short event sequence.
func (c *client) invokeOnce(ctx context.Context, req provider.Request, body []byte) (<-chan provider.StreamEvent, error) {
	httpReq, err := c.newRequest(ctx, c.endpoint(req.Model), body, "application/json")
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{Status: resp.StatusCode, Body: string(data)}
	}

	var wr wireResponse
	if err := json.Unmarshal(data, &wr); err != nil {
		return nil, fmt.Errorf("bedrock: decode response: %w", err)
	}

	out := make(chan provider.StreamEvent, len(wr.Content)+2)
	for _, b := range wr.Content {
		switch b.Type {
		case "text":
			if b.Text != "" {
				out <- provider.StreamEvent{Type: provider.EventTextDelta, Text: b.Text}
			}
		case "tool_use":
			input := b.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			out <- provider.StreamEvent{Type: provider.EventToolCall, ToolCall: &provider.ToolCall{
				ID: b.ID, Name: b.Name, Arguments: input,
			}}
		}
	}
	out <- provider.StreamEvent{Type: provider.EventUsage, Usage: &provider.Usage{
		InputTokens:  wr.Usage.InputTokens,
		OutputTokens: wr.Usage.OutputTokens,
	}}
	out <- provider.StreamEvent{Type: provider.EventDone}
	close(out)
	return out, nil
}

// invokeStream uses InvokeModelWithResponseStream and decodes the binary AWS
// event-stream into incremental provider events.
func (c *client) invokeStream(ctx context.Context, req provider.Request, body []byte) (<-chan provider.StreamEvent, error) {
	httpReq, err := c.newRequest(ctx, c.streamEndpoint(req.Model), body, "application/vnd.amazon.eventstream")
	if err != nil {
		return nil, err
	}
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
	go c.parseStream(resp.Body, out)
	return out, nil
}

// chunkPayload is the JSON envelope Bedrock wraps each model event in: the
// base64 "bytes" field decodes to one Anthropic streaming event.
type chunkPayload struct {
	Bytes []byte `json:"bytes"`
}

// innerEvent mirrors the Anthropic Messages streaming events that arrive inside
// each Bedrock chunk.
type innerEvent struct {
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

type streamBlock struct {
	kind string
	id   string
	name string
	json strings.Builder
}

// parseStream reads AWS event-stream frames, unwraps the Anthropic events, and
// emits provider events on out. It always closes out.
func (c *client) parseStream(body io.ReadCloser, out chan<- provider.StreamEvent) {
	defer close(out)
	defer func() { _ = body.Close() }()

	r := bufio.NewReader(body)
	blocks := map[int]*streamBlock{}
	usage := provider.Usage{}
	done := false

	for {
		msg, err := readESMessage(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			out <- provider.StreamEvent{Type: provider.EventError, Err: err}
			return
		}

		// Bedrock signals service-side problems via :message-type=exception or
		// an :exception-type header; surface the raw payload.
		if mt := msg.headers[":message-type"]; mt == "exception" || mt == "error" || msg.headers[":exception-type"] != "" {
			out <- provider.StreamEvent{Type: provider.EventError, Err: fmt.Errorf("bedrock stream error: %s", string(msg.payload))}
			return
		}

		var chunk chunkPayload
		if err := json.Unmarshal(msg.payload, &chunk); err != nil || len(chunk.Bytes) == 0 {
			continue // non-chunk frames (e.g. metadata) are ignored
		}
		var ev innerEvent
		if err := json.Unmarshal(chunk.Bytes, &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "message_start":
			usage.InputTokens = ev.Message.Usage.InputTokens
		case "content_block_start":
			blocks[ev.Index] = &streamBlock{kind: ev.ContentBlock.Type, id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					out <- provider.StreamEvent{Type: provider.EventTextDelta, Text: ev.Delta.Text}
				}
			case "input_json_delta":
				if b := blocks[ev.Index]; b != nil {
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
			done = true
		}
	}

	if !done {
		out <- provider.StreamEvent{Type: provider.EventUsage, Usage: &usage}
		out <- provider.StreamEvent{Type: provider.EventDone}
	}
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
			if n := len(out); n > 0 && out[n-1].Role == "user" && isToolResultMsg(out[n-1]) {
				out[n-1].Content = append(out[n-1].Content, block)
			} else {
				out = append(out, wireMessage{Role: "user", Content: []wireBlock{block}})
			}
		case provider.RoleSystem:
			// Handled via Request.System.
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

// --- helpers ----------------------------------------------------------------

func credsFromEnv() awsCreds {
	return awsCreds{
		accessKeyID:     strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID")),
		secretAccessKey: strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY")),
		sessionToken:    strings.TrimSpace(os.Getenv("AWS_SESSION_TOKEN")),
	}
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// APIError is a non-2xx response from Bedrock.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("bedrock API error: status %d: %s", e.Status, e.Body)
}

// Retryable reports whether the request may succeed if retried.
func (e *APIError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || (e.Status >= 500 && e.Status <= 599)
}
