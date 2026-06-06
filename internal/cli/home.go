package cli

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"github.com/defomok-max/Ties/internal/config"
	"github.com/defomok-max/Ties/internal/ui"
	"github.com/defomok-max/Ties/internal/version"
)

// provMeta describes a provider for the onboarding wizard.
type provMeta struct {
	id      string // provider id used in config and provider/model strings
	label   string // human-friendly label
	hint    string // example key shape
	model   string // sensible default model for this provider
	envHint string // how to authenticate without a stored key
	needKey bool   // whether the wizard should prompt for an API key
}

var onboardProviders = []provMeta{
	{id: "anthropic", label: "Anthropic — Claude", hint: "sk-ant-...", model: "anthropic/claude-3-5-sonnet-latest", needKey: true},
	{id: "openai", label: "OpenAI — GPT", hint: "sk-...", model: "openai/gpt-4o", needKey: true},
	{id: "gemini", label: "Google Gemini", hint: "AIza...", model: "gemini/gemini-1.5-pro", needKey: true},
	{id: "bedrock", label: "AWS Bedrock", model: "bedrock/anthropic.claude-3-5-sonnet-20240620-v1:0",
		envHint: "uses AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_REGION from your environment", needKey: false},
}

// cmdMenu launches the interactive home menu explicitly.
func cmdMenu(_ []string) error { return runHome() }

// interactiveTTY reports whether both stdin and stdout are terminals, so the
// menu can read keystrokes and paint a UI.
func interactiveTTY() bool { return isTerminal(os.Stdin) && isTerminal(os.Stdout) }

// runHome is the friendly launcher shown when you start `ties` with no command
// (or via `ties menu`): a first-run setup wizard when no provider is configured,
// then a simple numbered menu — chat, run a task, pick a model, manage keys.
func runHome() error {
	pr := ui.New(os.Stdout, themeName(), ui.ColorEnabled(os.Stdout))
	in := bufio.NewReader(os.Stdin)

	pr.Banner("terminal AI coding agent")
	pr.Println(pr.Dim("  " + version.String()))

	if !hasUsableProvider() {
		pr.Println("")
		pr.Println("  " + pr.Heading("Welcome!") + " Let's connect an AI provider to get you started.")
		if err := onboard(pr, in); err != nil {
			return err
		}
	}

	for {
		printHomeMenu(pr)
		pr.Print("\n  " + pr.Accent("❯ "))
		choice, ok := readLine(in)
		if !ok {
			pr.Println("")
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(choice)) {
		case "1", "", "c", "chat":
			pr.Println(pr.Dim("\n  starting chat — type /exit to come back to the menu\n"))
			if err := cmdChat([]string{"--tui"}); err != nil {
				pr.ErrorLine(err.Error())
			}
		case "2", "r", "run":
			pr.Print("\n  " + pr.Heading("Task") + " (one instruction): ")
			task, ok := readLine(in)
			if !ok {
				return nil
			}
			task = strings.TrimSpace(task)
			if task == "" {
				continue
			}
			if err := cmdRun([]string{task}); err != nil {
				pr.ErrorLine(err.Error())
			}
		case "3", "m", "model":
			if err := chooseModel(pr, in); err != nil {
				pr.ErrorLine(err.Error())
			}
		case "4", "k", "key", "auth":
			if err := onboard(pr, in); err != nil {
				pr.ErrorLine(err.Error())
			}
		case "5", "s", "status":
			pr.Println("")
			_ = cmdModels(nil)
		case "q", "quit", "exit", "0":
			pr.Println(pr.Dim("\n  bye 👋"))
			return nil
		default:
			pr.Println(pr.Warn("  unknown choice — pick a number from the menu"))
		}
	}
}

func printHomeMenu(pr *ui.Printer) {
	cfg, _ := config.Load(mustWd())
	pr.Println("")
	pr.Println("  " + pr.Heading("model") + "  " + cfg.Model + "   " + pr.Dim(providerStatusLine(cfg)))
	pr.Println("")
	pr.Println("  " + pr.Heading("What would you like to do?"))
	item(pr, "1", "Start chat", "full-screen interactive agent")
	item(pr, "2", "Run a task", "give it a single instruction")
	item(pr, "3", "Choose model", "switch the default provider/model")
	item(pr, "4", "Add / change API key", "connect another provider")
	item(pr, "5", "Status", "providers, keys and model")
	item(pr, "q", "Quit", "")
}

func item(pr *ui.Printer, key, title, desc string) {
	line := "   " + pr.Accent(key) + "  " + title
	if desc != "" {
		line += "   " + pr.Dim(desc)
	}
	pr.Println(line)
}

// onboard prompts the user to pick a provider and (if needed) paste an API key,
// then saves it to the global config and makes it the default model.
func onboard(pr *ui.Printer, in *bufio.Reader) error {
	pr.Println("")
	pr.Println("  " + pr.Heading("Connect a provider"))
	for i, p := range onboardProviders {
		extra := p.hint
		if !p.needKey && p.envHint != "" {
			extra = p.envHint
		}
		item(pr, strconv.Itoa(i+1), p.label, extra)
	}
	pr.Print("\n  " + pr.Accent("pick 1-"+strconv.Itoa(len(onboardProviders))+" ❯ "))
	sel, ok := readLine(in)
	if !ok {
		return nil
	}
	idx, err := strconv.Atoi(strings.TrimSpace(sel))
	if err != nil || idx < 1 || idx > len(onboardProviders) {
		pr.Println(pr.Warn("  no provider selected"))
		return nil
	}
	p := onboardProviders[idx-1]

	key := ""
	if p.needKey {
		pr.Print("  " + pr.Heading("API key") + " for " + p.label + " (" + p.hint + "): ")
		k, ok := readLine(in)
		if !ok {
			return nil
		}
		key = strings.TrimSpace(k)
		if key == "" {
			pr.Println(pr.Warn("  no key entered — nothing saved"))
			return nil
		}
	}

	if err := saveProvider(p.id, key, p.model); err != nil {
		return err
	}
	if p.needKey {
		pr.Println("  " + pr.Success("✓ saved") + " " + p.label + " key → " + config.GlobalPath())
	} else {
		pr.Println("  " + pr.Success("✓ selected") + " " + p.label + " — " + p.envHint)
	}
	pr.Println("  " + pr.Dim("default model set to ") + p.model)
	return nil
}

// chooseModel lets the user set the default model from the configured providers.
func chooseModel(pr *ui.Printer, in *bufio.Reader) error {
	pr.Println("")
	pr.Println("  " + pr.Heading("Choose default model"))
	for i, p := range onboardProviders {
		item(pr, strconv.Itoa(i+1), p.model, p.label)
	}
	pr.Println("   " + pr.Accent("c") + "  custom…   " + pr.Dim("type any provider/model"))
	pr.Print("\n  " + pr.Accent("❯ "))
	sel, ok := readLine(in)
	if !ok {
		return nil
	}
	sel = strings.TrimSpace(sel)
	var model string
	if strings.EqualFold(sel, "c") {
		pr.Print("  provider/model: ")
		m, ok := readLine(in)
		if !ok {
			return nil
		}
		model = strings.TrimSpace(m)
	} else if idx, err := strconv.Atoi(sel); err == nil && idx >= 1 && idx <= len(onboardProviders) {
		model = onboardProviders[idx-1].model
	}
	if model == "" {
		pr.Println(pr.Warn("  no model selected"))
		return nil
	}
	if err := saveModel(model); err != nil {
		return err
	}
	pr.Println("  " + pr.Success("✓ default model") + " → " + model)
	return nil
}

// saveProvider stores an API key for a provider and sets the default model.
func saveProvider(name, key, model string) error {
	cfg, err := loadGlobal()
	if err != nil {
		return err
	}
	if key != "" {
		pc := cfg.Providers[name]
		pc.APIKey = key
		cfg.Providers[name] = pc
	}
	if model != "" {
		cfg.Model = model
	}
	return config.Save(config.GlobalPath(), cfg)
}

// saveModel persists only the default model to the global config.
func saveModel(model string) error {
	cfg, err := loadGlobal()
	if err != nil {
		return err
	}
	cfg.Model = model
	return config.Save(config.GlobalPath(), cfg)
}

// hasUsableProvider reports whether the merged config can authenticate at least
// one provider (a stored/env key, a custom base URL, or AWS creds for Bedrock).
func hasUsableProvider() bool {
	cfg, err := config.Load(mustWd())
	if err != nil {
		return false
	}
	for _, pc := range cfg.Providers {
		if pc.APIKey != "" || pc.BaseURL != "" {
			return true
		}
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" || os.Getenv("AWS_PROFILE") != "" {
		return true
	}
	return false
}

func providerStatusLine(cfg *config.Config) string {
	var ready []string
	for name, pc := range cfg.Providers {
		if pc.APIKey != "" || pc.BaseURL != "" {
			ready = append(ready, name)
		}
	}
	if len(ready) == 0 {
		return "no provider connected"
	}
	return "connected: " + strings.Join(ready, ", ")
}

func themeName() string {
	if v := strings.TrimSpace(os.Getenv("TIES_THEME")); v != "" {
		return v
	}
	return "auto"
}

func mustWd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// readLine reads a single line from in, returning false at EOF.
func readLine(in *bufio.Reader) (string, bool) {
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimRight(line, "\r\n"), true
}
