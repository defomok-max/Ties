package cli

import (
	"bufio"
	"os"
	"strings"

	"github.com/defomok-max/Ties/internal/config"
	"github.com/defomok-max/Ties/internal/screen"
	"github.com/defomok-max/Ties/internal/ui"
	"github.com/defomok-max/Ties/internal/version"
)

// runHome is the entry point for the bare `ties` command and `ties menu`. It
// prefers a beautiful, mouse-clickable full-screen UI and gracefully falls back
// to the line-based menu when the terminal cannot be driven interactively.
func runHome() error {
	if screen.Supported(os.Stdin, os.Stdout) {
		sc := screen.New(os.Stdin, os.Stdout)
		if err := sc.Start(); err == nil {
			defer sc.Stop()
			return homeLoop(sc)
		}
		// Raw mode unavailable (e.g. restricted console) — fall back.
	}
	return runHomeLine()
}

// listItem is one row in an interactive list. Separators (sep) are shown but
// not selectable; danger rows render in the error color.
type listItem struct {
	id     string
	title  string
	hint   string
	sep    bool
	danger bool
}

// homeLoop drives the top-level menu.
func homeLoop(sc *screen.Screen) error {
	pr := ui.New(os.Stdout, themeName(), true)

	// First-run setup: needs line input, so drop out of the raw screen.
	if !hasUsableProvider() {
		suspend(sc, true, func() {
			in := bufio.NewReader(os.Stdin)
			welcomeLine(pr)
			_ = onboard(pr, in)
		})
	}

	for {
		cfg, _ := config.Load(mustWd())
		items := []listItem{
			{id: "chat", title: "💬  Start chat", hint: "full-screen interactive agent"},
			{id: "run", title: "⚡  Run a task", hint: "give it a single instruction"},
			{id: "model", title: "🧠  Choose model", hint: "current: " + cfg.Model},
			{id: "providers", title: "🔌  Providers & keys", hint: "add custom providers, manage keys"},
			{id: "settings", title: "⚙️   Settings", hint: "theme, fallback chain, status"},
			{sep: true},
			{id: "quit", title: "🚪  Quit", danger: true},
		}
		sub := providerStatusLine(cfg)
		idx := runList(sc, pr, "ties — what would you like to do?", sub, items)
		if idx < 0 {
			return nil
		}
		switch items[idx].id {
		case "chat":
			suspend(sc, false, func() { _ = cmdChat([]string{"--tui"}) })
		case "run":
			suspend(sc, true, func() {
				in := bufio.NewReader(os.Stdin)
				pr.Print("\n  " + pr.Heading("Task") + " (one instruction): ")
				task, ok := readLine(in)
				if ok && strings.TrimSpace(task) != "" {
					pr.Println("")
					_ = cmdRun([]string{strings.TrimSpace(task)})
				}
			})
		case "model":
			modelPicker(sc, pr)
		case "providers":
			providersScreen(sc, pr)
		case "settings":
			settingsScreen(sc, pr)
		case "quit":
			return nil
		}
	}
}

// modelPicker lets the user pick the default model from every known candidate.
func modelPicker(sc *screen.Screen, pr *ui.Printer) {
	for {
		cfg, _ := config.Load(mustWd())
		cands := modelCandidates(cfg)
		items := make([]listItem, 0, len(cands)+4)
		for _, m := range cands {
			it := listItem{id: "set:" + m, title: m}
			if m == cfg.Model {
				it.hint = "✓ current default"
			}
			items = append(items, it)
		}
		items = append(items,
			listItem{sep: true},
			listItem{id: "custom", title: "✎  Custom model…", hint: "type any provider/model"},
			listItem{id: "fallback", title: "⛓  Fallback chain…", hint: "ordered models to try on error"},
		)
		idx := runList(sc, pr, "Choose default model", "click a model or press its number", items)
		if idx < 0 {
			return
		}
		id := items[idx].id
		switch {
		case strings.HasPrefix(id, "set:"):
			if err := saveModel(strings.TrimPrefix(id, "set:")); err != nil {
				flash(sc, pr, pr.Err("✗ "+err.Error()))
			}
			return
		case id == "custom":
			suspend(sc, true, func() {
				in := bufio.NewReader(os.Stdin)
				pr.Print("\n  provider/model (e.g. openai/gpt-4o): ")
				m, ok := readLine(in)
				if ok && strings.TrimSpace(m) != "" {
					if err := saveModel(strings.TrimSpace(m)); err != nil {
						pr.Println(pr.Err("  ✗ " + err.Error()))
					} else {
						pr.Println(pr.Success("  ✓ default model → " + strings.TrimSpace(m)))
					}
				}
			})
			return
		case id == "fallback":
			fallbackForm(sc, pr)
			return
		}
	}
}

// fallbackForm collects an ordered fallback chain via a line prompt.
func fallbackForm(sc *screen.Screen, pr *ui.Printer) {
	suspend(sc, true, func() {
		in := bufio.NewReader(os.Stdin)
		cfg, _ := config.Load(mustWd())
		pr.Println("\n  " + pr.Heading("Fallback chain"))
		pr.Println("  " + pr.Dim("comma-separated provider/model list, tried in order. Empty clears it."))
		if len(cfg.Models) > 0 {
			pr.Println("  " + pr.Dim("current: "+strings.Join(cfg.Models, ", ")))
		}
		pr.Print("  ❯ ")
		line, ok := readLine(in)
		if !ok {
			return
		}
		var models []string
		for _, p := range strings.Split(line, ",") {
			if t := strings.TrimSpace(p); t != "" {
				models = append(models, t)
			}
		}
		if err := setFallbackChain(models); err != nil {
			pr.Println(pr.Err("  ✗ " + err.Error()))
			return
		}
		if len(models) == 0 {
			pr.Println(pr.Success("  ✓ fallback chain cleared"))
		} else {
			pr.Println(pr.Success("  ✓ fallback chain set: " + strings.Join(models, " → ")))
		}
	})
}

// providersScreen lists providers and offers adding a custom one.
func providersScreen(sc *screen.Screen, pr *ui.Printer) {
	for {
		cfg, _ := config.Load(mustWd())
		names := providerNames(cfg)
		items := make([]listItem, 0, len(names)+3)
		for _, n := range names {
			pc := cfg.Providers[n]
			label := n
			if pc.Label != "" {
				label = pc.Label + " (" + n + ")"
			}
			status := "no key"
			if providerReady(n, pc) {
				status = "ready"
			}
			items = append(items, listItem{
				id:    "prov:" + n,
				title: "🔌  " + label,
				hint:  providerKind(pc) + " · " + status,
			})
		}
		if len(names) == 0 {
			items = append(items, listItem{sep: true, title: "  (no providers configured yet)"})
		}
		items = append(items,
			listItem{sep: true},
			listItem{id: "add", title: "➕  Add custom provider", hint: "OpenAI- or Anthropic-compatible endpoint"},
		)
		idx := runList(sc, pr, "Providers & keys", "OpenRouter, Ollama, Together, Azure, local gateways…", items)
		if idx < 0 {
			return
		}
		id := items[idx].id
		switch {
		case id == "add":
			addProviderForm(sc, pr)
		case strings.HasPrefix(id, "prov:"):
			providerDetail(sc, pr, strings.TrimPrefix(id, "prov:"))
		}
	}
}

// providerDetail shows actions for a single provider.
func providerDetail(sc *screen.Screen, pr *ui.Printer, name string) {
	for {
		cfg, _ := config.Load(mustWd())
		pc := cfg.Providers[name]
		models := strings.Join(pc.Models, ", ")
		if models == "" {
			models = "(catalogue defaults)"
		}
		key := "not set"
		if pc.APIKey != "" {
			key = mask(pc.APIKey)
		}
		base := pc.BaseURL
		if base == "" {
			base = "(default)"
		}
		items := []listItem{
			{id: "key", title: "🔑  Set / change API key", hint: "current: " + key},
			{id: "base", title: "🌐  Set base URL", hint: "current: " + base},
			{id: "type", title: "🔧  Set protocol type", hint: "current: " + valueOr(pc.Type, "built-in")},
			{id: "models", title: "🧠  Manage models", hint: models},
			{id: "default", title: "⭐  Set as default model", hint: "pick one of its models"},
			{sep: true},
			{id: "remove", title: "🗑   Remove provider", danger: true},
		}
		title := "Provider · " + name
		if pc.Label != "" {
			title = "Provider · " + pc.Label + " (" + name + ")"
		}
		idx := runList(sc, pr, title, providerKind(pc), items)
		if idx < 0 {
			return
		}
		switch items[idx].id {
		case "key":
			promptSet(sc, pr, "API key for "+name, true, func(v string) error {
				return setProviderField(name, "apiKey", v)
			})
		case "base":
			promptSet(sc, pr, "Base URL for "+name+" (blank = default)", false, func(v string) error {
				return setProviderField(name, "baseUrl", v)
			})
		case "type":
			promptSet(sc, pr, "Protocol type for "+name+" (openai|anthropic, blank = built-in)", false, func(v string) error {
				return setProviderField(name, "type", strings.TrimSpace(v))
			})
		case "models":
			manageModels(sc, pr, name)
		case "default":
			pickProviderDefault(sc, pr, name)
		case "remove":
			if confirm(sc, pr, "Remove provider "+name+"?") {
				if err := removeProvider(name); err != nil {
					flash(sc, pr, pr.Err("✗ "+err.Error()))
				}
				return
			}
		}
	}
}

// pickProviderDefault sets the default model to one of a provider's models.
func pickProviderDefault(sc *screen.Screen, pr *ui.Printer, name string) {
	cfg, _ := config.Load(mustWd())
	models := modelsForProvider(name, cfg.Providers[name])
	if len(models) == 0 {
		promptSet(sc, pr, "Model id for "+name, false, func(v string) error {
			if strings.TrimSpace(v) == "" {
				return nil
			}
			return saveModel(name + "/" + strings.TrimSpace(v))
		})
		return
	}
	items := make([]listItem, 0, len(models))
	for _, m := range models {
		items = append(items, listItem{id: "m:" + m, title: name + "/" + m})
	}
	idx := runList(sc, pr, "Default model for "+name, "", items)
	if idx < 0 {
		return
	}
	if err := saveModel(name + "/" + strings.TrimPrefix(items[idx].id, "m:")); err != nil {
		flash(sc, pr, pr.Err("✗ "+err.Error()))
	}
}

// manageModels lets the user add/remove model ids for a provider.
func manageModels(sc *screen.Screen, pr *ui.Printer, name string) {
	for {
		cfg, _ := config.Load(mustWd())
		pc := cfg.Providers[name]
		items := make([]listItem, 0, len(pc.Models)+3)
		for _, m := range pc.Models {
			items = append(items, listItem{id: "del:" + m, title: "🗑   " + m, hint: "remove"})
		}
		if len(pc.Models) == 0 {
			items = append(items, listItem{sep: true, title: "  (using catalogue defaults)"})
		}
		items = append(items,
			listItem{sep: true},
			listItem{id: "add", title: "➕  Add model id"},
		)
		idx := runList(sc, pr, "Models · "+name, "click a model to remove it", items)
		if idx < 0 {
			return
		}
		id := items[idx].id
		switch {
		case id == "add":
			promptSet(sc, pr, "New model id for "+name, false, func(v string) error {
				return addProviderModel(name, v)
			})
		case strings.HasPrefix(id, "del:"):
			_ = removeProviderModel(name, strings.TrimPrefix(id, "del:"))
		}
	}
}

// addProviderForm collects a full custom-provider definition via line prompts.
func addProviderForm(sc *screen.Screen, pr *ui.Printer) {
	suspend(sc, true, func() {
		in := bufio.NewReader(os.Stdin)
		pr.Println("\n  " + pr.Heading("Add a custom provider"))
		pr.Println("  " + pr.Dim("Works with any OpenAI- or Anthropic-compatible endpoint"))
		pr.Println("  " + pr.Dim("(OpenRouter, Together, Groq, Azure OpenAI, local Ollama, gateways…)"))

		name := ask(pr, in, "Short name (e.g. openrouter)")
		if name == "" {
			pr.Println(pr.Warn("  cancelled"))
			return
		}
		wire := strings.ToLower(ask(pr, in, "Protocol type [openai|anthropic] (default openai)"))
		if wire == "" {
			wire = "openai"
		}
		label := ask(pr, in, "Display label (optional)")
		base := ask(pr, in, "Base URL (e.g. https://openrouter.ai/api/v1)")
		key := ask(pr, in, "API key (optional — leave blank for local/keyless)")
		modelsRaw := ask(pr, in, "Model ids, comma-separated (optional)")
		var models []string
		for _, m := range strings.Split(modelsRaw, ",") {
			if t := strings.TrimSpace(m); t != "" {
				models = append(models, t)
			}
		}
		if err := addCustomProvider(name, wire, label, base, key, models, nil); err != nil {
			pr.Println(pr.Err("  ✗ " + err.Error()))
			return
		}
		pr.Println(pr.Success("  ✓ added provider " + name))
		if len(models) > 0 {
			pr.Print("  ")
			if yes(ask(pr, in, "Set "+name+"/"+models[0]+" as default model? [Y/n]")) {
				_ = saveModel(name + "/" + models[0])
				pr.Println(pr.Success("  ✓ default model → " + name + "/" + models[0]))
			}
		}
	})
}

// settingsScreen offers theme selection, status and fallback chain.
func settingsScreen(sc *screen.Screen, pr *ui.Printer) {
	for {
		cfg, _ := config.Load(mustWd())
		items := []listItem{
			{id: "theme", title: "🎨  Theme", hint: "current: " + cfg.Theme},
			{id: "fallback", title: "⛓  Fallback chain", hint: chainHint(cfg)},
			{id: "status", title: "📊  Show full status", hint: "providers, keys, models"},
			{id: "configpath", title: "📁  Config file path", hint: config.GlobalPath()},
		}
		idx := runList(sc, pr, "Settings", "config: "+config.GlobalPath(), items)
		if idx < 0 {
			return
		}
		switch items[idx].id {
		case "theme":
			pickTheme(sc, pr)
		case "fallback":
			fallbackForm(sc, pr)
		case "status":
			suspend(sc, true, func() {
				pr.Println("")
				_ = cmdModels(nil)
			})
		case "configpath":
			flash(sc, pr, pr.Dim(config.GlobalPath()))
		}
	}
}

// pickTheme lets the user choose a color theme.
func pickTheme(sc *screen.Screen, pr *ui.Printer) {
	items := []listItem{
		{id: "auto", title: "auto", hint: "follow default (dark)"},
		{id: "dark", title: "dark"},
		{id: "light", title: "light"},
		{id: "mono", title: "mono", hint: "no color"},
	}
	idx := runList(sc, pr, "Theme", "", items)
	if idx < 0 {
		return
	}
	_ = editGlobal(func(cfg *config.Config) error { cfg.Theme = items[idx].id; return nil })
}

// ---- shared helpers ----

func welcomeLine(pr *ui.Printer) {
	pr.Println("")
	pr.Println("  " + pr.Heading("Welcome to ties!") + " Let's connect an AI provider to get started.")
}

// suspend leaves the interactive screen, runs fn on the normal terminal, then
// re-enters. When pause is true it waits for Enter so output stays readable.
func suspend(sc *screen.Screen, pause bool, fn func()) {
	sc.Stop()
	fn()
	if pause {
		pr := ui.New(os.Stdout, themeName(), true)
		pr.Print(pr.Dim("\n  press Enter to return to the menu… "))
		in := bufio.NewReader(os.Stdin)
		_, _ = readLine(in)
	}
	_ = sc.Start()
}

// promptSet runs a single line prompt and applies set(value).
func promptSet(sc *screen.Screen, pr *ui.Printer, label string, secret bool, set func(string) error) {
	suspend(sc, true, func() {
		in := bufio.NewReader(os.Stdin)
		hint := ""
		if secret {
			hint = pr.Dim(" (input is visible)")
		}
		pr.Print("\n  " + pr.Heading(label) + hint + ": ")
		v, ok := readLine(in)
		if !ok {
			return
		}
		if err := set(strings.TrimSpace(v)); err != nil {
			pr.Println(pr.Err("  ✗ " + err.Error()))
			return
		}
		pr.Println(pr.Success("  ✓ saved"))
	})
}

// confirm asks a yes/no question on the normal terminal.
func confirm(sc *screen.Screen, pr *ui.Printer, q string) bool {
	answer := false
	suspend(sc, false, func() {
		in := bufio.NewReader(os.Stdin)
		pr.Print("\n  " + pr.Warn(q) + " [y/N]: ")
		v, _ := readLine(in)
		answer = yes(v)
	})
	return answer
}

// flash shows a transient message at the bottom of the screen.
func flash(sc *screen.Screen, pr *ui.Printer, msg string) {
	suspend(sc, true, func() { pr.Println("\n  " + msg) })
}

func ask(pr *ui.Printer, in *bufio.Reader, label string) string {
	pr.Print("  " + pr.Accent("› ") + label + ": ")
	v, _ := readLine(in)
	return strings.TrimSpace(v)
}

func yes(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes" || s == ""
}

func valueOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func chainHint(cfg *config.Config) string {
	if len(cfg.Models) == 0 {
		return "none"
	}
	return strings.Join(cfg.Models, " → ")
}

// runList renders a titled, scrollable, mouse- and keyboard-driven selectable
// list. It returns the chosen item index (into items) or -1 if cancelled.
func runList(sc *screen.Screen, pr *ui.Printer, title, subtitle string, items []listItem) int {
	sel := firstSelectable(items, 0, +1)
	if sel < 0 {
		sel = 0
	}
	offset := 0
	for {
		w, h := sc.Size()
		headerRows := 6
		footerRows := 2
		capacity := h - headerRows - footerRows
		if capacity < 3 {
			capacity = 3
		}
		offset = clampOffset(items, sel, offset, capacity)

		rowToItem := map[int]int{}
		sc.BeginFrame()
		row := 0
		put := func(s string) { sc.WriteLine(s); row++ }

		put("")
		put("  " + pr.Accent("●") + " " + pr.Heading("ties") + "  " + pr.Dim(shortVersion()))
		put("")
		put("  " + pr.Heading(title))
		if subtitle != "" {
			put("  " + pr.Dim(clip(subtitle, w-4)))
		} else {
			put("")
		}
		put("")

		end := offset + capacity
		if end > len(items) {
			end = len(items)
		}
		for i := offset; i < end; i++ {
			it := items[i]
			if it.sep {
				if strings.TrimSpace(it.title) != "" {
					put("  " + pr.Dim(it.title))
				} else {
					put("")
				}
				continue
			}
			rowToItem[row] = i
			put(renderRow(pr, it, i == sel, w))
		}
		if offset > 0 || end < len(items) {
			put(pr.Dim("  ↑↓ scroll · more items"))
		} else {
			put("")
		}
		put(pr.Dim("  ↑/↓ move · Enter/click select · Esc/q back · click anywhere"))
		sc.EndFrame()

		evs, err := sc.Read()
		if err != nil {
			return -1
		}
		for _, e := range evs {
			if e.Kind == screen.EventKey {
				switch e.Key {
				case screen.KeyUp:
					sel = stepSelectable(items, sel, -1)
				case screen.KeyDown:
					sel = stepSelectable(items, sel, +1)
				case screen.KeyHome:
					sel = firstSelectable(items, 0, +1)
				case screen.KeyEnd:
					sel = firstSelectable(items, len(items)-1, -1)
				case screen.KeyPgUp:
					for i := 0; i < capacity; i++ {
						sel = stepSelectable(items, sel, -1)
					}
				case screen.KeyPgDn:
					for i := 0; i < capacity; i++ {
						sel = stepSelectable(items, sel, +1)
					}
				case screen.KeyEnter:
					if !items[sel].sep {
						return sel
					}
				case screen.KeyEsc, screen.KeyCtrlC:
					return -1
				case screen.KeyRune:
					if e.Rune == 'q' {
						return -1
					}
					if e.Rune == 'k' {
						sel = stepSelectable(items, sel, -1)
					}
					if e.Rune == 'j' {
						sel = stepSelectable(items, sel, +1)
					}
					if e.Rune >= '1' && e.Rune <= '9' {
						if t := nthSelectable(items, int(e.Rune-'1')); t >= 0 {
							return t
						}
					}
				}
			}
			if e.Kind == screen.EventMouse {
				m := e.Mouse
				if m.Wheel == -1 {
					sel = stepSelectable(items, sel, -1)
					continue
				}
				if m.Wheel == 1 {
					sel = stepSelectable(items, sel, +1)
					continue
				}
				if it, ok := rowToItem[m.Y-1]; ok {
					sel = it
					if m.Press && m.Button == 0 {
						return it
					}
				}
			}
		}
	}
}

// renderRow styles one selectable row, highlighting the selected one.
func renderRow(pr *ui.Printer, it listItem, selected bool, width int) string {
	marker := "   "
	title := it.title
	if it.danger {
		title = pr.Err(title)
	}
	body := title
	if it.hint != "" {
		body += "  " + pr.Dim(it.hint)
	}
	line := marker + body
	if selected {
		// Reverse-video bar across the row for a clear, theme-agnostic
		// highlight that also reads as "clickable".
		plain := "  ❯ " + it.title
		if it.hint != "" {
			plain += "  " + it.hint
		}
		plain = clip(plain, width-2)
		return ui.SGR("7", padTo(plain, width-2), pr.ColorOn())
	}
	return clip(line, width-1)
}

// ---- list navigation helpers ----

func firstSelectable(items []listItem, from, dir int) int {
	for i := from; i >= 0 && i < len(items); i += dir {
		if !items[i].sep {
			return i
		}
	}
	return -1
}

func stepSelectable(items []listItem, cur, dir int) int {
	for i := cur + dir; i >= 0 && i < len(items); i += dir {
		if !items[i].sep {
			return i
		}
	}
	return cur
}

func nthSelectable(items []listItem, n int) int {
	count := 0
	for i := range items {
		if items[i].sep {
			continue
		}
		if count == n {
			return i
		}
		count++
	}
	return -1
}

// clampOffset keeps the selected item visible within a window of capacity rows.
func clampOffset(items []listItem, sel, offset, capacity int) int {
	if sel < offset {
		return sel
	}
	if sel >= offset+capacity {
		return sel - capacity + 1
	}
	if offset > len(items)-capacity {
		offset = len(items) - capacity
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

func shortVersion() string {
	v := version.Version
	if v == "" {
		v = "dev"
	}
	return v
}

func clip(s string, max int) string {
	if max < 1 {
		max = 1
	}
	if ui.DisplayWidth(s) <= max {
		return s
	}
	// Trim by runes until within width (ANSI-free strings expected here).
	r := []rune(s)
	for len(r) > 0 && ui.DisplayWidth(string(r)) > max-1 {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func padTo(s string, width int) string {
	w := ui.DisplayWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}
