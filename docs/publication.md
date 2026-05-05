# Publication Checklist

Use this checklist before publishing Simphony as a public GitHub repository or inviting outside contributors.

## Repository Description

Suggested GitHub repository description:

> Long-running Go orchestrator that turns Linear issues into isolated Codex workspaces with a status API and dashboard.

Suggested repository topics:

- `go`
- `codex`
- `linear`
- `automation`
- `orchestrator`
- `coding-agent`
- `developer-tools`

## Required Review

- Confirm the repository license is still appropriate for the intended publication model. Simphony currently includes the MIT License.
- Confirm the `go.mod` module path matches the public GitHub repository URL.
- Confirm `WORKFLOW.md` contains placeholders, not a production Linear project slug or machine-specific Codex path.
- Confirm `.env.example` contains placeholder values only.
- Confirm lifecycle hook examples do not include private repository tokens or local absolute paths.
- Confirm generated assets, local workspaces, dependency directories, and caches are ignored.
- Confirm `.github/dependabot.yml` covers the dependency ecosystems maintainers want Dependabot to update.
- Run `go test ./...` to validate backend behavior, local Markdown links, fenced JSON and YAML examples, public-doc placeholder hygiene, the checked-in workflow, and documented workflow examples.
- Confirm [SECURITY.md](../SECURITY.md), [SUPPORT.md](../SUPPORT.md), [CONTRIBUTING.md](../CONTRIBUTING.md), and [CODE_OF_CONDUCT.md](../CODE_OF_CONDUCT.md) match the maintainers' preferred process.

## Public Runtime Guidance

The checked-in workflow is a starter template, not a production deployment file. Public examples should keep:

- `tracker.project_slug` generic,
- credentials referenced through environment variables,
- `codex.command` portable unless a platform-specific example is clearly labeled,
- `workspace.root` disposable,
- hooks free of credentials and organization-specific paths.
