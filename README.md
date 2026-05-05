# Simphony

[![CI](https://github.com/kbsartain/simphony/actions/workflows/ci.yml/badge.svg)](https://github.com/kbsartain/simphony/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Simphony is a Go implementation of the [OpenAI Symphony specification](https://github.com/openai/symphony/blob/main/SPEC.md). It is a long-running automation service that watches an issue tracker, creates an isolated workspace for each eligible issue, runs Codex against that issue, and exposes runtime state through an optional HTTP API and React dashboard.

The project is currently focused on Linear as the tracker backend and Codex app-server as the agent runtime.

Simphony is intended for teams that want a configurable, unattended bridge between issue tracking and coding-agent execution. Treat `WORKFLOW.md` as local runtime configuration; the reusable starter templates live in [workflow examples](docs/workflow-examples.md).

## What It Does

- Polls Linear for issues in configured active states.
- Filters issues that are already running, already claimed, completed, blocked, or outside the active state set.
- Creates one filesystem workspace per issue.
- Runs Codex app-server over stdio from inside the issue workspace.
- Moves successful coding runs to review, then runs approved merge-stage issues through a separate merge prompt.
- Retries failed runs with exponential backoff.
- Reconciles running work against tracker state and removes workspaces for terminal issues.
- Hot-reloads `WORKFLOW.md` changes without restarting the process.
- Serves a JSON status API and optional dashboard.

## Repository Layout

```text
cmd/simphony/          CLI entry point
internal/agent/        Codex app-server stdio runner
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
- Codex CLI with app-server support.

Install Codex according to the official OpenAI instructions for your platform, then make sure the configured command can run `app-server --listen stdio://`.

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
- Set `codex.command` to `codex app-server` if `codex` is on `PATH`, or to the full executable path plus `app-server` if needed.
- Optionally set `codex.model` and `codex.model_provider` to force a specific Codex model selection.
- Add workspace hooks such as `hooks.after_create` to clone or otherwise prepare the repository Codex should edit.
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

## Pipeline States

Simphony treats normal coding work and approved merge work as separate pipeline stages:

- Coding issues come from `pipeline.coding_states` and move to `pipeline.review_state` after a successful Codex turn.
- Reviewed issues should be moved to `pipeline.merge_state`; Simphony dispatches them with merge-focused instructions.
- Successful merge-stage runs move to `pipeline.done_state` and the issue workspace is removed when that state is terminal.

The merge state is automatically included in tracker active states, and the done state is automatically included in terminal states.

## Codex Model Selection

Set `codex.model` to pass a model name to Codex app-server. Set `codex.model_provider` when your Codex installation needs an explicit provider name. These values are optional; when omitted, Codex uses its own configured defaults.

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

Simphony is an early implementation. The main backend paths are covered by tests, but tracker access and Codex execution require real credentials and local runtime configuration. Review command, sandbox, and hook settings before running it against a repository with write access.

Current limitations:

- Linear is the only tracker adapter.
- Runtime state is in memory only.
- Codex user-input requests are rejected because Simphony runs unattended.
- The dashboard is intentionally minimal and focused on operational status.
