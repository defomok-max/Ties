// Command ties is a terminal AI coding agent: a blend of Claude Code,
// OpenCode and Codex CLI with multi-provider support, MCP, skills, sessions
// and a permission system.
package main

import (
	"os"

	"github.com/defomok-max/Ties/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
