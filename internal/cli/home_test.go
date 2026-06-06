package cli

import (
	"bufio"
	"strings"
	"testing"

	"github.com/defomok-max/Ties/internal/config"
)

func TestReadLine(t *testing.T) {
	in := bufio.NewReader(strings.NewReader("hello\r\nworld\n"))
	if got, ok := readLine(in); !ok || got != "hello" {
		t.Fatalf("first line: got %q ok=%v", got, ok)
	}
	if got, ok := readLine(in); !ok || got != "world" {
		t.Fatalf("second line: got %q ok=%v", got, ok)
	}
	if _, ok := readLine(in); ok {
		t.Fatalf("expected EOF on third read")
	}
}

func TestSaveProviderAndModel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// Ensure no env keys leak a provider into the merged config.
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "AWS_ACCESS_KEY_ID", "AWS_PROFILE"} {
		t.Setenv(k, "")
	}

	if hasUsableProvider() {
		t.Fatal("expected no usable provider before setup")
	}

	if err := saveProvider("anthropic", "sk-ant-xyz", "anthropic/claude-3-5-sonnet-latest"); err != nil {
		t.Fatalf("saveProvider: %v", err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Providers["anthropic"].APIKey != "sk-ant-xyz" {
		t.Fatalf("key not saved: %+v", cfg.Providers["anthropic"])
	}
	if cfg.Model != "anthropic/claude-3-5-sonnet-latest" {
		t.Fatalf("model not set: %q", cfg.Model)
	}
	if !hasUsableProvider() {
		t.Fatal("expected usable provider after saving a key")
	}

	if err := saveModel("openai/gpt-4o"); err != nil {
		t.Fatalf("saveModel: %v", err)
	}
	cfg2, _ := config.Load(dir)
	if cfg2.Model != "openai/gpt-4o" {
		t.Fatalf("model not updated: %q", cfg2.Model)
	}
	// Existing key must be preserved when only the model changes.
	if cfg2.Providers["anthropic"].APIKey != "sk-ant-xyz" {
		t.Fatal("saveModel must not drop existing provider keys")
	}
}
