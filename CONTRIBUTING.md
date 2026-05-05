# Contributing

Thanks for working on Simphony. This project is still early, so changes should stay close to the Symphony specification and the existing package boundaries.

By participating, contributors are expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Local Setup

Install backend dependencies and run tests from the repository root:

```bash
go mod download
go test ./...
```

Install dashboard dependencies and run the production build:

```bash
cd dashboard
npm ci
npm run build
```

## Development Guidelines

- Keep Go code formatted with `gofmt`.
- Prefer table-driven tests for logic-heavy Go changes.
- Preserve context cancellation paths in scheduler, tracker, workspace, and agent code.
- Keep public API types in `pkg/api` aligned with dashboard types in `dashboard/src/api/types.ts`.
- Avoid adding persistent storage unless the architecture and specification requirements are updated.
- Keep `WORKFLOW.md` examples free of machine-specific paths, project IDs, and credentials.
- Keep local Markdown links valid, fenced JSON and YAML examples parseable, public docs free of stale/internal placeholders, and fenced `WORKFLOW.md` examples parseable; `go test ./...` validates them.
- Follow [SECURITY.md](SECURITY.md) for vulnerability reporting and avoid posting credentials, private repository URLs, or sensitive logs in public issues or pull requests.

## Before Opening A Pull Request

Run:

```bash
go test ./...
go vet ./...
cd dashboard
npm run build
```

For changes that affect runtime behavior, also test against a non-production Linear project and a disposable workspace root.

The GitHub Actions workflow in `.github/workflows/ci.yml` runs the same backend and dashboard checks for pushes and pull requests.
