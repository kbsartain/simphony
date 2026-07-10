# Environment And Secrets

Simphony expects credentials to come from the environment or from local agent configuration. Do not commit real API keys, project-specific secrets, or machine-specific executable paths.

The repository includes [`.env.example`](../.env.example) as a reference. Simphony does not automatically load dotenv files; provide these values through your shell, secret manager, or process supervisor.

## Required Runtime Values

| Name | Used by | Purpose |
| --- | --- | --- |
| `LINEAR_API_KEY` | Simphony tracker client | Authenticates Linear GraphQL requests. |
| `OPENAI_API_KEY` or local Codex auth | Codex app-server | Authenticates Codex, depending on your Codex installation. |
| `ANTHROPIC_API_KEY` | Claude Code Agent SDK | Authenticates Claude when `agent_runtime.provider: claude`. |
| `OPENAI_BASE_URL` | Codex/OpenAI-compatible runtime | Optional endpoint for OpenAI-compatible gateways. |
| `ANTHROPIC_BASE_URL` | Claude/Anthropic-compatible runtime | Optional endpoint for Anthropic-compatible gateways. |

`WORKFLOW.md` can reference environment variables by using `$NAME`:

```yaml
tracker:
  api_key: $LINEAR_API_KEY

agent_runtime:
  provider: claude
  api_key: $ANTHROPIC_API_KEY
  endpoint_url: $ANTHROPIC_BASE_URL
```

Only values that begin with `$` are resolved this way. Literal `api_key` and `auth_token` values are rejected; non-secret configuration strings are used literally.

## Setting Values Locally

PowerShell:

```powershell
$env:LINEAR_API_KEY = "replace-with-your-linear-api-key"
$env:OPENAI_API_KEY = "replace-with-your-openai-api-key"
$env:ANTHROPIC_API_KEY = "replace-with-your-anthropic-api-key"
```

POSIX shells:

```bash
export LINEAR_API_KEY="replace-with-your-linear-api-key"
export OPENAI_API_KEY="replace-with-your-openai-api-key"
export ANTHROPIC_API_KEY="replace-with-your-anthropic-api-key"
```

These commands set variables for the current shell session. Use your operating system's secret manager, shell profile, or process manager for longer-running deployments.

## What Not To Commit

Avoid committing:

- real Linear API keys,
- OpenAI API keys,
- Anthropic API keys,
- private repository clone tokens,
- local absolute paths to one developer's Codex binary,
- workflow files that point at production issue states without review.

Keep checked-in examples generic, using placeholders such as `your-linear-project-slug` and `codex app-server`.

## Hook Credentials

Lifecycle hooks inherit the Simphony process environment. If a hook clones a private repository, prefer credentials supplied by the environment or the host Git credential manager instead of embedding tokens in `WORKFLOW.md`.

## Project Preflight

Before dispatching work, Simphony runs a local environment preflight for each running project. Blocking failures prevent candidate issue fetch and dispatch, so tracker state is not changed when the local machine cannot safely prepare a workspace.

The preflight currently checks:

- default and per-stage agent runtime command availability,
- explicitly configured agent API-key and auth-token references resolve to non-empty values,
- workspace root configuration,
- Git repository readability for `git_worktree` projects,
- Windows-incompatible tracked paths when running on Windows.

The dashboard shows each project as `Ready`, `Warning`, or `Blocked` with the concrete finding. A blocked project is skipped until the configuration or host environment is corrected.

If `api_key` or `auth_token` is present in workflow configuration but its environment variable resolves empty, preflight blocks dispatch and identifies the affected stage. If neither field is configured, preflight permits dispatch because Codex or Claude may use an authenticated local SDK session. Simphony cannot verify that local session in advance; an authentication failure from the SDK follows normal runtime error and retry handling.

## Windows And WSL

Windows is supported for projects whose repositories can be checked out on Windows. Some repositories contain POSIX-only tracked paths, such as filenames with `?`, `*`, trailing periods, or reserved Windows device names. Windows cannot create those paths, so `git worktree add` fails before an agent can start.

For those projects, run the project runtime from WSL/Linux or rename/remove the incompatible tracked paths upstream. Simphony reports incompatible tracked paths as a blocking preflight failure with WSL/Linux as the recommended host workaround.
