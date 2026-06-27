# Operations

This page covers the day-to-day behavior of a running Simphony process.

## Startup

Start Simphony with an explicit workflow file:

```bash
go run ./cmd/simphony -workflow ./WORKFLOW.md
```

Start enabled projects from a multi-project registry:

```bash
go run ./cmd/simphony -config ./simphony.yaml
```

Validate a registry without starting workers:

```bash
go run ./cmd/simphony validate -config ./simphony.yaml
```

List resolved projects, workspace roots, tracker slugs, runtime defaults, and static health:

```bash
go run ./cmd/simphony projects -config ./simphony.yaml
```

Start only one enabled project from a registry:

```bash
go run ./cmd/simphony -config ./simphony.yaml -project alpha
```

The `-config` mode validates project isolation, starts project runtimes, and, when the registry has a `server:` block, exposes the aggregate project API. The dashboard can use these project-scoped routes when it is run separately during development.

Multi-project startup refuses enabled projects with overlapping workspace roots or workspace roots under the registry directory unless the registry explicitly allows those cases in `security:`. It also refuses non-loopback registry server bind addresses unless `security.allow_remote_dashboard: true` is set, and prints warnings when multiple enabled projects reference the same Linear project slug.

When the dashboard is pointed at an aggregate server, it discovers `/api/v1/projects` and shows a project selector. If the aggregate project endpoint is not available, the dashboard falls back to the single-project API routes.

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
- `project_id`
- `project_name`

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

In multi-project mode:

```bash
curl http://localhost:8080/api/v1/projects
curl http://localhost:8080/api/v1/projects/alpha/state
curl http://localhost:8080/api/v1/projects/alpha/issues/SIM-123
curl -X POST http://localhost:8080/api/v1/projects/alpha/refresh
curl http://localhost:8080/api/v1/projects/alpha/settings
```

The project list includes supervisor-wide slot usage and marks projects waiting for a global agent slot.

State is in memory only. Restarting Simphony clears running, claimed, retry, completed, and token-total counters. On the next startup, Simphony uses Linear state and workspace directories to decide what to do next.

## Multi-Project Recovery

Use `simphony projects -config ./simphony.yaml` before startup to catch registry, workflow, workspace, and runtime-default problems without launching agents.

When a multi-project process is running, start triage from the aggregate project list:

```bash
curl http://localhost:8080/api/v1/projects
```

The project list shows which projects are disabled, stopped, failed, running, retrying, or waiting on the supervisor concurrency gate. A failed project does not stop healthy sibling projects. Fix that project's `WORKFLOW.md`, Linear settings, hooks, or workspace root, then restart the Simphony process to start the failed runtime cleanly.

To narrow the blast radius while debugging, start only the affected project:

```bash
go run ./cmd/simphony -config ./simphony.yaml -project alpha
```

This still loads the full registry for validation and summaries, but only launches the selected project's tracker, workspace manager, watcher, and orchestrator.

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

In multi-project mode, each enabled project has its own watcher. A workflow reload failure is logged with `project_id` and `project_name` and does not reload or stop sibling projects.

## Shutdown

Use Ctrl+C or send SIGTERM. Simphony cancels running workers, waits up to 30 seconds for the orchestrator loop, then waits up to 30 seconds for workers.

Workspaces are not removed on ordinary shutdown. They are removed when issues reach terminal states, including during startup cleanup.

In multi-project mode, shutdown stops each started project runtime independently. Stopped or failed projects that never started have no watcher or worker process to clean up.

## Retries

Failures are retried with exponential backoff:

- first retry after 10 seconds,
- subsequent retries double the delay,
- delay is capped by `agent.max_retry_backoff_ms`.

Continuation turns are managed inside a running Codex session. After each turn, Simphony checks the issue state in Linear. If the issue is still active, the runner starts another turn on the same Codex thread until the issue leaves an active state or reaches `agent.max_turns`.
