# Architecture

Simphony is a single-process orchestrator. It keeps runtime state in memory and reconstructs work from the tracker and workspace filesystem instead of using a database.

## Runtime Flow

1. Load `WORKFLOW.md`.
2. Resolve and validate typed configuration.
3. Initialize the tracker client, workspace manager, Codex runner, and optional HTTP server.
4. Remove workspaces for terminal tracker issues during startup cleanup.
5. Start the poll loop.
6. On every tick, reconcile running work and dispatch eligible issues.
7. Watch `WORKFLOW.md` and hot-reload future scheduler settings when it changes.

## Config Layer

`internal/config` parses YAML front matter and the prompt body from `WORKFLOW.md`. It applies defaults, resolves environment variables for values such as `$LINEAR_API_KEY`, validates required fields, and watches the workflow file with `fsnotify`.

Hot reload replaces configuration and dependencies used for future scheduler operations. In-flight Codex workers continue with the configuration they were launched with.

## Tracker

`internal/tracker` currently implements a Linear GraphQL adapter. The public `api.Tracker` interface supports:

- fetching candidate issues in configured active states,
- fetching issues by state for startup cleanup,
- refreshing specific running issue states for reconciliation.

Linear issues are normalized into `api.Issue`, including priority, state, labels, branch name, URL, timestamps, and blocking relationships.

## Orchestrator

`internal/orchestrator` owns `api.OrchestratorState`. Its poll tick is:

```text
reconcile running sessions -> fetch candidates -> filter -> sort -> dispatch
```

Filtering removes issues that are missing required fields, not active, terminal, already running, already claimed, completed, or blocked while in `Todo`.

Dispatch respects global concurrency and optional per-state concurrency limits. Issues are sorted by priority, creation time, and identifier.

Failures are retried with exponential backoff starting at 10 seconds and capped by `agent.max_retry_backoff_ms`. Continuation turns happen inside the agent runner while the issue remains active, allowing multiple turns on the same Codex thread before the runner exits.

## Workspace Manager

`internal/workspace` creates one directory per issue under the configured root. Workspace names are derived from sanitized issue identifiers and checked to ensure they remain inside the root.

Lifecycle hooks run from the workspace directory:

- `after_create`
- `before_run`
- `after_run`
- `before_remove`

`after_create` and `before_run` are fatal when they fail. `after_run` and `before_remove` are best-effort cleanup/inspection hooks.

## Agent Runner

`internal/agent` launches Codex app-server as a subprocess with the workspace as `cwd`. It speaks newline-delimited JSON-RPC over stdio.

The runner:

- initializes the Codex protocol,
- starts a thread,
- renders the first prompt from `WORKFLOW.md`,
- starts turns,
- streams notifications back to the orchestrator,
- tracks thread, turn, process, and token usage metadata,
- rejects user-input requests because Simphony runs unattended,
- starts continuation turns while the orchestrator says the issue is still active.

## Server And Dashboard

`internal/server` exposes a small HTTP API:

- `GET /api/v1/state`
- `GET /api/v1/{issue_identifier}`
- `POST /api/v1/refresh`

If `dashboard/dist` exists, the Go server serves it from `/`. The dashboard is a React/Vite application that polls the state endpoint and displays running sessions, queued retries, and token totals.

## Error Categories

Error codes are centralized in `pkg/api/errors.go` and follow the Symphony specification where applicable. Examples include:

- `missing_workflow_file`
- `workflow_parse_error`
- `unsupported_tracker_kind`
- `missing_tracker_api_key`
- `invalid_workspace_cwd`
- `codex_not_found`
- `response_timeout`
- `turn_timeout`
- `turn_failed`
- `max_turns_reached`

Linear-specific categories cover API transport, status, GraphQL, pagination, and payload issues.
