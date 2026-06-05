package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeAndEnv(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, ".ties.json")
	if err := os.WriteFile(proj, []byte(`{"model":"openai/gpt-4o","permission":{"bash":"allow"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Point global config somewhere empty.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	t.Setenv("TIES_MAX_STEPS", "7")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "openai/gpt-4o" {
		t.Errorf("model = %q", cfg.Model)
	}
	if cfg.MaxSteps != 7 {
		t.Errorf("maxSteps = %d", cfg.MaxSteps)
	}
	if cfg.Permission["bash"] != "allow" {
		t.Errorf("bash perm = %q", cfg.Permission["bash"])
	}
	if cfg.Providers["anthropic"].APIKey != "sk-test" {
		t.Errorf("anthropic key not picked up from env")
	}
}

func TestFindProjectConfig(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, ".ties.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := FindProjectConfig(nested)
	if got != cfgPath {
		t.Errorf("FindProjectConfig = %q want %q", got, cfgPath)
	}
}
