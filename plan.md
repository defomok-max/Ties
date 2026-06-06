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
              multiedit/patch/list/glob/grep/bash/webfetch/todo),
              FS confined to root
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
- Still planned: native Bedrock wire format; a fuller pricing/context catalog.

### 4.4 Richer tools ✅
- Done: tool output-truncation budget (`maxToolOutput`); `webfetch`
  (HTTP(S) GET → readable text); `patch` (unified-diff applier with context
  matching, line drift, create/delete); `multiedit` (atomic multi-replace on one
  file); `todo` (in-run planning list rendered to the UI).
- Planned: structured `tree`; per-tool timeouts.

### 4.5 MCP depth ⬜
- HTTP/SSE transport in addition to stdio; resources & prompts (not just tools);
  `ties mcp add` to scaffold config; capability negotiation and reconnection.

### 4.6 Skills depth ⬜
- `references/` + `scripts/` progressive disclosure; per-skill allowed tools;
  a `ties skill add` scaffolder; project vs. global vs. bundled precedence.

### 4.7 Agent features (the "unique" layer) ⬜
- **Sub-agents / pair-agents:** spawn a scoped agent for a subtask.
- **Ralph loops:** bounded autonomous "keep going until done/criteria" mode.
- ✅ **Budgets:** hard token/$ ceilings per run with graceful stop —
  `maxCostUSD` / `maxTokens` config; the agent accounts usage after each turn
  and stops cleanly when a ceiling is reached (`agent.Budget`, `agent.Spent()`).
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

**Resolved since the first slice.** The highest-leverage gaps are now shipped: a
styled themed UI with spinner/diffs (§4.1), retries + model fallback (§4.2), a
custom-provider system + cost/token metering and a **native Gemini provider**
(§4.3), a full set of richer tools — `webfetch`, `patch`, `multiedit`, `todo`
plus output truncation (§4.4), and per-run **token/$ budgets** with graceful
stop (§4.7).

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
