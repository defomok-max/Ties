package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
			fmt.Printf("%-16s %s  [%s]\n", name, srv.Command+" "+strings.Join(srv.Args, " "), status)
		}
		return nil
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
	default:
		return fmt.Errorf("unknown session subcommand %q", args[0])
	}
}

// ---- skill ----

func cmdSkill(args []string) error {
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
	fmt.Println("Registered providers: " + strings.Join(provider.Available(), ", "))
	return nil
}
