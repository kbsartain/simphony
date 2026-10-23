# Future Enhancements Backlog

This backlog captures follow-on work that is valuable after the mandatory [Security Remediation Plan](security-remediation-plan.md) and [Security Remediation Backlog](security-remediation-backlog.md) are complete.

The next near-term step is not feature development or a live Linear-backed E2E run. First complete the P0/P1 security remediation, then perform the controlled disposable-environment validation in `SEC-053`. The E2E items below may resume only after the control-plane and credential/SSRF exit gates pass.

## E2E Validation First

- `FE-0.1` Create a real `simphony.yaml` with one enabled project wired to Linear.
- `FE-0.2` Validate `simphony validate -config ./simphony.yaml` and `simphony projects -config ./simphony.yaml`.
- `FE-0.3` Start Simphony in project-registry mode and verify `/api/v1/projects`, project state, settings, refresh, and issue detail routes.
- `FE-0.4` Use the dashboard Project Setup and Settings pages to confirm selected-project scoping, masked secrets, Linear test connection, and URL project persistence.
- `FE-0.5` Run one issue from Linear through dispatch, workspace preparation, agent execution, status reporting, completion transition, and cleanup.
- `FE-0.6` Exercise failure paths: invalid Linear key, invalid project slug, full global slot pool, stopped/disabled project, workflow reload error, and agent failure retry.
- `FE-0.7` Capture any defects or UX friction found during E2E testing before starting new enhancements.

## Security And Access Control

Authentication, local-only binding, trusted origins, CSRF protection, route permissions, audit logging, credential-origin binding, and SSRF defenses are mandatory remediation rather than optional enhancements:

- `FE-1.1` is superseded by `SEC-001` through `SEC-006` in the security remediation backlog.
- `FE-1.2` is superseded by the remote-access and reverse-proxy documentation in `SEC-052`.
- `FE-1.4` is superseded by the mandatory privileged-action audit logging in `SEC-050`.
- `FE-1.3` remains a post-remediation enhancement: expand the baseline read/operator permission model into full multi-user roles if Simphony is used by more than one operator.
- `FE-1.5` remains a post-remediation enhancement: evaluate OS secret stores or a vault integration after origin-scoped credential references from `SEC-010` and `SEC-011` are stable.

## Health And Observability

- `FE-2.1` Add persisted health history across process restarts.
- `FE-2.2` Add optional scheduled tracker validation with a safe interval, timeout, and last-success/last-failure timestamps.
- `FE-2.3` Add dashboard timelines for project runtime events, reloads, dispatch deferrals, retries, and completion transitions.
- `FE-2.4` Add exportable diagnostic bundles with config summaries, redacted logs, project summaries, and recent events.
- `FE-2.5` Add alert hooks for project startup failures, repeated retries, watcher failures, and exhausted supervisor slots.

## Project Onboarding UX

- `FE-3.1` Build a richer project onboarding wizard for adding a Linear-backed project from scratch.
- `FE-3.2` Add project ID suggestions derived from folder names or Linear project slugs while keeping IDs explicitly editable before save.
- `FE-3.3` Add inline validation for workspace root safety, duplicate Linear slugs, server bind risk, and missing environment variables before saving registry edits.
- `FE-3.4` Add reusable project templates for common workflows: single repo, multiple repos, review-resolution enabled, and local-only testing.
- `FE-3.5` Add a guided first-run checklist after creating a starter registry.

## Provider And Agent Runtime Enhancements

- `FE-4.1` Build a provider compatibility matrix for Codex/OpenAI-compatible and Claude-compatible runtimes.
- `FE-4.2` Add UI affordances for testing provider command availability and runtime environment variables without launching a full issue. Initial command preflight and packaged-Codex diagnostics are complete; an explicit UI test action remains.
- `FE-4.3` Add clearer diagnostics for OpenAI-compatible endpoints that do not fully support the Codex app-server expectations. Model-catalog HTTP/status diagnostics are complete; turn-protocol diagnostics remain.
- `FE-4.4` Add runtime presets for common OpenAI-compatible gateways and Anthropic-compatible gateways while preserving custom endpoint support. Initial endpoint/key presets and manual provider model-catalog refresh are complete.
- `FE-4.5` Consider per-project provider overrides in Project Setup if global defaults prove too coarse during real multi-project operation.
- `FE-4.6` Add true per-stage execution SDK selection so one project can use Codex app-server for implementation and the Claude Agent SDK for adversarial review. See [Operator Controls and Per-Stage Runtime Backlog](operator-controls-and-stage-runtime-backlog.md).

## Operator Controls

- `FE-6.1` Add soft project and stage pause controls that block new dispatch and retries without terminating in-flight work. See [Operator Controls and Per-Stage Runtime Backlog](operator-controls-and-stage-runtime-backlog.md).
- `FE-6.2` Add bounded handling for delayed GitHub check registration and preserve useful verification-output tails in merge diagnostics.

## Operational Hardening

- `FE-5.1` Add an E2E test harness using fake Linear and fake agent processes for repeatable regression coverage.
- `FE-5.2` Add smoke-test scripts for Windows development and CI.
- `FE-5.3` Add optional persisted run metadata for post-restart diagnostics without changing the orchestrator's in-memory control model.
- `FE-5.4` Add backup/restore guidance for `simphony.yaml`, project `WORKFLOW.md` files, and workspace roots.
- `FE-5.5` Add clearer upgrade notes when registry schema or dashboard settings behavior changes.

## Product Decisions To Revisit After E2E

- Whether the baseline single-operator read/operator permission model should expand into multi-user roles. Authentication itself is mandatory.
- Whether passive scheduled tracker health is useful enough to justify extra Linear traffic and credential exposure surface.
- Whether the current owner-aware FIFO limiter is sufficient under real concurrent project load.
- Whether project setup should remain registry-file-oriented or evolve toward a more guided control-plane experience.
- Whether persistent health/run metadata is needed before adding more automation behavior.
