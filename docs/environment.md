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

Only values that begin with `$` are resolved this way. Other strings are used literally.

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
