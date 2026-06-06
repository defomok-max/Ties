package bedrock

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/defomok-max/Ties/internal/provider"
)

func TestEndpoint(t *testing.T) {
	c := &client{region: "us-west-2"}
	got := c.endpoint("anthropic.claude-3-5-sonnet-20240620-v1:0")
	want := "https://bedrock-runtime.us-west-2.amazonaws.com/model/anthropic.claude-3-5-sonnet-20240620-v1%3A0/invoke"
	if got != want {
		t.Fatalf("endpoint = %s", got)
	}
}

// TestInvokeOnce exercises the non-streaming InvokeModel fallback path.
func TestInvokeOnce(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("request was not signed")
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello"},{"type":"tool_use","id":"t1","name":"read","input":{"path":"x"}}],"usage":{"input_tokens":11,"output_tokens":7}}`))
	}))
	defer srv.Close()

	c := &client{
		region:        "us-east-1",
		creds:         awsCreds{accessKeyID: "AKID", secretAccessKey: "SECRET"},
		http:          srv.Client(),
		now:           func() time.Time { return time.Unix(0, 0).UTC() },
		headers:       nil,
		disableStream: true,
	}
	// Point the endpoint at the test server by overriding the host via baseURL trick:
	c.endpointOverride = srv.URL + "/model/%s/invoke"

	req := provider.Request{
		Model:    "anthropic.claude-3-5-sonnet",
		System:   "sys",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		Tools:    []provider.ToolDefinition{{Name: "read", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
	ch, err := c.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var toolCalls int
	var usage provider.Usage
	for ev := range ch {
		switch ev.Type {
		case provider.EventTextDelta:
			text += ev.Text
		case provider.EventToolCall:
			toolCalls++
		case provider.EventUsage:
			usage = *ev.Usage
		}
	}
	if text != "hello" {
		t.Fatalf("text = %q", text)
	}
	if toolCalls != 1 {
		t.Fatalf("toolCalls = %d", toolCalls)
	}
	if usage.InputTokens != 11 || usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v", usage)
	}
	if gotBody["anthropic_version"] != anthropicVer {
		t.Fatalf("missing anthropic_version: %v", gotBody["anthropic_version"])
	}
	if _, hasModel := gotBody["model"]; hasModel {
		t.Fatal("body must not contain a model field (it is in the URL)")
	}
}

func TestMissingCreds(t *testing.T) {
	c := &client{region: "us-east-1", http: http.DefaultClient}
	if _, err := c.Stream(context.Background(), provider.Request{Model: "m"}); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("expected credentials error, got %v", err)
	}
}

// chunkFrame wraps an Anthropic streaming event JSON into a Bedrock
// event-stream "chunk" frame, the way the real service does.
func chunkFrame(inner string) []byte {
	payload, _ := json.Marshal(chunkPayload{Bytes: []byte(inner)})
	return encodeESMessage(map[string]string{
		":message-type": "event",
		":event-type":   "chunk",
		":content-type": "application/json",
	}, payload)
}

func TestStreamEventStream(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":11,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hel"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"read"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"x\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","usage":{"output_tokens":7}}`,
		`{"type":"message_stop"}`,
	}

	var gotBody map[string]any
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("request was not signed")
		}
		gotAccept = r.Header.Get("Accept")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		for _, e := range events {
			_, _ = w.Write(chunkFrame(e))
		}
	}))
	defer srv.Close()

	c := &client{
		region: "us-east-1",
		creds:  awsCreds{accessKeyID: "AKID", secretAccessKey: "SECRET"},
		http:   srv.Client(),
		now:    func() time.Time { return time.Unix(0, 0).UTC() },
	}
	c.endpointOverride = srv.URL + "/model/%s/invoke-with-response-stream"

	req := provider.Request{
		Model:    "anthropic.claude-3-5-sonnet",
		System:   "sys",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		Tools:    []provider.ToolDefinition{{Name: "read", Description: "d"}},
	}
	ch, err := c.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	var text string
	var deltas int
	var toolArgs string
	var usage provider.Usage
	for ev := range ch {
		switch ev.Type {
		case provider.EventTextDelta:
			text += ev.Text
			deltas++
		case provider.EventToolCall:
			toolArgs = string(ev.ToolCall.Arguments)
		case provider.EventUsage:
			usage = *ev.Usage
		case provider.EventError:
			t.Fatalf("unexpected stream error: %v", ev.Err)
		}
	}

	if text != "hello" {
		t.Fatalf("text = %q", text)
	}
	if deltas != 2 {
		t.Fatalf("expected 2 incremental text deltas, got %d", deltas)
	}
	if toolArgs != `{"path":"x"}` {
		t.Fatalf("toolArgs = %q", toolArgs)
	}
	if usage.InputTokens != 11 || usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v", usage)
	}
	if gotAccept != "application/vnd.amazon.eventstream" {
		t.Fatalf("Accept = %q", gotAccept)
	}
	if _, hasModel := gotBody["model"]; hasModel {
		t.Fatal("body must not contain a model field (it is in the URL)")
	}
}

func TestStreamSurfacesException(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		frame := encodeESMessage(map[string]string{
			":message-type":   "exception",
			":exception-type": "throttlingException",
		}, []byte(`{"message":"slow down"}`))
		_, _ = w.Write(frame)
	}))
	defer srv.Close()

	c := &client{
		region: "us-east-1",
		creds:  awsCreds{accessKeyID: "AKID", secretAccessKey: "SECRET"},
		http:   srv.Client(),
		now:    func() time.Time { return time.Unix(0, 0).UTC() },
	}
	c.endpointOverride = srv.URL + "/model/%s/invoke-with-response-stream"

	ch, err := c.Stream(context.Background(), provider.Request{Model: "m", Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var sawErr bool
	for ev := range ch {
		if ev.Type == provider.EventError {
			sawErr = true
			if !strings.Contains(ev.Err.Error(), "slow down") {
				t.Fatalf("error did not include payload: %v", ev.Err)
			}
		}
	}
	if !sawErr {
		t.Fatal("expected an EventError from the exception frame")
	}
}
