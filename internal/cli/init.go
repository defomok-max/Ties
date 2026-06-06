package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cmdInit scaffolds an AGENTS.md project-context file in the working directory
// (mirrors Claude Code's `/init`). It refuses to overwrite an existing context
// file and makes a best-effort guess at the project's build/test commands.
func cmdInit(args []string) error {
	force := false
	for _, a := range args {
		if a == "--force" || a == "-f" {
			force = true
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	// Don't clobber an existing context file.
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "TIES.md"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil && !force {
			return fmt.Errorf("%s already exists (use --force to overwrite AGENTS.md)", name)
		}
	}
	target := filepath.Join(root, "AGENTS.md")
	content := scaffoldAgents(root)
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", target)
	fmt.Println("Edit it to capture this project's conventions, then run `ties run` / `ties chat`.")
	return nil
}

// scaffoldAgents builds a starter AGENTS.md, guessing commands from the files
// present in dir.
func scaffoldAgents(dir string) string {
	name := filepath.Base(dir)
	build, test := guessCommands(dir)

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", name)
	b.WriteString("> Project context for AI coding agents (Ties, Claude Code, OpenCode, Codex).\n")
	b.WriteString("> Keep this short and high-signal; the agent reads it on every run.\n\n")

	b.WriteString("## Overview\n\n")
	b.WriteString("_Describe what this project is and its high-level architecture._\n\n")

	b.WriteString("## Commands\n\n")
	fmt.Fprintf(&b, "- Build: `%s`\n", build)
	fmt.Fprintf(&b, "- Test:  `%s`\n", test)
	b.WriteString("- Lint:  `_add your linter_`\n\n")

	b.WriteString("## Conventions\n\n")
	b.WriteString("- _Code style, naming, and patterns to follow._\n")
	b.WriteString("- _What to avoid (e.g. external deps, breaking the public API)._\n\n")

	b.WriteString("## Notes\n\n")
	b.WriteString("- _Anything the agent should always keep in mind._\n")
	return b.String()
}

// guessCommands returns best-effort build and test commands based on marker
// files. Falls back to placeholders when the stack is unknown.
func guessCommands(dir string) (build, test string) {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	switch {
	case exists("go.mod"):
		return "go build ./...", "go test ./..."
	case exists("Cargo.toml"):
		return "cargo build", "cargo test"
	case exists("package.json"):
		return "npm run build", "npm test"
	case exists("pyproject.toml"), exists("setup.py"):
		return "python -m build", "pytest"
	case exists("Makefile"):
		return "make", "make test"
	default:
		return "_add your build command_", "_add your test command_"
	}
}
