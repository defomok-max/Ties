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

func TestStreamConvertsResponse(t *testing.T) {
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
		region:  "us-east-1",
		creds:   awsCreds{accessKeyID: "AKID", secretAccessKey: "SECRET"},
		http:    srv.Client(),
		now:     func() time.Time { return time.Unix(0, 0).UTC() },
		headers: nil,
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
