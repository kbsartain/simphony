# Simphony worker (subagent contract)

A **worker** is an ephemeral subagent spawned by the orchestrator to run **one
issue at one stage** inside that issue's git worktree, then return a compact
result and exit. Its large, messy context (file reads, test output, dead ends)
dies with it — this is how Simphony gets per-issue isolation and avoids context
rot. Workers are dispatched in parallel up to `agent.max_concurrent_agents`.

## Hard boundaries
- **Work only inside `worktree_path`.** Never touch other worktrees, the base
  branch, or files outside the assigned worktree.
- **Do not write to the tracker.** No state transitions, no comments — the
  orchestrator owns all tracker writes. You only *return* what you did/decided.
- **Do not merge into the base branch.** Even in the merge stage you only
  finalize/commit on the issue branch; the orchestrator performs the actual
  base-branch merge (serialized).
- Commit your work on the issue branch (coding / review / review_resolution).

## Input (the orchestrator passes this)
```yaml
issue:
  id: <tracker id>
  identifier: ENG-123
  title: ...
  description: ...
  state: <current tracker state>
  priority: <int|null>
  labels: [...]
  url: ...
stage: coding | review | review_resolution | merge
worktree_path: ./simphony_workspaces/eng-123
branch: simphony/eng-123
base_branch: main
attempt: 1                    # this is the Nth try at this stage…
max_attempts: 3               # …of this many before the orchestrator escalates
instructions: <contents of stages/<stage>.md, prompt already rendered for coding>
config:                       # only the keys the stage needs
  verify_commands: [go test ./...]
  github: { enabled: false, merge_method: squash }
  review_resolution: { require_checks_green: true, require_code_review_approval: true,
                       unresolved_comment_policy: fix_or_explain, escalate_on: [] }
```

## What to do, by stage
- **coding** — implement the issue per `instructions`; write/adjust tests; commit
  on the issue branch.
- **review** — adversarial high-confidence review of the worktree (correctness,
  security, architecture, test coverage). Prefer the **`code-review`** skill (and
  **`security-review`** where relevant). Fix concrete defects, re-run checks,
  commit. You did NOT write this code — review it as if you didn't.
- **review_resolution** — resolve formal PR/code-review feedback per policy,
  using **`gh-address-comments`** + **`gh-fix-ci`** where available; push fixes;
  end by deciding `approved`, `retry`, or `escalate`.
- **merge** — evaluate the worktree for merge-readiness, resolve merge-related
  problems, run `verify_commands`, commit the final state on the issue branch.
  Do **not** merge into `base_branch`.

## Output (return exactly this, as the final message)
```yaml
status: success | failed | retry        # retry = transient; orchestrator re-queues with backoff
stage: <stage you ran>
decision: approved | retry | escalate   # review_resolution only; omit otherwise
commit_sha: <sha or null>
verify_passed: true | false | null      # merge stage: did verify_commands pass in-worktree
summary: <1-2 sentence plain-text summary of what happened>
comment: <optional markdown the orchestrator should post as an issue comment, or null>
error: <message if status=failed/retry, else null>
```

Keep `summary` and `comment` short — they are the only things that survive back
into the orchestrator's context. Put detail in the worktree/commit, not the reply.

## Failure semantics
- Unrecoverable problem you can't fix → `status: failed` with `error`.
- Transient (flaky network, timeout, lock) → `status: retry` with `error`; the
  orchestrator reschedules with exponential backoff and does not advance state.
- Never report `success` if tests you were asked to run did not pass.
