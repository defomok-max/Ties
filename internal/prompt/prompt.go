// Package prompt assembles the system prompt for the agent from static
// guidance plus dynamic context (workspace path, available skills).
package prompt

import (
	"fmt"
	"strings"
)

// Params carries the dynamic pieces of the system prompt.
type Params struct {
	WorkspaceRoot string
	OS            string
	SkillCatalog  string // "name: description" lines, may be empty
	// ProjectContext is the concatenated body of discovered AGENTS.md /
	// CLAUDE.md / TIES.md files. It carries repo-specific instructions and may
	// be empty.
	ProjectContext string
}

const base = `You are ties, an autonomous terminal coding agent. You help the user build,
debug and reason about software by reading and editing files and running shell
commands in their workspace.

Operating principles:
- Be precise and concise. Prefer doing over explaining.
- Investigate before acting: read files and search the codebase first.
- Make the smallest change that fully solves the task; keep the build green.
- When editing, read the file first and match existing style.
- Use the bash tool for builds, tests, linters and git. Report failures clearly.
- Never fabricate file contents or command output; call a tool to find out.
- When you have completed the task, stop calling tools and give a short summary.`

// Build returns the full system prompt string.
func Build(p Params) string {
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n## Environment\n")
	fmt.Fprintf(&b, "- Workspace root: %s\n", p.WorkspaceRoot)
	if p.OS != "" {
		fmt.Fprintf(&b, "- OS: %s\n", p.OS)
	}
	if strings.TrimSpace(p.ProjectContext) != "" {
		b.WriteString("\n## Project context\n")
		b.WriteString("The following instructions come from the project's AGENTS.md / ")
		b.WriteString("CLAUDE.md / TIES.md files. Treat them as authoritative for this ")
		b.WriteString("workspace; later files override earlier ones.\n\n")
		b.WriteString(p.ProjectContext)
		b.WriteString("\n")
	}
	if strings.TrimSpace(p.SkillCatalog) != "" {
		b.WriteString("\n## Available skills\n")
		b.WriteString("These are reusable knowledge units. Load a skill's full body with the ")
		b.WriteString("`skill` tool when its description matches the task before acting.\n")
		b.WriteString(p.SkillCatalog)
	}
	return b.String()
}
