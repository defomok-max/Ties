// Package gemini implements the provider.Provider interface against Google's
// Gemini (Generative Language) API using only the standard library. It speaks
// the native generateContent wire format (not the OpenAI-compatible shim).
package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/defomok-max/Ties/internal/provider"
)

const (
	defaultBaseURL = "https://generativelanguage.googleapis.com"
	apiVersion     = "v1beta"
)

func init() {
	provider.Register("gemini", func(o provider.Options) (provider.Provider, error) {
		base := o.BaseURL
		if base == "" {
			base = defaultBaseURL
		}
		return &client{apiKey: o.APIKey, baseURL: strings.TrimRight(base, "/"), headers: o.Headers, http: &http.Client{Timeout: 0}}, nil
	})
}

type client struct {
	apiKey  string
	baseURL string
	headers map[string]string
	http    *http.Client
}

func (c *client) Name() string { return "gemini" }

// --- wire types -------------------------------------------------------------

type wirePart struct {
	Text             string            `json:"text,omitempty"`
	FunctionCall     *wireFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *wireFunctionResp `json:"functionResponse,omitempty"`
}

type wireFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type wireFunctionResp struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type wireContent struct {
	Role  string     `json:"role,omitempty"`
	Parts []wirePart `json:"parts"`
}

type wireFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type wireRequest struct {
	SystemInstruction *wireContent  `json:"system_instruction,omitempty"`
	Contents          []wireContent `json:"contents"`
	Tools             []struct {
		FunctionDeclarations []wireFuncDecl `json:"functionDeclarations"`
	} `json:"tools,omitempty"`
	GenerationConfig *struct {
		Temperature float64 `json:"temperature,omitempty"`
	} `json:"generationConfig,omitempty"`
}

// Stream implements provider.Provider.
func (c *client) Stream(ctx context.Context, req provider.Request) (<-chan provider.StreamEvent, error) {
	if c.apiKey == "" && c.baseURL == defaultBaseURL {
		return nil, fmt.Errorf("gemini: missing API key")
	}
	wreq := wireRequest{
		Contents: toContents(req.Messages),
	}
	if req.System != "" {
		wreq.SystemInstruction = &wireContent{Parts: []wirePart{{Text: req.System}}}
	}
	if decls := toFuncDecls(req.Tools); len(decls) > 0 {
		wreq.Tools = append(wreq.Tools, struct {
			FunctionDeclarations []wireFuncDecl `json:"functionDeclarations"`
		}{FunctionDeclarations: decls})
	}
	if req.Temperature > 0 {
		wreq.GenerationConfig = &struct {
			Temperature float64 `json:"temperature,omitempty"`
		}{Temperature: req.Temperature}
	}
	body, err := json.Marshal(wreq)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/%s/models/%s:streamGenerateContent", c.baseURL, apiVersion, url.PathEscape(req.Model))
	q := url.Values{"alt": {"sse"}}
	if c.apiKey != "" {
		q.Set("key", c.apiKey)
	}
	endpoint += "?" + q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
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
	go parseSSE(resp.Body, out)
	return out, nil
}

// --- conversion -------------------------------------------------------------

func toFuncDecls(tools []provider.ToolDefinition) []wireFuncDecl {
	if len(tools) == 0 {
		return nil
	}
	out := make([]wireFuncDecl, 0, len(tools))
	for _, t := range tools {
		out = append(out, wireFuncDecl{Name: t.Name, Description: t.Description, Parameters: sanitizeSchema(t.Parameters)})
	}
	return out
}

// sanitizeSchema returns the JSON schema, defaulting empty object schemas which
// Gemini rejects when a tool takes no parameters.
func sanitizeSchema(s json.RawMessage) json.RawMessage {
	if len(s) == 0 {
		return nil
	}
	return s
}

func toContents(msgs []provider.Message) []wireContent {
	idToName := map[string]string{}
	var out []wireContent
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleUser:
			out = append(out, wireContent{Role: "user", Parts: []wirePart{{Text: m.Content}}})
		case provider.RoleAssistant:
			var parts []wirePart
			if m.Content != "" {
				parts = append(parts, wirePart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				idToName[tc.ID] = tc.Name
				args := tc.Arguments
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				parts = append(parts, wirePart{FunctionCall: &wireFunctionCall{Name: tc.Name, Args: args}})
			}
			out = append(out, wireContent{Role: "model", Parts: parts})
		case provider.RoleTool:
			name := idToName[m.ToolCallID]
			if name == "" {
				name = "tool"
			}
			resp := json.RawMessage(mustJSON(map[string]string{"result": m.Content}))
			part := wirePart{FunctionResponse: &wireFunctionResp{Name: name, Response: resp}}
			// Gemini wants function responses in a user-role turn; merge runs.
			if n := len(out); n > 0 && out[n-1].Role == "user" && isFuncRespContent(out[n-1]) {
				out[n-1].Parts = append(out[n-1].Parts, part)
			} else {
				out = append(out, wireContent{Role: "user", Parts: []wirePart{part}})
			}
		case provider.RoleSystem:
			// handled via system_instruction
		}
	}
	return out
}

func isFuncRespContent(c wireContent) bool {
	for _, p := range c.Parts {
		if p.FunctionResponse == nil {
			return false
		}
	}
	return len(c.Parts) > 0
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}

// --- SSE parsing ------------------------------------------------------------

type sseChunk struct {
	Candidates []struct {
		Content struct {
			Parts []wirePart `json:"parts"`
			Role  string     `json:"role"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

func parseSSE(body io.ReadCloser, out chan<- provider.StreamEvent) {
	defer close(out)
	defer func() { _ = body.Close() }()

	usage := provider.Usage{}
	callN := 0

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk sseChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.UsageMetadata.PromptTokenCount > 0 {
			usage.InputTokens = chunk.UsageMetadata.PromptTokenCount
		}
		if chunk.UsageMetadata.CandidatesTokenCount > 0 {
			usage.OutputTokens = chunk.UsageMetadata.CandidatesTokenCount
		}
		for _, cand := range chunk.Candidates {
			for _, p := range cand.Content.Parts {
				switch {
				case p.FunctionCall != nil:
					callN++
					args := p.FunctionCall.Args
					if len(args) == 0 {
						args = json.RawMessage(`{}`)
					}
					out <- provider.StreamEvent{Type: provider.EventToolCall, ToolCall: &provider.ToolCall{
						ID:        fmt.Sprintf("call_%d", callN),
						Name:      p.FunctionCall.Name,
						Arguments: args,
					}}
				case p.Text != "":
					out <- provider.StreamEvent{Type: provider.EventTextDelta, Text: p.Text}
				}
			}
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

// APIError is a non-2xx HTTP response from the Gemini API.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("gemini API error: status %d: %s", e.Status, e.Body)
}

// Retryable reports whether the request may succeed if retried.
func (e *APIError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || (e.Status >= 500 && e.Status <= 599)
}

// RetryAfter is a best-effort hint for how long to wait before retrying.
func (e *APIError) RetryAfter() time.Duration { return 2 * time.Second }
