// Package config loads layered configuration for ties.
//
// Precedence (low to high): built-in defaults < global config file
// (~/.config/ties/ties.json) < project config (.ties.json, discovered by
// walking up from the working directory) < environment variables.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProviderConfig holds credentials and endpoint overrides for a single
// model provider. The provider may be a built-in ("anthropic", "openai") or a
// user-defined custom provider that speaks a compatible protocol.
type ProviderConfig struct {
	APIKey  string `json:"apiKey,omitempty"`
	BaseURL string `json:"baseUrl,omitempty"`
	// Type selects which wire protocol to use for a CUSTOM provider:
	// "openai" (Chat Completions) or "anthropic" (Messages). Empty means the
	// provider key is itself a built-in name.
	Type string `json:"type,omitempty"`
	// Label is an optional human-friendly name shown by `ties models`.
	Label string `json:"label,omitempty"`
	// Models lists known model ids for this provider (display + discovery).
	Models []string `json:"models,omitempty"`
	// Headers are extra HTTP headers sent on every request to this provider.
	Headers map[string]string `json:"headers,omitempty"`
}

// MCPServer describes an external Model Context Protocol server that ties
// launches and connects to over stdio.
type MCPServer struct {
	// Command is the executable to run (stdio transport).
	Command string `json:"command,omitempty"`
	// Args are passed to the command.
	Args []string `json:"args,omitempty"`
	// Env is extra environment for the server process (KEY=VALUE merged on top
	// of the current environment).
	Env map[string]string `json:"env,omitempty"`
	// URL, when set, selects the Streamable HTTP transport instead of stdio.
	URL string `json:"url,omitempty"`
	// Headers are extra HTTP headers sent on every request (HTTP transport).
	Headers map[string]string `json:"headers,omitempty"`
	// Enabled toggles the server without removing its definition.
	Enabled *bool `json:"enabled,omitempty"`
}

// IsHTTP reports whether the server uses the HTTP transport.
func (s MCPServer) IsHTTP() bool { return s.URL != "" }

// IsEnabled reports whether the server should be started. Missing == enabled.
func (s MCPServer) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// Config is the fully merged configuration.
type Config struct {
	// Model is the default model in "provider/model" form, e.g.
	// "anthropic/claude-3-5-sonnet-latest".
	Model string `json:"model,omitempty"`
	// Models is an optional ordered fallback chain in "provider/model" form.
	// If the primary model errors, the next is tried.
	Models []string `json:"models,omitempty"`
	// MaxSteps caps the agent reasoning/acting iterations per run.
	MaxSteps int `json:"maxSteps,omitempty"`
	// MaxToolOutput caps the characters of a tool result fed back to the model
	// (0 = a sensible default). Prevents one huge file from blowing context.
	MaxToolOutput int `json:"maxToolOutput,omitempty"`
	// Retries is the number of automatic retries on transient provider errors.
	Retries int `json:"retries,omitempty"`
	// MaxCostUSD optionally caps the estimated spend of a single run (0 = off).
	MaxCostUSD float64 `json:"maxCostUSD,omitempty"`
	// MaxTokens optionally caps total tokens consumed by a single run (0 = off).
	MaxTokens int `json:"maxTokens,omitempty"`
	// ToolTimeout caps the seconds a single tool call may run (0 = no limit).
	ToolTimeout int `json:"toolTimeout,omitempty"`
	// Providers holds per-provider credentials keyed by provider name.
	Providers map[string]ProviderConfig `json:"providers,omitempty"`
	// Permission rules: map of "tool" or "tool:pattern" -> allow|ask|deny.
	Permission map[string]string `json:"permission,omitempty"`
	// MCP servers keyed by a friendly name.
	MCP map[string]MCPServer `json:"mcp,omitempty"`
	// SkillDirs are additional directories scanned for SKILL.md files.
	SkillDirs []string `json:"skillDirs,omitempty"`
	// Theme controls TUI colors ("auto", "dark", "light").
	Theme string `json:"theme,omitempty"`

	// sources records the files that contributed to this config (for `ties config`).
	sources []string `json:"-"`
}

// Sources returns the config files that were merged, lowest precedence first.
func (c *Config) Sources() []string { return c.sources }

// Default returns the built-in default configuration.
func Default() *Config {
	return &Config{
		Model:         "anthropic/claude-3-5-sonnet-latest",
		MaxSteps:      50,
		MaxToolOutput: 16000,
		Retries:       2,
		Providers:     map[string]ProviderConfig{},
		Permission:    map[string]string{"*": "ask", "read": "allow", "list": "allow", "glob": "allow", "grep": "allow"},
		MCP:           map[string]MCPServer{},
		Theme:         "auto",
	}
}

// GlobalPath returns the path of the user-level config file.
func GlobalPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ties", "ties.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".ties", "ties.json")
	}
	return filepath.Join(home, ".config", "ties", "ties.json")
}

// FindProjectConfig walks up from dir looking for a ".ties.json" file and
// returns its path (or "" if none found).
func FindProjectConfig(dir string) string {
	cur, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(cur, ".ties.json")
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// Load merges defaults, the global file, the nearest project file and
// environment variables. workingDir is used to discover the project config.
func Load(workingDir string) (*Config, error) {
	cfg := Default()

	if p := GlobalPath(); p != "" {
		if err := mergeFile(cfg, p); err != nil {
			return nil, err
		}
	}
	if p := FindProjectConfig(workingDir); p != "" {
		if err := mergeFile(cfg, p); err != nil {
			return nil, err
		}
	}
	applyEnv(cfg)
	return cfg, nil
}

func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var override Config
	if err := json.Unmarshal(data, &override); err != nil {
		return &ParseError{Path: path, Err: err}
	}
	cfg.merge(&override)
	cfg.sources = append(cfg.sources, path)
	return nil
}

// merge overlays non-zero fields of o onto c.
func (c *Config) merge(o *Config) {
	if o.Model != "" {
		c.Model = o.Model
	}
	if o.MaxSteps != 0 {
		c.MaxSteps = o.MaxSteps
	}
	if o.MaxToolOutput != 0 {
		c.MaxToolOutput = o.MaxToolOutput
	}
	if o.Retries != 0 {
		c.Retries = o.Retries
	}
	if o.MaxCostUSD != 0 {
		c.MaxCostUSD = o.MaxCostUSD
	}
	if o.MaxTokens != 0 {
		c.MaxTokens = o.MaxTokens
	}
	if o.ToolTimeout != 0 {
		c.ToolTimeout = o.ToolTimeout
	}
	if len(o.Models) > 0 {
		c.Models = o.Models
	}
	if o.Theme != "" {
		c.Theme = o.Theme
	}
	for k, v := range o.Providers {
		cur := c.Providers[k]
		if v.APIKey != "" {
			cur.APIKey = v.APIKey
		}
		if v.BaseURL != "" {
			cur.BaseURL = v.BaseURL
		}
		if v.Type != "" {
			cur.Type = v.Type
		}
		if v.Label != "" {
			cur.Label = v.Label
		}
		if len(v.Models) > 0 {
			cur.Models = v.Models
		}
		if len(v.Headers) > 0 {
			if cur.Headers == nil {
				cur.Headers = map[string]string{}
			}
			for hk, hv := range v.Headers {
				cur.Headers[hk] = hv
			}
		}
		c.Providers[k] = cur
	}
	for k, v := range o.Permission {
		c.Permission[k] = v
	}
	for k, v := range o.MCP {
		c.MCP[k] = v
	}
	if len(o.SkillDirs) > 0 {
		c.SkillDirs = append(c.SkillDirs, o.SkillDirs...)
	}
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("TIES_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := os.Getenv("TIES_MAX_STEPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxSteps = n
		}
	}
	if v := os.Getenv("TIES_TOOL_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ToolTimeout = n
		}
	}
	if v := os.Getenv("TIES_THEME"); v != "" {
		cfg.Theme = v
	}
	// Provider keys via well-known env vars.
	setProviderKey(cfg, "anthropic", firstEnv("TIES_ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY"))
	setProviderKey(cfg, "openai", firstEnv("TIES_OPENAI_API_KEY", "OPENAI_API_KEY"))
	setProviderKey(cfg, "gemini", firstEnv("TIES_GEMINI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"))
	if v := os.Getenv("ANTHROPIC_BASE_URL"); v != "" {
		setProviderBaseURL(cfg, "anthropic", v)
	}
	if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
		setProviderBaseURL(cfg, "openai", v)
	}
	if v := os.Getenv("GEMINI_BASE_URL"); v != "" {
		setProviderBaseURL(cfg, "gemini", v)
	}
}

func setProviderKey(cfg *Config, name, key string) {
	if key == "" {
		return
	}
	p := cfg.Providers[name]
	p.APIKey = key
	cfg.Providers[name] = p
}

func setProviderBaseURL(cfg *Config, name, url string) {
	p := cfg.Providers[name]
	p.BaseURL = url
	cfg.Providers[name] = p
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// Save writes cfg as pretty JSON to path, creating parent dirs.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// ParseError wraps a JSON parse failure with the offending file path.
type ParseError struct {
	Path string
	Err  error
}

func (e *ParseError) Error() string { return "config " + e.Path + ": " + e.Err.Error() }
func (e *ParseError) Unwrap() error { return e.Err }
