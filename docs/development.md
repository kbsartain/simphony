# Development

## Backend

Run tests from the repository root:

```bash
go test ./...
```

Build the CLI:

```bash
go build ./cmd/simphony
```

On Windows, prefer the verified production build script:

```powershell
.\build.ps1
```

It runs vet and tests, builds the root `simphony.exe` explicitly, and verifies that the executable timestamp changed. This prevents a successful package build from leaving an older production executable in place.

Install the CLI from the public module path:

```bash
go install github.com/kbsartain/simphony/cmd/simphony@latest
```

Run with a workflow file:

```bash
go run ./cmd/simphony -workflow ./WORKFLOW.md
```

Use `gofmt` on changed Go files before committing. The backend test suite also validates local Markdown links, fenced JSON and YAML examples, public-doc placeholder hygiene, the checked-in `WORKFLOW.md`, and the fenced workflow examples in `docs/workflow-examples.md`.

## Dashboard

Install dependencies once:

```bash
cd dashboard
npm ci
```

Run the development server:

```bash
npm run dev
```

Build production assets:

```bash
npm run build
```

The production build writes to `dashboard/dist`. When that directory exists, the Go server serves it from `/`.

## GitHub Automation

The GitHub Actions workflow in `.github/workflows/ci.yml` runs backend tests, vet, whitespace checks, and the dashboard production build on pushes and pull requests.

Dependabot is configured in `.github/dependabot.yml` for Go modules, dashboard npm dependencies, and GitHub Actions updates.

## Local Runtime Checklist

Before running Simphony against real issues:

- Set `LINEAR_API_KEY`.
- Confirm `tracker.project_slug` points to the intended Linear project.
- Confirm `tracker.active_states` only includes states Simphony should work on.
- Confirm `workspace.root` points to a safe location.
- Confirm hooks clone or prepare the correct repository.
- Confirm `codex.command` works from a terminal.
- Review Codex sandbox and approval settings.

## Windows Notes

This repository is developed on Windows and aims to keep path handling portable.

- Go path operations should use `filepath`.
- Hooks run with `cmd /C` on Windows and `bash -lc` on POSIX.
- If the `codex` command is not on `PATH`, configure `codex.command` with the full executable path followed by `app-server`.
- PowerShell examples use `$env:NAME = "value"` to set environment variables for the current session.
- `.gitattributes` normalizes source and documentation files to LF line endings so Windows and POSIX contributors produce consistent diffs.
- `.editorconfig` documents editor defaults for indentation, final newlines, charset, and trailing whitespace.

## Testing Scope

The current test suite covers workflow parsing and config resolution, prompt rendering, Linear response normalization, orchestrator scheduling behavior, workspace safety, agent protocol behavior through fakes where practical, local Markdown link checks, fenced JSON and YAML example validation, public-doc placeholder checks, and parse/render checks for public workflow documentation examples.

End-to-end runs require external services and local credentials, so they should be validated manually against a test Linear project before using production issue states.
