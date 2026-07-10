# Troubleshooting

This guide maps common startup and runtime symptoms to the configuration areas that usually cause them.

## Workflow Fails To Load

Look for errors containing:

- `missing_workflow_file`
- `workflow_parse_error`
- `unsupported_tracker_kind`
- `missing_tracker_api_key`
- `missing_tracker_project_slug`
- `literal_secret_in_config`

Checks:

- Confirm the `-workflow` path points to an existing `WORKFLOW.md`.
- Confirm the YAML front matter starts and ends with `---`.
- Confirm `tracker.kind` is `linear`.
- Confirm `LINEAR_API_KEY` is set when `tracker.api_key` is `$LINEAR_API_KEY`.
- Confirm `tracker.project_slug` is a real Linear project slug, not the example placeholder.
- Confirm every `api_key` and `auth_token` value starts with `$`. Literal credentials are rejected at config-load time.

## No Issues Are Dispatched

Checks:

- Confirm the target Linear project has issues in `tracker.active_states`.
- Confirm issue records have an ID, identifier, title, and state.
- Confirm issues in `Todo` are not blocked by non-terminal related issues.
- Confirm `agent.max_concurrent_agents` and any `agent.max_concurrent_agents_by_state` values leave an available slot.
- Call `POST /api/v1/refresh` or use the dashboard refresh button to trigger an immediate tick.

## Codex Does Not Start

Look for errors containing:

- `codex_not_found`
- `response_timeout`
- `port_exit`

Checks:

- Run the configured command manually with `--listen stdio://`.
- If `codex` is not on `PATH`, use a full executable path in `codex.command`.
- Confirm Codex authentication is configured before starting Simphony.
- On Windows, quote paths with spaces in `codex.command`.

## Workspace Is Empty

Simphony creates the workspace directory but does not clone a repository by default.

Add an `after_create` hook to clone or copy the repository Codex should edit:

```yaml
hooks:
  after_create: git clone https://github.com/example/repo.git .
```

If the hook fails, the attempt is retried. Check hook output in the Simphony process logs.

## Dashboard Cannot Reach The API

Checks:

- Confirm the Go server is running with `server.port` configured.
- In development, the Vite dashboard proxies `/api` to `http://localhost:8080`.
- If the backend uses a different port, update `dashboard/vite.config.ts`.
- For production serving, run `npm run build` in `dashboard/` so `dashboard/dist` exists.

## Agent Stops Before The Issue Is Done

Checks:

- Confirm the issue remains in one of `tracker.active_states`.
- Confirm the issue has not moved to a configured terminal state.
- Increase `agent.max_turns` if the agent reaches `max_turns_reached`.
- Increase `codex.turn_timeout_ms` or `codex.stall_timeout_ms` if long-running turns are expected.
