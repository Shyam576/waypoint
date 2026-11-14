# Waypoint Architecture & Security Audit

Date: 2026-11-14
Scope: full repository at the current `main` (commit `396a7c8` at audit time).
Method: direct code reading of every backend package under `internal/`, all
8 SQL migrations, the router/middleware wiring, the MCP server, the sample
GitHub Actions workflow, `.env.example`, and the frontend auth/session
guards; plus running the existing automated test suite (unit tests and the
DB-backed integration tests) against a disposable local Postgres instance.
No destructive operations were run and no real/production environment was
touched.

This is not a rebuild. It is an assessment of what the current
implementation actually enforces, versus what it appears to enforce from
its naming and comments, so that the next phase of work targets real gaps
instead of re-implementing things that already work.

## Test baseline (established this session)

```
go build ./...   -> clean
go vet ./...     -> clean
go test ./...    -> all passing
```

With `WAYPOINT_TEST_DATABASE_URL` pointed at a disposable Postgres
container (not the dev or any shared database):

```
internal/config          2 tests  pass
internal/releases        2 tests  pass  (stage machine, risk level validation)
internal/integrationtest 6 tests  pass  (gate blocks missing evidence/approvals,
                                          gate blocks on open rejection,
                                          self-approval + wrong-role rejected,
                                          production deployment enforces gate
                                          and promotes stage,
                                          MCP accept materializes / reject
                                          does not)
```

Total: 10 automated tests across 3 packages. This is a real but narrow
baseline — see Finding 15 for what it does not cover.

## Executive summary

The existing implementation is a genuine, working release-tracking system
with several things done correctly and worth preserving as-is: append-only
audit (enforced at both the application and database level), a single
centralized stage machine, a single `gate.Evaluate()` used by every code
path that needs an eligibility answer, bcrypt password hashing, tokens
stored only as salted-free SHA-256 hashes with the raw value shown exactly
once, and a structurally sound three-way auth split (web session / MCP
token / CI token) that already prevents an MCP-authenticated identity from
reaching any approval or deployment endpoint — confirmed by reading the
router's route groups, not assumed from naming.

The most important finding, and the direct answer to this audit's central
question ("does the gate prevent an unauthorized production deployment, or
only prevent it from being recorded?"): **today it only prevents
recording.** `internal/deployments.Service.record()` re-checks
`gate.Evaluate()` before accepting a deployment record, which is correct
and well-tested, but nothing in the system has any power over the actual
deployment mechanism. A CI pipeline (or anyone holding direct registry/
cluster credentials) can deploy to production through any path that never
calls Waypoint's API at all, and Waypoint would have no record of it and
no ability to have blocked it. The sample GitHub Actions workflow calls
the eligibility check before deploying, which is good practice, but that
check is opt-in automation inside one workflow file, not an access-control
boundary enforced by infrastructure. This is Finding 1 below, and it is
the correct next priority — it matches what the brief's Phase Two already
suspected.

## Findings

Findings are ordered by severity, then by how foundational they are to
later phases (an artifact-integrity gap, for instance, has to be fixed
alongside the deployment-authorization gap, not after it).

---

### CRITICAL

**1. Production deployment enforcement is advisory, not binding.**
File: `internal/deployments/deployments.go:246-255` (the `record` method),
`deploy/github-actions/deploy.yml`.
Issue: `gate.Evaluate()` is only consulted when something chooses to call
`POST /api/v1/ci/releases/{id}/deployments`. There is no deployment
authorization artifact that a real deployment mechanism (a Kubernetes
admission controller, an image-signing/verification step, a registry
proxy, a deploy script) is required to obtain from Waypoint and present
before the artifact can actually run in production. The "gate" is a
record-keeping gate, not a deployment gate.
Impact: anyone with direct cluster or registry credentials — or a modified
or entirely different CI workflow — can put an unapproved artifact into
production with zero interaction with Waypoint, and the system will not
notice until (if ever) someone manually compares reality to the dashboard.
This defeats the platform's stated purpose.
Recommendation: Phase Two. Design a short-lived, single-use deployment
authorization (e.g. a signed, time-boxed token minted by
`EligibilityForCI` only when eligible, bound to release ID + artifact
digest, consumed exactly once by `RecordFromCI`) and document what has to
exist *outside* Waypoint's own code for that token to be unavoidable:
GitHub Environment required reviewers/protection rules, branch protection
on the workflow file itself, OIDC-scoped short-lived cloud credentials
restricted to that one workflow, and Kubernetes RBAC that does not grant
ordinary developers direct `kubectl apply`/image-push rights to the
production namespace or registry.

**2. Approvals and evidence are not bound to a specific build artifact.**
Files: `internal/gate/gate.go`, `internal/releases/model.go`,
`migrations/0003_releases.sql`–`0005_deployments.sql`.
Issue: `gate.Evaluate()` reasons about stage, evidence *types* present, and
approval *count* — it never looks at `deployments.artifact_digest`, and
there is no table recording which artifact a release's approvals were
actually approving. Approvals attach to a release row, not to an
immutable artifact identity (commit SHA + image digest + config/migration
checksum).
Impact: a release can collect its required approvals while everyone
involved is looking at commit A, and then a deployment can be recorded (and
accepted by the gate, since the gate never checks this) against an
entirely different artifact built from commit B. This is precisely the
"wrong value reached production despite a governance process existing on
paper" shape of risk the brief's GMC incident describes.
Recommendation: Phase Three, designed together with Finding 1 (the
deployment authorization token should carry the exact artifact identity
the approvals were granted against). Introduce a `release_candidates` (or
similarly named) table capturing git repo/branch/commit SHA/image
digest/config version/migration checksum, and require re-validation per
project policy if the candidate changes after approval.

---

### HIGH

**3. A project's gate policy can be weakened for an in-progress release with no guard.**
File: `internal/projects/service.go:203-242` (`UpdatePolicy`).
Issue: `UpdatePolicy` only checks that the caller `canManage` the project.
It has no awareness of releases currently sitting in
`ready_for_production` waiting on the requirements being changed. A
release coordinator can drop `required_approvals_count` from 2 to 0, or
remove a required evidence type, moments before a deployment attempt, and
every release in that project becomes retroactively "eligible" with no
revalidation and no distinct audit signal from routine policy tuning.
Impact: the gate's guarantees are only as strong as "nobody changed the
policy recently," which is not a guarantee at all.
Recommendation: Phase Six. Either snapshot the effective policy onto a
release when it enters `ready_for_production` (so `gate.Evaluate` reads
the release's own snapshot, not the live project policy), or require that
a policy change which *reduces* requirements cannot apply to releases
already past a configurable stage without a separate, specifically-audited
override action.

**4. No high-risk / payment-sensitive change classification exists.**
Files: `internal/releases/model.go` (`RiskLevel` is `low`/`medium`/`high`
only, and nothing reads it), `internal/projects/model.go` (`Policy` is
uniform regardless of risk).
Issue: `risk_level` is stored on every release but is not consulted by
`gate.Evaluate`, by approval requirements, or by anything else. The
brief's motivating incident is explicitly payment-related; today there is
no mechanism to require extra independent approval, extra evidence, or a
configuration-diff check for a release flagged as touching payments,
auth, schema migrations, or infra config.
Recommendation: Phase Five. Add a `critical` risk tier, let policy define
additional requirements keyed by risk tier, and let a release (or a
specific linked change) be tagged with the high-risk categories it
touches.

**5. Evidence can be self-attested by anyone with project visibility, with no verification state.**
File: `internal/evidence/evidence.go:123-151` (`Submit`).
Issue: `Submit` calls `GetForService`, which only checks `CanView` (any
project member), not an edit or role-specific check. A developer can
submit their own `security_review` or `uat_signoff` evidence. Evidence is
free-text (`title`, `notes`, optional `attachment_url`) with no owner
field, no verification/sign-off status, and no distinction between a
machine-verifiable check and a human attestation.
Impact: the gate's "missing required evidence" check can be satisfied by
anyone typing a plausible title, which materially weakens what
"required evidence" is supposed to mean.
Recommendation: Phase Four. Replace free-text evidence with the structured
checklist engine the brief describes: templates, required/optional items,
a responsible owner, a verification status, and reviewer sign-off, with
explicit separation between automated-check results and human
attestations.

**6. MCP and CI tokens never expire.**
File: `migrations/0001_core.sql` (`mcp_tokens`, `ci_tokens` — only
`revoked_at`, no `expires_at`), `internal/authn/service.go`.
Issue: both token types are valid forever until a human notices and
revokes them. A CI token is the credential that can place a deployment
record (and, per Finding 1, is the credential a real enforcement mechanism
would eventually gate on), so its blast radius if leaked is high.
Recommendation: Phase Six. Add `expires_at` to both token tables, default
to a bounded lifetime (e.g. 90 days for CI tokens), and surface token age
in the web UI so stale, never-rotated tokens are visible rather than
silent.

**7. The GitHub Actions integration's supporting controls are undocumented.**
Files: `deploy/github-actions/deploy.yml`, `README.md`.
Issue: the sample workflow itself behaves correctly — it calls the
eligibility check as an early step and a failed step halts the job before
the deploy step runs — but nothing in the repository documents or
enforces the organizational controls that make this unavoidable rather
than optional: branch protection on the workflow file (so a developer
cannot edit out the gate-check step on a feature branch and still trigger
`workflow_dispatch` against `production`), required reviewers on the
`production` GitHub Environment, restricting direct cluster/registry
credentials away from individual developers, and OIDC-scoped, short-lived
cloud credentials limited to this one workflow. This is the same root
cause as Finding 1; it is listed separately because its fix is
organizational/documentation, not code, and the brief explicitly asks for
it as a named deliverable.
Recommendation: `docs/deployment-security.md`, written alongside the
Phase Two implementation so the documented controls match what the code
actually requires.

---

### MEDIUM

**8. No rate limiting or lockout on authentication endpoints.**
File: `internal/server/router.go` — no rate-limiting middleware exists
anywhere in the stack. `POST /api/v1/auth/login` and
`POST /api/v1/bootstrap/admin` both accept unlimited attempts.
Recommendation: a per-IP and per-account token-bucket limiter in front of
both endpoints, as part of the Phase Six hardening pass.

**9. Separation of duties is one hardcoded rule, not a configurable policy.**
File: `internal/approvals/approvals.go:124-146` (`Decide`).
Issue: the only separation-of-duties check is "the release's nominal
owner cannot approve it." It does not consider the actual author of the
change (if different from the formal owner) or whether the sole approver
also submitted the evidence being relied on.
Recommendation: Phase Six — make this a per-project/per-risk-tier policy
rather than a single hardcoded rule, per the brief's explicit ask.

**10. No password reset/rotation and no user deactivation endpoint.**
Files: `internal/server/users_admin.go`, `internal/users`. The `is_active`
column exists in the schema and is checked on login/token resolution, but
nothing ever sets it to `false`, and there is no self-service or
admin-initiated password change path.
Recommendation: not purely a security bug, but an operational gap that
will be hit immediately in real use; should be closed alongside the
Phase Six hardening pass.

**11. No row locking between gate evaluation and deployment recording.**
File: `internal/deployments/deployments.go:246-288` (`record`). The gate
check and the insert happen in the same transaction but without a
`SELECT ... FOR UPDATE` on the release row, so a concurrent approval
revocation could land between the two. Narrow window, low likelihood, but
worth closing once the deployment-authorization work (Finding 1) touches
this code anyway.

**12. Deployment outcomes have no post-deployment verification state.**
File: `internal/deployments/deployments.go` — `Result` is only
`success`/`failure`, reported by the same call that records the
deployment happened. There is no `verification_pending` /
`verification_passed` / `verification_failed` state, and nothing
distinguishes "the deploy step exited 0" from "the application is actually
healthy in production."
Recommendation: Phase Eight, extending the deployment state machine.

---

### LOW

**13. Admin bootstrap token compared with `!=` instead of a constant-time comparison.**
File: `internal/authn/handlers.go:113` (`BootstrapAdmin`). Theoretical
timing side-channel; low practical risk given network jitter and the fact
the endpoint self-disables once any user exists, but trivial to fix with
`crypto/subtle.ConstantTimeCompare`.

**14. No CSRF token; defense relies entirely on `SameSite=Lax` cookies.**
Files: `internal/authn/handlers.go`, `internal/server/router.go`
(`corsMiddleware`). Adequate for the current architecture (the frontend
only ever talks to the API same-origin, through the Next.js rewrite
proxy), but not defense-in-depth, and would become a real gap if the API
is ever exposed to a different calling origin.

**15. Automated test coverage is thin and skews to the happy path.**
10 tests total (see baseline above). Not covered by any existing test:
cross-project access denial (a user from project A reading/acting on
project B's releases), concurrent deployment or approval-revocation races,
CI/MCP token expiry and reuse, rollback workflows, emergency-release
authorization, audit-log immutability enforced at the database level (the
trigger exists and was read, but nothing exercises it), MCP permission
boundaries (confirmed correct by reading the router, but not asserted by
a test that would fail if someone later wires an MCP route to a
privileged handler), and any frontend end-to-end workflow.

## What is already solid (do not rework)

- Append-only audit log, enforced at both the Go layer (no edit/delete
  handlers exist) and the Postgres layer (`BEFORE UPDATE/DELETE` trigger
  raising an exception) — `internal/audit`, `migrations/0008_audit.sql`.
- A single stage machine (`allowedTransitions` in
  `internal/releases/model.go`) is the only path that can change
  `releases.stage`; production is reachable only via
  `PromoteToProduction`, called from inside the same transaction as a
  successful production deployment record.
- A single `gate.Evaluate()` backs both the human-readable eligibility
  endpoint and the deployment-recording enforcement path, so the UI and
  the enforcement can never disagree about what "eligible" means today
  (they can still both be wrong per Findings 1–2, but they are not
  inconsistent with each other).
- The three authentication schemes are structurally separated at the
  router level (`internal/server/router.go`): the MCP-token route group
  contains exactly 5 routes (whoami, context, and 3 pending-only submit
  endpoints) and has no path to approvals, policy, or deployment
  endpoints. This was verified by reading the router, not inferred from
  naming — an AI agent authenticated via MCP structurally cannot approve a
  release, change a policy, or record a deployment.
  MCP submissions always land as `pending` and require a non-MCP-
  authenticated reviewer to accept or reject (`internal/mcpnotes`).
- Self-approval by a release's nominal owner is blocked server-side
  (`internal/approvals/approvals.go`).
- Secrets handling: all tokens (session, MCP, CI) are stored only as
  SHA-256 hashes; the raw value is returned exactly once at issuance and
  never persisted or logged. Passwords use bcrypt. Internal errors are
  logged server-side with full detail and reduced to a generic message for
  the client (`internal/httpx.Error`) — no stack traces or raw DB errors
  ever reach a response body.
- Every multi-step business write (approve, transition, record deployment,
  accept an MCP submission) runs inside a single Postgres transaction
  alongside its audit-log write, so a crash mid-operation cannot leave a
  business-state change recorded without its audit entry or vice versa.
- Foreign keys and `CHECK` constraints are consistently present across all
  8 migrations for every enum-like column and parent/child relationship
  (verified by grep across all migration files, not sampled).

## Not yet assessed in this pass

The frontend's route protection (`RequireAuth`, `SessionProvider`) was
reviewed in an earlier session while redesigning the UI and found to
correctly gate on session state, but was not re-verified line-by-line in
this audit pass. `internal/support` (post-release support items) and
`internal/dbx` migration runner were read at a high level only. These are
lower-risk, read-mostly or purely structural areas and are not expected to
change the priority ordering above, but should get a pass before anything
in this doc is treated as the final word on them.
