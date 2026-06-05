package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/defomok-max/Ties/internal/agent"
	"github.com/defomok-max/Ties/internal/provider"
	"github.com/defomok-max/Ties/internal/session"
	"github.com/defomok-max/Ties/internal/tool"
)

type agentFlags struct {
	model     string
	yes       bool
	resume    string
	noSession bool
	maxSteps  int
	rest      []string
}

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

func cmdRun(args []string) error {
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

	ag := a.newAgent(p, model, sess, flags)
	if err := ag.Run(ctx, input); err != nil {
		return err
	}
	fmt.Println()
	if sess != nil {
		fmt.Fprintf(os.Stderr, "\n[session %s]\n", sess.Meta.ID)
	}
	return nil
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

	ag := a.newAgent(p, model, sess, flags)

	fmt.Printf("ties chat — model %s, session %s\nType your message. /exit to quit, /tools to list tools.\n\n", model, sess.Meta.ID)
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for {
		fmt.Print("\n> ")
		if !in.Scan() {
			break
		}
		line := strings.TrimSpace(in.Text())
		switch {
		case line == "":
			continue
		case line == "/exit" || line == "/quit":
			return nil
		case line == "/tools":
			fmt.Println(strings.Join(a.reg.Names(), ", "))
			continue
		}
		if err := ag.Run(ctx, line); err != nil {
			fmt.Fprintln(os.Stderr, "error: "+err.Error())
		}
		fmt.Println()
	}
	return nil
}

// newAgent wires callbacks for streaming output and tool approval.
func (a *app) newAgent(p provider.Provider, model string, sess *session.Session, flags agentFlags) *agent.Agent {
	maxSteps := a.cfg.MaxSteps
	if flags.maxSteps > 0 {
		maxSteps = flags.maxSteps
	}
	ag := &agent.Agent{
		Provider: p,
		Model:    model,
		System:   a.system,
		Tools:    a.reg,
		Perm:     a.perm,
		Session:  sess,
		MaxSteps: maxSteps,
		OnText: func(delta string) {
			fmt.Print(delta)
		},
		OnToolStart: func(name, args string) {
			fmt.Fprintf(os.Stderr, "\n\033[2m· %s %s\033[0m\n", name, truncateArgs(args))
		},
		OnToolResult: func(name string, res tool.Result) {
			if res.IsError {
				fmt.Fprintf(os.Stderr, "\033[31m  ✗ %s\033[0m\n", firstLine(res.Content))
			}
		},
	}
	if flags.yes {
		ag.Approve = func(_, _ string) bool { return true }
	} else {
		ag.Approve = approvePrompt
	}
	return ag
}

func approvePrompt(name, target string) bool {
	fmt.Fprintf(os.Stderr, "\n\033[33mAllow %s", name)
	if target != "" {
		fmt.Fprintf(os.Stderr, " (%s)", truncateArgs(target))
	}
	fmt.Fprint(os.Stderr, "? [y/N] ")
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
