# Linear Setup

Linear is currently Simphony's only tracker adapter. The adapter reads issues from a single Linear project and normalizes them into Simphony's issue model.

## Required Configuration

```yaml
tracker:
  kind: linear
  api_key: $LINEAR_API_KEY
  project_slug: your-linear-project-slug
  active_states:
    - Todo
    - In Progress
```

Set the token before starting Simphony:

```powershell
$env:LINEAR_API_KEY = "replace-with-your-linear-api-key"
```

On POSIX shells:

```bash
export LINEAR_API_KEY="replace-with-your-linear-api-key"
```

## Project Slug

`tracker.project_slug` is matched against Linear's project `slugId` field. Use the slug from the Linear project URL or API, not the workspace name and not an issue identifier.

The default endpoint is:

```text
https://api.linear.app/graphql
```

Override `tracker.endpoint` only for testing or a compatible proxy.

## Issue Selection

Simphony fetches issues in `tracker.active_states` for the configured project. Candidate issues must include:

- `id`
- `identifier`
- `title`
- `state`

Issues are skipped when they are already running, already claimed, completed during this process lifetime, terminal, outside the active state set, or blocked while in `Todo`.

## Terminal States

Terminal states are used for reconciliation and startup cleanup.

```yaml
tracker:
  terminal_states:
    - Done
    - Canceled
```

If omitted, Simphony uses:

- `Closed`
- `Cancelled`
- `Canceled`
- `Duplicate`
- `Done`

When a running issue moves into a terminal state, Simphony cancels the worker and removes the issue workspace after running `hooks.before_remove`, if configured.

## Fields Used By Simphony

The Linear adapter reads:

- issue ID and identifier,
- title and description,
- priority,
- state name,
- branch name,
- URL,
- labels,
- created and updated timestamps,
- inverse `blocks` relations for blocker detection.

Labels are normalized to lowercase before being passed to prompts and API responses.

## Blocked Issues

For issues in `Todo`, Simphony checks inverse Linear relations of type `blocks`. If a blocker is not in a terminal state, the issue is not dispatched.

Issues in other active states are not blocked by this rule.
