package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/defomok-max/Ties/internal/provider"
)

func sampleSession(t *testing.T) *Session {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Create("anthropic/claude")
	if err != nil {
		t.Fatal(err)
	}
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "fix the <bug> in main.go"},
		{Role: provider.RoleAssistant, Content: "On it.", ToolCalls: []provider.ToolCall{
			{ID: "1", Name: "edit", Arguments: json.RawMessage(`{"path":"main.go"}`)},
		}},
		{Role: provider.RoleTool, Content: "edited", ToolCallID: "1"},
		{Role: provider.RoleAssistant, Content: "Done."},
	}
	for _, m := range msgs {
		if err := s.Append(m); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestExportMarkdown(t *testing.T) {
	s := sampleSession(t)
	defer func() { _ = s.Close() }()
	out, err := s.Export("md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Ties session", "User", "Assistant", "edit(", "fix the <bug>"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown export missing %q:\n%s", want, out)
		}
	}
}

func TestExportHTMLEscapes(t *testing.T) {
	s := sampleSession(t)
	defer func() { _ = s.Close() }()
	out, err := s.Export("html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "<!doctype html>") {
		t.Error("html export should start with doctype")
	}
	if strings.Contains(out, "<bug>") {
		t.Error("html export must escape user angle brackets")
	}
	if !strings.Contains(out, "&lt;bug&gt;") {
		t.Error("html export should contain escaped bug marker")
	}
}

func TestExportUnknownFormat(t *testing.T) {
	s := sampleSession(t)
	defer func() { _ = s.Close() }()
	if _, err := s.Export("pdf"); err == nil {
		t.Fatal("expected error for unknown format")
	}
}
