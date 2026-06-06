# Ties

**Ties** is a terminal AI coding agent — a blend of [Claude Code](https://docs.anthropic.com/en/docs/claude-code),
[OpenCode](https://github.com/sst/opencode) and Codex CLI. It runs an agentic
loop over your codebase: it reads and edits files, runs shell commands, and uses
tools, MCP servers and skills to get real work done from the terminal.

Written in **Go 1.23 with only the standard library** — no vendor SDKs, no heavy
frameworks. It compiles offline into a single static binary.

> Early but real: the core agent loop, four providers, tools, permissions,
> sessions, MCP (stdio + HTTP), skills, autonomous loops and TDD mode all work
> today. See [`plan.md`](./plan.md) for the architecture and roadmap.

## Features

- 🔌 **Multi-provider** — Anthropic, OpenAI, Google Gemini and AWS Bedrock
  behind one streaming interface; adding a vendor is one file. Pick with
  `provider/model`.
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
  `ties chat --tui` opens a **full-screen interface**: a fixed header, a
  scrolling transcript with syntax-highlighted code blocks, and a live status
  bar (token/cost metering + spinner) — all in pure stdlib.
- 🛠️ **Built-in tools** — `read`, `write`, `edit`, `multiedit`, `patch`,
  `list`, `glob`, `grep`, `tree`, `bash`, `webfetch` and a `todo` planner, all
  confined to the workspace root, with output-truncation budgets.
- 🔐 **Permissions** — every tool call is gated by an allow / ask / deny engine
  (deny always wins), configurable per tool or per pattern.
- 🔁 **Autonomous loops & TDD** — `--loop`/`--until` keep the agent iterating
  and self-verifying until the goal is done; `--tdd` enforces red→green→refactor.
- 🧩 **MCP** — connect Model Context Protocol servers over **stdio or HTTP** and
  their tools
  appear to the agent automatically.
- 📚 **Skills** — drop `SKILL.md` files in `skills/`; the agent sees their
  descriptions and loads full bodies on demand.
- 💾 **Sessions** — append-only JSONL transcripts you can list, show and resume.
- 🧱 **Single binary, offline build** — zero third-party modules.

## Install

You only need **Go 1.23+** and **git**. There are no other dependencies — Ties
builds offline into one static binary.

**One command (recommended):**

```bash
git clone https://github.com/defomok-max/Ties.git
cd Ties
make install          # builds and puts `ties` on your PATH
```

`make install` writes to `/usr/local/bin` (using `sudo` if needed). To install
without sudo, pick a user dir on your PATH:

```bash
make install PREFIX=$HOME/.local      # installs to ~/.local/bin
```

**Or run the installer script** (same thing, no make required):

```bash
sh install.sh                  # or: PREFIX=$HOME/.local sh install.sh
```

**Or with the Go toolchain** (no clone needed):

```bash
go install github.com/defomok-max/Ties/cmd/ties@latest
# the binary lands in $(go env GOPATH)/bin — make sure that's on your PATH
```

**Or just build the binary** and move it yourself:

```bash
make build            # produces ./ties
./ties --help
```

Verify it's installed:

```bash
ties version
ties --help
```

## Quick start

Get going in two steps — it feels just like Claude Code:

```bash
# 1. Add a provider key (stored in ~/.config/ties/ties.json) — pick any one:
ties auth login anthropic           # prompts for the key, no echo
#   …or just export an env var instead:
export ANTHROPIC_API_KEY=sk-...     # or OPENAI_API_KEY / GEMINI_API_KEY

# 2. Start coding from the terminal:
ties chat --tui                     # full-screen interactive agent
```

That's it. A few more ways to drive it:

```bash
# One-shot task in the current repo (it reads, edits and runs commands for you)
ties run "add a --version flag and update the README"

# Plain interactive chat (line UI instead of full-screen)
ties chat

# Pick a different model on the fly
ties run -m openai/gpt-4o "explain internal/agent/agent.go"

# Let it work autonomously until the goal is verified done
ties run -y --loop "make `go test ./...` pass"
```

Tips for a smooth console experience:

- `ties chat --tui` gives you the full-screen UI (header, scrollback, syntax
  highlighting, live token/cost bar). On a pipe or non-TTY it falls back to the
  plain line UI automatically.
- Add `-y/--yes` to auto-approve tool calls when you trust the task; otherwise
  Ties asks before editing files or running shell commands.
- Colors honor `NO_COLOR` / `FORCE_COLOR`; themes via `--theme dark|light|mono`
  or `TIES_THEME`.
- Drop an `AGENTS.md` (or `CLAUDE.md` / `TIES.md`) in your repo — run
  `ties init` to scaffold one — and Ties loads it as project context.

## Commands

| Command | Description |
| --- | --- |
| `ties run [prompt]` | Run a single agent task (reads stdin if no prompt) |
| `ties chat` | Interactive chat session |
| `ties init` | Scaffold an `AGENTS.md` project-context file |
| `ties auth login/list/logout` | Manage provider credentials |
| `ties config [path]` | Show merged config and its sources |
| `ties mcp list/add/remove/tools` | Manage MCP servers and inspect their tools |
| `ties session list/show <id>` | Inspect transcripts |
| `ties session export <id> [--format md\|html]` | Export a transcript to share |
| `ties skill list/show <name>/add <name>` | Inspect or scaffold skills |
| `ties tools` | List built-in tools |
| `ties models` | List providers and the default model |
| `ties version` | Print version |

Common flags for `run`/`chat`: `-m/--model`, `-y/--yes` (auto-approve tools),
`--resume <id>`, `--no-session`, `--plan` (read-only plan mode),
`--tdd` (test-driven mode), `--max-steps <n>`. `chat` also takes `--tui` for
the full-screen interface (falls back to the line UI when stdout is not a TTY).
`run` also takes `-q/--quiet`
and `-o/--output text|json` for non-interactive scripting, plus an autonomous
loop: `--loop` (keep iterating until done), `--max-loops <n>` (default 12) and
`--until <text>` (stop when the final message contains the text).

### Autonomous loops & MCP scaffolding

```bash
# Keep going until the agent verifies the goal and prints TIES_TASK_COMPLETE
ties run -y --loop "make `go test ./...` pass"
ties run -y --until "all green" "fix the failing tests"

# Test-driven: write a failing test first, then implement to green
ties run -y --tdd "add a Reverse(string) helper with tests"

# Register MCP servers without hand-editing JSON
ties mcp add fs -- npx -y @modelcontextprotocol/server-filesystem .
ties mcp add remote --url https://mcp.example.com/rpc --header "Authorization:Bearer <token>"
ties skill add my-workflow      # scaffolds skills/my-workflow/SKILL.md
```

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
    "filesystem": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "."] },
    "remote":     { "url": "https://mcp.example.com/rpc", "headers": { "Authorization": "Bearer <token>" } }
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

### AWS Bedrock

Bedrock's Anthropic Claude models work with no API key — they use standard AWS
credentials. Set `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` (and
`AWS_SESSION_TOKEN` if using temporary creds), plus a region via `AWS_REGION` or
the provider's `baseUrl`:

```jsonc
{
  "model": "bedrock/anthropic.claude-3-5-sonnet-20240620-v1:0",
  "providers": { "bedrock": { "baseUrl": "us-east-1" } }
}
```

Requests are SigV4-signed. By default Ties uses Bedrock's
`InvokeModelWithResponseStream` API and decodes the binary
`vnd.amazon.eventstream` framing for **true token-by-token streaming**; set
`TIES_BEDROCK_NO_STREAM=1` to fall back to the buffered `InvokeModel` API.

Environment overrides: `TIES_MODEL`, `TIES_MAX_STEPS`, `TIES_THEME`,
`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` (or `GOOGLE_API_KEY`),
`ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, `GEMINI_BASE_URL`, `NO_COLOR`,
`FORCE_COLOR`. For Bedrock: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
`AWS_SESSION_TOKEN`, `AWS_REGION` (or `AWS_DEFAULT_REGION`),
`TIES_BEDROCK_NO_STREAM`.

### Chat slash-commands

`/help` · `/tools` · `/skills` · `/context` · `/model` · `/usage` · `/clear` · `/exit`

## Development

A `Makefile` wraps the common tasks (run `make help` to list them):

```bash
make build      # build ./ties with version info stamped in
make test       # go test ./...
make race       # race detector on the concurrent packages
make lint       # gofmt + go vet + golangci-lint (if installed)
make install    # build and install onto your PATH
make clean      # remove build artifacts
```

Or the raw toolchain:

```bash
go build ./...
go vet ./...
gofmt -l .
golangci-lint run ./...
go test ./...
```

## License

MIT © defomok-max
