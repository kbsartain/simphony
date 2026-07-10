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

## A Stage Uses The Wrong SDK Or Cannot Authenticate

Checks:

- Distinguish `stage_overrides.<stage>.provider` (`codex` or `claude`) from `model_provider` (`openai`, `anthropic`, or a router label).
- Confirm the stage is one of `coding`, `review`, `review_resolution`, or `merge`.
- Check the running snapshot or dispatch log for `stage`, `execution_provider`, `model_provider`, and `model`.
- If preflight reports `agent_api_key_unresolved` or `agent_auth_token_unresolved`, set the referenced environment variable or remove the field to intentionally use an authenticated local SDK session.
- When switching SDKs, configure stage-specific endpoints and credentials. Simphony deliberately does not carry Codex credentials, commands, environment, or sandbox settings into Claude, or vice versa.
- Pause the affected stage before changing settings. Save the workflow, confirm hot reload succeeds, then resume the stage; an in-flight agent keeps its original runtime.

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

## Hook Failure Output Omits The Useful Error

Hook subprocess output is retained as a bounded diagnostic: the first 8 KiB, the final 24 KiB, and a truncation marker containing the original byte count. ANSI terminal controls are stripped. This keeps startup context and the final test summary without allowing verbose commands to create unbounded retry errors, API payloads, or tracker comments.

If the failure still lacks a useful tail, confirm the verification tool writes its final summary before exiting and does not redirect it to a separate file. For very large machine-readable reports, configure the hook to print a concise failure summary and store the full artifact inside the issue workspace.

## GitHub Reports No Checks Immediately After PR Creation

The GitHub PR gate treats `gh pr checks` reporting “no checks reported” as a registration race, not an immediate failed check. It probes during `github.checks_registration_grace_ms`, then starts the normal check watcher once any check appears.

- `checks_not_registered` means no check appeared before the registration grace period expired. Confirm the workflow has a `pull_request` trigger and that the branch/path filters include this PR.
- `checks_failed` means checks registered and at least one failed. Inspect the named GitHub check and its logs.
- `checks_timeout` means the overall `github.checks_timeout_ms` elapsed during registration or while registered checks were running.

The registration grace is bounded by the overall timeout. Increasing it can help slow GitHub workflow registration, but it will not hide a missing or incorrectly filtered workflow indefinitely.
