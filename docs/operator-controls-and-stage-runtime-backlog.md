# Operator Controls and Per-Stage Runtime Backlog

Last updated: 2026-07-10

This document is the durable implementation and session-handoff record for near-term Simphony work on operator pause controls, adversarial review, and merge diagnostics. Development for this backlog is managed directly in the Simphony repository through Codex sessions. Simphony must not orchestrate changes to its own source tree.

## Working Agreement

- Build and test Simphony directly from this repository.
- Do not register this repository as a Simphony-managed project while this backlog is active.
- The Simphony Linear board may be used to organize, prioritize, and track this work. Link board issues back to this document, which remains the durable technical requirements and session-handoff source of truth.
- Preserve progress in commits and update the **Session Handoff** section before ending an incomplete work session.
- Keep unrelated worktree changes intact.
- Follow the project rule that runtime orchestration state remains in memory. Workflow and registry files may contain operator configuration, but a database must not be introduced for pause state.

## Decisions Already Made

1. Pause operations are **soft pauses**. They prevent new work from starting but do not terminate an in-flight agent.
2. Polling and reconciliation continue while paused so the dashboard remains current and running work can finish normally.
3. Pause controls are required at both project and pipeline-stage scope.
4. Pipeline stages are `coding`, `review`, `review_resolution`, and `merge`.
5. Per-stage model routing is already supported inside one agent SDK. The new requirement is true per-stage SDK selection between Codex app-server and the Claude Agent SDK.
6. A primary use case is adversarial review: implementation and review should be able to use different model families, providers, credentials, and execution SDKs.
7. A stage switch does not transfer a live thread between SDKs. The repository workspace, Git history, issue data, and tracker state are the handoff boundary.
8. Workflow hot reload must allow an operator to pause a stage, change its runtime/model configuration, and resume it without restarting the project.

## Original Capability Baseline

- `agent_runtime.provider` selects `codex` or `claude` once for the entire project.
- `agent_runtime.stage_overrides` supports model, model-provider label, reasoning effort, endpoint, credentials, environment, and skills.
- Stage overrides currently do not support the execution SDK or command.
- A static human-review gate is possible by omitting the review state from `tracker.active_states`, but there is no runtime pause button.
- Project `Stop()` is not an acceptable pause mechanism because it cancels workers and stops reconciliation.
- Workflow configuration hot reload already applies to future runs.

## Delivery Order

### Phase 1: Soft Pause Controls

#### `PAUSE-1` Control state and semantics

- [x] Add project-level paused state to the running orchestrator.
- [x] Add a paused-stage set keyed by normalized pipeline stage name.
- [x] Expose paused state and paused stages in project/state snapshots.
- [x] Decide and document restart semantics. Runtime pauses are in-memory and clear on process restart; durable human gates continue to use tracker/workflow configuration.
- [x] Confirm no operator-action audit structure currently exists; do not add a database solely for pause attribution.

Acceptance criteria:

- Pausing does not cancel or signal any running worker.
- The state endpoint immediately reflects the pause.
- Repeated pause or resume requests are idempotent.
- Unknown stage names return a validation error.

#### `PAUSE-2` Central dispatch gating

- [x] Block ordinary candidate dispatch when the project is paused.
- [x] Block only matching-stage dispatch when a stage is paused.
- [x] Apply the same gate to retry paths, including completion-transition retries that could otherwise advance review or merge work.
- [x] Continue tracker polling, reconciliation, terminal cleanup, event handling, and completion processing while paused.
- [x] Preserve queued retry entries without increasing their attempt count merely because they are paused.
- [x] On resume, trigger an immediate refresh rather than waiting for the next polling interval.
- [x] Report a clear deferred reason such as `project_paused` or `stage_paused:review`.

Acceptance criteria:

- Work already running at pause time completes normally.
- Coding may complete and transition an issue into `In Review` while review is paused.
- No review worker starts until review is resumed.
- A paused merge stage cannot merge through a queued retry.
- Unpaused stages continue to dispatch when only one stage is paused.

#### `PAUSE-3` Project API

- [x] Add idempotent project pause and resume endpoints.
- [x] Add idempotent stage pause and resume endpoints.
- [x] Return the resulting control state from each mutation.
- [x] Support the single-project API consistently if that surface remains supported.
- [x] Add API tests for success, repeated operations, unknown project, stopped project, and invalid stage.

Proposed routes:

```text
POST /api/v1/projects/{project_id}/pause
POST /api/v1/projects/{project_id}/resume
POST /api/v1/projects/{project_id}/stages/{stage}/pause
POST /api/v1/projects/{project_id}/stages/{stage}/resume
```

#### `PAUSE-4` Dashboard controls

- [x] Add a project Pause/Resume control with an unmistakable current-state indicator.
- [x] Add per-stage Pause/Resume controls to the workflow or runtime view.
- [x] Show that in-flight work will finish when the operator pauses.
- [x] Display paused stages on project overview and issue/runtime views where relevant.
- [x] Disable or debounce duplicate mutation requests while one is pending.
- [x] Refresh state immediately after a successful mutation.
- [x] Preserve accessibility, keyboard operation, responsive layout, and confirmation/error feedback.

#### `PAUSE-5` Verification and documentation

- [x] Add table-driven orchestrator tests for every stage and both normal/retry dispatch paths.
- [x] Add project runtime and HTTP API tests.
- [x] Add dashboard type/build coverage and browser-based visual verification.
- [x] Document soft-pause semantics, restart behavior, and the difference between Pause and Stop.
- [x] Document the static `tracker.active_states` human-review gate as a durable alternative.

### Phase 2: True Per-Stage Agent SDK Selection

#### `SDK-1` Configuration model

- [x] Add `provider` to `AgentStageOverride` with supported values `codex` and `claude`.
- [x] Add the stage-level fields needed to form a complete provider runtime. At minimum evaluate `command`, permission/sandbox settings, allowed/disallowed tools, and Claude setting sources.
- [x] Define inheritance precisely: common model/endpoint/environment values may inherit, but provider-specific defaults must be recomputed when a stage changes SDK.
- [x] Ensure secrets remain masked in API responses and preserved during settings updates.
- [x] Keep unknown future stage keys forward compatible.

Target configuration:

```yaml
agent_runtime:
  provider: codex
  model: gpt-5.6-sol

  stage_overrides:
    coding:
      provider: codex
      model: gpt-5.6-sol

    review:
      provider: claude
      model: claude-opus-4-6
      api_key: $ANTHROPIC_API_KEY

    merge:
      provider: codex
      model: gpt-5.6-sol
```

Acceptance criteria:

- A Claude stage never inherits `codex app-server` as its command.
- A Codex stage never inherits Claude-only permission or tool configuration accidentally.
- Existing workflows without stage-level `provider` behave exactly as before.
- Invalid SDK/provider combinations fail during validation or preflight, before issue dispatch.

#### `SDK-2` Effective runtime resolution

- [x] Replace the Codex-named effective-config helper with a provider-neutral stage runtime resolver.
- [x] Resolve the complete effective runtime before selecting `runCodex` or `runClaude`.
- [x] Apply provider defaults first, then safe project-level common values, then the selected stage override.
- [x] Preserve stage-specific endpoint, credentials, environment, reasoning, and skills.
- [x] Record effective SDK, model, and model-provider label on the running entry and in structured logs.

Acceptance criteria:

- One issue can progress Codex coding -> Claude review -> Codex merge.
- Continuation turns inside a stage remain on the same SDK/session.
- A new stage begins a new provider-native session using the existing workspace.
- Hot-reloaded stage settings affect future runs but do not mutate an in-flight run.

#### `SDK-3` Preflight and model catalogs

- [x] Run command/package/credential preflight for every SDK referenced by an active stage, not only the project default. Explicit secret references must resolve non-empty; absent explicit credentials may use an authenticated local SDK session.
- [x] Report failures with the affected stage in the diagnostic.
- [x] Allow model-catalog refresh for the effective provider of each stage.
- [x] Keep Codex-router model selection distinct from true Claude SDK selection in labels and help text.

#### `SDK-4` Dashboard settings

- [x] Add an execution SDK selector to every stage override.
- [x] Change visible fields and defaults based on the selected stage SDK.
- [x] Clearly distinguish `Execution SDK` from `Model provider`.
- [x] Support the operational flow: pause review -> change SDK/model -> save/hot reload -> resume review.
- [x] Preserve custom endpoints and environment-backed secret references.

#### `SDK-5` Cross-provider verification

- [x] Add configuration resolution tests for mixed-SDK workflows.
- [x] Add runner tests proving provider selection happens after stage resolution.
- [x] Add fake Codex and fake Claude processes for a deterministic cross-stage lifecycle test.
- [x] Test stage-specific credentials, commands, environment, permissions, and masked API output.
- [x] Test hot reload while the affected stage is paused.
- [x] Update configuration, workflow example, environment, and troubleshooting documentation.

### Phase 3: Merge-Gate Diagnostic Hardening

#### `DIAG-1` GitHub check registration race

Implementation audit: this checkout does not currently execute `gh pr checks` in the orchestrator or workspace merge path. Review/review-resolution prompts delegate check inspection to the selected agent, while `MergeWorkspaceToBaseBranch` performs a local Git merge and optional push. The error observed for GEE-129 therefore came from a different or newer merge-gate implementation than the one present here. Before implementing these items, decide whether to recover that implementation or introduce a native, configurable GitHub gate in this repository.

- [ ] Treat `gh pr checks` reporting “no checks reported” as pending for a bounded grace period after PR creation or branch update.
- [ ] Continue polling until checks appear, the grace period expires, or the overall checks timeout expires.
- [ ] Distinguish no-check timeout from an actual failed check in errors and Linear comments.
- [ ] Add deterministic tests for delayed check registration.

#### `DIAG-2` Verification output retention

- [x] Preserve the useful failure tail and final test summary when a verbose verification command exits nonzero.
- [x] Avoid flooding project state, API payloads, logs, or Linear comments with unbounded output.
- [x] Use a bounded 8 KiB head plus 24 KiB tail representation with an explicit truncation marker and original byte count.
- [x] Strip ANSI control sequences so retained output remains readable in logs, dashboard payloads, and tracker comments.
- [x] Add tests using long output whose actual failure appears at the end.

Context: GEE-129 initially hit a GitHub "no checks reported" race and later produced a transient `pnpm test` exit 1 whose pasted output contained only passing suites. Automatic retries eventually succeeded, all 2,001 tests passed on reproduction, GitHub CI passed, and PR #4 merged. No GEE-129 source fix was required.

## Definition of Done

This backlog is complete when:

- Operators can soft-pause and resume an entire project or one pipeline stage from the dashboard.
- Pauses never terminate in-flight work and cannot be bypassed by retries.
- Operators can pause review, change its SDK/model, and resume without restarting Simphony.
- A single project can execute coding, review, and merge through different SDKs where configured.
- Mixed-SDK behavior is covered by deterministic backend tests and dashboard verification.
- Configuration and operational documentation clearly distinguish SDK, model provider, model, Pause, Stop, and durable human-review gates.

## Session Handoff

Current status: Phase 1, Phase 2, and `DIAG-2` are implemented. Stage configuration and the dashboard can select Codex or Claude with provider-specific command/tool/permission/sandbox fields; the runner resolves the effective stage runtime before SDK selection without leaking cross-provider credentials or commands; preflight checks stage SDK commands and explicit credential references with stage-specific diagnostics; running snapshots/logs identify the effective SDK and model; and model catalogs accept a stage selector. Browser verification exercised both pause controls and a live stage SDK-selector change with zero console errors. A deterministic subprocess test drives one issue through Codex coding -> Claude review -> Codex merge -> Done, and a paused-review test proves hot-reloaded SDK/model settings apply on resume. Hook failures now retain bounded head-and-tail output so the final failure summary survives verbose test runs.

CON-199 recovery: the literal-secret validation guard, error code, focused tests, fixture updates, and documentation were ported into this checkout. The proposed deletion/untracking of root `WORKFLOW.md` was not copied because this checkout still uses that file; workflow relocation remains a separate migration decision. The original CON-199 worktree remains outside this managed checkout.

Recommended next task: resolve the `DIAG-1` architecture choice—recover the merge-gate implementation that emitted the GEE-129 error, or add a new native configurable GitHub checks gate before `MergeWorkspaceToBaseBranch`.

Latest verification:

```text
go test ./...
cd dashboard && npm run build
```

Result: all Go packages and the dashboard TypeScript/Vite production build passed on 2026-07-10. Browser verification also passed against a local mock API. A race-enabled orchestrator run was attempted but could not build because `gcc` is not installed on this Windows environment.

Files changed for this backlog capture:

- `docs/operator-controls-and-stage-runtime-backlog.md`
- `docs/configuration.md`
- `docs/future-enhancements.md`
- `docs/troubleshooting.md`
- `internal/config/*`
- `internal/orchestrator/*`
- `internal/project/manager_test.go`
- `internal/server/*_test.go`
- `pkg/api/*`
