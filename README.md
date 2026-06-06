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

- 🔌 **Multi-provider** — Anthropic, OpenAI and Google Gemini behind one
  streaming interface; adding a vendor is one file. Pick with `provider/model`.
- 🧬 **Custom providers** — point at any OpenAI- or Anthropic-compatible
  endpoint (OpenRouter, Groq, Together, local Ollama, gateways) with a `type`,
  `baseUrl`, `apiKey` and custom `headers`. No code required.
- ♻️ **Resilient** — automatic retries with exponential backoff + jitter on
  transient errors, plus an ordered **model-fallback chain**.
- 💰 **Cost & token metering** — live token accounting and an estimated USD
  cost from a built-in pricing table.
- 🛑 **Run budgets** — optional `maxCostUSD` / `maxTokens` ceilings stop a
  runaway agent before it burns through your wallet or context.
- 🧫 **Sub-agents** — the `task` tool spawns a scoped child agent for a focused
  subtask; it shares your tools but keeps its own short transcript and draws
  from the parent's remaining budget.
- 📝 **Plan mode** — `--plan` makes a run read-only (edits and shell disabled)
  so the agent proposes a concrete plan before touching anything.
- ⏱️ **Per-tool timeouts** — `toolTimeout` caps how long any single tool call
  may run, so a hung command can't stall the whole session.
- 📤 **Session export** — `ties session export <id> --format md|html` turns a
  transcript into a shareable Markdown or standalone HTML page.
- 🧠 **Project memory** — auto-loads `AGENTS.md` / `CLAUDE.md` / `TIES.md` (the
  same files Claude Code, OpenCode and Codex use) from the repo and a global
  config dir into the system prompt. Scaffold one with `ties init`.
- 📎 **`@file` references** — mention `@path/to/file` in any prompt and its
  contents are inlined for the agent automatically.
- 🤖 **Scriptable runs** — `--quiet` silences the UI and `--output json` prints
  a machine-readable result (final text, session id, usage, cost) for CI/pipes.
- 🎨 **Beautiful, dependency-free TUI** — themes (`dark` / `light` / `mono`),
  banner, spinner, colored tool lines and diffs, boxes; honors `NO_COLOR`.
- 🛠️ **Built-in tools** — `read`, `write`, `edit`, `multiedit`, `patch`,
  `list`, `glob`, `grep`, `tree`, `bash`, `webfetch` and a `todo` planner, all
  confined to the workspace root, with output-truncation budgets.
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
export ANTHROPIC_API_KEY=sk-...     # or OPENAI_API_KEY / GEMINI_API_KEY

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
| `ties init` | Scaffold an `AGENTS.md` project-context file |
| `ties auth login/list/logout` | Manage provider credentials |
| `ties config [path]` | Show merged config and its sources |
| `ties mcp list/tools` | Inspect MCP servers and discovered tools |
| `ties session list/show <id>` | Inspect transcripts |
| `ties session export <id> [--format md\|html]` | Export a transcript to share |
| `ties skill list/show <name>` | Inspect skills |
| `ties tools` | List built-in tools |
| `ties models` | List providers and the default model |
| `ties version` | Print version |

Common flags for `run`/`chat`: `-m/--model`, `-y/--yes` (auto-approve tools),
`--resume <id>`, `--no-session`, `--plan` (read-only plan mode),
`--max-steps <n>`. `run` also takes `-q/--quiet` and `-o/--output text|json`
for non-interactive scripting.

### Project context & `@file` references

Drop an `AGENTS.md` (or `CLAUDE.md` / `TIES.md`) in your repo — `ties init`
scaffolds one — and its contents are injected into every run's system prompt,
nearest file winning. In a prompt, `@path/to/file` inlines that file:

```bash
ties run "explain the bug in @internal/agent/agent.go"
echo "summarize @README.md" | ties run -y --quiet
ties run -y --output json "what does this repo do?" | jq .final
```

## Configuration

Config is merged from (lowest to highest precedence): built-in defaults →
`~/.config/ties/ties.json` → the nearest `.ties.json` walking up from the
working directory → environment variables.

```jsonc
{
  "model": "anthropic/claude-3-5-sonnet-latest",
  // Optional fallback chain: if the primary errors, the next is tried.
  "models": ["anthropic/claude-3-5-sonnet-latest", "openai/gpt-4o"],
  "maxSteps": 50,
  "maxToolOutput": 16000,   // cap chars of a tool result fed back to the model
  "retries": 2,             // auto-retries on 429 / 5xx (backoff + jitter)
  "maxCostUSD": 0,          // 0 = off; stop the run past this estimated spend
  "maxTokens": 0,           // 0 = off; stop the run past this many tokens
  "toolTimeout": 0,         // 0 = off; max seconds any single tool call may run
  "theme": "dark",          // dark | light | mono | auto
  "providers": {
    "anthropic": { "apiKey": "sk-...", "baseUrl": "https://api.anthropic.com" },
    "openai":    { "apiKey": "sk-..." },
    "gemini":    { "apiKey": "..." }
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

### Custom providers

Any OpenAI- or Anthropic-compatible endpoint works without code — just declare
it under `providers` with a `type`:

```jsonc
{
  "model": "groq/llama-3.3-70b-versatile",
  "providers": {
    // OpenRouter, Groq, Together, Fireworks, … (OpenAI Chat Completions)
    "groq": {
      "type": "openai",
      "baseUrl": "https://api.groq.com/openai",
      "apiKey": "gsk_...",
      "label": "Groq",
      "models": ["llama-3.3-70b-versatile"]
    },
    // Local Ollama (no key needed; baseUrl ending in /v1 is handled)
    "ollama": {
      "type": "openai",
      "baseUrl": "http://localhost:11434/v1",
      "models": ["qwen2.5-coder"]
    },
    // Anything needing extra auth headers (e.g. Azure, gateways)
    "gateway": {
      "type": "anthropic",
      "baseUrl": "https://my-gateway.example.com",
      "headers": { "X-My-Auth": "token" }
    }
  }
}
```

Then: `ties run -m groq/llama-3.3-70b-versatile "…"`. Run `ties models` to see
all configured providers, their type and key status.

Environment overrides: `TIES_MODEL`, `TIES_MAX_STEPS`, `TIES_THEME`,
`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` (or `GOOGLE_API_KEY`),
`ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, `GEMINI_BASE_URL`, `NO_COLOR`,
`FORCE_COLOR`.

### Chat slash-commands

`/help` · `/tools` · `/skills` · `/context` · `/model` · `/usage` · `/clear` · `/exit`

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
