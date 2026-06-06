package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/defomok-max/Ties/internal/tool"
)

// httpClient speaks the MCP "Streamable HTTP" transport: JSON-RPC messages are
// POSTed to a single endpoint, and the server replies with either a plain JSON
// object or an SSE (text/event-stream) body carrying the response.
type httpClient struct {
	name    string
	url     string
	headers map[string]string
	http    *http.Client

	mu        sync.Mutex
	nextID    int
	sessionID string
}

// StartHTTP connects to an MCP server over HTTP and performs the handshake.
func StartHTTP(ctx context.Context, name, url string, headers map[string]string) (*httpClient, error) {
	c := &httpClient{
		name:    name,
		url:     url,
		headers: headers,
		http:    &http.Client{Timeout: 120 * time.Second},
	}
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *httpClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "ties", "version": "0.1.0"},
	}
	if _, err := c.rpc(ctx, "initialize", params, false); err != nil {
		return err
	}
	return c.notify(ctx, "notifications/initialized", map[string]any{})
}

func (c *httpClient) notify(ctx context.Context, method string, params any) error {
	_, err := c.rpc(ctx, method, params, true)
	return err
}

// rpc sends one JSON-RPC message. When notify is true no id is sent and no
// response is awaited.
func (c *httpClient) rpc(ctx context.Context, method string, params any, notify bool) (json.RawMessage, error) {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	var id int
	if !notify {
		c.mu.Lock()
		c.nextID++
		id = c.nextID
		c.mu.Unlock()
		msg["id"] = id
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("mcp %s: http %d: %s", c.name, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if notify {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, nil
	}

	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return c.readSSEResponse(resp.Body, id)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return decodeRPC(c.name, data, id)
}

// readSSEResponse scans an SSE body for the JSON-RPC response whose id matches.
func (c *httpClient) readSSEResponse(r io.Reader, wantID int) (json.RawMessage, error) {
	data, err := io.ReadAll(io.LimitReader(r, 8<<20))
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		res, err := decodeRPC(c.name, []byte(payload), wantID)
		if err == nil || !errNoMatch(err) {
			return res, err
		}
	}
	return nil, fmt.Errorf("mcp %s: no response for id %d in stream", c.name, wantID)
}

type noMatchError struct{ id int }

func (e noMatchError) Error() string { return fmt.Sprintf("no rpc response with id %d", e.id) }
func errNoMatch(err error) bool      { _, ok := err.(noMatchError); return ok }

func decodeRPC(name string, data []byte, wantID int) (json.RawMessage, error) {
	var resp rpcResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, noMatchError{wantID}
	}
	if resp.ID != wantID {
		return nil, noMatchError{wantID}
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp %s: %s (code %d)", name, resp.Error.Message, resp.Error.Code)
	}
	return resp.Result, nil
}

// ListTools fetches the server's advertised tools.
func (c *httpClient) ListTools(ctx context.Context) ([]mcpToolDef, error) {
	raw, err := c.rpc(ctx, "tools/list", map[string]any{}, false)
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []mcpToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

// CallTool invokes a tool on the server and flattens text content.
func (c *httpClient) CallTool(ctx context.Context, name string, args json.RawMessage) (tool.Result, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	raw, err := c.rpc(ctx, "tools/call", map[string]any{"name": name, "arguments": json.RawMessage(args)}, false)
	if err != nil {
		return tool.Result{}, err
	}
	var res callResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return tool.Result{}, err
	}
	var text string
	for i, b := range res.Content {
		if i > 0 {
			text += "\n"
		}
		text += b.Text
	}
	return tool.Result{Content: text, IsError: res.IsError}, nil
}

// Tools returns RemoteTool adapters namespaced as "<server>_<tool>".
func (c *httpClient) Tools(ctx context.Context) ([]tool.Tool, error) {
	defs, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tool.Tool, 0, len(defs))
	for _, d := range defs {
		out = append(out, &RemoteTool{
			client:      c,
			localName:   c.name + "_" + d.Name,
			remoteName:  d.Name,
			description: d.Description,
			schema:      d.InputSchema,
		})
	}
	return out, nil
}

// Close releases the HTTP session (best-effort DELETE if a session id exists).
func (c *httpClient) Close() error {
	if c.sessionID == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodDelete, c.url, nil)
	if err != nil {
		return nil //nolint:nilerr // best-effort teardown
	}
	req.Header.Set("Mcp-Session-Id", c.sessionID)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	return nil
}
