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
  reasoning_effort: medium
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
  reasoning_effort: high
  skills:
    - architecture-standards
  stage_overrides:
    coding:
      skills:
        - conjit-product-ui
    review:
      reasoning_effort: xhigh
      skills:
        - code-review
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

## Claude Code Agent SDK

This example switches the coding agent from Codex app-server to the Claude Code Agent SDK shim. Install the Claude SDK package where Node can resolve it, or provide your own `claude.command` wrapper.

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
agent:
  max_concurrent_agents: 2
  max_turns: 10
agent_runtime:
  provider: claude
  model: claude-sonnet-4
  api_key: $ANTHROPIC_API_KEY
claude:
  permission_mode: acceptEdits
  allowed_tools:
    - Read
    - Edit
    - Write
    - Bash
    - Glob
    - Grep
  setting_sources:
    - project
    - local
server:
  port: 8080
---

You are working on issue {{ issue.identifier }}: {{ issue.title }}.

{{ issue.description }}
```

## Compatible Model Gateways

Use `agent_runtime.endpoint_url` and `agent_runtime.api_key` when your Codex or Claude setup should talk to an OpenAI-compatible or Anthropic-compatible gateway.

```yaml
agent_runtime:
  provider: codex
  model: qwen-coder
  endpoint_url: https://openai-compatible.example/v1
  api_key: $ROUTER_API_KEY
```

```yaml
agent_runtime:
  provider: claude
  model: claude-sonnet-4
  endpoint_url: https://anthropic-compatible.example
  api_key: $ANTHROPIC_COMPATIBLE_API_KEY
```

## Safer First Run

For a first run against a real Linear project:

- use a test project or non-production issue state,
- set `agent.max_concurrent_agents` to `1`,
- set `workspace.root` to a disposable directory,
- use a repository clone URL that points to a sandbox or fork,
- keep the selected agent's permissions restrictive until the workflow is proven.
