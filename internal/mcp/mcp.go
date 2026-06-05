// Package mcp implements a minimal Model Context Protocol client over the
// stdio transport (newline-delimited JSON-RPC 2.0). Discovered MCP tools are
// adapted to the tool.Tool interface so the agent can call them transparently.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/defomok-max/Ties/internal/tool"
)

const protocolVersion = "2024-11-05"

// Client is a connection to one MCP server.
type Client struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	mu     sync.Mutex
	nextID int

	pendMu  sync.Mutex
	pending map[int]chan rpcResponse
	closed  chan struct{}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// Start launches the server process and performs the MCP handshake.
func Start(ctx context.Context, name, command string, args []string, env map[string]string) (*Client, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{
		name:    name,
		cmd:     cmd,
		stdin:   stdin,
		pending: map[int]chan rpcResponse{},
		closed:  make(chan struct{}),
	}
	go c.readLoop(stdout)

	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		if resp.ID == 0 {
			continue // notification or unsolicited; ignore
		}
		c.pendMu.Lock()
		ch := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.pendMu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
	close(c.closed)
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	ch := make(chan rpcResponse, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()

	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := c.write(req); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, fmt.Errorf("mcp %s: connection closed", c.name)
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp %s: %s (code %d)", c.name, resp.Error.Message, resp.Error.Code)
		}
		return resp.Result, nil
	case <-time.After(60 * time.Second):
		return nil, fmt.Errorf("mcp %s: timeout calling %s", c.name, method)
	}
}

func (c *Client) notify(method string, params any) error {
	return c.write(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *Client) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

func (c *Client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "ties", "version": "0.1.0"},
	}
	if _, err := c.call(ctx, "initialize", params); err != nil {
		return err
	}
	return c.notify("notifications/initialized", map[string]any{})
}

type mcpToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ListTools fetches the server's advertised tools.
func (c *Client) ListTools(ctx context.Context) ([]mcpToolDef, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
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

type callResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// CallTool invokes a tool on the server and flattens text content.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (tool.Result, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": json.RawMessage(args)})
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

// Close terminates the server process.
func (c *Client) Close() error {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return c.cmd.Wait()
}

// RemoteTool adapts an MCP tool to the tool.Tool interface.
type RemoteTool struct {
	client      *Client
	localName   string
	remoteName  string
	description string
	schema      json.RawMessage
}

// Name returns the namespaced tool name (server_tool).
func (t *RemoteTool) Name() string { return t.localName }

// Description returns the tool description.
func (t *RemoteTool) Description() string { return t.description }

// Schema returns the JSON schema for arguments.
func (t *RemoteTool) Schema() json.RawMessage {
	if len(t.schema) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return t.schema
}

// Run forwards the call to the MCP server.
func (t *RemoteTool) Run(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	return t.client.CallTool(ctx, t.remoteName, args)
}

// Tools returns RemoteTool adapters for everything the server exposes,
// namespaced as "<server>_<tool>".
func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
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
