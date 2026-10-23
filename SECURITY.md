# Security Policy

Simphony runs local commands, lifecycle hooks, and Codex app-server processes inside issue workspaces. Treat workflow files, hooks, tracker credentials, repository credentials, and Codex authentication as sensitive runtime configuration.

## Reporting A Vulnerability

Please do not include exploit details, credentials, tokens, private repository URLs, or sensitive logs in a public issue.

If GitHub private vulnerability reporting is enabled for this repository, use that flow. Otherwise, contact the maintainers through the project's preferred private channel. If no private channel is available, open a minimal public issue that states there is a security concern and wait for a maintainer to establish a private disclosure path.

## Runtime Security Guidance

- Set `SIMPHONY_OPERATOR_TOKEN` to a randomly generated value of at least 32 characters before enabling the HTTP server. Every dashboard and API request is authenticated.
- Keep the Simphony listener on `127.0.0.1` or `::1`. For remote use, put an authenticated TLS reverse proxy in front of the loopback listener; direct wildcard/LAN binds are rejected.
- Cross-origin access is disabled by default. Add only exact scheme/host/port values to `SIMPHONY_TRUSTED_ORIGINS` when a separate trusted UI origin is required.
- Do not commit real Linear, OpenAI, GitHub, or private repository tokens.
- Keep `WORKFLOW.md` examples generic in public branches.
- Review `hooks.after_create`, `hooks.before_run`, `hooks.after_run`, and `hooks.before_remove` before running Simphony against real repositories.
- Use a disposable `workspace.root` for first runs and test projects.
- Keep the default `workspace-write` sandbox. `allow_unsafe_host_access: true` is an explicit high-risk exception, not a compatibility default.
- Require the structured `simphony:approved` tracker label (or a configured `tracker.trusted_labels` value) before dispatching an issue.
- Run Simphony as a dedicated non-administrator account with repository- and provider-scoped credentials and outbound firewall rules.
- Avoid embedding credentials in hook commands; prefer environment variables, a local credential manager, or your process supervisor's secret handling.
- Review generated changes before merging agent output into protected branches.

## Supported Versions

Simphony is early-stage software. Unless maintainers publish a version support matrix, security fixes target the default branch. Release binaries require Go 1.26.10 or a newer supported patched toolchain and must pass `govulncheck ./...`.
