# HTTP API

The HTTP API is enabled when `server.port` is set in `WORKFLOW.md`.

```yaml
server:
  port: 8080
```

The server allows cross-origin requests and returns JSON for all API responses.

In multi-project `-config` mode, a registry-level `server:` block enables the aggregate project API:

- `GET /api/v1/projects`
- `GET /api/v1/projects/{project_id}/state`
- `GET /api/v1/projects/{project_id}/issues/{issue_identifier}`
- `POST /api/v1/projects/{project_id}/refresh`
- `POST /api/v1/projects/{project_id}/pause`
- `POST /api/v1/projects/{project_id}/resume`
- `POST /api/v1/projects/{project_id}/stages/{stage}/pause`
- `POST /api/v1/projects/{project_id}/stages/{stage}/resume`
- `GET /api/v1/projects/{project_id}/settings`
- `PUT /api/v1/projects/{project_id}/settings`
- `POST /api/v1/projects/{project_id}/settings/validate-tracker`

The dashboard uses these routes to discover projects, select the active project, and scope runtime/settings calls.

When exactly one project runtime is running in multi-project mode, the aggregate server also accepts the single-project compatibility routes `/api/v1/state`, `/api/v1/refresh`, `/api/v1/pause`, `/api/v1/resume`, `/api/v1/stages/{stage}/pause|resume`, `/api/v1/settings`, `/api/v1/settings/validate-tracker`, and `/api/v1/{issue_identifier}`. A server started directly from one `WORKFLOW.md` accepts the same control routes. When zero or multiple projects are running, callers must use project-scoped routes.

Project-scoped routes return `project_disabled` for configured disabled projects and `project_not_running` for enabled projects that failed to start or are stopped.

## List Projects

```http
GET /api/v1/projects
```

Returns configured projects, per-project runtime counts, and shared supervisor concurrency usage.

```json
{
  "generated_at": "2026-05-01T12:00:00Z",
  "concurrency": {
    "max_concurrent_agents": 10,
    "used_agents": 7,
    "available_agents": 3
  },
  "projects": [
    {
      "id": "alpha",
      "name": "Alpha",
      "workflow_path": "C:\\work\\alpha\\WORKFLOW.md",
      "enabled": true,
      "running": true,
      "max_concurrent_agents": 2,
      "counts": {
        "running": 2,
        "retrying": 0,
        "claimed": 2,
        "completed": 4
      },
      "control": {
        "paused": false,
        "paused_stages": []
      },
      "waiting_on_supervisor": true,
      "last_supervisor_deferred_at": "2026-05-01T11:59:30Z",
      "workflow_watcher_running": true
    }
  ]
}
```

`waiting_on_supervisor` means that project's last dispatch pass had eligible work but could not acquire a shared global slot. It clears when the project successfully dispatches again.

`workflow_watcher_running` reports whether the project's `WORKFLOW.md` hot-reload watcher is active. If watcher setup failed, `workflow_watcher_error` contains the setup error.

## Get State

```http
GET /api/v1/state
```

Returns the current orchestrator snapshot.

```json
{
  "generated_at": "2026-05-01T12:00:00Z",
  "control": {
    "paused": false,
    "paused_stages": ["review"]
  },
  "counts": {
    "running": 1,
    "retrying": 0
  },
  "running": [
    {
      "issue_id": "issue-id",
      "issue_identifier": "SIM-123",
      "state": "In Progress",
      "session_id": "thread-turn",
      "turn_count": 1,
      "last_event": "turn/completed",
      "last_message": "",
      "started_at": "2026-05-01T11:59:00Z",
      "last_event_at": "2026-05-01T12:00:00Z",
      "tokens": {
        "input_tokens": 100,
        "output_tokens": 50,
        "total_tokens": 150
      }
    }
  ],
  "retrying": [],
  "codex_totals": {
    "input_tokens": 100,
    "output_tokens": 50,
    "total_tokens": 150,
    "seconds_running": 60
  },
  "rate_limits": {}
}
```

## Soft Pause Controls

Pause controls are idempotent and affect only future work. Running agents are not cancelled. Polling, reconciliation, event handling, and normal completion of in-flight work continue while paused. Runtime pauses are in memory and clear when the process restarts.

Pause is distinct from stopping a project runtime. Pause keeps the runtime, polling, reconciliation, hot reload, and in-flight workers alive while gating new dispatches and retries. Stop shuts down the runtime and cancels its workers. Use Pause for temporary operator intervention; use Stop only when the runtime itself should be taken offline.

```http
POST /api/v1/projects/{project_id}/pause
POST /api/v1/projects/{project_id}/resume
POST /api/v1/projects/{project_id}/stages/{stage}/pause
POST /api/v1/projects/{project_id}/stages/{stage}/resume
```

Supported stages are `coding`, `review`, `review_resolution`, and `merge`. A successful mutation returns the complete control state:

```json
{
  "paused": false,
  "paused_stages": ["review"]
}
```

Resuming queues an immediate refresh and rechecks retained retries without increasing their attempt count. Invalid stages return `invalid_stage`. Disabled and stopped projects use the existing `project_disabled` and `project_not_running` responses.

## Get Issue Detail

```http
GET /api/v1/{issue_identifier}
```

Returns details for an issue currently known to the in-memory orchestrator state. Running and retrying issues are supported.
Completed or idle tracker issues are not persisted in this API; query Linear directly for historical issue state.

```json
{
  "issue_identifier": "SIM-123",
  "issue_id": "issue-id",
  "status": "running",
  "workspace": {
    "path": "C:\\work\\simphony_workspaces\\SIM-123"
  },
  "attempts": {
    "restart_count": 1,
    "current_retry_attempt": 0
  },
  "running": {
    "issue_id": "issue-id",
    "issue_identifier": "SIM-123",
    "state": "In Progress",
    "session_id": "thread-turn",
    "turn_count": 1,
    "last_event": "turn/completed",
    "last_message": "",
    "started_at": "2026-05-01T11:59:00Z",
    "last_event_at": "2026-05-01T12:00:00Z",
    "tokens": {
      "input_tokens": 100,
      "output_tokens": 50,
      "total_tokens": 150
    }
  },
  "retry": null,
  "logs": {
    "codex_session_logs": []
  },
  "recent_events": [],
  "last_error": null,
  "tracked": {}
}
```

For running issues, `attempts.restart_count` currently reflects the active Codex turn count. For retrying issues, `attempts.current_retry_attempt` reflects the queued retry attempt.

If the issue is not in the running or retry queues, the server returns `404`.

Retrying issues return the same wrapper shape with `running` set to `null` and `retry` populated:

```json
{
  "issue_identifier": "SIM-123",
  "issue_id": "issue-id",
  "status": "retrying",
  "workspace": {
    "path": ""
  },
  "attempts": {
    "restart_count": 0,
    "current_retry_attempt": 2
  },
  "running": null,
  "retry": {
    "issue_id": "issue-id",
    "issue_identifier": "SIM-123",
    "attempt": 2,
    "due_at": "2026-05-01T12:05:00Z",
    "error": "before_run hook: exit status 1"
  },
  "logs": {
    "codex_session_logs": []
  },
  "recent_events": [],
  "last_error": "before_run hook: exit status 1",
  "tracked": {}
}
```

## Trigger Refresh

```http
POST /api/v1/refresh
```

Queues an immediate orchestrator tick. `GET` is also accepted for convenience.

```json
{
  "queued": true,
  "coalesced": false,
  "requested_at": "2026-05-01T12:00:00Z",
  "operations": ["tick"]
}
```

If a refresh is already pending, `queued` is `false` and `coalesced` is `true`.

## Get Settings

```http
GET /api/v1/settings
```

Returns the editable `WORKFLOW.md` front matter, the resolved runtime configuration, and the prompt template. Literal secret values are masked as `********`; environment references such as `$LINEAR_API_KEY` are returned as references.

## Update Settings

```http
PUT /api/v1/settings
```

Validates, applies, and saves updated workflow settings. If a masked secret value is submitted unchanged, Simphony preserves the current value from `WORKFLOW.md`.

## Validate Linear Settings

```http
POST /api/v1/settings/validate-tracker
POST /api/v1/projects/{project_id}/settings/validate-tracker
```

Validates the submitted tracker settings against Linear without saving them. The server resolves the workflow config, creates a Linear client, and fetches candidate issues for the configured project and active states. In multi-project mode, use the project-scoped route unless exactly one project runtime is running.

```json
{
  "ok": true,
  "project_slug": "your-linear-project-slug",
  "active_states": ["Todo", "In Progress", "Approved"],
  "candidate_count": 3,
  "message": "Linear settings validated"
}
```

## Errors

Errors use a consistent wrapper:

```json
{
  "error": {
    "code": "not_found",
    "message": "Issue SIM-123 not found"
  }
}
```

Common HTTP statuses:

- `404` for unknown API paths or issue identifiers.
- `405` for unsupported methods.
