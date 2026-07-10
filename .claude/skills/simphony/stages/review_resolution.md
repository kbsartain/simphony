# Stage: review_resolution (optional)

Only active when `review_resolution.enabled: true` **and**
`pipeline.review_resolution_state` is set. Triggered when the issue is in that
state. Sits between `review` and `merge`, and handles **formal PR / code-review
feedback** autonomously.

## On start
Post a status comment summarizing policy:

```
Simphony review resolution started
Autonomous PR/code-review resolution is running.

Policy:
- Require checks green: {require_checks_green}
- Require review approval: {require_code_review_approval}
- Unresolved comments: {unresolved_comment_policy}
```

## Instructions (verbatim from pipelineStage)

> Resolve formal PR/code-review feedback for issue {{ issue.identifier }}
> autonomously. Inspect the PR, unresolved review comments, review decision, and
> CI/check results using the repository's configured GitHub tooling. Fix
> actionable feedback, reply to comments when appropriate, rerun relevant
> checks, and push updates. Require checks green: {require_checks_green}. Require
> review approval: {require_code_review_approval}. Unresolved comment policy:
> {unresolved_comment_policy}. Escalate on: {escalate_on}. End your final
> response with exactly one directive line: `SIMPHONY_REVIEW_DECISION: approved`,
> `SIMPHONY_REVIEW_DECISION: retry`, or `SIMPHONY_REVIEW_DECISION: escalate`.

Use the **`gh-address-comments`** and **`gh-fix-ci`** skills for this stage where
available (mirrors `agent_runtime.stage_overrides.review_resolution.skills`).

## Decision handling (orchestrator applies it)
The worker ends with a `SIMPHONY_REVIEW_DECISION:` line and returns it as
`decision`. The **orchestrator** parses the last such line and acts:
- `approved` (also the default if no directive) → move to `pipeline.merge_state`;
  post `Simphony review resolution approved`.
- `retry` → increment attempt counter; if `attempt <= max_attempts` re-dispatch
  another pass (post `Simphony review resolution retry scheduled — Attempt N of M`),
  else escalate.
- `escalate` → move to `review_resolution.escalation_state` (default
  `review_state`) and comment `**Simphony review resolution escalated** …`.

Defaults: `max_attempts: 3`, `require_checks_green: true`,
`require_code_review_approval: true`, `unresolved_comment_policy: fix_or_explain`.
