# Security Remediation Backlog

Last updated: 2026-10-22

Review baseline: `be9e15938b9d29a1ff52348595b7add3b26b6928`

Plan: [Security Remediation Plan](security-remediation-plan.md)

This is the durable implementation and session-handoff record for correcting Simphony's identified security vulnerabilities. Development is performed directly in the Simphony repository. Simphony must not orchestrate changes to its own source tree, and deployment/restart remains an operator action.

## Working Agreement

- Complete work in the dependency order below. Authentication and local-only binding precede feature work that expands access.
- Keep projects paused and the service stopped or loopback-constrained until `SEC-001` through `SEC-013` pass their negative tests.
- Implement every security control in both single-workflow and project-registry server modes.
- Treat tracker text, endpoint URLs, redirect targets, HTTP responses, and masked client fields as untrusted input.
- Never use a real credential in SSRF, redirect, prompt-injection, or logging tests. Use fake servers and sentinel values.
- A ticket is not complete until its acceptance criteria and verification commands pass and relevant documentation is updated.
- Preserve unrelated worktree changes. Record incomplete work in **Session Handoff** before ending a session.
- Attach verification evidence to the ticket or commit. Do not mark a finding fixed based only on code inspection.

## Priority and Sizing

- **P0**: active critical exposure; containment or prerequisite for safe testing.
- **P1**: critical/high remediation required before normal unattended use.
- **P2**: medium hardening or assurance required before the remediation release is complete.
- Sizes are planning estimates: **S** (focused), **M** (multi-file), **L** (cross-cutting/migration).

## Backlog Summary

The status column and **Session Handoff** are the live completion record. The detailed checklists below retain the original review decomposition for traceability; an implemented control may use a stricter or simpler design than the original proposal.

| ID | Priority | Work item | Status |
| --- | --- | --- | --- |
| `SEC-000` | P0 | Apply and document immediate containment | Operator action pending |
| `SEC-001` | P0 | Make every server loopback-only by default | Implemented and verified |
| `SEC-002` | P0 | Add control-plane authentication | Implemented and verified |
| `SEC-003` | P0 | Replace wildcard CORS with exact trusted origins | Implemented and verified |
| `SEC-004` | P0 | Enforce CSRF and mutation-origin policy | Implemented and verified |
| `SEC-005` | P1 | Enforce route permissions | Implemented and verified |
| `SEC-006` | P1 | Add authorization negative tests | Implemented and verified |
| `SEC-010` | P0 | Bind Linear credentials to an approved origin | Implemented and verified |
| `SEC-011` | P0 | Bind model-provider credentials to approved origins | Implemented and verified |
| `SEC-012` | P0 | Block SSRF destinations and redirect escapes | Implemented and verified |
| `SEC-013` | P1 | Bound outbound requests and responses | Implemented and verified |
| `SEC-020` | P1 | Replace agent environment inheritance with an allowlist | Implemented and verified |
| `SEC-021` | P1 | Make Codex workspace sandboxing the secure default | Implemented and verified |
| `SEC-022` | P1 | Define hardened Claude tool and permission defaults | Implemented and verified |
| `SEC-023` | P1 | Gate dispatch on trusted issue provenance | Implemented and verified |
| `SEC-024` | P1 | Document and validate scoped service identity and egress | Documentation complete; operator validation pending |
| `SEC-025` | P2 | Frame tracker content as untrusted prompt data | Implemented and verified |
| `SEC-030` | P1 | Upgrade, pin, and attest the Go toolchain | Implemented and verified |
| `SEC-031` | P1 | Upgrade Vite and vulnerable transitive dependencies | Implemented and verified |
| `SEC-032` | P1 | Add Go and npm vulnerability gates to CI | Implemented and verified |
| `SEC-033` | P1 | Add secret scanning and redaction fixtures to CI | Implemented and verified |
| `SEC-034` | P1 | Patch and continuously audit the root Agent SDK dependency tree | Implemented and verified |
| `SEC-040` | P2 | Add inbound HTTP server timeouts | Implemented and verified |
| `SEC-041` | P2 | Add route-specific request and response limits | Implemented and verified |
| `SEC-042` | P2 | Add bounded rate limits and browser security headers | Implemented and verified |
| `SEC-050` | P2 | Add redacted audit logging for privileged actions | Implemented and verified |
| `SEC-051` | P1 | Build the adversarial security regression suite | Implemented and verified |
| `SEC-052` | P2 | Update security, API, configuration, and operations docs | Implemented and verified |
| `SEC-053` | P1 | Perform final validation and controlled redeployment | Code validation complete; operator redeployment pending |

### Implemented design decisions

- Direct remote binding is rejected in all modes. Remote use requires Simphony to remain on loopback behind an authenticated TLS reverse proxy.
- Automation uses bearer authentication. Browsers use the built-in HTTP Basic challenge (`simphony` plus the operator token), so no credential is exposed to dashboard JavaScript or browser storage.
- Because the browser flow has no cookie session, CSRF protection is enforced with exact same/trusted `Origin` or `Referer` validation plus JSON-only unsafe methods. Bearer clients without browser-origin headers remain supported.
- The first release has one operator role. A single outer middleware protects the complete mux, including dashboard assets, API routes, catch-alls, and unknown paths; there is no anonymous operational route.
- The authentication limiter has both a global failure threshold and a fixed 1,024-source memory bound.

## Phase 0: Immediate Containment

### `SEC-000` Apply and document immediate containment

**Finding:** The reviewed service exposes privileged operations without authentication; no listener was active at review time, which provides a safe containment window.

- [ ] Keep production Simphony stopped until a test session is needed.
- [ ] Confirm every test invocation binds only to `127.0.0.1` and is blocked from LAN ingress by the host firewall.
- [ ] Keep all projects and stages paused; use fake tracker/provider services for security tests.
- [ ] Inventory endpoints to which Linear and provider credentials may have been sent.
- [ ] Rotate any credential whose destination history cannot be verified.
- [ ] Back up `simphony.yaml` and active `WORKFLOW.md` files before migration.
- [ ] Add a concise containment/recovery checklist to operations documentation.

Acceptance criteria:

- `Get-NetTCPConnection` or an equivalent check shows no wildcard/public Simphony listener.
- The containment checklist identifies the operator, timestamp, credential-rotation decision, and configuration backup location without recording secret values.
- No live project can dispatch during adversarial testing.

Verification:

```powershell
Get-NetTCPConnection -State Listen | Where-Object LocalPort -eq 8080
```

## Phase 1: Authenticated, Local-Only Control Plane

### `SEC-001` Make every server loopback-only by default

**Finding:** `internal/server/server.go:70` constructs `:%d`, which listens on every interface in single-workflow mode.

- [ ] Centralize listen-address resolution for single-workflow and registry modes.
- [ ] Default an omitted host to `127.0.0.1`, not `0.0.0.0`, `::`, or an empty host.
- [ ] Validate configured addresses before starting the listener.
- [ ] Reject non-loopback binding unless authentication is configured and an explicit remote-access switch is enabled.
- [ ] Log the normalized listen address and whether remote access is enabled, without logging credentials.
- [ ] Add IPv4 and IPv6 loopback/wildcard table tests for both server modes.

Acceptance criteria:

- A default startup listens only on loopback in both modes.
- Wildcard and LAN binds fail closed unless the explicit remote prerequisites are satisfied.
- Existing explicit loopback configurations remain compatible.

Verification:

```text
go test ./internal/server/... ./internal/project/...
go test ./...
```

### `SEC-002` Add control-plane authentication and dashboard session handling

**Finding:** API state and mutation routes have no inbound authentication.

- [ ] Define a provider-neutral `Principal` and authentication middleware shared by both server modes.
- [ ] Source the bootstrap/operator secret from an environment or OS-backed reference, never a literal workflow/API field.
- [ ] Support `Authorization: Bearer` for non-browser clients using constant-time credential comparison.
- [ ] Add a browser login/bootstrap flow that exchanges the operator secret for a short-lived in-memory session.
- [ ] Store the browser session only in a `HttpOnly`, `SameSite=Strict` cookie; set `Secure` for HTTPS deployments and never use `localStorage` for the credential.
- [ ] Define expiration, logout, invalidation on secret rotation, and bounded failed-login behavior.
- [ ] Require authentication for the dashboard and all API routes except a minimal liveness endpoint containing no operational data.
- [ ] Fail startup on a non-loopback bind when valid authentication configuration is absent.
- [ ] Redact credentials and session identifiers from logs and API errors.

Acceptance criteria:

- Anonymous requests cannot read project/configuration state or invoke mutations.
- Valid bearer and browser-session clients work without exposing the bootstrap secret to JavaScript storage.
- Invalid, expired, rotated, and malformed credentials return uniform `401` responses without a timing-relevant or descriptive secret oracle.
- Authentication behavior is identical in both server modes.

Verification:

```text
go test ./internal/server/... -run Auth
go test ./...
cd dashboard && npm run build
```

### `SEC-003` Replace wildcard CORS with an exact trusted-origin policy

**Finding:** `internal/server/server.go:204` and `internal/server/projects.go:150` permit wildcard origins and mutation methods.

- [ ] Remove wildcard `Access-Control-Allow-Origin` behavior.
- [ ] Disable cross-origin API access by default.
- [ ] When explicitly configured, normalize and match exact scheme/host/effective-port origins.
- [ ] Reject wildcard origins when credentials or cookies are enabled.
- [ ] Return `Vary: Origin` for allowlisted cross-origin responses.
- [ ] Limit allowed methods and headers to the routes that require them.
- [ ] Add preflight tests for allowed, denied, `null`, malformed, case/port, and subdomain-confusion origins.

Acceptance criteria:

- A webpage hosted on an unapproved origin cannot read or mutate localhost Simphony.
- Same-origin dashboard requests continue to work.
- CORS denial never falls back to `*` and never reflects an arbitrary request origin.

Verification:

```text
go test ./internal/server/... -run 'CORS|Origin'
```

### `SEC-004` Enforce CSRF and mutation-origin policy

**Finding:** Browser-reachable mutation routes do not require a CSRF proof or trusted origin.

- [ ] Inventory every state-changing route and classify its supported authentication modes.
- [ ] Require a trusted `Origin` for browser/cookie-authenticated mutations.
- [ ] Issue and validate a session-bound CSRF token using a documented header pattern.
- [ ] Require the expected JSON content type and reject form/simple-request mutation payloads.
- [ ] Define deliberate behavior for non-browser bearer clients that omit `Origin`.
- [ ] Reject missing/mismatched CSRF tokens before parsing or applying request data.
- [ ] Add tests for forged forms, malicious fetches, absent origin, stale token, token/session mismatch, and valid bearer automation.

Acceptance criteria:

- A malicious website cannot perform settings, project lifecycle, refresh, pause/resume, or other mutations through an authenticated browser.
- CSRF failures have no side effects and produce a consistent `403` response.

Verification:

```text
go test ./internal/server/... -run 'CSRF|Mutation|Origin'
```

### `SEC-005` Classify routes and enforce permissions

- [ ] Create a route inventory covering dashboard assets, liveness, status, issue detail, settings, validation, model catalogs, refresh, project lifecycle, and stage controls.
- [ ] Assign each route a minimum permission: public liveness, authenticated read, or operator mutation.
- [ ] Enforce the permission at route registration or a single auditable policy layer.
- [ ] Keep a future role expansion possible without scattering identity checks through handlers.
- [ ] Ensure unknown and unauthorized project identifiers do not disclose different information to unauthenticated callers.

Acceptance criteria:

- Every registered route has an explicit permission classification.
- Tests fail if a new route is registered without a classification.
- Sensitive settings and masked secret metadata are never public.

### `SEC-006` Add endpoint-wide authorization negative tests

- [ ] Build a table-driven test that exercises every route as anonymous, invalid, authenticated-read, and operator clients.
- [ ] Cover single-workflow and project-registry route trees.
- [ ] Assert both HTTP status and absence of state changes.
- [ ] Include WebSocket/SSE/static-file routes if introduced later.
- [ ] Add a regression case proving a new unclassified mutation route fails the suite.

Acceptance criteria:

- Every sensitive endpoint denies insufficient principals.
- CI runs the matrix and reports the specific route on failure.

## Phase 2: Credential-Origin Binding and SSRF Prevention

### `SEC-010` Bind Linear credentials to an approved origin

**Finding:** `internal/config/workflow.go:359` and `internal/tracker/linear.go:398` allow a configured endpoint to receive the real Linear authorization value.

- [ ] Represent a tracker credential as an origin-scoped reference, not an endpoint-independent string.
- [ ] Normalize origin as scheme, host, and effective port; reject URL userinfo, fragments, and unsupported schemes.
- [ ] Treat the official Linear API origin as a known provider default.
- [ ] Require an explicit, separate credential reference for a custom tracker origin.
- [ ] Clear or reject masked-secret preservation when the normalized origin changes.
- [ ] Prevent validation/test-connection calls from bypassing the origin binding.
- [ ] Add migration logic that binds an existing reference only to its currently configured origin and fails on ambiguity.

Acceptance criteria:

- Changing the tracker URL cannot cause the previous Linear key to be sent to the new host.
- Masked API updates preserve a credential only when the normalized origin is unchanged.
- Logs and errors identify the blocked origin without exposing the credential.

Verification:

```text
go test ./internal/config/... ./internal/tracker/... ./internal/server/... -run 'Origin|Credential|Linear|Masked'
```

### `SEC-011` Bind model-provider credentials to approved origins

**Finding:** `internal/server/model_catalog.go:37` can send provider credentials to an arbitrary catalog endpoint.

- [ ] Define known provider origins and their accepted authentication mechanism.
- [ ] Scope each provider secret reference to an exact origin and execution SDK/provider identity.
- [ ] Require an explicitly selected credential reference for custom origins; never reuse the former origin's masked secret automatically.
- [ ] Apply the same policy to model catalog, preflight, agent runtime, and per-stage overrides.
- [ ] Detect conflicting endpoint-primary credentials so only the intended auth header is sent.
- [ ] Return a safe validation error when a stage/provider/endpoint/credential tuple is incomplete or inconsistent.

Acceptance criteria:

- A settings update that changes the provider origin cannot retain or transmit the old credential.
- Catalog refresh and agent execution resolve the same origin-scoped credential policy.
- Only one intended credential/header reaches the approved endpoint.

Verification:

```text
go test ./internal/config/... ./internal/server/... ./internal/agent/... -run 'Provider|Catalog|Credential|Origin'
```

### `SEC-012` Block unsafe SSRF destinations and redirect escapes

- [ ] Build one hardened outbound URL policy used by tracker validation, model catalogs, and future credential-bearing clients.
- [ ] Allow HTTPS by default; permit HTTP only for an explicit development allowlist.
- [ ] Deny loopback, private, link-local, multicast, unspecified, broadcast, and cloud-metadata address ranges by default for custom remote origins.
- [ ] Resolve and validate every destination at connection time; protect against mixed public/private DNS answers and DNS rebinding.
- [ ] Limit redirect count and revalidate scheme, origin, hostname, and resolved address at every hop.
- [ ] Never forward authorization, cookies, or provider-specific auth headers across an origin change.
- [ ] Reject URL userinfo, ambiguous host encodings, IPv4-in-IPv6 tricks, integer/hex IP forms, and malformed ports.
- [ ] Make any local-development exception static/operator-configured and unavailable through an ordinary API mutation.

Acceptance criteria:

- Requests to metadata, localhost, RFC1918, link-local, and equivalent encoded destinations are denied before credentials are sent.
- Cross-origin redirects arrive at the target without authorization material or are rejected entirely.
- A fake DNS-rebinding test cannot move a validated hostname to a denied address at connect time.

Verification:

```text
go test ./internal/... -run 'SSRF|Redirect|Rebind|Metadata|PrivateAddress'
```

### `SEC-013` Bound outbound requests and responses

- [ ] Centralize outbound transports and clients instead of constructing unbounded defaults in handlers.
- [ ] Configure connect, TLS-handshake, response-header, idle-connection, and total-request timeouts.
- [ ] Set a small redirect limit through the `SEC-012` policy.
- [ ] Limit response bytes before decoding; use route/provider-specific caps for catalog and tracker responses.
- [ ] Reject trailing or multiple JSON values where a single document is expected.
- [ ] Bound diagnostic excerpts and strip control characters before logging errors.
- [ ] Add slow-header, never-ending body, oversized body, compressed-body, and malformed JSON tests.

Acceptance criteria:

- A slow or oversized upstream cannot retain a worker indefinitely or cause unbounded memory growth.
- Limit failures are typed, observable, redacted, and do not retry without bound.

## Phase 3: Agent Isolation and Prompt-Injection Containment

### `SEC-020` Replace agent environment inheritance with an allowlist

**Finding:** `internal/agent/runner.go:934` starts from the entire host environment and removes only a small denylist.

- [ ] Replace parent-environment copying with a documented platform allowlist.
- [ ] Include only required OS process variables, locale, temporary paths, and explicitly configured stage variables.
- [ ] Inject only the selected stage's provider credential; exclude Linear, unrelated providers, cloud credentials, CI tokens, and arbitrary parent variables.
- [ ] Reject attempts to configure protected variables through tracker-derived or API-controlled data.
- [ ] Define Windows and POSIX allowlists and test both deterministically.
- [ ] Redact effective-environment diagnostics while still showing variable names and sources.
- [ ] Add compatibility fixtures for Codex app-server and Claude CLI authentication modes.

Acceptance criteria:

- A sentinel secret placed in the parent environment is absent from every agent process unless explicitly approved for that stage.
- Codex and Claude can still start under supported authenticated configurations.
- No credential is injected into a stage that does not require it.

Verification:

```text
go test ./internal/agent/... -run 'Environment|Credential|Codex|Claude'
```

### `SEC-021` Make Codex workspace sandboxing the secure default

**Finding:** `internal/config/workflow.go:864` defaults Codex sandboxing to `none`; automatic approval maps to `never`, leaving the agent unrestricted but unable to ask for safer escalation.

- [ ] Change the default sandbox policy to `workspace-write` with approval policy `never`.
- [ ] Constrain writable roots to the issue worktree and explicit temporary/cache paths required by the runtime.
- [ ] Prevent workspace path traversal or symlink escape from expanding writable scope.
- [ ] Require a clearly named unsafe configuration override for unsandboxed execution.
- [ ] Emit a high-severity startup/state warning and audit event when the unsafe override is active.
- [ ] Remove unsandboxed settings from default examples and generated configuration.
- [ ] Add migration diagnostics for legacy workflows that relied on implicit `none`.

Acceptance criteria:

- A default Codex stage cannot write outside its worktree or obtain approval to expand access.
- Legacy unsandboxed behavior is never selected silently.
- Workspace preparation, coding, review, and merge compatibility tests pass under the new default.

### `SEC-022` Define hardened Claude tool and permission defaults

- [ ] Establish explicit Claude defaults for permission mode, allowed tools, disallowed tools, setting sources, and workspace scope.
- [ ] Ensure command-line argument construction cannot let variadic tool flags consume the prompt.
- [ ] Prevent stage overrides from inheriting incompatible Codex or overly permissive Claude settings.
- [ ] Require the same explicit unsafe override and warning model used by `SEC-021` for unrestricted host access.
- [ ] Test shell, file-read/write, network-capable tools, and provider-specific credentials in fake Claude processes.

Acceptance criteria:

- A default Claude stage is limited to the issue workspace and approved tool set.
- Unsupported or unsafe tool/permission combinations fail during preflight, before dispatch.
- Cross-provider stage transitions recompute secure provider defaults.

### `SEC-023` Gate dispatch on trusted issue provenance

- [ ] Add a configurable issue trust policy based on an explicit approval label and/or allowlisted author/team identity.
- [ ] Make a fail-closed trust policy the default for new configurations.
- [ ] Keep untrusted issues visible in state with a stable `untrusted_issue` deferred reason, but never dispatch them.
- [ ] Ensure issue text cannot add its own approval; trust signals come from structured tracker metadata.
- [ ] Re-evaluate trust before every new stage and retry so removal of approval stops future work.
- [ ] Document migration for existing unattended projects and provide a dry-run report of issues that would be deferred.

Acceptance criteria:

- An issue from an untrusted author without the required approval signal cannot launch an agent.
- Adding/removing an authorized structured trust signal predictably enables/stops future dispatch.
- Tracker title, description, and comments cannot override the gate.

### `SEC-024` Document and validate scoped service identity and egress

- [ ] Document running Simphony under a dedicated non-administrator OS account.
- [ ] Define minimum scopes for Linear, GitHub, repository, and provider credentials.
- [ ] Document host firewall/proxy rules that restrict agent egress to required providers and source-control endpoints.
- [ ] Add startup diagnostics that report unsafe identity indicators and whether egress controls are operator-managed, without blocking supported local development unexpectedly.
- [ ] Provide separate production and disposable security-test profiles.
- [ ] Verify the release with a non-admin service account and disposable repository.

Acceptance criteria:

- The operations guide contains a concrete least-privilege deployment recipe for Windows and a platform-neutral equivalent.
- The test profile cannot reach production tracker/repository credentials.
- Security diagnostics expose configuration gaps without printing secrets.

### `SEC-025` Frame tracker content as untrusted prompt data

**Finding:** `internal/prompt/render.go:67` places issue title and description directly into agent prompts.

- [ ] Delimit title, description, and comments as untrusted data with stable, escaped boundaries.
- [ ] Add a high-priority instruction that tracker content cannot change system policy, permissions, workspace scope, or credential handling.
- [ ] Avoid interpolating tracker content into shell commands, tool names, configuration keys, or environment variables.
- [ ] Add prompt-rendering tests for boundary injection, Markdown/code fences, control characters, and oversized text.
- [ ] Document that prompt framing is defense in depth and not a replacement for `SEC-020`-`SEC-024`.

Acceptance criteria:

- Rendered prompts clearly distinguish system instructions from tracker data for all supported templates.
- Malformed issue text cannot break the framing or alter non-prompt runtime configuration.

## Phase 4: Runtime and Dependency Patching

### `SEC-030` Upgrade, pin, and attest the Go toolchain

**Finding:** Reviewed binaries were built with Go 1.26.4; `govulncheck` reported 17 reachable standard-library vulnerabilities, including `GO-2026-6809` and multiple `net/http` denial/desynchronization issues.

- [ ] Install and pin Go 1.26.10 or a newer supported patched release.
- [ ] Update `go.mod`/toolchain declarations and CI runner configuration consistently.
- [ ] Rebuild every distributed `simphony.exe`; do not retain binaries produced by Go 1.26.4 as release artifacts.
- [ ] Run tests, vet, and `govulncheck` using the pinned toolchain.
- [ ] Capture `go version -m` output and checksums for release artifacts.
- [ ] Document the minimum supported Go patch level.

Acceptance criteria:

- `go version -m` shows Go 1.26.10 or newer for every release binary.
- `govulncheck ./...` reports no reachable known vulnerability at release time, or a documented time-bounded exception is approved.
- All Go tests and vet pass with the pinned toolchain.

Verification:

```text
go version
go test ./...
go vet ./...
govulncheck ./...
go version -m simphony.exe
```

### `SEC-031` Upgrade Vite and vulnerable transitive dependencies

**Finding:** The full dashboard audit found four high-severity development dependency findings in Vite 8.1.0, PostCSS 8.5.15, nanoid 3.3.15, and source-map-js 1.2.1. Production dependencies were clean.

- [ ] Upgrade Vite beyond 8.1.5 to a compatible patched version.
- [ ] Resolve patched PostCSS, nanoid, and source-map-js versions through supported direct dependency updates.
- [ ] Regenerate and review the lockfile using the repository package manager.
- [ ] Build and test the dashboard from a clean `npm ci` installation.
- [ ] Run both production-only and full dependency audits.
- [ ] Confirm the development server is not used as a production serving path.

Acceptance criteria:

- Full `npm audit --audit-level=high` reports no high or critical findings.
- Dashboard type checking/build and relevant tests pass.
- Lockfile changes contain no unexplained package-source or lifecycle-script changes.

Verification:

```text
cd dashboard
npm ci
npm run build
npm audit --omit=dev --audit-level=high
npm audit --audit-level=high
```

### `SEC-032` Add Go and npm vulnerability gates to CI

- [ ] Add a pinned `govulncheck` step for `./...`.
- [ ] Run `npm ci`, production audit, full high-severity audit, and dashboard build.
- [ ] Pin CI actions and tool installers to reviewed versions or immutable commits.
- [ ] Define a time-bounded, owner-attributed exception process; never silently ignore scanner failure.
- [ ] Preserve useful scanner output as a CI artifact without exposing secrets.
- [ ] Add scheduled scans in addition to pull-request checks if repository policy permits.

Acceptance criteria:

- A fixture branch with a known reachable Go vulnerability or high npm finding fails CI.
- Tool download/scanner errors fail closed and are distinguishable from a clean result.

### `SEC-033` Add secret scanning and redaction fixtures to CI

- [ ] Add a maintained secret scanner for tracked content and relevant commit history.
- [ ] Configure narrow, reviewed allowlist entries for synthetic fixtures only.
- [ ] Add tests that pass sentinel secrets through config errors, HTTP errors, runner failures, audit events, and structured logs and assert redaction.
- [ ] Document immediate rotation/removal steps for a true positive.
- [ ] Ensure scanner output itself does not republish the full secret.

Acceptance criteria:

- A synthetic unallowlisted credential fails CI.
- Redaction tests cover Linear, provider, bearer, cookie/session, and common cloud token shapes.
- The normal tree passes without broad regex exclusions.

### `SEC-034` Patch and continuously audit the root Agent SDK dependency tree

**Finding:** A post-remediation root audit found seven additional transitive findings outside `dashboard/`: critical `proxy-addr`; high `@modelcontextprotocol/sdk`, `fast-uri`, and `ip-address`; and moderate `@hono/node-server`, `hono`, and `qs`.

- [x] Apply the non-breaking npm audit remediation without `--force`.
- [x] Review the lockfile delta and require Node.js 22 or newer.
- [x] Reproduce the root dependency tree with `npm ci`.
- [x] Run production-only and complete high-severity audits.
- [x] Add a separate root install/audit job to CI.

Acceptance criteria:

- Root `npm audit --omit=dev --audit-level=high` and `npm audit --audit-level=high` both report zero vulnerabilities.
- CI installs from `package-lock.json` and fails closed on future high or critical findings.

## Phase 5: HTTP and Resource Hardening

### `SEC-040` Add inbound HTTP server timeouts

**Finding:** The HTTP servers do not configure `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, or `IdleTimeout`.

- [ ] Construct explicit `http.Server` values for both server modes.
- [ ] Set and document conservative header-read, request-read, response-write, and idle timeouts.
- [ ] Set `MaxHeaderBytes` and preserve graceful shutdown behavior.
- [ ] Confirm ordinary dashboard assets and API operations fit within the limits.
- [ ] Add slow-header, stalled-body, slow-reader, keep-alive, and shutdown tests.

Initial values to validate: 5s header read, 15s request read, 30s response write, 60s idle, and 1 MiB maximum headers. Adjust only with measured compatibility evidence.

Acceptance criteria:

- Slowloris-style connections and stalled requests are terminated within the documented bound.
- Graceful shutdown still completes active bounded requests and cancels overdue work.

### `SEC-041` Add route-specific request and response limits

**Finding:** JSON bodies are decoded without `http.MaxBytesReader`, and outbound decoding is not explicitly size-bounded.

- [ ] Define a default API body limit and explicit smaller/larger limits for exceptional routes.
- [ ] Apply `http.MaxBytesReader` before every JSON decoder.
- [ ] Reject unknown fields where the mutation schema is security-sensitive and reject trailing JSON documents.
- [ ] Bound multipart/upload routes if any are introduced.
- [ ] Bound API response collections and diagnostic strings to avoid amplification.
- [ ] Reuse `SEC-013` for outbound response limits.
- [ ] Add exact-boundary, over-limit, compressed, chunked, and multiple-JSON-value tests.

Acceptance criteria:

- Oversized input returns `413` without applying partial state.
- Decoder and limit behavior is consistent across all mutation handlers.
- No endpoint allocates based solely on an attacker-controlled declared size.

### `SEC-042` Add rate limits and browser security headers

- [ ] Add per-source and global limits for login/auth failures, validation/catalog calls, refresh, settings updates, and lifecycle mutations.
- [ ] Bound rate-limit state and define reverse-proxy client-IP trust explicitly; do not trust arbitrary forwarding headers.
- [ ] Return `429` with safe retry guidance.
- [ ] Add `X-Content-Type-Options: nosniff`, a restrictive Content Security Policy, `Referrer-Policy`, and clickjacking protection through CSP `frame-ancestors`.
- [ ] Add HSTS only for deployments that are guaranteed to remain HTTPS.
- [ ] Ensure dashboard bundling works without unsafe inline script exemptions where practical.

Acceptance criteria:

- Repeated login and expensive validation requests are throttled without unbounded memory growth.
- The dashboard loads under the documented CSP and cannot be framed by an untrusted origin.
- Proxy trust cannot be spoofed to evade rate limits.

## Phase 6: Assurance, Documentation, and Controlled Release

### `SEC-050` Add redacted audit logging for privileged actions

- [ ] Emit a structured audit event for authentication, settings mutation, credential-origin change, validation/catalog request, project start/stop/pause/resume, stage control, unsafe-agent override, and hook configuration change.
- [ ] Include timestamp, request ID, principal ID, source, action, target, outcome, and changed field names.
- [ ] Exclude raw secrets, cookies, authorization headers, full prompts, and sensitive response bodies.
- [ ] Make audit events distinguishable from ordinary diagnostic logs and document retention/export expectations.
- [ ] Test successful, denied, failed, and no-op mutations.

Acceptance criteria:

- Each privileged action can be attributed without revealing credentials.
- Denied cross-origin, authorization, SSRF, and unsafe-policy attempts leave a useful bounded audit event.
- Redaction tests from `SEC-033` cover the audit path.

### `SEC-051` Build the adversarial security regression suite

- [ ] Add an anonymous-to-command-chain regression proving authentication blocks the settings-to-hook path before state change or process launch.
- [ ] Add malicious-origin and CSRF browser simulations against localhost.
- [ ] Add credential-sentinel tests for endpoint changes, masked values, redirect chains, and catalog/tracker validation.
- [ ] Add metadata/private-network/encoded-address/DNS-rebinding SSRF cases.
- [ ] Add malicious issue prompts requesting environment secrets, host-file reads, out-of-worktree writes, network access, and policy override.
- [ ] Assert the default Codex and Claude policies prevent the requested effects using fake agent processes and disposable workspaces.
- [ ] Add slow/oversized inbound and outbound HTTP cases.
- [ ] Run all cases for both server modes and all supported operating-system-specific policy resolution paths.
- [ ] Produce a concise machine-readable test report for release evidence.

Acceptance criteria:

- Every critical/high review finding has at least one test that fails on the reviewed baseline behavior and passes with the remediation.
- Tests use only fake credentials/endpoints and cannot reach production services.
- The suite is deterministic enough for required CI status.

Verification:

```text
go test ./... -count=1
go vet ./...
govulncheck ./...
cd dashboard && npm ci && npm run build && npm audit --audit-level=high
```

### `SEC-052` Update security, API, configuration, and operations docs

- [ ] Update `SECURITY.md` with supported versions, reporting, and secure deployment expectations.
- [ ] Document authentication setup, login/session behavior, bearer automation, logout, and secret rotation.
- [ ] Document bind defaults, remote-access prerequisites, exact CORS origins, CSRF behavior, proxy trust, TLS, and firewall guidance.
- [ ] Document origin-scoped credentials, custom endpoint restrictions, masked-secret migration, and local-development exceptions.
- [ ] Document agent environment allowlists, sandbox/tool policies, trust gates, service accounts, and egress controls.
- [ ] Update API examples so every protected route shows the required authentication mode.
- [ ] Update configuration examples and troubleshooting for all new fail-closed validation errors.
- [ ] Remove or redirect older backlog language that describes authentication and audit logging as optional future enhancements.

Acceptance criteria:

- A new operator can deploy the secure local configuration without relying on undocumented defaults.
- No example recommends wildcard binding/origins, literal secrets, unsandboxed agents, or untrusted automatic dispatch.
- Documentation clearly distinguishes secure defaults from explicit unsafe exceptions.

### `SEC-053` Perform final validation and controlled redeployment

- [ ] Review every acceptance criterion and link its test/evidence.
- [ ] Run the complete release gate from a clean checkout with the pinned toolchains.
- [ ] Build staged binaries and capture checksums plus `go version -m` attestations.
- [ ] Validate against fake tracker/provider services and a disposable repository under a non-admin account.
- [ ] Back up production configuration and prepare documented rollback steps.
- [ ] Have the operator stop the old process and start the staged release with all projects paused.
- [ ] Verify loopback/default bind, anonymous denial, authenticated dashboard, CSRF denial, audit event, approved-origin validation, and one sandboxed disposable issue.
- [ ] Rotate credentials identified in `SEC-000` and invalidate old operator sessions.
- [ ] Resume production projects only after the smoke checklist passes.
- [ ] Record residual risks and obtain explicit acceptance for any remaining unsafe override.

Acceptance criteria:

- All P0/P1 tickets and required P2 release gates are complete.
- Critical/high exploit paths are closed in an operator-observed smoke test.
- Rollback does not restore public/wildcard exposure.
- Release evidence identifies the exact source commit, dependency lockfile, toolchain, binary hashes, test results, and operator validation timestamp.

## Program Definition of Done

- [x] Critical unauthenticated command-execution chain closed and regression-tested.
- [x] Critical credential-forwarding SSRF chain closed and regression-tested.
- [x] Prompt-injection impact constrained by trust gates, environment isolation, sandbox/tool policy, scoped identity, and egress guidance.
- [x] Go, root Node, and dashboard high-severity vulnerabilities removed from the staged artifact dependency trees.
- [x] Inbound and outbound HTTP operations are time- and size-bounded.
- [x] CI enforces Go, root Node, dashboard vulnerability, secret, build, and security regression checks.
- [x] Secure deployment and migration documentation is complete.
- [ ] Controlled redeployment passes and evidence is retained.

## Session Handoff

Current status: code remediation and local verification are complete. The service was not started, restarted, or deployed. Production containment, credential-history review/rotation, configuration backup, non-admin disposable-environment validation, and the controlled smoke/redeployment checklist remain operator actions under `SEC-000`, `SEC-024`, and `SEC-053`.

Verification completed on 2026-10-22:

- Go `1.26.10` on Windows/amd64.
- `go test ./... -count=1`: passed.
- `go vet ./...`: passed.
- `govulncheck ./...`: zero reachable vulnerabilities. One imported-package advisory was unreachable from Simphony code.
- Root `npm ci`, production audit, and full high-severity audit: passed with zero vulnerabilities.
- Dashboard `npm ci`, TypeScript/Vite production build, production audit, and full high-severity audit: passed with zero vulnerabilities using Vite `8.3.5`.
- Gitleaks: 88 commits scanned; no leaks found.
- `git diff --check`: passed.
- A pre-final-fix validation `simphony.exe` was built with `-trimpath` by Go `1.26.10`; SHA-256 `8126998877A4678A5DFA499664274506B9F4350920457601E4B7E927CE25AC9F`. This hash is superseded by the final unrestricted-sandbox guard change and must not be used as release evidence.

That superseded binary reports source revision `be9e15938b9d29a1ff52348595b7add3b26b6928` with `vcs.modified=true`, as expected for the uncommitted remediation worktree. A release artifact must be rebuilt from the eventual reviewed commit so its embedded revision is clean and reproducible.

Preserve the unrelated pre-existing changes to `.claude/skills/simphony/scripts/worktree.ps1`, `WORKFLOW.md`, `simphony.yaml`, and `webbridge-req-1.json`. Deployment/restart remains the operator's responsibility; keep projects paused until the documented smoke checklist passes.
