package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeMCP is a minimal Streamable-HTTP MCP server for tests. It replies to
// initialize, tools/list and tools/call; one path returns SSE, exercising both
// response encodings.
func fakeMCP(t *testing.T, sse bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&msg)
		if msg.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result string
		switch msg.Method {
		case "initialize":
			result = `{"protocolVersion":"2024-11-05","capabilities":{}}`
		case "tools/list":
			result = `{"tools":[{"name":"echo","description":"echo text","inputSchema":{"type":"object","properties":{"text":{"type":"string"}}}}]}`
		case "tools/call":
			result = `{"content":[{"type":"text","text":"pong"}],"isError":false}`
		}
		body := `{"jsonrpc":"2.0","id":` + itoa(msg.ID) + `,"result":` + result + `}`
		w.Header().Set("Mcp-Session-Id", "sess-123")
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message\ndata: " + body + "\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestHTTPClientJSON(t *testing.T) { runHTTPClient(t, false) }
func TestHTTPClientSSE(t *testing.T)  { runHTTPClient(t, true) }

func runHTTPClient(t *testing.T, sse bool) {
	srv := fakeMCP(t, sse)
	defer srv.Close()

	c, err := StartHTTP(context.Background(), "fake", srv.URL, nil)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	if c.sessionID != "sess-123" {
		t.Fatalf("session id not captured: %q", c.sessionID)
	}
	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "fake_echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	res, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.Content != "pong" || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// Ensure *httpClient satisfies the Server interface.
var _ Server = (*httpClient)(nil)
