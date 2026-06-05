package session

import (
	"testing"

	"github.com/defomok-max/Ties/internal/provider"
)

func TestCreateAppendOpen(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Create("anthropic/claude")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(provider.Message{Role: provider.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(provider.Message{Role: provider.RoleAssistant, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	id := s.Meta.ID
	_ = s.Close()

	reopened, err := store.Open(id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if len(reopened.Messages) != 2 {
		t.Fatalf("got %d messages", len(reopened.Messages))
	}
	if reopened.Messages[0].Content != "hi" {
		t.Errorf("msg0 = %q", reopened.Messages[0].Content)
	}

	metas, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Model != "anthropic/claude" {
		t.Fatalf("list = %+v", metas)
	}
}
