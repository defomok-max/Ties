// Package cli implements the ties command-line interface using only the
// standard library: a small command router plus the wiring that assembles
// config, providers, tools, MCP servers, skills, permissions and sessions.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/defomok-max/Ties/internal/config"
	"github.com/defomok-max/Ties/internal/mcp"
	"github.com/defomok-max/Ties/internal/permission"
	"github.com/defomok-max/Ties/internal/prompt"
	"github.com/defomok-max/Ties/internal/provider"
	"github.com/defomok-max/Ties/internal/provider/resilient"
	"github.com/defomok-max/Ties/internal/skill"
	"github.com/defomok-max/Ties/internal/tool"
	"github.com/defomok-max/Ties/internal/ui"
	"github.com/defomok-max/Ties/internal/version"

	// Register providers via their init() side effects.
	_ "github.com/defomok-max/Ties/internal/provider/anthropic"
	_ "github.com/defomok-max/Ties/internal/provider/openai"
)

// Execute is the entry point invoked by main. It returns a process exit code.
func Execute(args []string) int {
	if len(args) == 0 {
		usage()
		return 1
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "run":
		err = cmdRun(rest)
	case "chat":
		err = cmdChat(rest)
	case "auth":
		err = cmdAuth(rest)
	case "config":
		err = cmdConfig(rest)
	case "mcp":
		err = cmdMCP(rest)
	case "session":
		err = cmdSession(rest)
	case "skill":
		err = cmdSkill(rest)
	case "tools":
		err = cmdTools(rest)
	case "models":
		err = cmdModels(rest)
	case "version", "--version", "-v":
		fmt.Println(version.String())
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "ties: unknown command %q\n\n", cmd)
		usage()
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ties: "+err.Error())
		return 1
	}
	return 0
}

func usage() {
	fmt.Print(`ties — a terminal AI coding agent (Claude Code / OpenCode / Codex style)

Usage:
  ties <command> [flags]

Commands:
  run [prompt]      Run a single agent task (reads stdin if no prompt)
  chat              Start an interactive chat session
  auth              Manage provider credentials (login/list/logout)
  config            Show the merged configuration and its sources
  mcp               Manage MCP servers (list/tools)
  session           Inspect sessions (list/show)
  skill             Inspect skills (list/show)
  tools             List the built-in tools
  models            List available providers and the default model
  version           Print the version
  help              Show this help

Common flags (run/chat):
  -m, --model <provider/model>   Override the model
  -y, --yes                      Auto-approve tool calls (no prompts)
      --resume <id>              Resume an existing session
      --no-session               Do not persist a session (run only)
      --max-steps <n>            Cap agent iterations

Environment:
  ANTHROPIC_API_KEY, OPENAI_API_KEY   Provider credentials
  TIES_MODEL                          Default model override
`)
}

// app bundles everything a command needs to run the agent.
type app struct {
	cfg     *config.Config
	root    string
	reg     *tool.Registry
	skills  []*skill.Skill
	perm    *permission.Engine
	system  string
	clients []*mcp.Client
	ui      *ui.Printer
}

// setup loads config and assembles tools, skills, MCP servers and the prompt.
func setup(ctx context.Context, enableMCP bool) (*app, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	reg := tool.DefaultRegistry(root)

	skillDirs := skill.DefaultDirs(root, cfg.SkillDirs)
	skills := skill.Discover(skillDirs)
	reg.Register(newSkillTool(skills))

	var clients []*mcp.Client
	if enableMCP {
		for name, srv := range cfg.MCP {
			if !srv.IsEnabled() || srv.Command == "" {
				continue
			}
			c, cerr := mcp.Start(ctx, name, srv.Command, srv.Args, srv.Env)
			if cerr != nil {
				fmt.Fprintf(os.Stderr, "ties: mcp %s failed to start: %v\n", name, cerr)
				continue
			}
			clients = append(clients, c)
			tools, terr := c.Tools(ctx)
			if terr != nil {
				fmt.Fprintf(os.Stderr, "ties: mcp %s tools: %v\n", name, terr)
				continue
			}
			for _, t := range tools {
				reg.Register(t)
			}
		}
	}

	sys := prompt.Build(prompt.Params{
		WorkspaceRoot: root,
		OS:            runtime.GOOS,
		SkillCatalog:  skill.Catalog(skills),
	})

	pr := ui.New(os.Stderr, cfg.Theme, ui.ColorEnabled(os.Stderr))

	return &app{
		cfg:     cfg,
		root:    root,
		reg:     reg,
		skills:  skills,
		perm:    permission.New(cfg.Permission),
		system:  sys,
		clients: clients,
		ui:      pr,
	}, nil
}

func (a *app) close() {
	for _, c := range a.clients {
		_ = c.Close()
	}
}

// makeProvider resolves a single "provider/model" string into a provider
// instance (wrapped with retries) and the bare model id. It supports custom
// providers declared in config via a "type" of openai|anthropic.
func (a *app) makeProvider(model string) (provider.Provider, string, error) {
	name, bare := provider.SplitModel(model)
	if name == "" {
		return nil, "", fmt.Errorf("model %q must be in provider/model form", model)
	}
	pc := a.cfg.Providers[name]
	kind := name
	if pc.Type != "" { // custom provider speaking a known protocol
		kind = pc.Type
	}
	p, err := provider.New(kind, provider.Options{APIKey: pc.APIKey, BaseURL: pc.BaseURL, Headers: pc.Headers})
	if err != nil {
		return nil, "", err
	}
	// A built-in endpoint with no key and no custom base cannot authenticate.
	if pc.APIKey == "" && pc.BaseURL == "" {
		return nil, "", fmt.Errorf("no API key for provider %q — run `ties auth login %s` or set the env var", name, name)
	}
	p = resilient.Retrying(p, resilient.RetryOptions{
		MaxRetries: a.cfg.Retries,
		Base:       500 * time.Millisecond,
		OnRetry: func(attempt int, err error, wait time.Duration) {
			a.ui.Println(a.ui.Warn(fmt.Sprintf("· retry %d after %s (%s)", attempt, wait.Round(time.Millisecond), shortErr(err))))
		},
	})
	return p, bare, nil
}

// buildProvider resolves the model (plus any configured fallback chain) into a
// single provider. modelOverride, when set, bypasses the fallback chain.
func (a *app) buildProvider(modelOverride string) (provider.Provider, string, error) {
	models := []string{a.cfg.Model}
	if modelOverride != "" {
		models = []string{modelOverride}
	} else if len(a.cfg.Models) > 0 {
		models = append([]string{}, a.cfg.Models...)
	}

	var entries []resilient.Entry
	var firstBare string
	for i, m := range models {
		p, bare, err := a.makeProvider(m)
		if err != nil {
			// If the primary builds but a later fallback can't, skip it; if the
			// very first fails, surface the error.
			if i == 0 {
				return nil, "", err
			}
			continue
		}
		if firstBare == "" {
			firstBare = bare
		}
		entries = append(entries, resilient.Entry{Provider: p, Model: bare})
	}
	if len(entries) == 0 {
		return nil, "", fmt.Errorf("no usable model could be constructed")
	}
	p := resilient.Chain(entries, resilient.ChainOptions{
		OnFallback: func(from, to string, err error) {
			a.ui.Println(a.ui.Warn(fmt.Sprintf("· model %s failed (%s) — falling back to %s", from, shortErr(err), to)))
		},
	})
	return p, firstBare, nil
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

func (a *app) sessionDir() string { return filepath.Join(a.root, ".ties", "sessions") }

// skillTool lets the model load a skill body on demand.
type skillTool struct{ byName map[string]*skill.Skill }

func newSkillTool(skills []*skill.Skill) *skillTool {
	m := map[string]*skill.Skill{}
	for _, s := range skills {
		m[s.Name] = s
	}
	return &skillTool{byName: m}
}

func (t *skillTool) Name() string { return "skill" }
func (t *skillTool) Description() string {
	return "Load the full body of a named skill (reusable knowledge). Call before acting when a skill description matches the task."
}
func (t *skillTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Skill name from the available skills list"}},"required":["name"]}`)
}
func (t *skillTool) Run(_ context.Context, args json.RawMessage) (tool.Result, error) {
	var a struct {
		Name string `json:"name"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}
	s, ok := t.byName[a.Name]
	if !ok {
		avail := make([]string, 0, len(t.byName))
		for n := range t.byName {
			avail = append(avail, n)
		}
		return tool.Result{Content: "unknown skill. available: " + strings.Join(avail, ", "), IsError: true}, nil
	}
	return tool.Result{Content: s.Body}, nil
}
