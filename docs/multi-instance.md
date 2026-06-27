# Running Multiple Projects Today

Until the multi-project supervisor runtime is implemented, the supported way to operate more than one Simphony project is to run one Simphony process per project.

Each process needs its own:

- `WORKFLOW.md`
- Linear project slug and API key
- workspace root
- HTTP server port, if the dashboard/API is enabled
- shell environment for agent provider keys and endpoint URLs

This keeps orchestrator state, retry queues, workspace cleanup, hooks, and agent subprocesses isolated by operating-system process.

## Example Layout

```text
projects/
  alpha/
    WORKFLOW.md
  beta/
    WORKFLOW.md
```

In `projects/alpha/WORKFLOW.md`:

```yaml
tracker:
  kind: linear
  api_key: $ALPHA_LINEAR_API_KEY
  project_slug: alpha
workspace:
  root: ./simphony_workspaces
server:
  port: 8080
```

In `projects/beta/WORKFLOW.md`:

```yaml
tracker:
  kind: linear
  api_key: $BETA_LINEAR_API_KEY
  project_slug: beta
workspace:
  root: ./simphony_workspaces
server:
  port: 8081
```

Start each process separately:

```bash
simphony -workflow ./projects/alpha/WORKFLOW.md
simphony -workflow ./projects/beta/WORKFLOW.md
```

On Windows PowerShell:

```powershell
simphony -workflow .\projects\alpha\WORKFLOW.md
simphony -workflow .\projects\beta\WORKFLOW.md
```

## Isolation Checklist

- Use different `workspace.root` values unless each workflow lives in a separate project directory and uses a relative workspace root.
- Use different `server.port` values when both dashboards are enabled.
- Use project-specific Linear API key environment variables when possible.
- Confirm `tracker.project_slug` before enabling active issue states.
- Confirm hooks clone or prepare the intended repository for that project.
- Keep provider credentials in the process environment rather than hard-coding tokens into workflow files.

## Multi-Project Registry

Simphony can start multiple enabled project runtimes from a registry file:

```bash
simphony -config ./simphony.yaml
```

This mode starts one isolated project runtime per enabled project. The aggregate dashboard/API exposes a project selector and project-scoped runtime/settings routes from the shared process.

To validate the registry and enabled project workflows without starting workers:

```bash
simphony validate -config ./simphony.yaml
```

To inspect resolved project settings and static health:

```bash
simphony projects -config ./simphony.yaml
```

To start only one enabled project from the registry:

```bash
simphony -config ./simphony.yaml -project alpha
```

A minimal registry looks like:

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

Global `agent_runtime` values are defaults. Project workflows can override individual `agent_runtime` fields in their own `WORKFLOW.md`. The dashboard Project Setup page can edit these global defaults, along with the registry `server`, `concurrency`, `security`, and `projects` sections. Blank API key and auth-token fields preserve existing registry secrets; entered values replace them. Registry edits are persisted to `simphony.yaml` and take effect for workers after restart.

The registry-level `server` block enables project-scoped API routes such as `/api/v1/projects` and `/api/v1/projects/alpha/state`. Registry-level `concurrency.max_concurrent_agents` limits total running agent sessions across all enabled projects in the supervisor process. `projects[].max_concurrent_agents` caps one project below that global total.

The `security` block keeps accidental cross-project access hard. By default, enabled projects must use non-overlapping workspace roots outside the registry directory and the aggregate dashboard/API must bind to localhost or another loopback address. Use the allow flags only for a deliberate, reviewed exception.

Each enabled project workflow should set a unique `workspace.root`; the single-project default temp root is shared and will be rejected when multiple enabled projects resolve to it.

See [Two local projects registry](examples/two-projects.simphony.yaml) for a copyable two-project workstation example.

## Common Problems

Port collision:

- Symptom: the second process logs a server startup failure.
- Fix: assign each workflow a unique `server.port`, or disable the server for one process.

Workspace overlap:

- Symptom: two projects create or remove issue directories under the same root.
- Fix: give each project a distinct `workspace.root`. In multi-project mode, startup validation rejects overlap unless `security.allow_workspace_overlap` is true.

Wrong Linear project:

- Symptom: Simphony dispatches issues from an unexpected board.
- Fix: verify `tracker.project_slug`, active states, and the environment variable used by `tracker.api_key`.

Dashboard not showing projects:

- Symptom: the React dashboard falls back to single-project routes or does not show the project selector.
- Fix: verify the registry has a `server:` block and that `GET /api/v1/projects` is reachable.

Remote dashboard bind rejected:

- Symptom: startup or validation fails after setting `server.bind_address: 0.0.0.0`.
- Fix: prefer `127.0.0.1` for local operation. If remote access is intentional, set `security.allow_remote_dashboard: true` and put the server behind an appropriate private network, proxy, or tunnel.
