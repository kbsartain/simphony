# Multi-Project Backlog

This backlog captures the phased path for supporting multiple concurrent Simphony projects without redesigning the existing orchestrator into one large project-aware runtime.

## Goal

Support multiple long-running coding projects from one Simphony installation while keeping each project's workflow, Linear configuration, workspaces, agent sessions, retry state, hooks, and secrets isolated.

Status: this implementation backlog is complete for the current iteration. Follow-on ideas are tracked in [Future Enhancements Backlog](future-enhancements.md), and the next step is wiring a real project to Linear for end-to-end validation.

The preferred architecture is a supervisor plus isolated project runtimes:

```text
Simphony supervisor
  global config
  project registry
  aggregate API/dashboard
  optional global concurrency gate

Project runtime A
  WORKFLOW.md
  tracker client
  workspace manager
  agent runner
  orchestrator state

Project runtime B
  WORKFLOW.md
  tracker client
  workspace manager
  agent runner
  orchestrator state
```

## Decisions

- Keep the current single-project `-workflow` mode as the compatibility path.
- Add multi-project support as a supervisor that starts one isolated runtime per project.
- Treat `WORKFLOW.md` as project-specific configuration.
- Allow global defaults for provider, SDK/runtime choice, model usage, server settings, and aggregate concurrency.
- Let project config override global defaults where override is safe and explicit.
- Avoid a shared persistent database for now. State remains in memory and reconstructs from tracker plus filesystem state.

## Non-Goals For The First Iteration

- A single project-aware orchestrator with shared internal state.
- Cross-project issue deduplication or dependency scheduling.
- Cross-project credential sharing beyond explicit global defaults.
- Built-in secret vaulting.
- Multi-user dashboard authorization. The first secure baseline should remain local or trusted-network operation.

## Phase 0 - Baseline Multi-Instance Documentation

Document the supported fallback: running one Simphony instance per project.

Backlog:

- `MP-0.1` Document separate installs/processes for multiple projects.
- `MP-0.2` Document required isolation knobs: unique `WORKFLOW.md`, workspace root, server port, Linear project, and credentials.
- `MP-0.3` Add troubleshooting notes for port collisions, overlapping workspace roots, and wrong Linear project slugs.

Acceptance:

- A user can run two independent projects today without code changes.
- Docs clearly explain what is isolated and what remains the operator's responsibility.

## Phase 1 - Global Config And Project Registry

Introduce a global configuration file that lists projects and provides shared defaults.

Backlog:

- `MP-1.1` Define a global config schema, tentatively `simphony.yaml`.
- `MP-1.2` Add `projects[]` entries with stable `id`, display `name`, `workflow_path`, and optional `enabled`.
- `MP-1.3` Add global `agent_runtime` defaults for provider, command, model, base URL env name, API key env name, and extra environment variables.
- `MP-1.4` Add global server defaults: bind address, port, dashboard enablement, and optional API prefix.
- `MP-1.5` Add global concurrency settings: maximum total running agents and optional per-project defaults.
- `MP-1.6` Implement config loading and validation while preserving the existing `-workflow` CLI flag.
- `MP-1.7` Add a new `-config` CLI flag for multi-project mode.
- `MP-1.8` Add tests for relative path resolution from the global config file location.

Acceptance:

- `simphony -workflow ./WORKFLOW.md` still behaves exactly as it does today.
- `simphony -config ./simphony.yaml` loads multiple project definitions.
- Invalid project IDs, duplicate IDs, missing workflow files, and unsafe paths produce clear validation errors.
- Unknown config keys are ignored or retained according to the existing forward-compatibility posture.

## Phase 2 - Project Runtime Manager

Wrap the existing orchestrator dependencies in a per-project runtime lifecycle.

Backlog:

- `MP-2.1` Introduce `ProjectRuntime` with project metadata, resolved workflow config, tracker, workspace manager, runner, and orchestrator.
- `MP-2.2` Introduce `ProjectManager` to start, stop, restart, and inspect project runtimes.
- `MP-2.3` Run one workflow watcher per project.
- `MP-2.4` Scope startup cleanup to each project's workspace root and terminal Linear states.
- `MP-2.5` Add project-aware structured logging fields: `project_id` and `project_name`.
- `MP-2.6` Ensure one project's config reload failure does not stop healthy sibling projects.
- `MP-2.7` Add tests for runtime startup, shutdown, failed project initialization, and hot reload isolation.

Status:

- Done: `MP-2.1`, `MP-2.2`, `MP-2.3`, `MP-2.4`, `MP-2.5`, `MP-2.7`, and sibling startup failure isolation for `MP-2.6`.
- Done: orchestrator runtime logs now inherit `project_id` and `project_name` from each project worker.
- Done: manager lifecycle and project hot-reload isolation tests are in place.

Acceptance:

- Each project has independent tracker, workspace, retry queue, and session state.
- A project failure is visible but does not crash or poison other projects.
- Existing orchestrator logic is reused rather than duplicated.

## Phase 3 - Aggregate HTTP API

Add project-scoped API routes while keeping current single-project routes available in compatibility mode.

Backlog:

- `MP-3.1` Add `GET /api/v1/projects` for project summaries and health.
- `MP-3.2` Add `GET /api/v1/projects/{project_id}/state`.
- `MP-3.3` Add `GET /api/v1/projects/{project_id}/issues/{issue_identifier}`.
- `MP-3.4` Add `POST /api/v1/projects/{project_id}/refresh`.
- `MP-3.5` Add project-scoped settings routes for reading, saving, and validating Linear settings.
- `MP-3.6` Keep existing `/api/v1/state`, `/api/v1/{issue_identifier}`, and `/api/v1/refresh` routes for single-project mode.
- `MP-3.7` Add API tests for unknown project IDs, disabled projects, and secret masking.

Status:

- Done: `MP-3.1`, `MP-3.2`, `MP-3.3`, `MP-3.4`, `MP-3.5`, and `MP-3.6`.
- Done: `MP-3.7`; route tests cover project listing, state, refresh, issue detail, unknown project IDs, disabled projects, stopped projects, project settings, and secret masking.

Acceptance:

- Dashboard and external clients can enumerate projects before selecting one.
- Project-scoped calls cannot read or mutate another project's settings or state.
- Existing single-project clients do not break.

## Phase 4 - Dashboard Project UX

Make project selection and per-project settings visible in the UI.

Backlog:

- `MP-4.1` Add a project switcher to the app shell.
- `MP-4.2` Add a project overview page with status, active sessions, retry count, and last refresh time per project.
- `MP-4.3` Scope issue lists, issue detail, refresh actions, and settings to the selected project.
- `MP-4.4` Add project-specific Linear settings panels.
- `MP-4.5` Add visible warnings for unhealthy, disabled, or misconfigured projects.
- `MP-4.6` Preserve the current streamlined UI when exactly one project is configured.

Status:

- Done: `MP-4.1`, initial `MP-4.2`, `MP-4.3`, `MP-4.4`, and initial `MP-4.5`. The dashboard discovers aggregate projects, uses a left-nav project context shell, scopes runtime/detail/refresh/settings calls to the selected project, represents the selected project/page in the URL, and surfaces disabled, stopped, failed, retrying, running, and idle project states.
- Done: the project setup surface is now visible as a registry/admin landing page, including the current startup mode, restart-required registry toggle, single-workflow starter registry creation, add/edit/remove project registry flows, editable registry defaults, and editable global agent runtime defaults with secret-preserving replacement fields.
- Done: `MP-4.6`; when registry mode has exactly one project, the dashboard keeps project context and setup controls but hides duplicate aggregate project overview/health panels on the Runtime page.

Acceptance:

- A user can see which project they are controlling before changing settings or refreshing work.
- The selected project is represented in the URL or durable client state.
- Secret values remain masked in all settings views.

## Phase 5 - Global Concurrency Gate

Coordinate total agent usage across isolated project runtimes.

Backlog:

- `MP-5.1` Add a shared concurrency limiter owned by the supervisor.
- `MP-5.2` Require each project orchestrator to acquire a slot before launching an agent.
- `MP-5.3` Release slots on normal completion, cancellation, startup failure, and panic recovery paths.
- `MP-5.4` Support optional per-project caps in addition to the global cap.
- `MP-5.5` Add observability for waiting projects and active global slot usage.
- `MP-5.6` Add tests for fairness, release-on-error, shutdown, and starvation resistance.

Status:

- Done: `MP-5.1`, `MP-5.2`, `MP-5.3`, `MP-5.4`, and `MP-5.5`. The supervisor now creates one shared non-blocking slot limiter from `concurrency.max_concurrent_agents`, passes it to every project runtime, and each orchestrator acquires a slot immediately before claiming and launching an agent. Slots are released on normal cleanup, cancellation/stop, pre-run failure, and recovered worker panic. Registry `default_project_max_concurrent_agents` fills missing workflow limits, and `projects[].max_concurrent_agents` caps one project even if its workflow requests more. The project API and dashboard expose global slot usage and projects waiting on supervisor capacity.
- Done: `MP-5.6`; limiter edge cases, optional global-cap behavior, shared limiter wiring, stop release, panic release, before-run failure release, FIFO waiting order, stale-waiter cleanup, and owner-aware orchestrator acquisition are covered.

Acceptance:

- Ten configured agent slots means ten total running agents across all projects, not ten per project.
- A noisy project cannot exceed its configured cap.
- Slot accounting remains correct after agent failures.

## Phase 6 - Isolation And Security Hardening

Make accidental cross-project access hard.

Backlog:

- `MP-6.1` Validate that project workspace roots do not overlap unless explicitly allowed.
- `MP-6.2` Validate that each workspace root is separate from the Simphony install directory by default.
- `MP-6.3` Keep project environment variables scoped to the process launched for that project's agent.
- `MP-6.4` Mask project and global secrets in API responses, logs, and dashboard state.
- `MP-6.5` Add warnings when multiple projects reference the same Linear project slug.
- `MP-6.6` Bind the dashboard to localhost by default in multi-project mode unless configured otherwise.
- `MP-6.7` Document operational security guidance for shared machines and remote dashboard access.

Status:

- Done: `MP-6.1`; multi-project startup validation rejects overlapping enabled project workspace roots unless `security.allow_workspace_overlap` is true.
- Done: `MP-6.2`; multi-project startup validation rejects enabled project workspace roots under the registry directory unless `security.allow_workspace_under_registry_dir` is true.
- Done: `MP-6.5`; validation emits warnings when multiple enabled projects reference the same Linear endpoint/project slug.
- Done: `MP-6.3`; agent subprocesses scrub inherited OpenAI, Anthropic, and Linear provider/tracker values before applying the selected project's runtime config and explicit env.
- Done: `MP-6.6`; registry servers bind to `127.0.0.1` by default and non-loopback binds require `security.allow_remote_dashboard: true`.
- Done: `MP-6.4`; settings and registry API responses mask secrets, dashboard state uses masked values/configured flags, Linear upstream errors redact echoed API keys, and orchestrator logs scrub configured tracker/runtime/env secrets.
- Done: `MP-6.7`; configuration and operations docs cover workspace isolation, local binding, and the explicit remote dashboard/API opt-in.

Acceptance:

- Simphony refuses obviously unsafe workspace overlap.
- Secrets for one project are not exposed through another project API response.
- Operators get clear warnings before running a risky multi-project configuration.

## Phase 7 - Operations And Tooling

Make multi-project operation understandable and debuggable.

Backlog:

- `MP-7.1` Add `simphony validate -config ./simphony.yaml`.
- `MP-7.2` Add `simphony projects -config ./simphony.yaml` to list resolved projects and health.
- `MP-7.3` Add an option to start only one project from a multi-project config.
- `MP-7.4` Add health output for project runtime state, watcher state, tracker validation, and workspace root checks.
- `MP-7.5` Update operations docs with startup, shutdown, logging, and recovery procedures.
- `MP-7.6` Add example global configs for two local projects.

Status:

- Done: `MP-7.1`; `simphony validate -config ./simphony.yaml` validates the registry and enabled project workflows without starting runtimes. The legacy `-validate-config` flag remains available.
- Done: `MP-7.2`; `simphony projects -config ./simphony.yaml` lists configured projects, server/global concurrency settings, validation warnings, workspace roots, tracker slugs, runtime defaults, and config health.
- Done: `MP-7.3`; `simphony -config ./simphony.yaml -project <id>` starts only one enabled project runtime while leaving the full registry visible to summaries and the aggregate API.
- Done: `MP-7.4`; static registry/workspace/config health is available from `validate` and `projects`, watcher-state reporting is exposed through project summaries/API/dashboard, and live Linear validation is available as an explicit per-project settings/API action instead of background polling.
- Done: `MP-7.5`; operations docs cover startup, shutdown, logging fields, project-scoped inspection, selected-project debugging, hot reload isolation, and recovery guidance.
- Done: `MP-7.6`; `docs/examples/two-projects.simphony.yaml` provides a two-project workstation registry example.

Acceptance:

- Operators can validate config before starting long-running agents.
- Logs and health endpoints make it clear which project is failing.
- Examples cover the expected two-project developer workstation case.

## Recommended Implementation Order

1. Phase 0, because it gives users a safe path immediately.
2. Phase 1, because the registry and merge semantics define the contract.
3. Phase 2, because runtime isolation is the core architectural change.
4. Phase 3, because the dashboard needs project-scoped APIs.
5. Phase 4, because UI work should sit on stable API behavior.
6. Phase 5, because concurrency is easier to enforce after runtimes exist.
7. Phase 6 and Phase 7, with security checks added alongside each relevant implementation phase where practical.

## Open Questions

- Answered: global agent defaults fill missing values; project `agent_runtime` fields override individual global fields.
- Answered: disabled projects are shown as inactive so operators can edit, re-enable, or remove them from Project Setup.
- Answered: project IDs are explicit stable registry keys. Bootstrap/setup flows may derive starter IDs for convenience, but operators can edit them before saving.
- Answered: this iteration does not include built-in dashboard authentication. Non-loopback dashboard/API binding requires `security.allow_remote_dashboard: true`; remote deployments should sit behind a private network, authenticated proxy, or tunnel.
- Answered: the global concurrency gate uses lightweight owner-aware FIFO fairness. It avoids a larger scheduler while preventing one project from repeatedly bypassing earlier waiting projects.
