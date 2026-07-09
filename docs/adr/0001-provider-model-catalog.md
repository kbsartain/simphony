# ADR-0001: Provider/Model Configuration Catalog

**Status:** Accepted
**Date:** 2026-07-09
**Deciders:** kbsartain (project owner)

## Context

Simphony drives coding agents against multiple upstream model vendors (Anthropic,
z.ai, OpenAI, Kimi/Moonshot) through two local transports: the **Claude CLI**
(`runClaude` in `internal/agent/runner.go`) and the **Codex app-server**. Runtime
configuration is a single flat struct, `api.AgentRuntimeConfig`
(`pkg/api/types.go:152`), resolved across four layers (low → high precedence):

1. Registry default — `agent_runtime:` in `simphony.yaml`
2. Provider block — `claude:` / `codex:` in a project `WORKFLOW.md`
3. Project runtime — `agent_runtime:` in `WORKFLOW.md`
4. Stage overrides — `stage_overrides.{review,merge,review_resolution}`

Every layer repeats the same fields: `provider`, `model_provider`, `model`,
`endpoint_url`, `api_key`/`auth_token`, `reasoning_effort`, tool filters, sandbox,
timeouts (`applyRuntimeCommon`, `internal/config/workflow.go:880`).

### Forces at play

- **Two meanings of "provider," unvalidated.** `provider` selects the *transport*
  (`claude` vs `codex`); `model_provider` is a free-text *vendor* label. For the
  claude transport the real vendor is chosen implicitly by `endpoint_url` + key,
  so `model_provider` is effectively decorative. Observed drift today: both
  `WORKFLOW.md` files declare `model_provider: anthropic` while `endpoint_url` is
  z.ai serving `glm-5.2`; `simphony.yaml` labels the same setup `model_provider:
  zai`. Three sources, three answers, no error.
- **No catalog.** Each vendor's canonical base URL
  (`https://api.z.ai/api/anthropic`), auth style (`x-api-key` header vs bearer via
  `ANTHROPIC_AUTH_TOKEN`), model list, and per-model thinking support live
  nowhere. Users hand-copy URLs and model ids; typos fail silently at agent
  launch rather than at config load. This is the same failure class as the
  variadic-flag hang fixed in commit `81e7dda`.
- **Single-slot credentials.** One `api_key`/`auth_token` per resolved config.
  Switching vendors means swapping the key inline. Env-var references
  (`$ZAI_API_KEY`, resolved by `ResolveEnvVar`, `workflow.go:1163`) were just
  introduced and are the first step toward per-vendor key slots.
- **Thinking levels are not model-aware.** `reasoning_effort` is normalized to one
  generic scale (`normalizeReasoningEffort`) and forwarded regardless of whether
  the selected model accepts it. Anthropic Opus, z.ai GLM, and Kimi-K2 differ; some
  models take no thinking level at all.

### Constraints

- The harness runs live and unattended; config reloads on `WORKFLOW.md` change via
  the fsnotify watcher. Changes must not force a flag-day migration of existing
  `WORKFLOW.md` files or break in-flight runs.
- Solo maintainer. Prefer incremental, reversible steps over a big-bang rewrite.
- The catalog is small and slow-moving; correctness and clarity matter more than
  dynamic discovery of models.

## Decision

Introduce a **built-in, user-extensible provider catalog**. Each catalog entry is a
*vendor* keyed by name and carries the facts that are currently scattered or
implicit: transport, base URL, auth style, and a model list with per-model
supported thinking levels.

Projects and stages then **select** `provider` (a catalog vendor) + `model` and
inherit `base_url`, auth wiring, transport, and valid thinking levels from the
catalog. Explicit `endpoint_url`/`api_key`/`auth_token` remain permitted as
per-selection overrides. Config load **validates** the selection against the
catalog and **warns** (does not fail, during rollout) on mismatches such as
`model_provider: anthropic` with a z.ai endpoint.

Ship it in three non-breaking phases:

1. **Catalog + validation warnings.** Add the built-in catalog (in Go, overridable
   from `simphony.yaml`) and emit load-time warnings for unknown models, unknown
   thinking levels, and vendor/endpoint mismatches. No behavior change.
2. **Selection derivation.** Let `provider:` name a catalog vendor; derive
   `base_url`, auth env/style, and transport from the entry when not explicitly
   set. Keep the flat `claude:`/`codex:` blocks working as-is.
3. **Model-aware thinking + dashboard picker.** Enforce per-model thinking-level
   validity (warn → optionally error) and surface a provider/model picker in the
   dashboard driven by the catalog.

### Proposed shape

```yaml
providers:                     # built-in defaults in code; overridable in simphony.yaml
  anthropic:
    transport: claude
    base_url:  <anthropic default>
    auth: { env: ANTHROPIC_API_KEY, style: x-api-key }
    models:
      claude-opus-4-8: { thinking: [none, low, medium, high, xhigh] }
  zai:
    transport: claude            # Anthropic-compatible, driven through the Claude CLI
    base_url:  https://api.z.ai/api/anthropic
    auth: { env: ZAI_API_KEY, style: bearer }     # -> ANTHROPIC_AUTH_TOKEN
    models: { glm-5.2: { thinking: [low, medium, high] } }
  openai: { transport: codex, auth: { env: OPENAI_API_KEY }, models: { ... } }
  kimi:   { transport: claude, base_url: https://api.moonshot.ai/anthropic,
            auth: { env: KIMI_API_KEY, style: bearer }, models: { kimi-k2: { ... } } }

agent_runtime:                 # per project / registry / stage
  provider: zai                # names a catalog vendor, not a transport
  model: glm-5.2
  reasoning_effort: high        # validated against glm-5.2's thinking list
```

## Options Considered

### Option A: Built-in extensible catalog (proposed)

Ship provider defaults in Go; allow `simphony.yaml` to add/override entries;
projects select `provider + model` and inherit the rest.

| Dimension | Assessment |
|-----------|------------|
| Complexity | Medium — new type + validation + phased wiring |
| Cost | One-time build; small ongoing catalog upkeep |
| Scalability | Good — new vendor = one catalog entry |
| Team familiarity | High — same YAML/layering model |

**Pros:** single source of truth for base_url/auth/models; fail-fast at load;
one-line vendor switch; back-compatible; keeps per-selection overrides.
**Cons:** catalog needs maintenance as models change; two selection styles (flat
legacy + catalog) coexist during migration.

### Option B: Status quo — flat fields, hand-copied endpoints

Keep `provider` + `model_provider` + inline `endpoint_url`/keys.

| Dimension | Assessment |
|-----------|------------|
| Complexity | Low (no change) |
| Cost | Ongoing — silent misconfig, per-project duplication |
| Scalability | Poor — every project re-specifies vendor facts |
| Team familiarity | High |

**Pros:** zero work; maximally flexible.
**Cons:** the four problems above persist; misconfig surfaces as runtime exit-1,
not load errors; `model_provider` stays decorative and drifts.

### Option C: Strict external catalog file, mandatory selection

A required `providers.yaml`; remove the flat blocks; every project must select from
the catalog.

| Dimension | Assessment |
|-----------|------------|
| Complexity | High — flag-day migration |
| Cost | High up front |
| Scalability | Good |
| Team familiarity | Medium |

**Pros:** cleanest end state; no legacy path.
**Cons:** breaks every existing `WORKFLOW.md` at once; risky on a live unattended
harness; contradicts the incremental constraint.

## Trade-off Analysis

The core tension is **clean end-state vs. migration risk on a live system**.
Option C reaches the cleanest model but demands a flag-day rewrite of running
projects — unacceptable while the harness is unattended and reloads config on
save. Option B has no migration cost but leaves the exact silent-misconfig class
that already produced a production hang. Option A captures Option C's benefits (one
source of truth, fail-fast validation) while retaining Option B's zero-migration
property through phase 1's warn-only behavior, deferring any hard enforcement to
phase 3 after real configs have been observed against the catalog.

Auth style is the subtlest catalog field: Anthropic-native uses the `x-api-key`
header, while z.ai/Kimi Anthropic-compatible endpoints authenticate via
`ANTHROPIC_AUTH_TOKEN` (bearer). Today `runClaude` sets **both** env vars
defensively (`runner.go:618`), which works but hides the distinction. Encoding
`auth.style` per vendor makes the requirement explicit and lets the runner stop
blanket-setting both.

## Consequences

**Easier**
- Adding a vendor: one catalog entry vs. editing every project.
- Diagnosing config: unknown model / bad thinking level / vendor-endpoint mismatch
  caught at load with a clear message.
- Per-vendor keys: `auth.env` names the variable; supports multiple vendors at once.

**Harder / new burden**
- Catalog upkeep as vendors add/rename models.
- Two selection styles coexist through phases 1–2; docs must cover both.
- `simphony.yaml` gains a `providers:` section to document and test.

**To revisit**
- Whether phase 3 turns thinking-level validation from warn into a hard error.
- Whether to eventually deprecate the flat `claude:`/`codex:` blocks (a future ADR).
- Whether the catalog should ever be fetched/refreshed rather than compiled in.

## Action Items

1. [ ] Phase 1 — Add `api.ProviderCatalog`/`ProviderEntry` types and a built-in
       default catalog (anthropic, zai, openai, kimi).
2. [ ] Phase 1 — Parse an optional `providers:` override in `simphony.yaml`.
3. [ ] Phase 1 — Add load-time validation warnings: unknown model, unknown/invalid
       thinking level, `model_provider`/`endpoint_url` mismatch.
4. [ ] Phase 2 — Resolve `provider:` against the catalog to derive
       `base_url`, auth env/style, and transport when not explicitly set.
5. [ ] Phase 2 — Simplify `runClaude` auth wiring to honor `auth.style`.
6. [ ] Phase 3 — Enforce per-model thinking validity; add a dashboard
       provider/model picker sourced from the catalog.
7. [ ] Docs — Update `docs/configuration.md` with the catalog and migration notes.
8. [ ] Migration — Reconcile the current `model_provider: anthropic` vs z.ai
       endpoint drift in both `WORKFLOW.md` files to `provider: zai`.
