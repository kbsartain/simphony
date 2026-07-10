# Stage: coding

Triggered when the issue is in an active, non-pipeline state (default: `Todo`,
`Backlog`, or `working_state` `In Progress`).

## Pre-run transition
If the issue is not already in `tracker.working_state` (default `In Progress`),
move it there **before** doing any work, so the board reflects that work started.

## Workspace
Run inside the issue's isolated worktree (see `scripts/worktree.sh`). All file
edits and commands MUST stay inside that directory.

## Prompt (rendered from WORKFLOW.md body; variables from the issue)

```
You are working on issue {{ issue.identifier }}: {{ issue.title }}.

Priority: {{ issue.priority }}
State: {{ issue.state }}
Labels: {{ issue.labels | join: ", " }}

Description:
{{ issue.description }}

Please implement the necessary changes to resolve this issue.
Follow the project's coding standards and write tests where appropriate.
Keep changes scoped to the issue.
```

Available Liquid variables: `issue.id`, `issue.identifier`, `issue.title`,
`issue.state`, `issue.description`, `issue.priority`, `issue.labels`,
`issue.branch_name`, `issue.url`, `issue.created_at`, `issue.updated_at`,
`issue.blocked_by[]`.

## Completion
1. The worker commits work on the issue branch inside the worktree and returns
   `success` (with `commit_sha`).
2. The **orchestrator** then moves the issue to the first available of
   `[review_state, "In Review", "Review", "Done", "Completed"]`. Workers never
   write the tracker.
3. Do **not** merge yet — the base-branch merge happens only in the `merge` stage.

## Failure
If the worker returns `failed`/`retry` (e.g. the agent can't build, can't
authenticate, or tests won't pass), the orchestrator increments the attempt
count and retries with backoff; past `agent.max_attempts` it **escalates** the
issue to `pipeline.escalation_state` / `simphony:blocked` instead of looping
(`reference/state-machine.md` §8). Coding has no infinite-retry footgun here.
