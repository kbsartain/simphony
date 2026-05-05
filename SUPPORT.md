# Support

Simphony is early-stage software. The fastest way to get useful help is to include enough runtime context while keeping credentials and private repository details out of public posts.

## Before Opening An Issue

- Read the [README](README.md) for the project overview and quick start.
- Check the [documentation index](docs/README.md) for configuration, Linear setup, operations, and troubleshooting guides.
- Review [`.env.example`](.env.example) and confirm required environment variables are set in your shell or process supervisor.
- Run `go test ./...` and, for dashboard issues, `cd dashboard && npm run build`.

## Asking For Help

For bugs, use the bug report template and include:

- operating system,
- Go version,
- Node and npm versions for dashboard problems,
- relevant `WORKFLOW.md` sections with secrets removed,
- the smallest useful log excerpt,
- steps to reproduce the behavior.

For feature requests, describe the workflow problem first, then the desired behavior and configuration shape.

## Security Issues

Do not report vulnerabilities, tokens, private repository URLs, or sensitive logs in public issues. Follow [SECURITY.md](SECURITY.md) for private disclosure guidance.
