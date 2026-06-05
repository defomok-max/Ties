# Ties

**Ties** is a terminal AI coding agent — a blend of [Claude Code](https://docs.anthropic.com/en/docs/claude-code),
[OpenCode](https://github.com/sst/opencode) and Codex CLI. It runs an agentic
loop over your codebase: it reads and edits files, runs shell commands, and uses
tools, MCP servers and skills to get real work done from the terminal.

Written in **Go 1.23 with only the standard library** — no vendor SDKs, no heavy
frameworks. It compiles offline into a single static binary.

> Early but real: the core agent loop, two providers, tools, permissions,
> sessions, MCP and skills all work today. See [`plan.md`](./plan.md) for the
> architecture and roadmap.

## Features

- 🔌 **Multi-provider** — Anthropic and OpenAI today behind one streaming
  interface; adding a vendor is one file. Pick with `provider/model`.
- 🛠️ **Built-in tools** — `read`, `write`, `edit`, `list`, `glob`, `grep`,
  `bash`, all confined to the workspace root.
- 🔐 **Permissions** — every tool call is gated by an allow / ask / deny engine
  (deny always wins), configurable per tool or per pattern.
- 🧩 **MCP** — connect Model Context Protocol servers (stdio) and their tools
  appear to the agent automatically.
- 📚 **Skills** — drop `SKILL.md` files in `skills/`; the agent sees their
  descriptions and loads full bodies on demand.
- 💾 **Sessions** — append-only JSONL transcripts you can list, show and resume.
- 🧱 **Single binary, offline build** — zero third-party modules.

## Install

```bash
git clone https://github.com/defomok-max/Ties.git
cd Ties
go build -o ties ./cmd/ties
# optionally: mv ties to a directory on your PATH
```

## Quick start

```bash
# 1. Add a provider key (stored in ~/.config/ties/ties.json) or use an env var
ties auth login anthropic           # prompts for the key
export ANTHROPIC_API_KEY=sk-...     # or just set the env var

# 2. One-shot task
ties run "add a --version flag and update the README"

# 3. Interactive chat
ties chat

# 4. Use a different model
ties run -m openai/gpt-4o "explain internal/agent/agent.go"
```

## Commands

| Command | Description |
| --- | --- |
| `ties run [prompt]` | Run a single agent task (reads stdin if no prompt) |
| `ties chat` | Interactive chat session |
| `ties auth login/list/logout` | Manage provider credentials |
| `ties config [path]` | Show merged config and its sources |
| `ties mcp list/tools` | Inspect MCP servers and discovered tools |
| `ties session list/show <id>` | Inspect transcripts |
| `ties skill list/show <name>` | Inspect skills |
| `ties tools` | List built-in tools |
| `ties models` | List providers and the default model |
| `ties version` | Print version |

Common flags for `run`/`chat`: `-m/--model`, `-y/--yes` (auto-approve tools),
`--resume <id>`, `--no-session`, `--max-steps <n>`.

## Configuration

Config is merged from (lowest to highest precedence): built-in defaults →
`~/.config/ties/ties.json` → the nearest `.ties.json` walking up from the
working directory → environment variables.

```jsonc
{
  "model": "anthropic/claude-3-5-sonnet-latest",
  "maxSteps": 50,
  "providers": {
    "anthropic": { "apiKey": "sk-...", "baseUrl": "https://api.anthropic.com" },
    "openai":    { "apiKey": "sk-..." }
  },
  "permission": {
    "*": "ask",
    "read": "allow", "list": "allow", "glob": "allow", "grep": "allow",
    "bash:rm *": "deny"
  },
  "mcp": {
    "filesystem": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "."] }
  },
  "skillDirs": ["./skills"]
}
```

Environment overrides: `TIES_MODEL`, `TIES_MAX_STEPS`, `ANTHROPIC_API_KEY`,
`OPENAI_API_KEY`, `ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`.

## Development

```bash
go build ./...
go vet ./...
gofmt -l .
golangci-lint run ./...
go test ./...
```

## License

MIT © defomok-max
