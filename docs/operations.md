# Operations

This page covers the day-to-day behavior of a running Simphony process.

## Startup

Start Simphony with an explicit workflow file:

```bash
go run ./cmd/simphony -workflow ./WORKFLOW.md
```

On startup, Simphony:

1. Loads and validates `WORKFLOW.md`.
2. Initializes the Linear tracker, workspace manager, Codex runner, and optional HTTP server.
3. Removes workspaces for issues already in terminal states.
4. Performs an immediate poll tick.
5. Starts polling at `polling.interval_ms`.

## Logs

Simphony writes process logs to stdout and stderr. Operational log messages use key-value fields where possible.

Useful fields include:

- `issue_id`
- `issue_identifier`
- `session_id`
- `action`
- `reason`
- `status`
- `error`

Common `action` values include:

- `candidate_fetch`
- `dispatch_started`
- `dispatch_deferred`
- `workspace_prepare`
- `after_create`
- `before_run`
- `worker_exit`
- `stall_detected`
- `reconciliation`
- `startup_cleanup`

## Polling And Refresh

The poll loop runs on `polling.interval_ms`. Each tick reconciles running sessions first, then fetches and dispatches eligible issues.

Trigger an immediate tick through the API:

```bash
curl -X POST http://localhost:8080/api/v1/refresh
```

Refresh requests are coalesced when a refresh is already pending.

## Runtime State

Use the API or dashboard to inspect runtime state:

```bash
curl http://localhost:8080/api/v1/state
curl http://localhost:8080/api/v1/SIM-123
```

State is in memory only. Restarting Simphony clears running, claimed, retry, completed, and token-total counters. On the next startup, Simphony uses Linear state and workspace directories to decide what to do next.

## Hot Reload

Simphony watches `WORKFLOW.md` and reloads it after changes. Reload applies to future scheduler operations:

- polling interval,
- tracker settings,
- workspace root,
- lifecycle hooks,
- concurrency limits,
- Codex command and timeouts,
- prompt template for future runs.

In-flight Codex workers continue with the settings they were launched with.

## Shutdown

Use Ctrl+C or send SIGTERM. Simphony cancels running workers, waits up to 30 seconds for the orchestrator loop, then waits up to 30 seconds for workers.

Workspaces are not removed on ordinary shutdown. They are removed when issues reach terminal states, including during startup cleanup.

## Retries

Failures are retried with exponential backoff:

- first retry after 10 seconds,
- subsequent retries double the delay,
- delay is capped by `agent.max_retry_backoff_ms`.

Continuation turns are managed inside a running Codex session. After each turn, Simphony checks the issue state in Linear. If the issue is still active, the runner starts another turn on the same Codex thread until the issue leaves an active state or reaches `agent.max_turns`.
