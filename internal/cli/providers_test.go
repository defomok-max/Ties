package cli

import (
	"testing"

	"github.com/defomok-max/Ties/internal/config"
)

func TestAddAndRemoveCustomProvider(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := addCustomProvider("openrouter", "openai", "OpenRouter",
		"https://openrouter.ai/api/v1", "sk-test", []string{"meta/llama-3.1-70b"}, nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg, err := loadGlobal()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	pc, ok := cfg.Providers["openrouter"]
	if !ok {
		t.Fatal("provider not saved")
	}
	if pc.Type != "openai" || pc.BaseURL == "" || pc.APIKey != "sk-test" {
		t.Fatalf("bad provider: %+v", pc)
	}
	if len(pc.Models) != 1 || pc.Models[0] != "meta/llama-3.1-70b" {
		t.Fatalf("models: %+v", pc.Models)
	}
	if providerKind(pc) != "custom:openai" {
		t.Fatalf("kind: %s", providerKind(pc))
	}
	if !providerReady("openrouter", pc) {
		t.Fatal("should be ready")
	}

	if err := removeProvider("openrouter"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	cfg, _ = loadGlobal()
	if _, ok := cfg.Providers["openrouter"]; ok {
		t.Fatal("provider not removed")
	}
}

func TestAddCustomProviderValidation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := addCustomProvider("x", "bogus", "", "", "", nil, nil); err == nil {
		t.Fatal("expected error for bad type")
	}
	if err := addCustomProvider("", "openai", "", "", "", nil, nil); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestSetProviderFieldAndModels(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := addCustomProvider("local", "openai", "", "http://localhost:11434/v1", "", nil, nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := setProviderField("local", "apiKey", "abc"); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := addProviderModel("local", "qwen2.5-coder"); err != nil {
		t.Fatalf("add model: %v", err)
	}
	if err := addProviderModel("local", "qwen2.5-coder"); err != nil { // dedup
		t.Fatalf("dedup: %v", err)
	}
	cfg, _ := loadGlobal()
	pc := cfg.Providers["local"]
	if pc.APIKey != "abc" {
		t.Fatalf("key not set: %+v", pc)
	}
	if len(pc.Models) != 1 {
		t.Fatalf("expected 1 model, got %+v", pc.Models)
	}
	if err := removeProviderModel("local", "qwen2.5-coder"); err != nil {
		t.Fatalf("remove model: %v", err)
	}
	cfg, _ = loadGlobal()
	if len(cfg.Providers["local"].Models) != 0 {
		t.Fatalf("model not removed: %+v", cfg.Providers["local"].Models)
	}
	if err := setProviderField("local", "type", "bogus"); err == nil {
		t.Fatal("expected type validation error")
	}
}

func TestModelCandidates(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := addCustomProvider("openrouter", "openai", "", "https://openrouter.ai/api/v1", "k",
		[]string{"meta/llama-3.1-70b"}, nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg, _ := loadGlobal()
	cands := modelCandidates(cfg)
	var hasCustom, hasBuiltin bool
	for _, c := range cands {
		if c == "openrouter/meta/llama-3.1-70b" {
			hasCustom = true
		}
		if c == "anthropic/claude-3-5-sonnet-latest" {
			hasBuiltin = true
		}
	}
	if !hasCustom {
		t.Fatalf("custom model missing from candidates: %v", cands)
	}
	if !hasBuiltin {
		t.Fatalf("built-in catalogue missing from candidates: %v", cands)
	}
}

func TestSetFallbackChain(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	chain := []string{"anthropic/claude-3-5-sonnet-latest", "openai/gpt-4o"}
	if err := setFallbackChain(chain); err != nil {
		t.Fatalf("set chain: %v", err)
	}
	cfg, _ := loadGlobal()
	if len(cfg.Models) != 2 || cfg.Models[0] != chain[0] {
		t.Fatalf("chain not saved: %+v", cfg.Models)
	}
}

// Ensure modelsForProvider falls back to the catalogue for known names.
func TestModelsForProviderCatalogue(t *testing.T) {
	got := modelsForProvider("anthropic", config.ProviderConfig{})
	if len(got) == 0 {
		t.Fatal("expected catalogue defaults for anthropic")
	}
}
