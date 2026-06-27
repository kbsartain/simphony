# Simphony

[![CI](https://github.com/kbsartain/simphony/actions/workflows/ci.yml/badge.svg)](https://github.com/kbsartain/simphony/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Simphony is a Go implementation of the [OpenAI Symphony specification](https://github.com/openai/symphony/blob/main/SPEC.md). It is a long-running automation service that watches an issue tracker, creates an isolated workspace for each eligible issue, runs a coding agent against that issue, and exposes runtime state through an optional HTTP API and React dashboard.

The project is currently focused on Linear as the tracker backend. Codex app-server remains the default agent runtime, but workflows can also select the Claude Code Agent SDK shim or configure OpenAI-compatible and Anthropic-compatible endpoints through provider-neutral `agent_runtime` settings.

Simphony can run in the original single-project mode from one `WORKFLOW.md`, or in multi-project mode from a `simphony.yaml` registry. Multi-project mode starts isolated per-project runtimes under one supervisor, with shared dashboard navigation, registry setup, and optional global concurrency limits.

Simphony is intended for teams that want a configurable, unattended bridge between issue tracking and coding-agent execution. Treat `WORKFLOW.md` as local runtime configuration; the reusable starter templates live in [workflow examples](docs/workflow-examples.md).

## What It Does

- Polls Linear for issues in configured active states.
- Filters issues that are already running, already claimed, completed, blocked, or outside the active state set.
- Creates one filesystem workspace per issue.
- Runs the selected coding agent from inside the issue workspace.
- Moves successful coding runs to review, then runs approved merge-stage issues through a separate merge prompt.
- Retries failed runs with exponential backoff.
- Reconciles running work against tracker state and removes workspaces for terminal issues.
- Hot-reloads `WORKFLOW.md` changes without restarting the process.
- Serves a JSON status API and optional dashboard.
- Can supervise multiple projects from one `simphony.yaml`, keeping each project's workflow, Linear settings, workspaces, retry state, hooks, and agent environment isolated.

## Repository Layout

```text
cmd/simphony/          CLI entry point
internal/agent/        Agent runtime runners and shims
internal/config/       WORKFLOW.md loading, defaults, validation, hot reload
internal/orchestrator/ Scheduler, dispatch, retry, reconciliation, state snapshots
internal/prompt/       Liquid-style issue prompt rendering
internal/server/       Optional HTTP API and dashboard serving
internal/tracker/      Linear GraphQL tracker adapter
internal/workspace/    Workspace creation, cleanup, and lifecycle hooks
pkg/api/               Public domain types, tracker interface, and error codes
dashboard/             React/Vite dashboard
codex-schema/          Codex app-server JSON protocol schemas
docs/                  Project documentation
```

## Requirements

- Go 1.25 or newer.
- Node.js and npm for the dashboard.
- A Linear API key for tracker access.
- Codex CLI with app-server support, or Node.js with the Claude Code Agent SDK for `agent_runtime.provider: claude`.

Install Codex according to the official OpenAI instructions for your platform, then make sure the configured command can run `app-server --listen stdio://`. For Claude, install a supported Claude Agent SDK package where Node can resolve it, or configure `claude.command` to call your own wrapper.

See [.env.example](.env.example) for the environment variables Simphony commonly needs. The file is a reference only; Simphony does not load dotenv files automatically.

## Quick Start

Install the CLI directly from GitHub:

```bash
go install github.com/kbsartain/simphony/cmd/simphony@latest
```

Or build from a checkout:

```bash
go mod download
go build ./cmd/simphony
```

1. Set the tracker token:

```powershell
$env:LINEAR_API_KEY = "replace-with-your-linear-api-key"
```

2. Edit `WORKFLOW.md`:

- Set `tracker.project_slug` to your Linear project slug.
- Use the default `agent_runtime.provider: codex`, or set `agent_runtime.provider: claude` to use the Claude Code Agent SDK shim.
- Set `codex.command` to `codex app-server` if `codex` is on `PATH`, or configure `claude.command` if you use a custom Claude wrapper.
- Optionally set `agent_runtime.model`, `agent_runtime.endpoint_url`, and `agent_runtime.api_key` to select a model or compatible gateway.
- Add workspace hooks such as `hooks.after_create` to clone or otherwise prepare the repository the agent should edit.
- Adjust `tracker.active_states`, `pipeline.review_state`, `pipeline.merge_state`, workspace hooks, and concurrency limits for your workflow.

3. Run:

If installed with `go install` and your Go binary directory is on `PATH`:

```bash
simphony -workflow ./WORKFLOW.md
```

From a local build on Windows:

```powershell
.\simphony.exe -workflow .\WORKFLOW.md
```

From a local build on macOS or Linux:

```bash
./simphony -workflow ./WORKFLOW.md
```

If `server.port` is configured, the API listens on `http://localhost:<port>`.

## Multi-Project Mode

Use single-project mode when one Simphony process should manage one workflow:

```bash
simphony -workflow ./WORKFLOW.md
```

Use multi-project mode when one Simphony supervisor should manage multiple project runtimes:

```bash
simphony -config ./simphony.yaml
```

A registry lists project workflow files and optional global defaults:

```yaml
server:
  bind_address: 127.0.0.1
  port: 8080
  dashboard_enabled: true

concurrency:
  max_concurrent_agents: 10

agent_runtime:
  provider: codex
  model_provider: moonshot
  model: gpt-5.4
  endpoint_url: $OPENAI_BASE_URL
  api_key: $OPENAI_API_KEY

projects:
  - id: alpha
    name: Alpha
    workflow_path: projects/alpha/WORKFLOW.md
    max_concurrent_agents: 3
  - id: beta
    name: Beta
    workflow_path: projects/beta/WORKFLOW.md
```

The dashboard Project Setup page can create a starter registry from a single `WORKFLOW.md`, add/edit/remove registry project entries, and edit safe registry-level defaults such as server binding, global/project concurrency caps, isolation guardrails, and global agent runtime defaults. Registry changes are persisted to `simphony.yaml`; restarting with `-config` applies them to running project runtimes. Existing runtime secrets are preserved unless a replacement API key or auth token is explicitly entered.

Each running project performs a local preflight before dispatch. If the agent command, workspace configuration, Git repository, or host filesystem is not ready, Simphony marks the project blocked in the dashboard and skips issue claiming until the problem is corrected. On Windows, repositories with POSIX-only tracked paths should be run from WSL/Linux or cleaned upstream before using `git_worktree` mode.

Useful registry commands:

```bash
simphony validate -config ./simphony.yaml
simphony projects -config ./simphony.yaml
simphony -config ./simphony.yaml -project alpha
```

See [multi-instance and multi-project operation](docs/multi-instance.md), [configuration](docs/configuration.md), and the [two-project registry example](docs/examples/two-projects.simphony.yaml).

## Pipeline States

Simphony treats normal coding work and approved merge work as separate pipeline stages:

- Coding issues come from `pipeline.coding_states` and move to `pipeline.review_state` after a successful agent turn.
- If `review_resolution.enabled` is true, reviewed issues move through `pipeline.review_resolution_state`, where an autonomous agent resolves PR review comments, CI failures, and approval readiness before moving to `pipeline.merge_state`.
- Reviewed or review-resolved issues should be moved to `pipeline.merge_state`; Simphony dispatches them with merge-focused instructions.
- Successful merge-stage runs move to `pipeline.done_state` and the issue workspace is removed when that state is terminal.

The merge state is automatically included in tracker active states, the review-resolution state is included when enabled, and the done state is automatically included in terminal states.

## Agent Runtime Selection

Existing workflows can continue using only `codex:`. To switch providers explicitly, add `agent_runtime.provider`:

```yaml
agent_runtime:
  provider: codex
  model: gpt-5.4
```

```yaml
agent_runtime:
  provider: claude
  model: claude-sonnet-4
  api_key: $ANTHROPIC_API_KEY
```

`agent_runtime.endpoint_url` and `agent_runtime.api_key` support OpenAI-compatible and Anthropic-compatible gateways. For Codex, they are passed as `OPENAI_BASE_URL` and `OPENAI_API_KEY`; for Claude, they are passed as `ANTHROPIC_BASE_URL` and `ANTHROPIC_API_KEY`.

Global `agent_runtime` defaults can also live in `simphony.yaml`; each project `WORKFLOW.md` can override individual runtime fields when a project needs a different SDK, model, endpoint, or token source. In multi-project mode, the Project Setup page can edit these global defaults while masking/preserving existing secrets.

## Codex Model Selection

Set `codex.model` to pass a model name to Codex app-server. Set `codex.model_provider` when your Codex installation needs an explicit provider name. These values are optional; when omitted, Codex uses its own configured defaults.

Set `codex.reasoning_effort` to control per-turn reasoning globally. Use `codex.stage_overrides` to change model, provider, or reasoning by pipeline stage:

```yaml
codex:
  model: gpt-5.4
  reasoning_effort: high
  skills:
    - architecture-standards
  stage_overrides:
    coding:
      reasoning_effort: medium
      skills:
        - conjit-product-ui
    review:
      model: gpt-5.5
      reasoning_effort: xhigh
      skills:
        - code-review
        - security-review
    review_resolution:
      model: gpt-5.5
      reasoning_effort: xhigh
      skills:
        - github:gh-address-comments
        - github:gh-fix-ci
    merge:
      reasoning_effort: high
```

The model and provider fields are free-form strings. That lets a workflow select configured alternatives such as Claude, Gemini, Kimi, GLM, or DeepSeek through whatever provider/router your Codex installation supports.

`codex.skills` and `codex.stage_overrides.<stage>.skills` let you attach default skills by stage. Skill names are resolved through Codex at runtime when possible; use `{ name, path }` entries to pin a specific local skill file.

Enable autonomous PR review handling with:

```yaml
pipeline:
  review_resolution_state: Review Resolution

review_resolution:
  enabled: true
  max_attempts: 3
  require_checks_green: true
  require_code_review_approval: true
  unresolved_comment_policy: fix_or_explain
```

The review-resolution agent must finish with `SIMPHONY_REVIEW_DECISION: approved`, `retry`, or `escalate`. Approved advances to the merge state, retry schedules another autonomous pass, and escalate moves the issue to `review_resolution.escalation_state`.

## Dashboard

The dashboard is optional. During development, run Vite from the dashboard directory:

```bash
cd dashboard
npm ci
npm run dev
```

The Vite dev server runs on port 3000 and proxies `/api` requests to `http://localhost:8080`. If you change `server.port`, update `dashboard/vite.config.ts` for local dashboard development.

For production serving from the Go process:

```bash
cd dashboard
npm run build
cd ..
go run ./cmd/simphony -workflow ./WORKFLOW.md
```

When `dashboard/dist` exists, Simphony serves it from `/`.

## Documentation

- [Documentation index](docs/README.md) lists all guides.
- [Configuration](docs/configuration.md) explains `WORKFLOW.md`, defaults, and prompt templates.
- [Multi-instance and multi-project operation](docs/multi-instance.md) explains separate-process operation, `simphony.yaml`, and supervisor isolation.
- [Environment and secrets](docs/environment.md) explains required environment variables and safe credential handling.
- [Workflow examples](docs/workflow-examples.md) provides complete starter workflows.
- [Linear setup](docs/linear.md) explains project slugs, issue selection, terminal states, and blocker handling.
- [Architecture](docs/architecture.md) describes the scheduler, tracker, workspace, agent, and server flow.
- [HTTP API](docs/api.md) documents the status endpoints.
- [Operations](docs/operations.md) covers startup, logs, refreshes, hot reload, shutdown, and retries.
- [Development](docs/development.md) covers testing, dashboard builds, and Windows notes.
- [Troubleshooting](docs/troubleshooting.md) maps common runtime failures to likely causes.
- [Publication checklist](docs/publication.md) covers public GitHub repository readiness.
- [Dashboard](dashboard/README.md) documents the optional React/Vite UI.
- [Codex setup](CODEX_SETUP.md) summarizes app-server expectations and schema generation.
- [Contributing](CONTRIBUTING.md) covers local development expectations.
- [Code of conduct](CODE_OF_CONDUCT.md) sets expectations for project participation.
- [Security policy](SECURITY.md) covers vulnerability reporting and runtime security guidance.
- [Support](SUPPORT.md) explains what context to include when asking for help.

## Testing

Run backend tests:

```bash
go test ./...
go vet ./...
```

Run the dashboard typecheck and production build:

```bash
cd dashboard
npm run build
```

The GitHub Actions workflow in `.github/workflows/ci.yml` runs these checks on pushes and pull requests.

## License

Simphony is released under the [MIT License](LICENSE).

## Project Status

Simphony is an early implementation. The main backend paths are covered by tests, but tracker access and agent execution require real credentials and local runtime configuration. Review command, sandbox, and hook settings before running it against a repository with write access.

Current limitations:

- Linear is the only tracker adapter.
- Runtime state is in memory only.
- Agent user-input requests are rejected because Simphony runs unattended.
- Registry edits in the dashboard require a restart before they affect running project runtimes.
- The dashboard is intentionally minimal and focused on operational status.
