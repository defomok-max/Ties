# Ties — build plan & architecture

**Ties** is a terminal AI coding agent: a deliberate blend of **Claude Code**
(agentic loop, skills, permissions), **OpenCode** (multi-provider, single Go
binary) and **Codex CLI** (focused, scriptable runs). Written in Go 1.23 using
only the standard library — no vendor SDKs, no heavy framework deps — so it
builds offline into one static binary.

> Status legend: ✅ done · 🚧 in progress · ⬜ planned

---

## 1. Design goals

1. **Vendor-neutral.** The core never imports a provider SDK. Everything goes
   through one streaming `provider.Provider` interface. Adding a model vendor is
   one file + one `init()` registration.
2. **Single binary, offline build.** Zero third-party modules. `go build` works
   without network access.
3. **Safe by default.** Every tool call passes through a permission engine
   (allow / ask / deny, deny-wins). Filesystem access is confined to the
   workspace root.
4. **Composable context.** Layered config, MCP servers, and skills all feed the
   same agent loop.
5. **Inspectable.** JSONL session transcripts you can `cat`, replay and resume.

---

## 2. Architecture

```
cmd/ties                 process entrypoint
internal/
  cli         command router + wiring (run, chat, auth, config, mcp,
              session, skill, tools, models, version)
  config      layered config (defaults < ~/.config/ties < .ties.json < env)
  provider    vendor-neutral Provider interface + registry + SplitModel
    anthropic   Messages API client (SSE streaming, tool_use/tool_result)
    openai      Chat Completions client (SSE streaming, tool_calls)
    gemini      native generateContent client (SSE, functionCall/Response)
    resilient   retry+backoff wrapper + ordered model-fallback chain
  agent       ReAct loop: provider ⊕ tools ⊕ permission ⊕ session
  tool        Tool interface + registry + built-ins (read/write/edit/
              multiedit/patch/list/glob/grep/tree/bash/webfetch/todo/task),
              FS confined to root; `task` delegates to a sub-agent
  memory      AGENTS.md / CLAUDE.md / TIES.md discovery + prompt injection
  pricing     best-effort USD cost from an embedded price table
  permission  allow/ask/deny engine, deny-wins, glob patterns
  session     append-only JSONL transcripts (create/resume/list/show)
  mcp         Model Context Protocol client (stdio JSON-RPC), tool adapter
  skill       SKILL.md discovery + frontmatter parse (progressive disclosure)
  prompt      system prompt assembly (env + skills catalog)
  version     build metadata
```

**Data flow of one turn:** `cli` builds an `app` (config → provider → tool
registry [+ skill tool + MCP tools] → permission → system prompt → session) and
hands it to `agent.Run`. The agent streams a completion; text deltas are printed
live; tool calls are checked against the permission engine, executed, and their
results fed back until the model stops requesting tools.

---

## 3. Status of the first slice ✅

All of the following is implemented, builds, vets, is gofmt-clean, passes
`golangci-lint` v1.62.2 and `go test ./...`, and was verified end-to-end by
running the real binary against a mock Anthropic SSE server (tool call → tool
execution → final answer):

- ✅ `internal/provider` interface, registry, streaming events, usage, `SplitModel`
- ✅ `internal/provider/anthropic` — full SSE streaming + tool_use/tool_result, `APIError.Retryable()`
- ✅ `internal/provider/openai` — full SSE streaming + tool_calls (proves neutrality)
- ✅ `internal/agent` — ReAct loop with callbacks, permission gating, session/local transcript
- ✅ `internal/tool` — read, write, edit, list, glob (`**`), grep, bash; root-confined
- ✅ `internal/permission` — allow/ask/deny, deny-wins, `tool` / `tool:pattern` / `*` rules
- ✅ `internal/session` — JSONL create/resume/list/show + render
- ✅ `internal/mcp` — stdio JSON-RPC client, handshake, tools/list, tools/call, tool adapter
- ✅ `internal/skill` — SKILL.md discovery, frontmatter parse, catalog, `skill` tool
- ✅ `internal/config` — layered merge + env + project discovery
- ✅ `internal/cli` — `run`, `chat`, `auth`, `config`, `mcp`, `session`, `skill`, `tools`, `models`, `version`
- ✅ Tests for config, permission, session, skill, agent (mock provider)

---

## 4. Roadmap (next vertical slices)

### 4.1 TUI ✅ (first pass)
- Done: dependency-free `internal/ui` — themes (`dark`/`light`/`mono`), banner,
  braille spinner, colored tool lines + icons, red/green diff previews for
  `edit`/`write`, boxes, `NO_COLOR`/`FORCE_COLOR`/TTY detection, a token+cost
  status line, and chat slash-commands (`/help /tools /skills /model /usage
  /clear /exit`). Still planned: full-screen scrollback renderer, syntax
  highlighting, inline (non-line) permission prompt.

### 4.2 Resilience ✅
- Done: `internal/provider/resilient` — retries with exponential backoff +
  jitter on retryable errors (`APIError.Retryable()`), and an ordered
  model-fallback chain (`models: [...]`). Per-attempt + fallback callbacks
  surface in the UI.

### 4.3 More providers ✅ (custom providers + native Gemini)
- Done: user-defined custom providers via config `type: openai|anthropic` +
  `baseUrl` + `apiKey` + custom `headers`, covering OpenRouter, Groq, Together,
  local Ollama (smart `/v1` handling, optional key), Azure-style gateways.
  `internal/pricing` gives best-effort cost from a built-in table.
- Done: **native Gemini provider** (`internal/provider/gemini`) — the real
  `generateContent` SSE wire format (`?alt=sse&key=`, out-of-band
  `system_instruction`, `functionCall`/`functionResponse` mapping,
  `usageMetadata`→usage, retryable `APIError`). Registered as `gemini/…`; Gemini
  prices added to the pricing table.
- ✅ **AWS Bedrock provider** (`internal/provider/bedrock`) — Anthropic Claude
  on Bedrock via SigV4-signed `InvokeModel` (non-streaming), with the single
  JSON response adapted into the agent's streaming event model. Credentials from
  the standard AWS env vars; region from the provider `baseUrl` or
  `AWS_REGION`/`AWS_DEFAULT_REGION`. SigV4 signer + wire conversion are
  unit-tested (incl. an httptest server); the live network path needs real AWS
  credentials to exercise end-to-end. Registered as `bedrock/…`.
- Still planned: Bedrock's binary event-stream (true token streaming) and a
  fuller pricing/context catalog.

### 4.4 Richer tools ✅
- Done: tool output-truncation budget (`maxToolOutput`); `webfetch`
  (HTTP(S) GET → readable text); `patch` (unified-diff applier with context
  matching, line drift, create/delete); `multiedit` (atomic multi-replace on one
  file); `todo` (in-run planning list rendered to the UI); **per-tool timeouts**
  (`toolTimeout`, wraps each tool call in a deadline context); structured
  **`tree`** (depth-limited directory map, skips `.git`/`node_modules`).

### 4.5 MCP depth ✅ (HTTP transport + scaffolder)
- ✅ **Streamable HTTP transport** (`internal/mcp/http.go`) alongside stdio:
  JSON-RPC POSTed to one endpoint, parsing either `application/json` or
  `text/event-stream` (SSE) responses, capturing/reusing `Mcp-Session-Id`, with
  best-effort `DELETE` teardown. Selected when a server has a `url`. A
  transport-agnostic `mcp.Server` interface backs both transports.
- ✅ **`ties mcp add` / `ties mcp remove`** scaffold the global config —
  `ties mcp add <name> --url <url> [--header K:V]` (HTTP) or
  `ties mcp add <name> -- <command> [args...]` (stdio).
- Still planned: resources & prompts (not just tools); capability negotiation and
  auto-reconnection.

### 4.6 Skills depth ✅ (scaffolder)
- ✅ **`ties skill add <name>`** scaffolds `skills/<name>/SKILL.md` with valid
  frontmatter (`--force` to overwrite, name validation) so it's discovered on the
  next run.
- Still planned: per-skill allowed tools; project vs. global vs. bundled
  precedence ordering. (`references/`+`scripts/` progressive disclosure already
  works via the `skill` tool.)

### 4.7 Agent features (the "unique" layer) 🚧
- ✅ **Sub-agents:** the `task` tool spawns a scoped child agent for a subtask —
  shares provider/tools (minus `task`, no recursion)/permissions, fresh short
  transcript, its own step cap, draws from and folds spend back into the
  parent's remaining budget (`agent.RemainingBudget`/`AddSpent`).
- ✅ **Ralph loops:** `--loop` runs the agent in a bounded, repeating loop that
  re-checks and continues until it prints the `TIES_TASK_COMPLETE` marker, an
  `--until <text>` phrase appears in the final message, or `--max-loops`
  (default 12) is hit. The shared session means each iteration sees prior
  progress; the loop note is injected into the system prompt.
- ✅ **Budgets:** hard token/$ ceilings per run with graceful stop —
  `maxCostUSD` / `maxTokens` config; the agent accounts usage after each turn
  and stops cleanly when a ceiling is reached (`agent.Budget`, `agent.Spent()`).
- ✅ **Plan mode:** `--plan` makes a run read-only (mutating tools hard-denied
  via `agent.DenyTools`, prompt augmented) so the agent proposes before editing.
- ✅ **Session export:** `ties session export <id> --format md|html` renders a
  shareable transcript (`session.Export`).
- ✅ **TDD mode:** `--tdd` augments the system prompt to enforce the
  red→green→refactor discipline (write a failing test first, confirm it fails,
  implement the minimum to pass, then refactor while keeping tests green).
- **Voice in/out** and **pair-agents** remain planned.

### 4.8 Quality & packaging 🚧
- ✅ `--output json` and `--quiet` for scripting / non-interactive CI: a quiet
  run silences the UI and routes only the result to stdout; `--output json`
  prints `{model, session, final, usage, costUSD}`. Unattended runs deny any
  tool that would need an "ask" prompt unless `--yes` is set.
- GitHub Actions: build matrix, `go test`, `golangci-lint`, release binaries
  (note: workflow files must be added manually — the bot lacks `workflows` perm).
- `goreleaser` config, Homebrew tap, `go install` instructions.

### 4.9 Project memory & references ✅
- ✅ **Agent-context files:** `internal/memory` auto-discovers `AGENTS.md` /
  `CLAUDE.md` / `TIES.md` (the convention shared by Claude Code, OpenCode and
  Codex) by walking up from the working directory, plus an optional global file
  under `~/.config/ties`. The concatenated text is injected into the system
  prompt (nearest file wins); `/context` lists what loaded.
- ✅ **`ties init`:** scaffolds a starter `AGENTS.md`, guessing build/test
  commands from marker files (`go.mod`, `Cargo.toml`, `package.json`, …).
- ✅ **`@file` references:** `@path` tokens in any `run`/`chat` prompt inline the
  referenced file's contents (confined to the workspace root).

---

## 5. Self-assessment (the "оцени" part)

**Strengths.** The provider abstraction is genuinely vendor-neutral (two real
backends already). The whole thing builds with zero external modules, which
makes it trivial to vendor and audit. Safety (root confinement + deny-wins
permissions) is built in from the start, not bolted on. Sessions are plain JSONL
— debuggable and replayable. MCP + skills are first-class, matching Claude
Code's extensibility story.

**Resolved since the first slice.** The highest-leverage gaps are now shipped: a
styled themed UI with spinner/diffs (§4.1), retries + model fallback (§4.2), a
custom-provider system + cost/token metering and a **native Gemini provider**
(§4.3), a full set of richer tools — `webfetch`, `patch`, `multiedit`, `todo`,
output truncation and per-tool timeouts (§4.4), and an agentic-depth layer:
per-run **token/$ budgets**, **sub-agents** (`task`), read-only **plan mode**
and **session export** (§4.7). Most recently, a **project-memory & references**
layer (§4.9) brings Ties to parity with the reference CLIs: auto-loaded
`AGENTS.md`/`CLAUDE.md`/`TIES.md` context, `ties init`, `@file` prompt
references, a structured `tree` tool (§4.4) and scriptable `--quiet` /
`--output json` runs (§4.8).

**Gaps / risks to tackle next.** (1) The UI is still line-oriented, not a
full-screen renderer — no scrollback management or syntax highlighting yet.
(2) Pricing/context catalog is a small static table; unknown models just skip
cost. (3) Mid-stream provider errors aren't retried (only pre-stream), by
design. (4) Glob is a hand-rolled matcher — fine, but deserves more tests.
These are scheduled in §4.1, §4.3 and §4.7.

---

## 6. Conventions

- **No external deps.** stdlib only. If a dep is ever truly needed, it must be
  justified here first.
- **Lint:** keep impl structs unexported, return interfaces from constructors;
  always check `Fprintf`/`Close` errors (`_, _ =` / `defer func(){ _ = … }()`).
- **Errors:** tool failures return `Result{IsError:true}` (the model sees them);
  only infrastructure failures return a Go `error`.
- **Every slice** must keep `go build ./...`, `go vet`, `gofmt -l`,
  `golangci-lint run` and `go test ./...` all clean before push.
