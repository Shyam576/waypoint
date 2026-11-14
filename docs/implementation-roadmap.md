# Implementation Roadmap

This reorders the brief's ten feature phases by what
`docs/architecture-audit.md` actually found, not by the brief's original
numbering. Severity and dependency drive the order; two items explicitly
asked for by name in the brief (reporting metrics, most of the MCP
enhancements) are pushed to the end because the audit found no gap there
serious enough to prioritize over the governance holes.

Complexity is a rough S/M/L, estimating the change itself, not including
the "live with it for a sprint and find the edge cases" cost.

## Essential for a trustworthy MVP — do these first, in this order

**1. Deployment authorization binding (closes Audit Finding 1). — DONE.**
Implemented: `internal/deployments/authorization.go`,
`migrations/0009_deployment_authorizations.sql`,
`POST /api/v1/ci/releases/{id}/deployment-authorizations`. See
[`docs/deployment-security.md`](deployment-security.md) for exactly what
it closes and what it still doesn't (artifact integrity beyond a digest
match remains item 2, below).
Complexity: M. Depends on: nothing (do first).
A short-lived, single-use authorization object: `EligibilityForCI` mints
one only when the release is eligible, scoped to release ID + a specific
artifact digest, with an expiry (minutes, not hours) and single-use
semantics (a used or expired token is rejected). `RecordFromCI` requires
and consumes it instead of (or in addition to) re-running
`gate.Evaluate` inline. This alone does not make deployment physically
unbypassable — that also requires the infrastructure controls in item 1b
— but it closes the gap between "Waypoint says eligible" and "Waypoint
issued a specific, consumable authorization for this exact artifact,"
which is the precondition for 1b to mean anything.

**1b. Document the non-code enforcement boundary (closes Finding 7). — DONE.**
Written as [`docs/deployment-security.md`](deployment-security.md): required
GitHub Environment protection rules and reviewers on `production`, branch
protection on the workflow file, OIDC-scoped short-lived cloud
credentials restricted to that workflow, and the Kubernetes RBAC /
registry-access boundary that keeps ordinary developer credentials from
deploying directly. `deploy/github-actions/deploy.yml` was updated to the
real authorize-then-deploy-then-record flow.

**2. Release artifact integrity (closes Finding 2). — DONE (opt-in).**
Implemented: `internal/candidates` (new package), migration
`0010_release_candidates.sql`,
`POST /api/v1/releases/{id}/candidates` (web) and
`POST /api/v1/ci/releases/{id}/candidates` (CI). A release's current
candidate is the most recently registered row for it. Two enforcement
points:
- **Always on, no flag:** `IssueAuthorization` (item 1) rejects a
  request whose `artifact_digest` doesn't match a registered current
  candidate's `image_digest`, whenever one is registered.
- **Opt-in per project** (`policy.require_candidate_for_approval`,
  default `false`): when set, `gate.Evaluate` only counts an approval
  toward the required count while its stamped `candidate_id` matches the
  current candidate; registering a new candidate silently drops stale
  approvals from the count with no separate invalidation step.

Made opt-in for approvals (not evidence, and not mandatory) specifically
to avoid changing gate behavior for every existing project and every
existing test; see README "Release candidates and artifact integrity"
for the full behavior and `docs/architecture-audit.md` Finding 2 for why
evidence binding remains open (folded into item 4, the checklist
engine).

**3. Policy snapshotting per release (closes Finding 3).**
Complexity: S. Depends on: nothing, but touches the same `gate.Evaluate`
code item 2 touches — do them in the same pass to avoid two migrations to
the same read path.
Snapshot the effective policy onto a release when it enters
`ready_for_production` (or at creation — needs a decision, see Open
Questions). `gate.Evaluate` reads the snapshot, not the live project
policy, for any release that has one. Project policy edits remain
unrestricted for releases that haven't reached that stage yet.

**4. Structured checklist / evidence engine (closes Finding 5).**
Complexity: L. Depends on: item 3 (checklist version should be snapshotted
the same way policy is).
Replaces free-text evidence with: reusable per-project templates,
template versioning, required/optional items, a responsible owner per
item, a verification status, reviewer sign-off, rejection with a reason,
controlled exceptions, and completion timestamps — all per the categories
the brief lists (development / UAT / production-readiness /
post-deployment). A release keeps the checklist version assigned when it
was created even if the project's template changes later. This is the
largest single item in the roadmap; it touches the database schema, the
gate, the API, and most of the release-detail frontend page.

**5. High-risk / payment change controls (closes Finding 4).**
Complexity: M. Depends on: item 4 (risk-tier-specific requirements are
expressed as checklist/policy additions, not a separate system).
Add a `critical` risk tier; let policy define extra requirements per tier
(additional independent approval, specific evidence, a
config-diff/"no test values in prod config" automated check where
feasible); let a release be tagged with the high-risk categories it
touches (payment, auth, schema migration, infra config, secrets). Keep
machine-verifiable checks explicitly distinct from human attestations in
the data model — do not imply Waypoint can detect every wrong business
value.

**6. Security hardening pass (closes Findings 6, 8, 9, 10, 11, 13).**
Complexity: M, but it's six small, independent changes rather than one
large one — can be split across several focused PRs.
Token expiry + rotation surfaced in the UI; rate limiting on
`/auth/login` and `/bootstrap/admin`; configurable separation-of-duties
policy (beyond "not the owner"); password reset/change and user
deactivation endpoints; `SELECT ... FOR UPDATE` on the release row during
deployment recording; constant-time bootstrap token comparison.

## Depends on the above — do next

**7. Rollback and emergency release workflows (brief's Phase Seven).**
Complexity: M. Depends on: item 2 (a rollback targets a specific prior
candidate, which only exists once item 2 does) and item 1 (an emergency
release still goes through *a* authorization, just under a separately
defined, still-audited emergency policy — "emergency" must never mean
"unauthenticated bypass").
Rollback: link to the original deployment, target artifact, required
reason, operator, execution/verification tracking, full history
preserved. Emergency: explicit classification, a separately defined
emergency policy (e.g. reduced required-approval count but never zero,
unless a project explicitly configures that and it is loudly audited),
justification + authorizing person recorded, mandatory retrospective
review, and clear labeling everywhere a release or deployment is shown.

**8. Post-deployment verification states (closes Finding 12, brief's Phase Eight).**
Complexity: S–M. Depends on: item 1 (extends the same deployment model the
authorization work already has to touch).
Add `verification_pending` / `verification_passed` / `verification_failed`
/ `rollback_initiated` / `rollback_completed` / `rollback_failed` states,
accept both automated CI-reported verification and manual verification
evidence, and make it impossible to confuse "the deploy step exited 0"
with "the application is verified healthy."

## Low-risk, can happen any time — not blocked on anything above

**9. Dashboard and operational visibility (brief's Phase Nine).**
Complexity: S–M, incremental. Pure read-side aggregation
(`internal/server/dashboard.go` already has the pattern). Add: active/
failed/awaiting-verification deployments, emergency releases, open
incidents, upcoming release windows — each addable independently as the
underlying data model gains the states above. Defer the frequency/
failure-rate/MTTR/cycle-time *metrics* specifically until there is enough
real recorded-event data for them to mean anything; computing them from a
handful of rows in a demo database would be more misleading than useful,
which the brief itself warns against ("ensure metrics are derived from
actual recorded events").

**10. MCP/AI integration enhancements (brief's Phase Ten).**
Complexity: S. The audit found this boundary already correctly enforced
(structurally, not just by convention — see the audit's "what's already
solid" section), so this is incremental polish, not a gap to close:
richer `get_release_context` (surface checklist/candidate state once
items 2 and 4 exist), and keeping the explicit rule — already true today
and must stay true — that nothing reachable from an MCP token can approve
a release, change a policy, or authorize/record a production deployment.

## Testing, throughout

Every item above should land with tests, per the brief's explicit list of
priorities (gate policy, authorization/cross-project-access,
concurrency, approval revocation, artifact-change-after-approval,
expired/reused authorization, failed deployment handling, rollback,
emergency authorization, audit immutability, MCP permission boundaries).
`docs/testing-strategy.md` will be written alongside item 1 (the first
place several of these new categories of test are actually exercisable)
rather than speculatively now.

A disposable local Postgres container is the right way to run the
DB-backed integration tests without touching the dev database or anyone
else's: see the Testing section of the (to be updated) README. The one
used for this audit's baseline run was created with:

```
docker run -d --name waypoint-test-db -e POSTGRES_USER=waypoint \
  -e POSTGRES_PASSWORD=waypoint -e POSTGRES_DB=waypoint_test \
  -p 15432:5432 postgres:16-alpine
WAYPOINT_TEST_DATABASE_URL="postgres://waypoint:waypoint@localhost:15432/waypoint_test?sslmode=disable" go test ./...
```

## Open questions (need a decision before item 3, not before item 1)

- Should policy snapshot onto a release at *creation* or at the moment it
  enters `ready_for_production`? Creation is simpler and more predictable;
  entering `ready_for_production` lets a long-lived release pick up
  intentional policy improvements made while it was still in development.
  Recommend: snapshot at `ready_for_production` entry, since that is the
  point the brief and the current gate both treat as "now this release is
  subject to the production policy."
- Should an "emergency release" reduced-approval policy be allowed to go
  as low as zero required approvals, or should the system enforce a floor
  (e.g. always at least one approval, even under emergency policy)?
  Recommend a floor of one, with the emergency classification itself
  requiring a named authorizer — "emergency" should change the bar, not
  remove it.

## What I am doing next

Unless redirected, the next session of work implements item 1 (deployment
authorization) and item 1b (the accompanying documentation) together,
since item 1 is both the audit's top finding and the brief's own stated
highest priority, and ships with tests for: issuance only when eligible,
single-use consumption, expiry, and rejection of a mismatched artifact
digest.
