# HTTP API

The HTTP API is enabled when `server.port` is set in `WORKFLOW.md`.

```yaml
server:
  port: 8080
```

The server allows cross-origin requests and returns JSON for all API responses.

## Get State

```http
GET /api/v1/state
```

Returns the current orchestrator snapshot.

```json
{
  "generated_at": "2026-05-01T12:00:00Z",
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
```

Validates the submitted tracker settings against Linear without saving them. The server resolves the workflow config, creates a Linear client, and fetches candidate issues for the configured project and active states.

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
