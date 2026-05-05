# Security Policy

Simphony runs local commands, lifecycle hooks, and Codex app-server processes inside issue workspaces. Treat workflow files, hooks, tracker credentials, repository credentials, and Codex authentication as sensitive runtime configuration.

## Reporting A Vulnerability

Please do not include exploit details, credentials, tokens, private repository URLs, or sensitive logs in a public issue.

If GitHub private vulnerability reporting is enabled for this repository, use that flow. Otherwise, contact the maintainers through the project's preferred private channel. If no private channel is available, open a minimal public issue that states there is a security concern and wait for a maintainer to establish a private disclosure path.

## Runtime Security Guidance

- Do not commit real Linear, OpenAI, GitHub, or private repository tokens.
- Keep `WORKFLOW.md` examples generic in public branches.
- Review `hooks.after_create`, `hooks.before_run`, `hooks.after_run`, and `hooks.before_remove` before running Simphony against real repositories.
- Use a disposable `workspace.root` for first runs and test projects.
- Keep Codex sandbox and approval settings as restrictive as your workflow allows.
- Avoid embedding credentials in hook commands; prefer environment variables, a local credential manager, or your process supervisor's secret handling.
- Review generated changes before merging agent output into protected branches.

## Supported Versions

Simphony is early-stage software. Unless maintainers publish a version support matrix, security fixes are expected to target the default branch.
