package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/defomok-max/Ties/internal/provider"
)

func TestGeminiStreamTextAndToolCall(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "k" {
			t.Errorf("missing api key in query")
		}
		if r.URL.Query().Get("alt") != "sse" {
			t.Errorf("expected alt=sse")
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = io.WriteString(w, "data: "+s+"\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
		write(`{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello "}]}}]}`)
		write(`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"list","args":{"path":"."}}}]}}]}`)
		write(`{"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":7}}`)
	}))
	defer srv.Close()

	p, err := provider.New("gemini", provider.Options{APIKey: "k", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	req := provider.Request{
		Model:    "gemini-1.5-flash",
		System:   "be brief",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		Tools:    []provider.ToolDefinition{{Name: "list", Description: "list", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var toolName string
	var usage provider.Usage
	for ev := range ch {
		switch ev.Type {
		case provider.EventTextDelta:
			text += ev.Text
		case provider.EventToolCall:
			toolName = ev.ToolCall.Name
			if !strings.Contains(string(ev.ToolCall.Arguments), "path") {
				t.Errorf("tool args missing path: %s", ev.ToolCall.Arguments)
			}
		case provider.EventUsage:
			usage = *ev.Usage
		case provider.EventError:
			t.Fatalf("stream error: %v", ev.Err)
		}
	}
	if text != "Hello " {
		t.Errorf("text = %q", text)
	}
	if toolName != "list" {
		t.Errorf("toolName = %q", toolName)
	}
	if usage.InputTokens != 11 || usage.OutputTokens != 7 {
		t.Errorf("usage = %+v", usage)
	}
	// System prompt must be carried out-of-band.
	if gotBody["system_instruction"] == nil {
		t.Errorf("system_instruction not sent: %v", gotBody)
	}
}

func TestGeminiAPIErrorRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, "rate limited")
	}))
	defer srv.Close()
	p, _ := provider.New("gemini", provider.Options{APIKey: "k", BaseURL: srv.URL})
	_, err := p.Stream(context.Background(), provider.Request{Model: "gemini-1.5-flash"})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errorsAs(err, &apiErr) || !apiErr.Retryable() {
		t.Fatalf("expected retryable APIError, got %v", err)
	}
}

// errorsAs is a tiny local shim to avoid importing errors in every test file.
func errorsAs(err error, target **APIError) bool {
	if e, ok := err.(*APIError); ok {
		*target = e
		return true
	}
	return false
}
