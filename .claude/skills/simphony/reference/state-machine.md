# Simphony State Machine (canonical reference)

This is the behavioural contract the `simphony` skill emulates. It mirrors the
Go orchestrator in `internal/orchestrator/orchestrator.go` and the config in
`WORKFLOW.md` front matter. When emulating without the harness, follow this
document exactly.

## 1. Pipeline stages

The stage is derived **purely from the issue's tracker state**. There is no
separate stage field — state *is* the stage pointer.

| Issue state (configurable)     | Stage               | What the agent does |
|--------------------------------|---------------------|---------------------|
| `working_state` / any active non-pipeline state (e.g. `Todo`, `Backlog`, `In Progress`) | `coding` | Implement the issue in an isolated workspace. |
| `pipeline.review_state` (e.g. `In Review`) | `review` | Internal high-confidence review before approval. |
| `pipeline.review_resolution_state` (optional, only if enabled) | `review_resolution` | Autonomously resolve formal PR/code-review feedback. |
| `pipeline.merge_state` (e.g. `Approved`) | `merge` | Merge the branch to base and finalize. |

Stage resolution order (first match wins): `review_resolution` → `review` →
`merge` → else `coding`.

## 2. State transitions (the loop)

```
        pick up candidate
Todo ─────────────────────────► In Progress        (dispatch: coding starts)
(working_state transition happens BEFORE the agent runs, only for coding)

In Progress ──[coding done]───► In Review          (review_state)
In Review   ──[review done]───► Approved            (merge_state)
                    │  (if review_resolution enabled: In Review → Review Resolution → Approved)
Approved    ──[merge done]────► Done                (done_state; branch merged into base first)
```

Concrete transition functions:

- **coding complete** → move issue to the first available of
  `[review_state, "In Review", "Review", "Done", "Completed"]` (the
  `completionStatePreferences` list). Normally lands on `review_state`.
- **review complete** → move to `merge_state`
  (or to `review_resolution_state` if review-resolution is enabled).
- **review_resolution complete** → parse the agent's final
  `SIMPHONY_REVIEW_DECISION:` directive:
  - `approved` → move to `merge_state`
  - `retry` → run another review_resolution pass; after
    `review_resolution.max_attempts` passes, escalate.
  - `escalate` → move to `review_resolution.escalation_state`
    (default = `review_state`) and add an escalation comment.
- **merge complete** → run `verify.commands`, merge the issue branch into
  `workspace.base_branch` (or open/merge a GitHub PR if `github.enabled`),
  then move to `done_state`. On merge failure, comment and retry.

The `working_state` transition (`→ In Progress`) happens **at dispatch, before**
the coding agent runs, and only when the current state is not already
`working_state`. Review / merge / review_resolution stages do **not** get a
pre-run transition — they run in-place and only transition on completion.

## 3. Config defaults (WORKFLOW.md front matter)

```yaml
polling:
  interval_ms: 30000            # loop cadence
tracker:
  kind: linear
  # NOTE: list EVERY pipeline state you want processed. The harness auto-appends
  # working_state + merge_state (and review_resolution_state when enabled) to the
  # active set, but does NOT auto-append review_state — so `In Review` must be
  # listed explicitly or the review stage never runs. (Go: internal/config/
  # workflow.go appends working_state @378, merge_state @470, done_state→terminal
  # @471; there is no review_state append.) The raw Go default is just
  # [Todo, In Progress]; this fuller list is the recommended explicit config.
  active_states: [Backlog, Todo, In Progress, In Review, Approved]
  working_state: In Progress
  completion_states: [In Review, Review, Done, Completed]
  terminal_states: [Closed, Cancelled, Canceled, Duplicate, Done]  # done_state auto-added
  skip_labels: [human-led, simphony:blocked]  # never dispatch issues with these
pipeline:
  review_state: In Review
  merge_state: Approved
  done_state: Done
  escalation_state: Blocked        # where stuck work lands (see §8); a distinct
                                   # state or a `simphony:blocked` label if no state
  # review_resolution_state: (optional; enables the review_resolution stage)
review_resolution:                # only active if enabled AND review_resolution_state set
  enabled: false
  max_attempts: 3
  escalation_state: <review_state>
  require_checks_green: true
  require_code_review_approval: true
  unresolved_comment_policy: fix_or_explain
  escalate_on: []
workspace:
  mode: git_worktree
  repo: .
  root: ./simphony_workspaces
  base_branch: main
  branch_prefix: simphony/
  cleanup_worktrees: false
agent:
  max_concurrent_agents: 10
  max_turns: 20
  max_attempts: 3                 # per (issue, stage) tries before escalation (§8)
verify:
  commands: []                    # e.g. [go test ./...]
  timeout_ms: 600000
github:
  enabled: false                  # if true, merge via PR + wait for checks
```

Unknown front-matter keys are ignored (forward compatibility).

**Effective active set (emulator rule).** The real harness computes the set of
states it polls by auto-appending pipeline states to `active_states`. The
emulator has none of that code, so when it fetches candidates it must use:

```
effective_active = active_states
                 ∪ {working_state, review_state, merge_state}
                 ∪ {review_resolution_state}   # only if review_resolution enabled
                 − terminal_states
```

Always include every pipeline state here even if the user's `active_states`
omits one, or that stage's issues are invisible and silently never processed.

**Branch & base resolution.** For each issue:
- **Branch** = the tracker's suggested branch when it provides one (Linear's
  `gitBranchName`), else `workspace.branch_prefix + slug`. Prefer the tracker's
  name so worktrees match the PR/branch convention already on the board.
- **Base** = the **current remote tip** `origin/<workspace.base_branch>`, fetched
  first. Do **not** branch off the local base — Simphony works in worktrees, so
  the local base branch drifts and a stale base causes needless merge conflicts.
  (`scripts/worktree.sh` / `.ps1` do the fetch + `WT_BRANCH`/`-Branch` override.)
- **Worktree dir** = the **lowercased, sanitized identifier** (`sanitize()` in
  the scripts, e.g. `GEE-197` → `.../gee-197`). Always refer to worktrees by that
  canonical form so paths and `git worktree list` greps are stable across runs.

## 4. Candidate selection & ordering

Fetch issues in `tracker.active_states` for the configured project. **Skip** an
issue when it is:

- already running, already claimed, or completed during this run,
- in a terminal state, or outside the active-state set,
- carrying a **skip label** (`tracker.skip_labels`) — always includes the
  escalation marker `simphony:blocked`, plus any human-only markers the project
  configures (e.g. `human-led`). This is how an escalated issue leaves the
  active set without a dedicated state, and how the agent avoids grabbing work a
  human owns on a shared board.
- in `Todo` **and** blocked by a non-terminal blocker (inverse `blocks`
  relation). Issues in other active states are not subject to the block rule.

**Scope filter (optional).** When the invocation narrows the work — "work the
**milestone 1.5** tickets", "only the `ws:server` label", "this cycle" — apply it
as an extra candidate filter *on top of* the effective active set: keep only
issues whose `projectMilestone` / `label` / `cycle` matches (map a spoken name
like "1.5" to the concrete milestone, e.g. `Phase 1.5 — MSP + Mobile`). Nothing
else changes — same stages, ordering, concurrency; the scope just shrinks the
candidate pool. The completion condition should scope to the same set (drain =
zero *in-scope* issues remain active).

**Dispatch order:** by priority (Linear Urgent=1 → Low=4 first; **None=0 is
treated as lowest and ordered last**), then `created_at` oldest first, then
identifier lexicographic.

Concurrency is capped by `agent.max_concurrent_agents` (global) and any
per-state limits.

## 5. Soft pause / single-stage focus

The harness supports pausing individual stages (`coding`, `review`,
`review_resolution`, `merge`) and the whole project. When a stage is paused,
issues that resolve to that stage are **deferred, not processed** — they stay in
their current state and are retried on the next tick once unpaused.

"Focus on one stage" = pause every stage *except* the target. The skill
implements this directly with a `stage=<name>` argument: it only dispatches
issues whose resolved stage matches, and leaves all others untouched.

## 6. Comments & log coalescing (observability without spam)

Post tracker comments at these points (mirrors `postStatusComment` /
`postAgentComment`):

- coding / review / merge **failed**: the error + that a retry will happen.
- review complete: a concise review summary (from `stages/review.md`).
- review_resolution start (policy) / approved / retry scheduled / escalated.
- escalation (any stage, §8): why it escalated and after how many attempts.
- stage success transitions (optional): a one-line "moved to <state>".

### Coalesce repeats — never post the same message twice
The real harness posts a fresh comment on every tick, so a wedged issue (e.g. an
auth failure that retries for an hour) buries the log under dozens of identical
lines. The emulator **must not** do that. Each simphony comment carries a
machine-readable marker and a content **signature**; before posting, coalesce:

1. **Tag every comment** with a trailing marker the orchestrator can find and
   match on later:
   `<!-- simphony:kind=<kind> sig=<signature> -->`
   where `kind` ∈ {`coding-failed`,`review-failed`,`merge-failed`,
   `retry-scheduled`,`escalated`,`review-summary`,`rr-*`,`stage-done`,…}.

   **Signature recipe (pin this so every agent computes the same value).**
   `signature = kind + ":" + shorthash(normalize(salient_text))` where:
   - `salient_text` = for a failure, the worker's `error` (or the failing
     command + error class); for other kinds, the one-line status.
   - `normalize(s)`: lowercase; then delete volatile tokens so only the stable
     core remains — ISO timestamps & clock times, `attempt N/M` and `×N`
     counters, durations/ms, PIDs, commit SHAs, UUIDs, absolute paths, and
     `line:col` numbers; collapse runs of whitespace to one space; trim.
   - `shorthash(x)` = first 8 hex of `sha256(x)` (e.g.
     `printf '%s' "$x" | sha256sum` → first 8 chars). If hashing isn't handy,
     the documented fallback is a kebab slug of the first ~6 normalized words.

   Result: two identical auth failures produce the **same** `sig` (they coalesce)
   while a genuinely different error produces a different `sig` (new comment).
2. **Before posting**, read the issue's most recent comments (`list_comments`,
   newest first) and find the latest one carrying a simphony marker.
3. **If its `sig` matches** the comment you're about to post → **update that
   comment in place** (`save_comment` with its `id`) instead of adding a new
   one: bump a repeat counter and refresh the "last seen" line, e.g.

   ```
   **Simphony coding failed** 🔁 ×7
   authentication_failed
   first: 20:14Z · last: 21:03Z · attempt 7/∞→escalates at max_attempts
   <!-- simphony:kind=coding-failed sig=a1b2c3 -->
   ```
4. **If no match** (a genuinely new event, or a state transition) → post a
   **new** comment.

Net effect: a failure loop collapses to **one** self-updating comment plus
**one** escalation comment at the `max_attempts` cap (§8), instead of dozens.
Only distinct events and transitions create new comments.

### Alternative: one pinned run-log comment
For an even quieter board, keep a **single** `<!-- simphony:kind=run-log -->`
comment per issue and edit it each tick with a rolling tail of the last K
events (timestamp · stage · outcome). One comment, full history, zero spam.
Use this when reviewers prefer a compact audit trail over per-event comments.

## 7. Recovery model

No database. State lives in the tracker + the filesystem worktrees. On restart,
re-derive everything by re-reading issue states and existing worktrees; an issue
in `In Review` is simply a `review`-stage item, etc. This is why the tracker
state must always be kept authoritative and up to date.

## 8. Retries, attempts, and escalation

Failures must **converge**, not loop forever — a persistently failing issue that
keeps re-dispatching burns an unbounded budget (this is the real orchestrator's
weak spot: coding failures retry with backoff indefinitely). The emulator adds a
per-(issue, stage) **attempt cap** so stuck work escalates instead of thrashing.

- Track an attempt count per (issue, stage), keyed off the tracker so it
  survives restart: the count = number of `simphony:attempt` markers, or simply
  re-derive from the last N escalation/failure comments on the issue.
- On a worker `failed`/`retry`: increment the attempt count and reschedule with
  exponential backoff (base `max_retry_backoff_ms`, capped). **Do not advance.**
- When `attempts >= agent.max_attempts` (default 3): **escalate** —
  - move the issue to `pipeline.escalation_state` if one is configured, else
    apply a `simphony:blocked` label (so it drops out of the active set), and
  - post an escalation comment (why, attempt count, last error), and
  - release the claim + optionally keep the worktree for a human.
- `review_resolution` keeps its own `max_attempts` + `escalation_state`
  (§2); this general cap governs coding / review / merge failures.

Escalated / blocked issues are **terminal-for-now**: they leave the active set
and count as "done-for-now" for a `/goal` completion condition, so one wedged
issue never blocks a backlog drain.
