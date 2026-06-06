package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/defomok-max/Ties/internal/agent"
	"github.com/defomok-max/Ties/internal/pricing"
	"github.com/defomok-max/Ties/internal/provider"
	"github.com/defomok-max/Ties/internal/session"
	"github.com/defomok-max/Ties/internal/tool"
	"github.com/defomok-max/Ties/internal/ui"
)

type agentFlags struct {
	model     string
	yes       bool
	resume    string
	noSession bool
	maxSteps  int
	theme     string
	noColor   bool
	plan      bool
	quiet     bool
	output    string
	tdd       bool
	loop      bool
	maxLoops  int
	until     string
	rest      []string
}

// scripting reports whether this run should suppress interactive UI and stream
// nothing to stdout (so a machine-readable result is the only stdout).
func (f agentFlags) scripting() bool { return f.quiet || f.output == "json" }

func parseAgentFlags(args []string) (agentFlags, error) {
	f := agentFlags{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s needs a value", a)
			}
			i++
			return args[i], nil
		}
		switch a {
		case "-m", "--model":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.model = v
		case "-y", "--yes":
			f.yes = true
		case "--resume":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.resume = v
		case "--no-session":
			f.noSession = true
		case "--theme":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.theme = v
		case "--no-color":
			f.noColor = true
		case "--plan":
			f.plan = true
		case "--tdd":
			f.tdd = true
		case "--loop":
			f.loop = true
		case "--max-loops":
			v, err := next()
			if err != nil {
				return f, err
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return f, fmt.Errorf("--max-loops: %w", err)
			}
			f.maxLoops = n
		case "--until":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.until = v
			f.loop = true
		case "-q", "--quiet":
			f.quiet = true
		case "-o", "--output":
			v, err := next()
			if err != nil {
				return f, err
			}
			f.output = v
		case "--max-steps":
			v, err := next()
			if err != nil {
				return f, err
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return f, fmt.Errorf("--max-steps: %w", err)
			}
			f.maxSteps = n
		default:
			f.rest = append(f.rest, a)
		}
	}
	return f, nil
}

// applyUITheme reconfigures the app printer from --theme / --no-color flags.
func (a *app) applyUITheme(flags agentFlags) {
	theme := a.cfg.Theme
	if flags.theme != "" {
		theme = flags.theme
	}
	color := ui.ColorEnabled(os.Stderr)
	if flags.noColor {
		color = false
	}
	a.ui = ui.New(os.Stderr, theme, color)
}

func cmdRun(args []string) error {
	flags, err := parseAgentFlags(args)
	if err != nil {
		return err
	}
	if flags.output != "" && flags.output != "text" && flags.output != "json" {
		return fmt.Errorf("--output must be \"text\" or \"json\"")
	}
	ctx := context.Background()
	a, err := setup(ctx, true)
	if err != nil {
		return err
	}
	defer a.close()
	a.applyUITheme(flags)
	if flags.scripting() {
		// Silence the interactive UI so stdout carries only the result.
		a.ui = ui.New(io.Discard, "mono", false)
	}

	p, model, err := a.buildProvider(flags.model)
	if err != nil {
		return err
	}

	input := strings.TrimSpace(strings.Join(flags.rest, " "))
	if input == "" {
		data, _ := io.ReadAll(os.Stdin)
		input = strings.TrimSpace(string(data))
	}
	if input == "" {
		return fmt.Errorf("no prompt provided (pass it as an argument or via stdin)")
	}
	input = expandMentions(a.root, input)

	var sess *session.Session
	if !flags.noSession {
		store, serr := session.NewStore(a.sessionDir())
		if serr != nil {
			return serr
		}
		if flags.resume != "" {
			sess, err = store.Open(flags.resume)
		} else {
			sess, err = store.Create(a.cfg.Model)
		}
		if err != nil {
			return err
		}
		defer func() { _ = sess.Close() }()
	}

	usage := &usageMeter{}
	ag := a.newAgent(p, model, sess, flags, usage)
	var runErr error
	if flags.loop {
		runErr = a.runRalph(ctx, ag, input, flags)
	} else {
		runErr = ag.Run(ctx, input)
	}
	if runErr != nil {
		return runErr
	}

	sessID := ""
	if sess != nil {
		sessID = sess.Meta.ID
	}
	switch flags.output {
	case "json":
		return a.printJSONResult(model, sessID, usage)
	default:
		if flags.quiet {
			fmt.Println(strings.TrimSpace(a.lastAssistant.String()))
			return nil
		}
	}
	fmt.Println()
	a.printUsage(model, usage)
	if sess != nil {
		a.ui.Println(a.ui.Dim("session " + sess.Meta.ID))
	}
	return nil
}

// printJSONResult emits a single machine-readable object summarising the run.
func (a *app) printJSONResult(model, sessID string, u *usageMeter) error {
	out := map[string]any{
		"model":   model,
		"session": sessID,
		"final":   strings.TrimSpace(a.lastAssistant.String()),
		"usage": map[string]int{
			"inputTokens":  u.in,
			"outputTokens": u.out,
		},
	}
	if cost, ok := pricing.Estimate(model, u.in, u.out); ok {
		out["costUSD"] = cost
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func cmdChat(args []string) error {
	flags, err := parseAgentFlags(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	a, err := setup(ctx, true)
	if err != nil {
		return err
	}
	defer a.close()
	a.applyUITheme(flags)

	p, model, err := a.buildProvider(flags.model)
	if err != nil {
		return err
	}

	store, err := session.NewStore(a.sessionDir())
	if err != nil {
		return err
	}
	var sess *session.Session
	if flags.resume != "" {
		sess, err = store.Open(flags.resume)
	} else {
		sess, err = store.Create(a.cfg.Model)
	}
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	usage := &usageMeter{}
	ag := a.newAgent(p, model, sess, flags, usage)

	a.ui.Banner("terminal AI coding agent")
	a.ui.Printf(" %s  %s\n", a.ui.Heading("model"), model)
	a.ui.Printf(" %s  %s\n", a.ui.Heading("session"), sess.Meta.ID)
	if n := len(a.memory); n > 0 {
		a.ui.Printf(" %s  %d file(s) loaded\n", a.ui.Heading("context"), n)
	}
	a.ui.Println(a.ui.Dim(" /help for commands, /exit to quit"))

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for {
		a.ui.Print("\n" + a.ui.Accent("❯ "))
		if !in.Scan() {
			break
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			if a.handleSlash(line, model, usage) {
				return nil
			}
			continue
		}
		if err := ag.Run(ctx, expandMentions(a.root, line)); err != nil {
			a.ui.ErrorLine(err.Error())
		}
		fmt.Println()
	}
	return nil
}

// handleSlash processes a chat slash-command. It returns true to quit.
func (a *app) handleSlash(line, model string, usage *usageMeter) bool {
	switch strings.Fields(line)[0] {
	case "/exit", "/quit":
		return true
	case "/help":
		a.ui.Box("commands", strings.Join([]string{
			"/help            show this help",
			"/tools           list available tools",
			"/skills          list discovered skills",
			"/context         list loaded project-context files",
			"/model           show the active model",
			"/usage           show token usage & est. cost",
			"/clear           clear the screen",
			"/exit            quit",
		}, "\n"))
	case "/tools":
		a.ui.Println(strings.Join(a.reg.Names(), ", "))
	case "/skills":
		if len(a.skills) == 0 {
			a.ui.Println(a.ui.Dim("(no skills)"))
		}
		for _, s := range a.skills {
			a.ui.Printf("%s  %s\n", a.ui.Heading(s.Name), a.ui.Dim(s.Description))
		}
	case "/context":
		if len(a.memory) == 0 {
			a.ui.Println(a.ui.Dim("(no AGENTS.md / CLAUDE.md / TIES.md found)"))
		}
		for _, d := range a.memory {
			a.ui.Println(a.ui.Heading(d.Path))
		}
	case "/model":
		a.ui.Println(model)
	case "/usage":
		a.printUsage(model, usage)
	case "/clear":
		a.ui.Print("\x1b[2J\x1b[H")
	default:
		a.ui.Println(a.ui.Dim("unknown command; /help for the list"))
	}
	return false
}

// usageMeter accumulates token usage across turns.
type usageMeter struct {
	in  int
	out int
}

func (a *app) printUsage(model string, u *usageMeter) {
	if u.in == 0 && u.out == 0 {
		return
	}
	line := fmt.Sprintf("tokens: %d in / %d out", u.in, u.out)
	if cost, ok := pricing.Estimate(model, u.in, u.out); ok {
		line += fmt.Sprintf("  ·  est. $%.4f", cost)
	}
	a.ui.Println(a.ui.Dim(line))
}

// newAgent wires callbacks for streaming output, tool approval and metering.
func (a *app) newAgent(p provider.Provider, model string, sess *session.Session, flags agentFlags, usage *usageMeter) *agent.Agent {
	maxSteps := a.cfg.MaxSteps
	if flags.maxSteps > 0 {
		maxSteps = flags.maxSteps
	}
	var spin *ui.Spinner
	stopSpin := func() {
		if spin != nil {
			spin.Stop()
			spin = nil
		}
	}
	a.lastAssistant.Reset()
	scripting := flags.scripting()
	ag := &agent.Agent{
		Provider:      p,
		Model:         model,
		System:        a.system,
		Tools:         a.reg,
		Perm:          a.perm,
		Session:       sess,
		MaxSteps:      maxSteps,
		MaxToolOutput: a.cfg.MaxToolOutput,
		Budget:        agent.Budget{MaxUSD: a.cfg.MaxCostUSD, MaxTokens: a.cfg.MaxTokens},
		EstimateCost:  pricing.Estimate,
		OnText: func(delta string) {
			stopSpin()
			if !scripting {
				fmt.Print(delta)
			}
		},
		OnAssistantDone: func(text string) {
			if strings.TrimSpace(text) != "" {
				a.lastAssistant.Reset()
				a.lastAssistant.WriteString(text)
			}
		},
		OnToolStart: func(name, args string) {
			stopSpin()
			a.ui.ToolLine(name, a.ui.Dim(truncateArgs(args)))
			a.previewEdit(name, args)
			spin = a.ui.StartSpinner("working…")
		},
		OnToolResult: func(_ string, res tool.Result) {
			if res.IsError {
				a.ui.ErrorLine(firstLine(res.Content))
			}
		},
		OnUsage: func(u provider.Usage) {
			usage.in += u.InputTokens
			usage.out += u.OutputTokens
		},
	}
	switch {
	case flags.yes:
		ag.Approve = func(_, _ string) bool { return true }
	case scripting:
		// Non-interactive: there is no TTY to prompt, so deny anything that
		// would require asking. Use --yes for unattended runs that must edit.
		ag.Approve = func(_, _ string) bool { return false }
	default:
		ag.Approve = a.approvePrompt
	}
	if a.cfg.ToolTimeout > 0 {
		ag.ToolTimeout = time.Duration(a.cfg.ToolTimeout) * time.Second
	}
	if flags.plan {
		ag.DenyTools = map[string]bool{
			"write": true, "edit": true, "multiedit": true, "patch": true, "bash": true,
		}
		ag.System += planModeNote
		a.ui.Println(a.ui.Warn("· plan mode — read-only, edits disabled"))
	}
	if flags.tdd {
		ag.System += tddModeNote
		a.ui.Println(a.ui.Accent("· TDD mode — test first, then implement"))
	}
	if flags.loop {
		ag.System += ralphNote
	}
	a.wireTask(ag, p, model, usage)
	return ag
}

// wireTask installs the per-run spawn closure for the `task` sub-agent tool.
// Each call builds a fresh child agent that shares the provider, model, tools
// (minus `task` to prevent recursion), permissions and read-only/timeout policy
// of the parent, draws from the parent's remaining budget, and folds its spend
// back into the parent.
func (a *app) wireTask(parent *agent.Agent, p provider.Provider, model string, usage *usageMeter) {
	if a.task == nil {
		return
	}
	a.task.spawn = func(ctx context.Context, desc, prompt string) (string, error) {
		subReg := a.reg.Clone()
		subReg.Unregister("task")

		label := strings.TrimSpace(desc)
		if label == "" {
			label = firstLine(prompt)
		}
		a.ui.Println(a.ui.Accent("· task → " + truncateArgs(label)))

		childSteps := parent.MaxSteps / 2
		if childSteps < 4 {
			childSteps = 4
		}
		var last strings.Builder
		child := &agent.Agent{
			Provider:      p,
			Model:         model,
			System:        a.system + subAgentNote,
			Tools:         subReg,
			Perm:          a.perm,
			MaxSteps:      childSteps,
			MaxToolOutput: a.cfg.MaxToolOutput,
			Budget:        parent.RemainingBudget(),
			EstimateCost:  pricing.Estimate,
			ToolTimeout:   parent.ToolTimeout,
			DenyTools:     parent.DenyTools,
			Approve:       parent.Approve,
			OnAssistantDone: func(text string) {
				if strings.TrimSpace(text) != "" {
					last.Reset()
					last.WriteString(text)
				}
			},
			OnUsage: func(u provider.Usage) {
				usage.in += u.InputTokens
				usage.out += u.OutputTokens
			},
		}
		err := child.Run(ctx, prompt)
		cusd, ctok := child.Spent()
		parent.AddSpent(cusd, ctok)
		a.ui.Println(a.ui.Dim("· task done"))
		return last.String(), err
	}
}

// previewEdit renders a colored diff for edit/write tool calls.
func (a *app) previewEdit(name, args string) {
	if name != "edit" && name != "write" {
		return
	}
	var m struct {
		Old     string `json:"old"`
		New     string `json:"new"`
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(args), &m) != nil {
		return
	}
	switch name {
	case "edit":
		a.ui.Diff(m.Old, m.New)
	case "write":
		a.ui.Diff("", m.Content)
	}
}

func (a *app) approvePrompt(name, target string) bool {
	label := name
	if target != "" {
		label += " (" + truncateArgs(target) + ")"
	}
	a.ui.Print(a.ui.Warn("allow " + label + "? [y/N] "))
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func truncateArgs(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
