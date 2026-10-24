# Simphony — Agent Development Guide

## Project Overview
Simphony is a from-scratch implementation of the [OpenAI Symphony specification](https://github.com/openai/symphony/blob/main/SPEC.md).

Symphony is a long-running automation service that:
1. Polls Linear (or other trackers) for active issues
2. Creates isolated per-issue workspaces
3. Runs coding agents (OpenAI Codex app-server) against those issues
4. Manages the full lifecycle: dispatch, retry, reconciliation, observability

This implementation uses:
- **Go** for the core orchestrator service (scheduler, workspace manager, agent runner, tracker client, REST API)
- **TypeScript + React + Vite** for the optional dashboard UI

## Directory Layout

```
simphony/
├── cmd/simphony/          # Main entry point
├── internal/
│   ├── config/            # WORKFLOW.md loader, YAML front matter, typed config getters
│   ├── tracker/           # Issue tracker adapters (Linear GraphQL)
│   ├── orchestrator/      # Poll loop, dispatch, retries, reconciliation, state machine
│   ├── workspace/         # Workspace directory lifecycle, hooks
│   ├── agent/             # Codex app-server client (stdio protocol)
│   ├── server/            # Optional HTTP REST API + dashboard serving
│   └── logging/           # Structured logging
├── pkg/api/               # Public domain types and API contracts
├── dashboard/             # TypeScript React dashboard
│   ├── src/
│   │   ├── api/           # TypeScript API types and client
│   │   ├── components/    # React components
│   │   ├── App.tsx
│   │   └── main.tsx
│   ├── index.html
│   ├── package.json
│   ├── tsconfig.json
│   └── vite.config.ts
├── WORKFLOW.md            # Example workflow configuration
├── go.mod
└── README.md
```

## Architecture Principles

1. **Spec Compliance**: Follow the SPEC.md precisely. Where it uses RFC 2119 keywords (MUST, SHOULD, MAY), treat them as requirements.
2. **In-Memory State Only**: No persistent database. Recovery is tracker-driven + filesystem-driven.
3. **Hot Reload**: WORKFLOW.md changes must be detected and applied without restart.
4. **Forward Compatibility**: Unknown keys in WORKFLOW.md front matter MUST be ignored.
5. **Workspace Isolation**: The coding agent MUST only run inside the per-issue workspace directory.
6. **Structured Logging**: Use key=value phrasing. Include issue_id, issue_identifier, and session_id context.
7. **Error Taxonomy**: Use spec-defined error categories (missing_workflow_file, workflow_parse_error, codex_not_found, etc.)

## Key Interfaces (to implement)

### Config Layer (`internal/config`)
- `LoadWorkflow(path string) (*api.WorkflowDefinition, error)` — parse WORKFLOW.md
- `ResolveConfig(def *api.WorkflowDefinition, workflowDir string) (*api.WorkflowConfig, error)` — typed getters, defaults, env var resolution
- `WatchWorkflow(path string, onChange func())` — fsnotify-based hot reload

### Tracker (`internal/tracker`)
- `Tracker` interface:
  - `FetchCandidateIssues(ctx context.Context) ([]api.Issue, error)`
  - `FetchIssuesByStates(ctx context.Context, states []string) ([]api.Issue, error)`
  - `FetchIssueStatesByIDs(ctx context.Context, ids []string) (map[string]api.Issue, error)`

### Workspace Manager (`internal/workspace`)
- `Manager` interface:
  - `PrepareWorkspace(issue api.Issue) (*api.Workspace, error)`
  - `RemoveWorkspace(issueIdentifier string) error`
  - `RunHook(name string, script string, workspacePath string, timeoutMs int) error`

### Agent Runner (`internal/agent`)
- `Runner` interface:
  - `Run(ctx context.Context, issue api.Issue, workspace *api.Workspace, attempt *int, cfg *api.CodexConfig, eventCallback func(api.AgentEvent)) error`
- Must speak Codex app-server protocol over stdio
- Must support continuation turns on the same thread

### Orchestrator (`internal/orchestrator`)
- Owns the in-memory `api.OrchestratorState`
- Poll tick: reconcile → validate → fetch → dispatch
- Manages retry queue with exponential backoff
- Concurrency limits (global + per-state)

### Server (`internal/server`)
- Optional HTTP REST API
- `GET /api/v1/state`
- `GET /api/v1/<issue_identifier>`
- `POST /api/v1/refresh`
- Serve dashboard static files at `/`

## Build & Run

### Backend
```bash
cd simphony
go mod tidy
go build ./cmd/simphony
./simphony -workflow ./WORKFLOW.md
```

### Dashboard
```bash
cd simphony/dashboard
npm install
npm run dev          # dev server with proxy to :8080
npm run build        # production build -> dist/
```

### Tests
```bash
go test ./...                 # backend tests
cd dashboard && npm run build # dashboard typecheck
```

## Coding Conventions

### Go
- Use `gofmt` / `go vet`
- Idiomatic error handling: check errors explicitly, wrap with context
- Table-driven tests for logic-heavy packages
- Context propagation for cancellation
- Document all exported identifiers

### TypeScript
- Strict mode enabled
- Functional React components with hooks
- Async/await for API calls
- Types mirror Go API types in `dashboard/src/api/types.ts`

## Spec Reference Points

When implementing, verify against these spec sections:
- **Section 3**: Components (Workflow Loader, Config Layer, Tracker, Orchestrator, Workspace, Agent, Status, Logging)
- **Section 4**: Domain Models (Issue, Workspace, RunAttempt, AgentSession, RetryEntry, OrchestratorState)
- **Section 5**: Workflow File (front matter parsing, Liquid template rendering)
- **Section 6**: Configuration (defaults, env var resolution, validation rules)
- **Section 7**: Orchestrator Behavior (poll tick, dispatch gating, retry logic, reconciliation, startup cleanup)
- **Section 8**: Workspaces (path sanitization, hooks, lifecycle)
- **Section 9**: Agent Runner (Codex app-server stdio protocol, session management, continuation turns)
- **Section 10**: Tracker Adapter (Linear GraphQL queries, pagination, normalization)
- **Section 11**: Prompt Rendering (Liquid, strict variable checking)
- **Section 12**: Logging (structured, key=value, required context fields)
- **Section 13**: Optional HTTP Interface (REST API shape, dashboard)

## Windows Notes
This project is being developed on Windows. For shell hooks:
- The spec mandates `bash -lc` on POSIX. On Windows, use `cmd /C` or PowerShell as fallback, but prefer WSL/MSYS2 `bash` if available.
- Path handling must use `filepath` package for portability.

## Live harness operations (added 2026-09-24, from Claude memory import)

Beyond this dev guide: simphony also runs as Keith's **live, unattended coding-agent
harness** (`simphony.exe -config simphony.yaml`, registry mode, `127.0.0.1:8080`),
managing `simphony` (self) and `geekli` (`../geekli`), each via its own
`WORKFLOW.md`. It polls Linear and spawns a coding SDK subprocess per stage (Codex
app-server or Claude CLI); tracker state = stage.

- **Deploying is the USER's action, not the agent's** — compiled Go binary: agent
  builds/stages (`go build -o simphony.exe ./cmd/simphony`); Keith stops/starts
  (`Start-Process`) from a shell with the provider key env vars.
- **Keys are OS environment variables, no `.env`**: `ZAI_API_KEY`,
  `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `KIMI_API_KEY` (resolved at WORKFLOW.md
  load). z.ai key rotated 2026-07 after plaintext exposure in geekli git history.
- Keith configures projects by hand-editing each WORKFLOW.md, not the dashboard.
- **Provider/model presets live in the dashboard** (`dashboard/src/App.tsx`, `PROVIDER_OPTIONS`):
  each preset carries per-SDK endpoints (`endpoints.claude` / `endpoints.codex`, since one
  vendor can expose different endpoints per protocol), a model list, and an env-backed
  `$API_KEY` reference. Kimi (`https://api.moonshot.ai/anthropic` via Claude SDK,
  `$KIMI_API_KEY`; `$MOONSHOT_API_KEY` is the vendor-conventional alias) and DeepSeek
  (`https://api.deepseek.com/anthropic` via Claude SDK, `$DEEPSEEK_API_KEY`) are configured
  presets alongside live-verified Z.ai GLM (`https://api.z.ai/api/anthropic`). The historical
  ADR-0001 provider-catalog architecture was not kept; do not resurrect it.
- Codex with non-OpenAI endpoints (Kimi `/v1`, DeepSeek root, Z.ai
  `/api/coding/paas/v4`) remains a documented limitation: Simphony does not generate a
  per-workspace Codex `model_providers` configuration.
- Spend statistics come from the built-in `internal/pricing` table (list-price
  estimates per 1M tokens, prefix-matched by model): `cost_usd` accumulates in
  `updateTokens`, folds into `codex_totals`/`stage_totals` on completion, and is
  retained per issue identifier (FIFO cap 1000, in-memory, resets on restart).
  Unknown models yield tokens without cost.
- Production bug class to avoid: Claude CLI variadic `--disallowed-tools` swallows
  the trailing prompt (use `--`); setting BOTH `ANTHROPIC_API_KEY` and
  `ANTHROPIC_AUTH_TOKEN` makes the CLI send both auth headers → rejected by
  z.ai/Kimi (send one, endpoint-primary); a logged-in personal Claude account in
  `~/.claude.json` hijacks `ANTHROPIC_BASE_URL` auth when user settings load — the
  embedded shim therefore defaults `settingSources` to `["project", "local"]`
  (never `user`) unless the workflow sets `setting_sources` explicitly; launching
  the embedded shim through `cmd /C` with a quoted path mangles argv on Windows
  (node receives the quotes as part of the module path) — the runner invokes
  `node <shim>` directly, no shell, for the embedded-shim default path.

**Full imported memory archive:**
`%USERPROFILE%\Documents\kimi\workspace\claude-import-archive\simphony-memory\`
