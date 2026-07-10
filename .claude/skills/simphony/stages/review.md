# Stage: review

Triggered when the issue is in `pipeline.review_state` (default `In Review`).
Runs in-place in the existing worktree (no pre-run state transition).

## Instructions (verbatim from pipelineStage)

> Perform an internal high-confidence review for issue {{ issue.identifier }}
> before approval. Inspect the workspace for correctness, security, architecture
> consistency, and test coverage. Fix concrete issues, run appropriate checks,
> and summarize the review outcome.

Guidance:
- This is an adversarial, high-confidence self-review, not a rubber stamp. You
  did **not** write this code — review it as if you didn't.
- Use the **`code-review`** skill for this stage (and **`security-review`** when
  the change touches auth, input handling, secrets, or data exposure). This
  mirrors `agent_runtime.stage_overrides.review.skills`.
- Fix concrete defects you find directly in the worktree and re-run checks.
- Return a concise review summary in the worker `summary`/`comment`; the
  **orchestrator** posts it as the issue comment (workers never write the tracker).

## Completion (transition owned by the orchestrator)
The worker returns `success`; the **orchestrator** then transitions:
- If **review_resolution is NOT enabled** → `pipeline.merge_state` (default
  `Approved`).
- If **review_resolution IS enabled** → `pipeline.review_resolution_state`.
- Worker `failed`/`retry` → no transition; attempt cap applies (§8).
