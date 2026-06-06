package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/defomok-max/Ties/internal/config"
	"github.com/defomok-max/Ties/internal/provider"
	"github.com/defomok-max/Ties/internal/session"
)

// ---- auth ----

func cmdAuth(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ties auth <login|list|logout> [provider] [key]")
	}
	switch args[0] {
	case "login":
		return authLogin(args[1:])
	case "list":
		return authList()
	case "logout":
		return authLogout(args[1:])
	default:
		return fmt.Errorf("unknown auth subcommand %q", args[0])
	}
}

func loadGlobal() (*config.Config, error) {
	path := config.GlobalPath()
	cfg := &config.Config{Providers: map[string]config.ProviderConfig{}, Permission: map[string]string{}, MCP: map[string]config.MCPServer{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]config.ProviderConfig{}
	}
	return cfg, nil
}

func authLogin(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ties auth login <provider> [key]")
	}
	name := args[0]
	var key string
	if len(args) > 1 {
		key = args[1]
	} else {
		fmt.Print("Enter API key for " + name + ": ")
		var line string
		_, _ = fmt.Scanln(&line)
		key = strings.TrimSpace(line)
	}
	if key == "" {
		return fmt.Errorf("no key provided")
	}
	cfg, err := loadGlobal()
	if err != nil {
		return err
	}
	pc := cfg.Providers[name]
	pc.APIKey = key
	cfg.Providers[name] = pc
	if err := config.Save(config.GlobalPath(), cfg); err != nil {
		return err
	}
	fmt.Printf("Saved %s credentials to %s\n", name, config.GlobalPath())
	return nil
}

func authList() error {
	cfg, err := loadGlobal()
	if err != nil {
		return err
	}
	if len(cfg.Providers) == 0 {
		fmt.Println("(no stored credentials)")
		return nil
	}
	for name, pc := range cfg.Providers {
		fmt.Printf("%-12s %s\n", name, mask(pc.APIKey))
	}
	return nil
}

func authLogout(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ties auth logout <provider>")
	}
	cfg, err := loadGlobal()
	if err != nil {
		return err
	}
	delete(cfg.Providers, args[0])
	if err := config.Save(config.GlobalPath(), cfg); err != nil {
		return err
	}
	fmt.Printf("Removed %s credentials\n", args[0])
	return nil
}

func mask(s string) string {
	if s == "" {
		return "(empty)"
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "…" + s[len(s)-4:]
}

// ---- config ----

func cmdConfig(args []string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "path" {
		fmt.Println("global:  " + config.GlobalPath())
		if p := config.FindProjectConfig(root); p != "" {
			fmt.Println("project: " + p)
		}
		return nil
	}
	// Mask provider keys before printing.
	for name, pc := range cfg.Providers {
		pc.APIKey = mask(pc.APIKey)
		cfg.Providers[name] = pc
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(data))
	if srcs := cfg.Sources(); len(srcs) > 0 {
		fmt.Fprintln(os.Stderr, "\nmerged from: "+strings.Join(srcs, ", "))
	}
	return nil
}

// ---- mcp ----

func cmdMCP(args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		root, _ := os.Getwd()
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		if len(cfg.MCP) == 0 {
			fmt.Println("(no MCP servers configured)")
			return nil
		}
		for name, srv := range cfg.MCP {
			status := "enabled"
			if !srv.IsEnabled() {
				status = "disabled"
			}
			target := strings.TrimSpace(srv.Command + " " + strings.Join(srv.Args, " "))
			if srv.IsHTTP() {
				target = "http " + srv.URL
			}
			fmt.Printf("%-16s %s  [%s]\n", name, target, status)
		}
		return nil
	case "add":
		return mcpAdd(args[1:])
	case "remove", "rm":
		return mcpRemove(args[1:])
	case "tools":
		ctx := context.Background()
		a, err := setup(ctx, true)
		if err != nil {
			return err
		}
		defer a.close()
		for _, n := range a.reg.Names() {
			fmt.Println(n)
		}
		return nil
	default:
		return fmt.Errorf("unknown mcp subcommand %q", args[0])
	}
}

// mcpAdd registers an MCP server in the global config. Two forms:
//
//	ties mcp add <name> --url <url> [--header K:V ...]      (HTTP transport)
//	ties mcp add <name> [--header K:V ...] -- <command> [args...]   (stdio)
func mcpAdd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ties mcp add <name> (--url <url> | -- <command> [args...]) [--header K:V]")
	}
	name := args[0]
	rest := args[1:]
	srv := config.MCPServer{}
	headers := map[string]string{}
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--url":
			if i+1 >= len(rest) {
				return fmt.Errorf("--url needs a value")
			}
			srv.URL = rest[i+1]
			i++
		case "--header", "-H":
			if i+1 >= len(rest) {
				return fmt.Errorf("--header needs K:V")
			}
			k, v, ok := strings.Cut(rest[i+1], ":")
			if !ok {
				return fmt.Errorf("--header must be in K:V form")
			}
			headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
			i++
		case "--":
			if i+1 < len(rest) {
				srv.Command = rest[i+1]
				srv.Args = append([]string{}, rest[i+2:]...)
			}
			i = len(rest)
		default:
			// First bare token without "--" is treated as the command.
			if srv.Command == "" && srv.URL == "" {
				srv.Command = rest[i]
				srv.Args = append([]string{}, rest[i+1:]...)
				i = len(rest)
			}
		}
	}
	if srv.URL == "" && srv.Command == "" {
		return fmt.Errorf("provide --url <url> or -- <command> [args...]")
	}
	if len(headers) > 0 {
		srv.Headers = headers
	}
	cfg, err := loadGlobal()
	if err != nil {
		return err
	}
	if cfg.MCP == nil {
		cfg.MCP = map[string]config.MCPServer{}
	}
	cfg.MCP[name] = srv
	if err := config.Save(config.GlobalPath(), cfg); err != nil {
		return err
	}
	if srv.IsHTTP() {
		fmt.Printf("Added MCP server %q (http %s) to %s\n", name, srv.URL, config.GlobalPath())
	} else {
		fmt.Printf("Added MCP server %q (%s) to %s\n", name, strings.TrimSpace(srv.Command+" "+strings.Join(srv.Args, " ")), config.GlobalPath())
	}
	return nil
}

func mcpRemove(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ties mcp remove <name>")
	}
	cfg, err := loadGlobal()
	if err != nil {
		return err
	}
	if _, ok := cfg.MCP[args[0]]; !ok {
		return fmt.Errorf("no MCP server named %q", args[0])
	}
	delete(cfg.MCP, args[0])
	if err := config.Save(config.GlobalPath(), cfg); err != nil {
		return err
	}
	fmt.Printf("Removed MCP server %q\n", args[0])
	return nil
}

// ---- session ----

func cmdSession(args []string) error {
	root, _ := os.Getwd()
	store, err := session.NewStore(root + "/.ties/sessions")
	if err != nil {
		return err
	}
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		metas, err := store.List()
		if err != nil {
			return err
		}
		if len(metas) == 0 {
			fmt.Println("(no sessions)")
			return nil
		}
		for _, m := range metas {
			fmt.Printf("%s  %-40s  %s\n", m.ID, m.Model, m.Created.Format("2006-01-02 15:04"))
		}
		return nil
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: ties session show <id>")
		}
		s, err := store.Open(args[1])
		if err != nil {
			return err
		}
		defer func() { _ = s.Close() }()
		fmt.Println(s.Render())
		return nil
	case "export":
		rest := args[1:]
		format := "md"
		var id string
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--format", "-f":
				if i+1 < len(rest) {
					format = rest[i+1]
					i++
				}
			default:
				if id == "" {
					id = rest[i]
				}
			}
		}
		if id == "" {
			return fmt.Errorf("usage: ties session export <id> [--format md|html]")
		}
		s, err := store.Open(id)
		if err != nil {
			return err
		}
		defer func() { _ = s.Close() }()
		out, err := s.Export(format)
		if err != nil {
			return err
		}
		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}
		return nil
	default:
		return fmt.Errorf("unknown session subcommand %q", args[0])
	}
}

// ---- skill ----

func cmdSkill(args []string) error {
	if len(args) > 0 && args[0] == "add" {
		return skillAdd(args[1:])
	}
	ctx := context.Background()
	a, err := setup(ctx, false)
	if err != nil {
		return err
	}
	defer a.close()
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		if len(a.skills) == 0 {
			fmt.Println("(no skills found)")
			return nil
		}
		for _, s := range a.skills {
			fmt.Printf("%-24s %s\n", s.Name, s.Description)
		}
		return nil
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: ties skill show <name>")
		}
		for _, s := range a.skills {
			if s.Name == args[1] {
				fmt.Println(s.Body)
				return nil
			}
		}
		return fmt.Errorf("skill %q not found", args[1])
	default:
		return fmt.Errorf("unknown skill subcommand %q", args[0])
	}
}

// skillAdd scaffolds skills/<name>/SKILL.md with valid frontmatter under the
// working directory so the agent discovers it on the next run.
func skillAdd(args []string) error {
	force := false
	var name string
	for _, a := range args {
		switch a {
		case "--force", "-f":
			force = true
		default:
			if name == "" {
				name = a
			}
		}
	}
	if name == "" {
		return fmt.Errorf("usage: ties skill add <name> [--force]")
	}
	if !skillNameOK(name) {
		return fmt.Errorf("skill name must be lowercase letters, digits, '-' or '_'")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "skills", name)
	path := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", path)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := "---\n" +
		"name: " + name + "\n" +
		"description: One-line summary of when the agent should load this skill.\n" +
		"---\n\n" +
		"# " + name + "\n\n" +
		"## When to use\n\n" +
		"_Describe the situations where this knowledge applies._\n\n" +
		"## Steps\n\n" +
		"1. _First step._\n2. _Second step._\n\n" +
		"## Notes\n\n" +
		"- _Gotchas, references, and best practices._\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Printf("Created %s\n", path)
	fmt.Println("Edit the description and body, then it will be discovered automatically.")
	return nil
}

func skillNameOK(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// ---- tools ----

func cmdTools(_ []string) error {
	ctx := context.Background()
	a, err := setup(ctx, false)
	if err != nil {
		return err
	}
	defer a.close()
	for _, name := range a.reg.Names() {
		t, _ := a.reg.Get(name)
		fmt.Printf("%-10s %s\n", name, t.Description())
	}
	return nil
}

// ---- models ----

func cmdModels(_ []string) error {
	root, _ := os.Getwd()
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	fmt.Println("Default model: " + cfg.Model)
	if len(cfg.Models) > 0 {
		fmt.Println("Fallback chain: " + strings.Join(cfg.Models, " → "))
	}
	fmt.Println("Built-in providers: " + strings.Join(provider.Available(), ", "))

	// Configured providers (built-in + custom), with key status and models.
	names := make([]string, 0, len(cfg.Providers))
	for n := range cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) > 0 {
		fmt.Println("\nConfigured providers:")
		for _, n := range names {
			pc := cfg.Providers[n]
			kind := "built-in"
			if pc.Type != "" {
				kind = "custom:" + pc.Type
			}
			key := "no key"
			if pc.APIKey != "" {
				key = "key set"
			}
			label := n
			if pc.Label != "" {
				label = pc.Label
			}
			fmt.Printf("  %-14s [%s, %s]", label, kind, key)
			if len(pc.Models) > 0 {
				fmt.Printf("  models: %s", strings.Join(pc.Models, ", "))
			}
			fmt.Println()
		}
	}
	return nil
}
