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
  agent       ReAct loop: provider ⊕ tools ⊕ permission ⊕ session
  tool        Tool interface + registry + built-ins (read/write/edit/
              list/glob/grep/bash), FS confined to root
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

### 4.1 TUI 🚧
- Rich interactive chat UI (currently a readline-style REPL). Streaming render,
  scrollback, syntax highlighting, diff previews for `edit`/`write`, an inline
  permission prompt, model/cost status bar. Likely a hand-rolled ANSI renderer
  to keep the zero-dependency promise, or an optional Bubble Tea build tag.

### 4.2 Resilience ⬜
- Auto model-fallback + retries with exponential backoff + jitter, driven by
  `APIError.Retryable()`. Configurable fallback chain (`model`, `models: [...]`).

### 4.3 More providers ⬜
- Google Gemini, local (Ollama / OpenAI-compatible), Azure OpenAI, Bedrock.
- A `models.dev`-style catalog with context-window + pricing metadata so the
  status bar can show real cost.

### 4.4 Richer tools ⬜
- `webfetch` (read a URL), `patch` (unified-diff apply), `todo` (task list),
  `multiedit` (atomic multi-hunk edits), structured `tree` view.
- Per-tool timeouts and output truncation budgets.

### 4.5 MCP depth ⬜
- HTTP/SSE transport in addition to stdio; resources & prompts (not just tools);
  `ties mcp add` to scaffold config; capability negotiation and reconnection.

### 4.6 Skills depth ⬜
- `references/` + `scripts/` progressive disclosure; per-skill allowed tools;
  a `ties skill add` scaffolder; project vs. global vs. bundled precedence.

### 4.7 Agent features (the "unique" layer) ⬜
- **Sub-agents / pair-agents:** spawn a scoped agent for a subtask.
- **Ralph loops:** bounded autonomous "keep going until done/criteria" mode.
- **Budgets:** hard token/$ ceilings per run with graceful stop.
- **TDD mode:** write test → run → implement → green loop.
- **Voice in/out**, **session sharing/export** (markdown/html), **plan mode**
  (read-only proposal before edits).

### 4.8 Quality & packaging ⬜
- `--output json` for scripting; `--quiet`; non-interactive CI mode.
- GitHub Actions: build matrix, `go test`, `golangci-lint`, release binaries
  (note: workflow files must be added manually — the bot lacks `workflows` perm).
- `goreleaser` config, Homebrew tap, `go install` instructions.

---

## 5. Self-assessment (the "оцени" part)

**Strengths.** The provider abstraction is genuinely vendor-neutral (two real
backends already). The whole thing builds with zero external modules, which
makes it trivial to vendor and audit. Safety (root confinement + deny-wins
permissions) is built in from the start, not bolted on. Sessions are plain JSONL
— debuggable and replayable. MCP + skills are first-class, matching Claude
Code's extensibility story.

**Gaps / risks to tackle next.** (1) The UX is a plain REPL — the biggest
visible gap vs. Claude Code/OpenCode is the TUI. (2) No retry/fallback yet, so a
429 currently fails the run. (3) No cost/token accounting surfaced to the user.
(4) Tool output isn't truncated, so a huge file can blow the context. (5) Glob is
a hand-rolled matcher — fine, but needs more tests. Items 1–4 are the highest
leverage and are scheduled in §4.1–4.4.

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
