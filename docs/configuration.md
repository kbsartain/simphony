# Configuration

Simphony is configured by `WORKFLOW.md`. The file has YAML front matter for runtime settings and a Markdown prompt template body for the selected coding agent.

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

## Multi-Project Registry

Single-project mode starts one active project with `-workflow`. Multi-project mode starts enabled project runtimes from a global registry with `-config`:

```yaml
agent_runtime:
  provider: codex
  model: kimi-k2
  model_provider: moonshot
  endpoint_url: $OPENAI_BASE_URL
  api_key: $OPENAI_API_KEY
server:
  bind_address: 127.0.0.1
  port: 8080
concurrency:
  max_concurrent_agents: 10
  default_project_max_concurrent_agents: 5
security:
  allow_workspace_overlap: false
  allow_workspace_under_registry_dir: false
  allow_remote_dashboard: false
projects:
  - id: alpha
    name: Alpha
    workflow_path: ./projects/alpha/WORKFLOW.md
  - id: beta
    name: Beta
    workflow_path: ./projects/beta/WORKFLOW.md
    max_concurrent_agents: 2
```

`projects[].workflow_path` is resolved relative to the registry file. Project IDs must start with a letter or number and can contain letters, numbers, dots, underscores, and hyphens. Global `agent_runtime` values are applied as defaults when validating project workflows; a project's own `agent_runtime` fields override individual global fields.

Use `simphony validate -config ./simphony.yaml` to validate the registry and enabled project workflows without starting workers. Use `simphony projects -config ./simphony.yaml` to list resolved project settings. A registry-level `server` block enables the aggregate project API and dashboard project selector.

The dashboard Project Setup page can persist registry edits back to `simphony.yaml`. It supports project add/edit/remove, an explicit **Start paused** policy, server defaults, global and per-project concurrency caps, isolation guardrails, and global `agent_runtime` defaults. Project cards show whether restart will begin paused or actively dispatch the backlog. These registry edits are restart-required for running workers. Agent runtime API key and auth-token fields are write-only in the UI: leaving them blank preserves the existing registry value, while entering a value replaces it.

Set `start_paused: true` on a project registry entry when Simphony should start the runtime, continue polling and reconciliation, but dispatch no new work until an operator resumes the project. The startup pause is applied before the first poll loop begins, so an existing backlog cannot race the pause. It is the durable startup policy; resuming through the dashboard or API changes only the current in-memory runtime and does not rewrite `simphony.yaml`.

```yaml
projects:
  - id: geekli
    name: Geekli
    workflow_path: ../geekli/WORKFLOW.md
    start_paused: true
```

Registry-level `concurrency.max_concurrent_agents` is a supervisor-owned cap across all enabled projects. For example, `max_concurrent_agents: 10` means at most ten total agent sessions can run across the full multi-project process, even if each project's `WORKFLOW.md` allows more local concurrency.

`concurrency.default_project_max_concurrent_agents` fills `agent.max_concurrent_agents` for project workflows that do not set their own local limit. `projects[].max_concurrent_agents` is stricter: it caps that specific project even if its `WORKFLOW.md` requests a higher value.

Multi-project validation resolves every enabled project workflow before startup. By default, Simphony refuses workspace roots that overlap another enabled project or live under the registry directory. Set `security.allow_workspace_overlap: true` or `security.allow_workspace_under_registry_dir: true` only when the operator has intentionally accepted that risk. Registry servers also refuse non-loopback `server.bind_address` values unless `security.allow_remote_dashboard: true` is set. Simphony also warns when multiple enabled projects point at the same Linear project slug.

Each enabled project's `WORKFLOW.md` should set a unique `workspace.root`; the single-project default temp root is shared and will be rejected when more than one enabled project resolves to it.

See [Running multiple projects today](multi-instance.md) for the supported multi-process approach.

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
- `api_key` is required. Values beginning with `$` are resolved from the environment. Literal values are rejected at config-load time to prevent credentials from being committed to workflow files.
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
  review_resolution_state: Review Resolution
  merge_state: Approved
  done_state: Done
  coding_states:
    - Todo
    - In Progress
```

Successful coding-stage runs move to `review_state`. When autonomous review resolution is enabled, successful review-stage runs move to `review_resolution_state`; otherwise they move directly to `merge_state`. Issues moved to `merge_state` are dispatched with merge-focused instructions, and successful merge-stage runs move to `done_state`.

If `coding_states` is omitted, Simphony uses the tracker active states except the review, review-resolution, and merge states.

## Review Resolution

```yaml
review_resolution:
  enabled: true
  escalation_state: In Review
  max_attempts: 3
  require_checks_green: true
  require_code_review_approval: true
  unresolved_comment_policy: fix_or_explain
  escalate_on:
    - security_risk
    - schema_migration_risk
    - destructive_data_change
    - conflicting_reviews
    - max_attempts_exceeded
```

`review_resolution` enables an autonomous post-review stage for formal PR/code-review feedback. Simphony dispatches issues in `pipeline.review_resolution_state` with a review-resolution prompt that tells the agent to inspect the PR, unresolved comments, review decision, and CI/check results using the repository's configured GitHub tooling, then fix, respond, retry, or escalate.

The review-resolution agent must end with one of these directive lines:

- `SIMPHONY_REVIEW_DECISION: approved` moves the issue to `pipeline.merge_state`.
- `SIMPHONY_REVIEW_DECISION: retry` schedules another autonomous pass, capped by `max_attempts`.
- `SIMPHONY_REVIEW_DECISION: escalate` moves the issue to `escalation_state` and posts a Linear comment.

Use `agent_runtime.stage_overrides.review_resolution` for provider-neutral model/reasoning overrides, or `codex.stage_overrides.review_resolution` for Codex-specific skill selection.

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

In most workflows, `after_create` should clone the repository or copy a prepared checkout into the empty issue workspace before the coding agent starts.

On Windows, hooks run through `cmd /C`. On POSIX systems, hooks run through `bash -lc`.

## Merge Verification And GitHub Checks

```yaml
verify:
  commands:
    - pnpm lint
    - pnpm test
  timeout_ms: 600000

github:
  enabled: true
  merge_method: squash
  checks_timeout_ms: 1800000
  checks_registration_grace_ms: 60000
```

`verify.commands` are run in order before an approved branch is merged. In the local merge flow they run against the tentative merged result, which is rolled back on failure. In the GitHub flow they run in the issue worktree before the branch is pushed. Command output uses the bounded head-and-tail diagnostic described in troubleshooting.

When `github.enabled` is true, Simphony pushes the issue branch, opens or reuses a pull request, waits for GitHub checks, merges through `gh pr merge`, and refreshes the local base branch. `merge_method` accepts `merge`, `squash`, or `rebase`.

GitHub may briefly report “no checks reported” immediately after PR creation or a branch update. Simphony treats that result as pending for `checks_registration_grace_ms` (60 seconds by default), probing until checks register. Once registered, it watches them until success, failure, or the overall `checks_timeout_ms` deadline. Errors distinguish `checks_not_registered`, `checks_failed`, and `checks_timeout`.

Enabling the GitHub gate requires an installed and authenticated `gh` CLI. Project preflight checks `gh auth status` before dispatch.

## Agent

```yaml
agent:
  max_concurrent_agents: 10
  max_turns: 20
  max_retry_backoff_ms: 300000
  max_concurrent_agents_by_state:
    In Progress: 2
```

- `max_concurrent_agents` limits total running coding-agent sessions.
- `max_turns` limits continuation turns on a single agent session.
- `max_retry_backoff_ms` caps exponential retry delays.
- `max_concurrent_agents_by_state` optionally limits concurrency for specific tracker states.

## Agent Runtime

```yaml
agent_runtime:
  provider: codex
  model: gpt-5.4
  model_provider: openai
  reasoning_effort: high
  endpoint_url: $OPENAI_BASE_URL
  api_key: $OPENAI_API_KEY
  stage_overrides:
    coding:
      model_provider: kimi
      model: kimi-k2-coder
      endpoint_url: $KIMI_BASE_URL
      api_key: $KIMI_API_KEY
    review:
      model_provider: openai
      model: gpt-5.5
      endpoint_url: $OPENAI_BASE_URL
      api_key: $OPENAI_API_KEY
  env:
    OPENAI_ORG_ID: $OPENAI_ORG_ID
```

`agent_runtime.provider` selects the agent SDK used for future runs. Supported values are:

- `codex` uses Codex app-server over stdio. This is the default and remains compatible with existing `codex:` workflows.
- `claude` uses the embedded Claude Code Agent SDK shim.

Common fields in `agent_runtime` override provider-specific defaults from `codex:` or `claude:`. This lets a workflow switch SDKs by changing one selector while keeping shared model, endpoint, token, timeout, and stage settings in one place.

`endpoint_url`, `api_key`, `auth_token`, and `env` values beginning with `$` are resolved from environment variables. Literal values in `api_key` and `auth_token` are rejected at config-load time with `literal_secret_in_config`. Secrets are passed only to the agent subprocess environment and are omitted from the resolved API JSON. For `provider: codex`, `api_key` maps to `OPENAI_API_KEY` and `endpoint_url` maps to `OPENAI_BASE_URL`. For `provider: claude`, `api_key` maps to `ANTHROPIC_API_KEY` and `endpoint_url` maps to `ANTHROPIC_BASE_URL`.

`agent_runtime.provider` is the project default. A `stage_overrides` entry may set its own execution `provider` to `codex` or `claude` for `coding`, `review`, `review_resolution`, or `merge`. This is a new provider-native session at the stage boundary; Simphony passes the existing workspace, Git history, issue, and tracker state rather than attempting to transfer a live SDK thread.

Stage overrides support `provider`, `command`, `model`, `model_provider`, `reasoning_effort`, `endpoint_url`, `api_key`, `auth_token`, `env`, `skills`, `allowed_tools`, `disallowed_tools`, `permission_mode`, `setting_sources`, `approval_policy`, `thread_sandbox`, and `turn_sandbox_policy`. When a stage changes execution provider, Simphony recomputes provider-specific command, credential, permission, and sandbox defaults before applying the stage values. It does not inherit the other provider's model, endpoint, credentials, environment, command, or sandbox settings. Shared skills and operational timeout limits remain available.

For example, coding can use Codex while review uses the Claude Agent SDK:

```yaml
agent_runtime:
  provider: codex
  model: gpt-5.6

  stage_overrides:
    review:
      provider: claude
      model: claude-opus-4.7
      api_key: $ANTHROPIC_API_KEY
      permission_mode: acceptEdits
      allowed_tools: [Read, Edit, Bash]
```

Omit the Claude stage `command` to use Simphony's embedded Node.js SDK shim. Set it only for a custom wrapper. Use stage-level endpoint and credential overrides when stages target different provider endpoints. If every stage stays on one SDK and all models are available through one router endpoint, the top-level endpoint and key can still be shared.

Use these fields for OpenAI-compatible or Anthropic-compatible gateways:

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
    review_resolution:
      model: gpt-5.5
      reasoning_effort: xhigh
      skills:
        - github:gh-address-comments
        - github:gh-fix-ci
    merge:
      reasoning_effort: high
  approval_policy: auto
  thread_sandbox: none
  turn_sandbox_policy: none
  turn_timeout_ms: 3600000
  read_timeout_ms: 5000
  stall_timeout_ms: 300000
```

`codex:` is the provider-specific configuration for `agent_runtime.provider: codex`. If `agent_runtime` is omitted, Simphony behaves as if `agent_runtime.provider: codex` was set and uses this block directly.

The runner appends `--listen stdio://` when it is not already present. The subprocess is launched with the issue workspace as its current directory.

`model` and `model_provider` are optional. When present, they are passed to Codex app-server for thread and turn startup. Simphony treats these as provider-neutral strings, so non-OpenAI model IDs such as Claude, Gemini, Kimi, GLM, or DeepSeek variants can be configured when the underlying Codex installation has an appropriate provider/router configured.

`reasoning_effort` is optional and is passed to Codex as the per-turn `effort` override. Accepted values are `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`; `x-high`, `x_high`, and `max` are normalized to `xhigh`.

`stage_overrides` can override `model`, `model_provider`, `reasoning_effort`, `endpoint_url`, `api_key`, `auth_token`, `env`, and `skills` for a pipeline stage. Known stage keys are `coding`, `review`, `review_resolution`, and `merge`; unknown stage keys are accepted for forward compatibility and ignored until a matching stage exists. Provider-specific `codex.stage_overrides` and `claude.stage_overrides` use the same stage override shape as `agent_runtime.stage_overrides`.

The dashboard Settings page provides provider presets for the model-provider ID, API endpoint, and environment-backed API key reference. Selecting a preset writes those values into the editable workflow; it does not copy or expose the secret itself. After saving, **Refresh model catalog** calls the configured provider's model-list endpoint and adds the returned IDs to the model picker for the current browser session. This supports newly released or account-specific models without requiring a Simphony release. The built-in OpenAI picker includes `gpt-5.6` preview, but actual availability still depends on the configured OpenAI account.

Provider presets describe connection defaults, not protocol compatibility guarantees. The selected agent SDK or Codex model-provider/router configuration must support the provider's API surface.

`skills` selects default Codex skills for every stage. `stage_overrides.<stage>.skills` adds stage-specific skills. Skill entries can be simple names, which Simphony resolves through Codex `skills/list` at runtime, or objects with `name` and `path` when you want to pin a specific local `SKILL.md`. Resolved skills are sent as Codex skill input items on each turn; unresolved names are included in the prompt as visible guidance.

By default, `pipeline.review_state` is a durable human-review gate because it is omitted from `tracker.active_states`. An issue can enter `In Review`, but Simphony will not dispatch a review agent even after a process restart. Add the review state to `tracker.active_states` only when review should be agent-run, then configure `agent_runtime.stage_overrides.review.reasoning_effort: xhigh` or a review-specific model.

Dashboard stage pauses and changes made with the runtime pause API remain in memory only. A pause lets in-flight work finish while blocking new dispatches and retries for that stage. On restart, a project returns to the registry's `start_paused` policy; stage pauses always clear. Keep the review state out of `tracker.active_states` when the gate must survive restarts or require a human to advance the tracker state.

`approval_policy: auto` is mapped to Codex protocol value `never`.

`stall_timeout_ms` defaults to 300000. Set it to `0` to disable orchestrator-level stall detection.

Supported `turn_sandbox_policy` values are:

- `none`
- `read-only`
- `workspace-write`
- `danger-full-access`

## Claude

```yaml
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
```

`claude:` is the provider-specific configuration for `agent_runtime.provider: claude`. Simphony writes an embedded Node.js shim into the issue workspace and launches it unless `claude.command` or `agent_runtime.command` is set. The shim loads the Claude Agent SDK from the workspace or wrapper environment, runs one turn, emits Simphony-normalized JSON events, and resumes the prior Claude session for continuation turns.

Install a supported Claude SDK package where Node can resolve it, or set `SIMPHONY_CLAUDE_SDK_PACKAGE` to the package name your environment uses. The repository root declares `@anthropic-ai/claude-agent-sdk`; run `npm install` there for Simphony workspaces that inherit the root package manifest. Other managed repositories must make the SDK resolvable from their own workspace. The embedded shim tries `@anthropic-ai/claude-agent-sdk` first and falls back to `@anthropic-ai/claude-code`.

Set `claude.command` when you want to provide your own wrapper. The wrapper must read one JSON request from stdin and emit newline-delimited JSON events with `event`, optional `payload`, and optional `usage` fields.

## Server

```yaml
server:
  port: 8080
```

When `server.port` is set, Simphony starts the HTTP API. If `dashboard/dist` exists, the same server also serves the dashboard from `/`.

## Prompt Template

The Markdown body of `WORKFLOW.md` is rendered for the first agent turn. It can reference normalized issue fields:

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
