# Stage: merge

Triggered when the issue is in `pipeline.merge_state` (default `Approved`).
This is the point where human approval has (conceptually) been granted and the
change is finalized.

**This stage is split** (subagent model): a **worker** does the in-worktree
finalize; the **orchestrator** does the serialized base-branch merge. The
worker must **never** merge into `base_branch` — only the orchestrator does, one
at a time (the merge lock).

## Worker part (in the worktree only)

> Human review for issue {{ issue.identifier }} has been approved. Evaluate the
> existing workspace changes for merging, resolve merge-related issues, run
> `verify_commands`, and commit the final changes on the **issue branch**.

Return `verify_passed` + the final `commit_sha`. Report `failed`/`retry` (not
`success`) if verify did not pass. Do not touch `base_branch`.

## Orchestrator part (serialized — the merge lock)
Only after the worker returns `success` with `verify_passed: true`:

1. Re-run `verify.commands` at the tip if you did not trust the worker's run
   (optional; the worker already ran them in-worktree).
2. Merge the issue branch into `workspace.base_branch` (default `main`):
   - If `github.enabled`: open/locate the PR, wait for required checks to go
     green (respecting `checks_timeout_ms`), then merge with `merge_method`
     (e.g. `squash`).
   - Otherwise: fast-forward/merge locally into base and push.
   - **Serialize:** never run two base-branch merges concurrently.
3. On merge failure: post
   `**Simphony merge failed** … Simphony will retry automatically.`, increment
   attempts (§8), retry, and — past `max_attempts` — escalate. Do **not** advance.
4. On success: move the issue to `pipeline.done_state` (default `Done`).
5. If `workspace.cleanup_worktrees` is true, remove the issue worktree
   (running `hooks.before_remove` first if configured).
