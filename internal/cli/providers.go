package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/defomok-max/Ties/internal/config"
)

// knownModels is a small built-in catalogue of popular model ids per provider,
// used to populate the model picker with good defaults. Users can always add
// their own via custom providers or the "custom…" entry.
var knownModels = map[string][]string{
	"anthropic": {
		"claude-3-5-sonnet-latest",
		"claude-3-5-haiku-latest",
		"claude-3-opus-latest",
	},
	"openai": {
		"gpt-4o",
		"gpt-4o-mini",
		"o3-mini",
		"gpt-4-turbo",
	},
	"gemini": {
		"gemini-1.5-pro",
		"gemini-1.5-flash",
		"gemini-2.0-flash",
	},
	"bedrock": {
		"anthropic.claude-3-5-sonnet-20240620-v1:0",
		"anthropic.claude-3-haiku-20240307-v1:0",
	},
}

// editGlobal loads the global config, applies fn, and saves it back. fn should
// mutate cfg in place; returning an error aborts the save.
func editGlobal(fn func(cfg *config.Config) error) error {
	cfg, err := loadGlobal()
	if err != nil {
		return err
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]config.ProviderConfig{}
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return config.Save(config.GlobalPath(), cfg)
}

// providerNames returns provider names sorted, built-ins (configured) and
// custom alike.
func providerNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Providers))
	for n := range cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// providerKind labels a provider entry as built-in or a custom protocol.
func providerKind(pc config.ProviderConfig) string {
	if pc.Type != "" {
		return "custom:" + pc.Type
	}
	return "built-in"
}

// providerReady reports whether a provider can authenticate (key, base URL, or
// AWS creds for bedrock — handled by the caller for env).
func providerReady(name string, pc config.ProviderConfig) bool {
	return pc.APIKey != "" || pc.BaseURL != "" || name == "bedrock"
}

// modelsForProvider returns the model ids to show for a provider: its own
// configured Models if any, else the built-in catalogue for that name/type.
func modelsForProvider(name string, pc config.ProviderConfig) []string {
	if len(pc.Models) > 0 {
		return pc.Models
	}
	if m, ok := knownModels[name]; ok {
		return m
	}
	// Custom provider: fall back to the catalogue for its wire type.
	if m, ok := knownModels[pc.Type]; ok {
		return m
	}
	return nil
}

// modelCandidates aggregates every "provider/model" string the picker can
// offer, across all configured providers plus the built-in catalogue.
func modelCandidates(cfg *config.Config) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	// Configured providers first (most relevant to the user).
	for _, name := range providerNames(cfg) {
		for _, m := range modelsForProvider(name, cfg.Providers[name]) {
			add(name + "/" + m)
		}
	}
	// Then the built-in catalogue for any provider not yet covered.
	builtins := make([]string, 0, len(knownModels))
	for n := range knownModels {
		builtins = append(builtins, n)
	}
	sort.Strings(builtins)
	for _, name := range builtins {
		for _, m := range knownModels[name] {
			add(name + "/" + m)
		}
	}
	return out
}

// addCustomProvider registers (or overwrites) a custom provider in the global
// config. wireType must be "openai" or "anthropic".
func addCustomProvider(name, wireType, label, baseURL, apiKey string, models []string, headers map[string]string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("provider name is required")
	}
	if wireType != "openai" && wireType != "anthropic" {
		return fmt.Errorf("type must be 'openai' or 'anthropic' (got %q)", wireType)
	}
	return editGlobal(func(cfg *config.Config) error {
		pc := cfg.Providers[name]
		pc.Type = wireType
		if label != "" {
			pc.Label = label
		}
		if baseURL != "" {
			pc.BaseURL = baseURL
		}
		if apiKey != "" {
			pc.APIKey = apiKey
		}
		if len(models) > 0 {
			pc.Models = models
		}
		if len(headers) > 0 {
			if pc.Headers == nil {
				pc.Headers = map[string]string{}
			}
			for k, v := range headers {
				pc.Headers[k] = v
			}
		}
		cfg.Providers[name] = pc
		return nil
	})
}

// removeProvider deletes a provider entry from the global config.
func removeProvider(name string) error {
	return editGlobal(func(cfg *config.Config) error {
		if _, ok := cfg.Providers[name]; !ok {
			return fmt.Errorf("no provider named %q", name)
		}
		delete(cfg.Providers, name)
		return nil
	})
}

// setProviderField updates a single string field of a provider entry.
func setProviderField(name, field, value string) error {
	return editGlobal(func(cfg *config.Config) error {
		pc := cfg.Providers[name]
		switch field {
		case "apiKey":
			pc.APIKey = value
		case "baseUrl":
			pc.BaseURL = value
		case "type":
			if value != "" && value != "openai" && value != "anthropic" {
				return fmt.Errorf("type must be 'openai' or 'anthropic'")
			}
			pc.Type = value
		case "label":
			pc.Label = value
		default:
			return fmt.Errorf("unknown field %q", field)
		}
		cfg.Providers[name] = pc
		return nil
	})
}

// addProviderModel appends a model id to a provider's model list (dedup).
func addProviderModel(name, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("model id is required")
	}
	return editGlobal(func(cfg *config.Config) error {
		pc := cfg.Providers[name]
		for _, m := range pc.Models {
			if m == model {
				return nil
			}
		}
		pc.Models = append(pc.Models, model)
		cfg.Providers[name] = pc
		return nil
	})
}

// removeProviderModel drops a model id from a provider's model list.
func removeProviderModel(name, model string) error {
	return editGlobal(func(cfg *config.Config) error {
		pc := cfg.Providers[name]
		out := pc.Models[:0]
		for _, m := range pc.Models {
			if m != model {
				out = append(out, m)
			}
		}
		pc.Models = out
		cfg.Providers[name] = pc
		return nil
	})
}

// setFallbackChain stores an ordered fallback chain of "provider/model" ids.
func setFallbackChain(models []string) error {
	return editGlobal(func(cfg *config.Config) error {
		cfg.Models = models
		return nil
	})
}
