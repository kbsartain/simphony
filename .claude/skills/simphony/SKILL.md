---
name: simphony
description: >-
  Drive a full autonomous coding workflow the way the Simphony harness does, but
  from inside the agent instead of an external orchestrator. You act as a thin
  orchestrator that dispatches an isolated worker subagent per issue-stage and
  owns all tracker state. Use when the user wants to process Linear (or other
  tracker) issues through the coding → review → merge pipeline, run the Simphony
  poll loop, work a single issue end-to-end, or focus on one pipeline stage
  (e.g. only "In Review"). Triggers: "run simphony", "process the backlog",
  "work issue ENG-123", "act as the simphony orchestrator", "only do the review
  stage", "poll Linear and dispatch".
---

# Simphony orchestrator (harness-free, subagent-based)

You are the **Simphony orchestrator**. Like the real harness, you do **not** do
the coding yourself — you coordinate. For each issue you spawn an **isolated
worker subagent** (see `worker.md`) that runs one stage in that issue's git
worktree and returns a compact result. You keep your own context small, so you
can supervise many issues without context rot, and workers run in parallel.

**Read `reference/state-machine.md` first — it is the authoritative contract.**
`worker.md` defines the subagent input/output contract. `stages/*.md` hold the
per-stage instructions you pass to workers.

Invariants:
- **The tracker is the source of truth.** The issue's state *is* the stage
  pointer. No local database — restart re-derives everything from tracker states
  and existing worktrees.
- **You (the orchestrator) own every tracker write** — state transitions and
  comments. Workers never touch the tracker; they return intent, you commit it.
- **Only advance an issue after the work actually succeeded.** For merge, that
  means after verify + the real base-branch merge succeed.

## Configuration
Load config from the project's `WORKFLOW.md` front matter if present (parse the
YAML between the `---` fences); otherwise use the defaults in
`reference/state-machine.md` §3. Resolve `$ENV_VAR` values from the environment.
Ignore unknown keys. Keys you need: `tracker.{active_states,working_state,
terminal_states}`, `pipeline.{review_state,merge_state,done_state,
review_resolution_state,escalation_state}`, `review_resolution.*`, `workspace.*`,
`verify.*`, `github.*`, `agent.{max_concurrent_agents,max_attempts}`.

If no tracker tool (Linear MCP or equivalent) is connected, say so and stop —
this skill preserves state in the tracker and cannot run blind.

## The orchestrator tick
Run this loop (once per `/goal` turn, or once per invocation in a manual run):

1. **Reconcile.** Re-read states of issues you're tracking; drop terminal ones
   (and escalated/`simphony:blocked` ones) and remove their worktrees if
   cleanup is on. Never trust in-memory state over the tracker.
2. **Fetch candidates.** Issues in the **effective active set**
   (`reference/state-machine.md` §3) — `active_states` plus every pipeline state
   (`working_state`, `review_state`, `merge_state`, and `review_resolution_state`
   when enabled), minus `terminal_states`. Do **not** fetch only the literal
   `active_states`, or `In Review` items are invisible.
3. **Filter & order.** Apply `reference/state-machine.md` §4 — skip
   running/claimed/completed-this-run/terminal/escalated/blocked-in-Todo; order
   by priority → created_at → identifier.
4. **Compute free slots** = `max_concurrent_agents` − workers currently running.
5. **Dispatch** up to that many issues (see "Dispatching a worker"). Claim each
   issue before spawning so it isn't picked twice.
6. **Collect results** and apply transitions (see "Applying a result").
7. **Print the board snapshot** (see below) and repeat.

**Claiming (no DB).** A claim is in-orchestrator memory for the session, made
authoritative by the tracker: for `coding` the pre-run `working_state`
transition *is* the claim; for review/merge (no pre-run transition) hold the
claim in memory for the session. Because `/goal` runs one tick at a time and you
wait for the worker batch before the next tick, two ticks never overlap, so a
claim can't be double-taken within a session. On restart, in-memory claims are
gone — Reconcile re-derives from tracker state + existing worktrees.

### Board snapshot (every tick)
Emit one greppable line so `/goal`'s evaluator and you both have clean evidence:
```
BOARD: backlog=0 todo=0 in_progress=1 in_review=2 approved=0 done=14 escalated=1
```

## Dispatching a worker
For each selected issue:
1. **Resolve stage** from its current tracker state (review_resolution → review →
   merge → else coding).
2. **Respect stage focus / pause.** If running in `stage=<x>` mode, only dispatch
   issues whose resolved stage matches; leave all others untouched.
3. **coding pre-run transition.** If stage is `coding` and the issue is not in
   `working_state`, transition it to `working_state` *before* spawning the worker
   (so the board shows work started). Other stages get no pre-run transition.
4. **Prepare the worktree**:
   `WT_BRANCH=<issue.gitBranchName> scripts/worktree.sh prepare <identifier> <base_branch> <branch_prefix> <root> <repo>`
   (or `worktree.ps1 -Branch <issue.gitBranchName> …` on Windows). Pass the
   tracker's suggested branch via `WT_BRANCH`/`-Branch` when present; the script
   fetches and bases the worktree off `origin/<base_branch>` (the current remote
   tip), not the stale local base (`reference/state-machine.md` §3).
5. **Spawn the worker subagent** with the `worker.md` input contract: the issue
   fields, the resolved `stage`, the `worktree_path`, `branch`, `base_branch`,
   the current `attempt` / `max_attempts`, the rendered `stages/<stage>.md`
   instructions (render the coding prompt from the `WORKFLOW.md` body using the
   issue's fields), and only the config the stage needs.
   - **Run them in parallel:** issue all worker spawns for this tick as
     subagent/Task calls **in a single message** (or as background agents) so
     they execute concurrently, then wait for the batch. Cap the batch at the
     free-slot count.
   - **Per-stage skills** (mirrors `agent_runtime.stage_overrides.skills`): tell
     the worker which skill to use — `review` → the `code-review` (and
     `security-review`) skill; `review_resolution` → `gh-address-comments` +
     `gh-fix-ci`; `coding` → the project's own conventions. Pass these in the
     worker's `instructions`.

## Applying a result
When a worker returns (`worker.md` output), you — not the worker — do the writes.
**Every non-success increments the (issue, stage) attempt count and is subject to
the escalation cap** (`reference/state-machine.md` §8) so failures converge:

- `status: retry` (transient) → increment attempts; reschedule with exponential
  backoff; **no** state change; post the `comment` if provided.
- `status: failed` → increment attempts; post the `comment`; do not advance.
- **After either, if `attempts >= agent.max_attempts`** (default 3) → **escalate**
  instead of rescheduling: move the issue to `pipeline.escalation_state` (or apply
  a `simphony:blocked` label if no escalation state), post an escalation comment
  (stage, attempt count, last error), and release the claim. This is what stops
  a wedged issue (e.g. an agent that can't authenticate) from looping forever and
  draining the `/goal` budget.
- `status: success` → reset the attempt count and apply the stage transition:
  - **coding** → move to first available of `[review_state, "In Review",
    "Review", "Done", "Completed"]`.
  - **review** → move to `merge_state` (or `review_resolution_state` if
    review-resolution is enabled).
  - **review_resolution** → use `decision`: `approved` → `merge_state`; `retry`
    → re-dispatch review_resolution, and after `review_resolution.max_attempts`
    passes escalate; `escalate` → `review_resolution.escalation_state` (default
    `review_state`) with an escalation comment.
  - **merge** → **you** now run the serialized finalize: execute
    `verify.commands` against the worktree, then merge the issue branch into
    `base_branch` (or GitHub PR + wait for checks if `github.enabled`), then move
    to `done_state`. On any failure: post `**Simphony merge failed** …`, retry,
    do **not** advance.
- Post the worker's `comment` (if any) and the stage status comments from
  `reference/state-machine.md` §6 — **coalescing repeats**: before posting, check
  the issue's latest simphony-marked comment and, if the signature matches (same
  failure), **update it with a repeat counter** instead of adding a new one. A
  retry loop must collapse to one self-updating comment + one escalation, never
  dozens of duplicates.

## Concurrency & the merge lock
- Coding / review / review_resolution workers run **in parallel** up to
  `max_concurrent_agents` — safe because each has its own worktree.
- The **base-branch merge is single-threaded**: only ever perform one
  branch-into-`base_branch` merge at a time, even if several issues are in the
  merge stage. Serialize these in the orchestrator so two merges never race on
  `base_branch`.

## Modes
- **Single-issue** (`work issue ENG-123`): run the tick for just that issue,
  spawning one worker per stage, until it reaches `done_state`/terminal.
- **Poll loop** (`run simphony`): the full tick over all candidates. Intended to
  run under **`/goal`** (see below).
- **Stage focus** (`stage=<coding|review|review_resolution|merge>`): dispatch
  only matching-stage issues; leave everything else in place.

## Long-running behavior (`/goal`)
Run the orchestrator under **`/goal`** (Claude Code v2.1.139+, or Codex 0.128.0+
with `goals = true`). `/goal` drives the loop and checks a completion condition
against the tracker each turn; you fan out to parallel workers per turn; the turn
boundary is when the batch returns. Give a mechanically checkable condition
(e.g. "zero issues remain in the active states"), keep the BOARD snapshot in the
transcript, treat escalation/terminal states as acceptable "done-for-now," and
set a turn/time cap. Ready-to-use conditions: `reference/goal-conditions.md`.

## Safety & fidelity
- Workers never leave their worktree, never write the tracker, never merge base.
- Never advance an issue's state unless the work (and, for merge, verify + real
  merge) actually succeeded.
- Respect the block rule (Todo issues with non-terminal blockers are skipped).
- Treat `terminal_states` as stop signals: cancel work, optionally remove the
  worktree.
- First run: `max_concurrent_agents: 1`, disposable `workspace.root`, sandbox
  branch/fork until proven.

## Files
- `reference/state-machine.md` — canonical states, transitions, config, rules.
- `reference/goal-conditions.md` — `/goal` completion-condition templates.
- `worker.md` — the subagent input/output contract.
- `stages/{coding,review,review_resolution,merge}.md` — per-stage instructions.
- `scripts/worktree.{sh,ps1}` — worktree create/merge/remove.
