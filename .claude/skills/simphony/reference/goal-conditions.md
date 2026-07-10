# Running Simphony under `/goal`

`/goal` (a recent Claude Code / Codex build with the goals feature enabled —
confirm your build supports it) supplies the autonomous loop + stop condition;
this skill supplies the per-turn procedure. A separate evaluator model checks the
condition after every turn, so the condition **must be mechanically checkable
against evidence**. Simphony's design makes that easy: the tracker board is the
evidence — "done" is a database query.

## Evidence convention (do this every turn)
At the end of each turn, print one greppable board snapshot so the evaluator has
clean evidence and you get an audit trail:

```
BOARD: backlog=0 todo=0 in_progress=1 in_review=2 approved=0 done=14 blocked=1
```

Keys are the configured state names lowercased; `blocked` counts issues in the
`escalation_state` **or** carrying the `simphony:blocked` label (§8). Treat
**escalated/blocked + terminal states as acceptable "done-for-now"** — the
attempt cap (`agent.max_attempts`) escalates a wedged issue there instead of
looping, so it leaves the active set and never burns the budget. Always include a
turn or time cap as a backstop.

## Templates

### Single issue
```
/goal Work ENG-123 to completion using the simphony skill.
Done = Linear issue ENG-123 is in state "Done", branch simphony/eng-123 is merged
into main, and `go test ./...` exits 0. Prove it by querying Linear for the issue
state and running the tests. Do not modify any issue except ENG-123. Stop after 25 turns.
```

### Backlog drain (poll loop)
```
/goal Act as the simphony orchestrator over the <project> board using the simphony skill.
Done = zero issues remain in the effective active states (Backlog, Todo, In Progress,
In Review, Approved); every issue has reached Done, the escalation/Blocked state (or the
`simphony:blocked` label), or a terminal state. Prove by querying Linear and confirming the
active-state count is 0. Only touch project <project>. Stop after 4 hours or 200 turns.
```

### Scoped drain (e.g. a milestone)
```
/goal Work every ticket in milestone "Phase 1.5 — MSP + Mobile" on the <project>
board using the simphony skill. Done = zero in-scope issues remain in the active
states (each reached Done, the escalation/Blocked state or `simphony:blocked`
label, or a terminal state). Prove by querying Linear for issues in that
milestone and confirming the in-scope active-state count is 0. Only touch issues
in that milestone; skip `human-led`. Stop after 4 hours or 200 turns.
```

### Stage focus
```
/goal Run the simphony skill in stage=review mode.
Done = no issues remain in "In Review" (each advanced to Approved or beyond, or was
escalated). Prove by querying Linear for the In Review count == 0. Do not transition or
edit issues in any other state. Stop after 40 turns.
```

## Caveats
- `/goal` drives one orchestrator loop, but each turn the orchestrator fans out
  to **parallel worker subagents** (up to `max_concurrent_agents`), so Simphony's
  concurrency is preserved within a single `/goal` session. The turn boundary is
  when the worker batch returns. The base-branch **merge stays single-threaded**
  (merge lock) even under parallel workers.
- The evaluator only judges what it can see — keep the BOARD snapshot and any
  proof commands (test runs, tracker queries) in the transcript.
- Bound every goal with a turn/time cap; pair the cap with escalation states in
  the acceptable-terminal set so stuck work escalates rather than loops.
