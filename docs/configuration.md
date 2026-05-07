# Configuration

Simphony is configured by `WORKFLOW.md`. The file has YAML front matter for runtime settings and a Markdown prompt template body for Codex.

```markdown
---
tracker:
  kind: linear
  api_key: $LINEAR_API_KEY
  project_slug: your-linear-project-slug
---

You are working on issue {{ issue.identifier }}: {{ issue.title }}.
```

Unknown front matter keys are ignored so workflow files can evolve without breaking older versions of Simphony.

## Tracker

```yaml
tracker:
  kind: linear
  endpoint: https://api.linear.app/graphql
  api_key: $LINEAR_API_KEY
  project_slug: your-linear-project-slug
  active_states:
    - Todo
    - In Progress
    - Approved
  completion_states:
    - In Review
    - Done
  terminal_states:
    - Done
    - Canceled
```

- `kind` is required and currently supports `linear`.
- `api_key` is required. Values beginning with `$` are resolved from the environment.
- `project_slug` is required for Linear.
- `endpoint` defaults to Linear's GraphQL endpoint.
- `active_states` defaults to `Todo` and `In Progress`; the configured pipeline merge state is added automatically.
- `completion_states` controls the ordered target states for successful coding runs and defaults to review-oriented states plus terminal states.
- `terminal_states` defaults to `Closed`, `Cancelled`, `Canceled`, `Duplicate`, and `Done`.

See [Linear setup](linear.md) for project slug selection, issue filtering, and blocker behavior.

## Pipeline

```yaml
pipeline:
  review_state: In Review
  merge_state: Approved
  done_state: Done
  coding_states:
    - Todo
    - In Progress
```

Successful coding-stage runs move to `review_state`. Issues moved to `merge_state` are dispatched with merge-focused instructions, and successful merge-stage runs move to `done_state`.

If `coding_states` is omitted, Simphony uses the tracker active states except the review and merge states.

## Polling

```yaml
polling:
  interval_ms: 30000
```

`interval_ms` controls the scheduler tick interval and defaults to 30000.

## Workspace

```yaml
workspace:
  root: ./simphony_workspaces
```

`root` is where per-issue workspaces are created. Relative paths are resolved from the directory containing `WORKFLOW.md`. Issue identifiers are sanitized before being used as directory names.

If omitted, `root` defaults to a `symphony_workspaces` directory under the operating system temp directory. The checked-in `WORKFLOW.md` sets `./simphony_workspaces` explicitly so local runs stay under the project directory. `~` is expanded to the current user's home directory, and environment variables in the path are expanded before relative paths are resolved.

## Hooks

```yaml
hooks:
  after_create: git clone https://github.com/example/repo.git .
  before_run: go test ./...
  after_run: git status --short
  before_remove: git status --short
  timeout_ms: 60000
```

Hooks run with the workspace as the current directory. `after_create` and `before_run` failures stop the attempt and schedule a retry. `after_run` and `before_remove` failures are logged and ignored.

In most workflows, `after_create` should clone the repository or copy a prepared checkout into the empty issue workspace before Codex starts.

On Windows, hooks run through `cmd /C`. On POSIX systems, hooks run through `bash -lc`.

## Agent

```yaml
agent:
  max_concurrent_agents: 10
  max_turns: 20
  max_retry_backoff_ms: 300000
  max_concurrent_agents_by_state:
    In Progress: 2
```

- `max_concurrent_agents` limits total running Codex sessions.
- `max_turns` limits continuation turns on a single Codex thread.
- `max_retry_backoff_ms` caps exponential retry delays.
- `max_concurrent_agents_by_state` optionally limits concurrency for specific tracker states.

## Codex

```yaml
codex:
  command: codex app-server
  model: gpt-5.4
  model_provider: openai
  reasoning_effort: high
  skills:
    - architecture-standards
    - name: repo-design-system
      path: /absolute/path/to/repo-design-system/SKILL.md
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
    merge:
      reasoning_effort: high
  approval_policy: auto
  thread_sandbox: none
  turn_sandbox_policy: none
  turn_timeout_ms: 3600000
  read_timeout_ms: 5000
  stall_timeout_ms: 300000
```

The runner appends `--listen stdio://` when it is not already present. The subprocess is launched with the issue workspace as its current directory.

`model` and `model_provider` are optional. When present, they are passed to Codex app-server for thread and turn startup. Simphony treats these as provider-neutral strings, so non-OpenAI model IDs such as Claude, Gemini, Kimi, GLM, or DeepSeek variants can be configured when the underlying Codex installation has an appropriate provider/router configured.

`reasoning_effort` is optional and is passed to Codex as the per-turn `effort` override. Accepted values are `none`, `minimal`, `low`, `medium`, `high`, and `xhigh`; `x-high` and `x_high` are normalized to `xhigh`.

`stage_overrides` can override `model`, `model_provider`, and `reasoning_effort` for a pipeline stage. Known stage keys are `coding`, `review`, and `merge`; unknown stage keys are accepted for forward compatibility and ignored until a matching stage exists.

`skills` selects default Codex skills for every stage. `stage_overrides.<stage>.skills` adds stage-specific skills. Skill entries can be simple names, which Simphony resolves through Codex `skills/list` at runtime, or objects with `name` and `path` when you want to pin a specific local `SKILL.md`. Resolved skills are sent as Codex skill input items on each turn; unresolved names are included in the prompt as visible guidance.

By default, `pipeline.review_state` is a handoff state. To make `In Review` an agent-run internal review stage, include the review state in `tracker.active_states`, then configure `codex.stage_overrides.review.reasoning_effort: xhigh` or a review-specific model.

`approval_policy: auto` is mapped to Codex protocol value `never`.

`stall_timeout_ms` defaults to 300000. Set it to `0` to disable orchestrator-level stall detection.

Supported `turn_sandbox_policy` values are:

- `none`
- `read-only`
- `workspace-write`
- `danger-full-access`

## Server

```yaml
server:
  port: 8080
```

When `server.port` is set, Simphony starts the HTTP API. If `dashboard/dist` exists, the same server also serves the dashboard from `/`.

## Prompt Template

The Markdown body of `WORKFLOW.md` is rendered for the first Codex turn. It can reference normalized issue fields:

- `issue.id`
- `issue.identifier`
- `issue.title`
- `issue.description`
- `issue.priority`
- `issue.state`
- `issue.branch_name`
- `issue.url`
- `issue.labels`
- `issue.blocked_by`
- `issue.created_at`
- `issue.updated_at`

Simphony uses strict prompt rendering. Template errors stop the attempt and are reported with the `template_render_error` category.
