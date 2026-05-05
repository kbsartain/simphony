# Workflow Examples

These examples show complete `WORKFLOW.md` shapes for common local setups. Replace project slugs, repository URLs, and commands before running them.

## Minimal Linear Poller

This example polls Linear and runs Codex in empty issue workspaces. It is useful for validating tracker and Codex connectivity before adding repository hooks.

```markdown
---
tracker:
  kind: linear
  api_key: $LINEAR_API_KEY
  project_slug: your-linear-project-slug
  active_states:
    - Todo
    - In Progress
workspace:
  root: ./simphony_workspaces
codex:
  command: codex app-server
server:
  port: 8080
---

You are working on {{ issue.identifier }}: {{ issue.title }}.

{{ issue.description }}
```

## Clone A Repository For Each Issue

This example clones a repository when an issue workspace is first created, checks the workspace before Codex runs, and prints status afterward.

```markdown
---
tracker:
  kind: linear
  api_key: $LINEAR_API_KEY
  project_slug: your-linear-project-slug
  active_states:
    - Todo
    - In Progress
workspace:
  root: ./simphony_workspaces
hooks:
  after_create: git clone https://github.com/example-org/example-repo.git .
  before_run: git status --short
  after_run: git status --short
  timeout_ms: 120000
agent:
  max_concurrent_agents: 2
  max_turns: 10
codex:
  command: codex app-server
  turn_timeout_ms: 3600000
  stall_timeout_ms: 300000
server:
  port: 8080
---

You are working on issue {{ issue.identifier }}: {{ issue.title }}.

Priority: {{ issue.priority }}
State: {{ issue.state }}
Labels: {{ issue.labels | join: ", " }}

Description:
{{ issue.description }}

Please implement the requested change in this repository. Keep changes scoped to the issue.
```

## Windows With A Full Codex Path

Use a full Codex executable path when `codex` is not on `PATH`.

```yaml
codex:
  command: C:\Path\To\codex.exe app-server
```

Hooks on Windows run through `cmd /C`, so use commands that work in `cmd.exe` or call PowerShell explicitly:

```yaml
hooks:
  before_run: powershell -NoProfile -Command "git status --short"
```

## Safer First Run

For a first run against a real Linear project:

- use a test project or non-production issue state,
- set `agent.max_concurrent_agents` to `1`,
- set `workspace.root` to a disposable directory,
- use a repository clone URL that points to a sandbox or fork,
- keep `codex.turn_sandbox_policy` restrictive until the workflow is proven.
