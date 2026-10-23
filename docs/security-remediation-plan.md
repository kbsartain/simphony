# Security Remediation Plan

Last updated: 2026-10-22

Review baseline: `be9e15938b9d29a1ff52348595b7add3b26b6928`

This document is the delivery plan for correcting the security vulnerabilities found during the October 2026 review of Simphony. The companion [Security Remediation Backlog](security-remediation-backlog.md) is the issue-ready execution record.

## Executive Risk Statement

Simphony is a privileged local automation service. It accepts operator input over HTTP, stores or resolves tracker and model-provider credentials, launches agent processes, executes lifecycle hooks through a shell, and modifies source workspaces. Those capabilities make its control plane equivalent to an administrative interface.

The reviewed baseline has two critical attack chains:

1. An unauthenticated HTTP client can mutate runtime settings that ultimately reach shell-backed hooks. In single-workflow mode the server listens on every interface, and permissive CORS allows browser origins to call mutation routes.
2. Operator credentials can be sent to attacker-selected tracker or model-catalog endpoints. Masked-secret preservation can unintentionally reuse a credential after its destination changes.

The baseline also allows untrusted tracker text to reach an agent that may run without a sandbox and with most of the host environment, uses a Go runtime with reachable standard-library vulnerabilities, contains high-severity dashboard development dependencies, and lacks basic HTTP resource limits.

Until Phase 1 and Phase 2 are complete, Simphony should be treated as unsafe to expose beyond a trusted local operator session. Feature work that expands remote access or automated dispatch should not ship ahead of these controls.

## Scope

The review covered the Go service, REST API, dashboard build, tracker and provider HTTP clients, workflow/settings handling, agent-process environment construction, workspace hooks, dependencies, and CI configuration. The plan addresses:

- authentication, authorization, CORS, CSRF, and bind defaults;
- credential-origin binding and server-side request forgery (SSRF);
- prompt-injection containment and agent isolation;
- Go and dashboard dependency vulnerabilities;
- HTTP timeouts, body limits, response limits, rate limits, and security headers;
- privileged-operation audit logging, adversarial tests, CI gates, and operator documentation.

This plan does not attempt to make arbitrary shell hooks intrinsically safe. Hooks remain privileged code and must only be configurable by an authenticated, authorized operator. It also does not claim that prompt text can neutralize prompt injection; enforceable sandbox, environment, identity, and egress boundaries are required.

## Threat Model and Trust Boundaries

### Assets

- the host account and files accessible to the Simphony process;
- repositories, worktrees, Git credentials, and branch integrity;
- Linear, model-provider, GitHub, and other service credentials;
- workflow and registry configuration;
- tracker issue state and the integrity of pipeline transitions;
- availability of the control plane and agent worker pool.

### Adversaries and Untrusted Inputs

- an unauthenticated network client that can reach the HTTP listener;
- a malicious website opened in the operator's browser that targets localhost;
- a local unprivileged process sharing the workstation;
- a malicious or compromised tracker issue author;
- an attacker-controlled URL entered as a tracker or model endpoint;
- a compromised upstream endpoint that redirects or returns an oversized response;
- a dependency or build-system compromise.

### Trust Boundaries

| Boundary | Data crossing it | Required control |
| --- | --- | --- |
| Browser/CLI -> Simphony API | credentials, settings, lifecycle mutations | authentication, authorization, trusted origins, CSRF protection, rate limits |
| Simphony API -> workflow/registry/runtime | privileged configuration | schema validation, safe defaults, audit log, atomic update |
| Simphony -> tracker/model endpoint | API credentials and response data | exact origin binding, SSRF policy, redirect checks, time and size limits |
| Tracker issue -> prompt | attacker-controlled title/description/comments | dispatch trust gate, explicit untrusted-data framing, sandbox and least privilege |
| Simphony -> agent process | environment, filesystem, tools, network | allowlisted environment, workspace sandbox, scoped identity and egress |
| Workflow hooks -> shell | operator-supplied command text | privileged authorization, explicit enablement, logging, bounded execution |
| Source tree -> CI/release binary | dependencies and toolchains | pinned patched versions, vulnerability/secret gates, reproducible build evidence |

## Remediation Principles

1. **Fail closed.** Missing authentication, invalid origins, ambiguous credential destinations, and unsupported security configuration must prevent the privileged operation.
2. **Local by default.** Bind to loopback unless an operator explicitly enables remote access and satisfies authentication and transport requirements.
3. **Credentials belong to origins.** A secret approved for one scheme/host/port must never be silently reused for another.
4. **Tracker content is untrusted data.** Issue text never grants authority and does not weaken process, filesystem, tool, identity, or network controls.
5. **Least privilege per stage.** Each agent receives only the environment variables, tools, workspace access, and service identity required for its current stage.
6. **Bound all external work.** HTTP headers, bodies, redirects, response sizes, execution time, retries, and concurrency must have explicit limits.
7. **Preserve operator visibility.** Security denials and privileged changes are observable without logging secrets.
8. **Prove fixes with negative tests.** A control is incomplete until tests demonstrate that the former exploit path is rejected.

## Delivery Phases

| Phase | Objective | Exit gate |
| --- | --- | --- |
| 0. Containment | Keep the reviewed build inaccessible while fixes are prepared | listener stopped or loopback/firewall constrained; projects remain paused; suspect credentials assessed |
| 1. Control plane | Authenticate and authorize every sensitive route; make browser access same-origin by default | unauthenticated, cross-origin, and forged mutation requests fail in both server modes |
| 2. Credential and SSRF safety | Prevent secrets from following attacker-controlled URLs | credentials can reach only their approved origins; redirects and unsafe destinations are blocked |
| 3. Agent isolation | Limit the effect of malicious tracker content | untrusted issues cannot expose host secrets or write outside the workspace under default policy |
| 4. Runtime and dependencies | Remove known reachable vulnerabilities | patched Go-built binaries and a clean dashboard audit are reproducible in CI |
| 5. Resource hardening | Make inbound and outbound HTTP work bounded | slow, oversized, redirected, or abusive requests fail predictably without service instability |
| 6. Assurance and release | Make security properties durable | adversarial regression suite, CI security gates, updated docs, and controlled operator redeployment complete |

Phase 4 may run in parallel with Phases 1-3. Phase 5 can begin after the authentication middleware shape is stable. Model-catalog and tracker-validation mutations must not be re-enabled until Phase 2 passes its negative tests.

## Immediate Containment

Before implementation begins:

- keep Simphony stopped when it is not being actively tested;
- when testing, bind only to `127.0.0.1` and use host firewall rules to prevent non-loopback access;
- keep all registered projects and pipeline stages paused;
- do not expose the dashboard through a tunnel, reverse proxy, shared LAN address, or public ingress;
- do not test tracker or model endpoints supplied by untrusted content;
- review whether Linear or provider keys were ever sent to a non-approved endpoint; rotate any credential whose exposure cannot be ruled out;
- back up the registry and workflow files before schema or secret-reference migrations;
- use non-production service accounts and repositories for adversarial tests.

Containment is an operational safety measure, not a substitute for the code changes below.

## Target Security Architecture

### Inbound Control Plane

- Both single-workflow and project-registry servers use explicit loopback addresses and reject direct remote binding.
- All API and dashboard access requires the single operator identity; no anonymous operational or liveness route is exposed.
- CLI clients use a bearer credential sourced from the environment. Browsers use an HTTP Basic challenge with username `simphony` and the operator token as password, so dashboard JavaScript and browser storage never receive the credential.
- All routes map to the operator permission in this release. The outer security middleware protects the complete route tree and leaves room for future role separation.
- Browser mutations require a trusted same/exact origin or referer, and all unsafe methods with bodies require JSON. Non-browser bearer clients may omit browser-origin headers.
- CORS is disabled by default. Explicit origins are exact scheme/host/port matches; wildcard origins and credentialed wildcard behavior are rejected.
- Every non-loopback bind fails startup. Remote deployment keeps Simphony on loopback behind a documented authenticated TLS reverse proxy.

### Outbound Credential Handling

- Every tracker/provider credential is associated with an exact normalized origin: scheme, host, and effective port.
- Built-in providers have known default origins. A custom origin requires a separately selected secret reference and explicit operator approval.
- Changing an endpoint clears the previously preserved masked credential unless the new normalized origin is identical.
- Redirects are limited, revalidated at every hop, and never forward authorization material to a different origin.
- Unsafe URL schemes, URL userinfo, loopback, link-local, private, multicast, unspecified, and cloud-metadata destinations are denied by default. Explicit development exceptions are narrow, named, and never enabled by an API caller alone.
- Connection, TLS handshake, response-header, total-request, and response-body limits apply to all outbound calls.

### Agent Execution

- The default Codex policy is `workspace-write` with no approval-based escape. Unsandboxed execution requires an explicit unsafe override and produces a high-severity startup warning.
- Claude stages use an explicit least-privilege tool and permission policy rather than inherited permissive defaults.
- Agent subprocesses start from a small platform allowlist plus stage-specific variables. Tracker credentials, unrelated provider credentials, cloud credentials, and arbitrary parent-process variables are absent.
- The credential for the selected stage is the only provider secret injected for that stage.
- Dispatch requires a configured trust signal, such as an approved label and/or an allowlisted author or team. Untrusted issues remain visible but are not executed.
- Prompts clearly delimit tracker text as untrusted data. This is defense in depth and is never relied on instead of enforceable isolation.
- Operators receive guidance for dedicated OS/service accounts, scoped Git credentials, and network egress restrictions.

## Implementation and Migration Strategy

1. Land containment documentation and tests that capture the vulnerable behavior without executing real commands or sending real credentials.
2. Implement the control-plane primitives behind secure defaults: explicit loopback binding, authentication, an outer route policy, origin policy, and CSRF enforcement.
3. Protect browser access with the built-in Basic challenge and preserve a documented bearer flow for automation; never expose the operator token to dashboard JavaScript or browser storage.
4. Introduce origin-scoped credential records. During migration, associate an existing reference only with its currently configured origin; fail closed when that pairing is ambiguous.
5. Replace unrestricted outbound clients with centrally constructed hardened clients. Migrate Linear validation and model-catalog calls first.
6. Change agent environment construction and SDK defaults with deterministic Codex and Claude compatibility tests. Require an explicit unsafe compatibility override for legacy unsandboxed workflows.
7. Patch the Go toolchain and dashboard dependencies, then rebuild artifacts from the patched toolchain.
8. Add HTTP resource controls, audit events, adversarial tests, and CI gates.
9. Stage a release against fake tracker/provider services and a disposable repository. The user performs the production stop/start and confirms the listening address, authentication flow, audit output, and paused startup state.

Schema or behavior changes must include actionable validation errors and migration notes. Do not silently downgrade to insecure legacy behavior when an old configuration is loaded.

## Verification Strategy

### Unit and Integration Coverage

- middleware matrix for anonymous, read-only, operator, expired, and malformed credentials;
- same-origin, absent-origin, malicious-origin, and forged-CSRF request cases;
- endpoint normalization, origin changes, masked-secret preservation, redirect chains, DNS/IP classifications, and response-size limits;
- both single-workflow and project-registry server construction;
- agent environment snapshots for Codex and Claude on Windows and POSIX;
- sandbox and tool-policy resolution for every pipeline stage;
- trusted and untrusted issue dispatch decisions;
- server timeouts, request limits, rate limits, and security headers;
- audit-record completeness and secret redaction.

### Adversarial Regression Scenarios

The regression suite must prove that:

1. an anonymous client cannot read sensitive state or invoke any mutation;
2. a malicious website cannot mutate a localhost Simphony instance;
3. changing a Linear or provider URL cannot reuse or exfiltrate the old credential;
4. redirects cannot move an authorization header to a different origin;
5. metadata, loopback, private-network, and DNS-rebinding destinations are denied by default;
6. oversized or slow inbound/outbound messages are terminated within documented limits;
7. tracker text that asks for environment secrets or writes outside the worktree cannot obtain either under the default agent policy;
8. logs, API responses, test failures, and audit events do not contain raw secrets.

### Release Gates

Run at minimum:

```text
go test ./...
go vet ./...
govulncheck ./...
cd dashboard && npm ci
cd dashboard && npm run build
cd dashboard && npm audit --audit-level=high
```

CI must run the same checks from a clean checkout. Release evidence must include `go version -m` output for each distributed binary, dependency lockfile changes, and the security regression results.

## Deployment and Rollback

Deployment and process restart remain operator actions.

Before deployment:

- export/backup registry and workflow configuration;
- stage the new binary under a distinct name;
- confirm projects start paused and the default listener is loopback;
- prepare the operator credential and, for remote use, the trusted proxy/TLS configuration;
- rotate credentials identified during containment;
- retain the previous binary and configuration backup for rollback.

After deployment, verify the bound address, anonymous denial, authenticated dashboard login, mutation audit event, tracker validation against its approved origin, one sandboxed disposable issue, and clean logs. Do not resume normal projects until these checks pass.

If rollback is required, stop the new process, restore the prior binary and compatible configuration, and return to the Phase 0 containment posture. Rolling back must not re-expose the old listener. Security schema migrations should be additive or accompanied by a documented reverse transformation.

## Definition of Done

The remediation program is complete when:

- all critical and high findings are fixed and their former exploit paths have negative regression tests;
- medium findings are fixed or have a documented, approved residual-risk decision;
- both server modes share the same authentication, origin, timeout, and limit controls;
- credentials are demonstrably bound to approved origins and cannot cross redirects or endpoint changes;
- default agent execution is workspace-confined, least-privilege, and stripped of unrelated host secrets;
- release binaries are built with Go 1.26.10 or newer and pass `govulncheck` with no reachable known vulnerabilities;
- the dashboard full dependency audit has no high or critical findings;
- CI blocks regressions in vulnerability scanning, secret scanning, and security tests;
- operator, environment, configuration, API, and troubleshooting documentation describe secure defaults and migration steps;
- a controlled deployment and adversarial smoke test have passed, with evidence linked from `SEC-053` in the backlog.

## Residual Risk and Follow-Up

Even after completion, a deliberately enabled shell hook or unsandboxed agent remains capable of acting with the Simphony service account's privileges. Those modes must be visibly marked unsafe, excluded from default examples, and treated as an operator-accepted exception. A future independent penetration test should focus on authentication bypass, proxy trust, DNS rebinding, redirect behavior, agent sandbox escape, and secret redaction after the implementation stabilizes.
