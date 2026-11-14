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

**3. Policy snapshotting per release (closes Finding 3). — DONE.**
Implemented: migration `0011_release_policy_snapshots.sql`,
`releases.Service.Transition` snapshots the project's current policy
onto the release every time it enters `ready_for_production` (including
a later re-entry, e.g. after being sent back to `uat` for fixes —
deliberately refreshed as part of that audited transition, not silently).
`gate.Evaluate` prefers this snapshot over the live project policy
whenever one exists (`Result.PolicySnapshotAt` is non-nil when it does);
a release that has never reached `ready_for_production` is unaffected
and still reads the live policy, exactly as the Open Question below
recommended. `UpdatePolicy` itself was left completely unrestricted —
the fix is entirely on the read side.

**4. Structured checklist engine (closes Finding 5). — DONE.**
Implemented: new `internal/checklists` package, migration
`0012_checklists.sql`. Extends (per the brief's own Phase Four heading
— "extend," not replace) the existing free-text evidence system rather
than removing it: both exist side by side. Delivers every sub-item the
brief asks for: reusable per-project templates (`checklist_templates` +
`checklist_template_items`), template versioning (a new version
deactivates the previous one; existing releases are unaffected),
required/optional items (`is_required`), a responsible owner per item
(`AssignOwner`), a verification status (`pending`/`verified`/`rejected`/
`waived`), reviewer sign-off (`Verify`), rejection with a reason
(`Reject`, reason required), controlled exceptions (`Waive`, held to a
stricter permission bar than Verify/Reject since it overrides a
requirement rather than confirming it), and completion timestamps
(`decided_at`). A release is materialized with its own copy of the
project's active template version at creation time
(`MaterializeForRelease`), so it keeps that version even if the
project's template changes later — verified by a dedicated test. Every
new project is auto-seeded with a starter template matching the brief's
suggested categories and items, so the feature works without extra
setup; `policy.require_checklist_completion` (default `false`, same
opt-in convention as items 2/3) is what actually makes the gate block on
incomplete required items — `gate.Result.IncompleteChecklistItems` is
always populated for visibility regardless of the flag.

Known, documented limitation: `verification_method`
(`human_attestation`/`automated_check`) is metadata only in this pass —
nothing wires up a channel for an automated system to report a result,
so every item is still verified/rejected/waived by a human today
regardless of that field's value. Evidence is also still not bound to
a checklist item or a candidate; this item closes Finding 5 for
checklists specifically, not for the pre-existing free-text evidence
system.

**5. High-risk / payment change controls (closes Finding 4). — DONE.**
Implemented: migration `0013_risk_policies.sql`. `releases.RiskLevel`
gains `critical`; releases can be tagged with `risk_categories`
(`payment`/`authentication`/`schema_migration`/`infra_config`/`secrets`/
`sensitive_api`/`other` — classification/visibility only, per the
roadmap's original framing: risk_level is what drives policy, not the
category tags). New `project_risk_policies` table lets a project define,
per risk tier, an extra required-approvals count, extra required
evidence types, and whether explicit risk acceptance is required;
`gate.Evaluate` folds these into the base policy (and the policy
snapshot — these extras are frozen the same way the base policy is).
New `release_risk_acceptances` table + `releases.Service.AcceptRisk`
(held to the same permission bar as `checklists.Waive`: project
management, not just edit permission) implements "explicit risk
acceptance." Three new evidence types
(`config_comparison`/`smoke_test_results`/`payment_config_check`)
support the brief's specific asks; `payment_config_check` is explicitly
documented as a slot for a CI-submitted machine check, not something
Waypoint itself runs — see the `evidence.Type` doc comment for why this
is deliberately narrow and not a general "detect wrong business values"
feature.

Every new project is seeded with a sensible default risk policy (high:
+1 approval; critical: +1 approval, +security_review evidence, requires
risk acceptance) so the feature works without extra setup, the same
convention as the checklist template default in item 4.

**6. Security hardening pass (closes Findings 6, 8, 9, 10, 11, 13). — DONE.**
All six landed as independent changes:
- **Finding 6**: `mcp_tokens`/`ci_tokens` gain `expires_at`
  (`WAYPOINT_MCP_TOKEN_TTL_DAYS` default 180,
  `WAYPOINT_CI_TOKEN_TTL_DAYS` default 90); a token issued before this
  migration keeps working (`expires_at IS NULL`), every token issued
  from now on has a bounded lifetime. "Rotation surfaced in the UI" is
  not done — `expires_at` is returned by the existing list endpoints,
  but no frontend work happened this pass; see Limitations.
- **Finding 8**: `internal/ratelimit`, a minimal stdlib-only per-IP fixed
  window limiter (`WAYPOINT_AUTH_RATE_LIMIT_PER_MINUTE`, default 10) on
  `POST /auth/login` and `POST /bootstrap/admin`.
- **Finding 9**: `policy.require_approver_not_submitter` (default
  `false`): when set, an approver who submitted evidence for a release
  cannot also approve it — enforced live at decision time (not frozen
  into the policy snapshot, since it's an authorization check on the
  approver, the same way "not the release owner" already is, not a gate
  eligibility requirement).
- **Finding 10**: `POST /auth/change-password` (self-service, requires
  current password, invalidates the user's other sessions),
  `POST /users/{id}/password` (admin-set, no current-password check),
  `PATCH /users/{id}/status` (admin activate/deactivate — takes effect
  immediately since session/token resolution already checks
  `is_active`; blocks deactivating your own account).
- **Finding 11**: `gate.Service.EvaluateTx` (the same `Evaluate` logic,
  parameterized over the querier) + `releases.Store.LockForUpdate`;
  `deployments.Service.record` now locks the release row and
  re-evaluates the gate inside the same transaction as the deployment
  insert, closing the gap between "the gate said eligible" and "the
  deployment got recorded." Verified structurally and via the existing
  concurrent-authorization test continuing to pass; a dedicated
  race-reproduction test was not written (see README Limitations for
  why — the race window this closes is too narrow to reproduce
  deterministically without a flaky, timing-dependent test).
- **Finding 13**: `crypto/subtle.ConstantTimeCompare` for the bootstrap
  token check.

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

## Open questions (need a decision before item 7)

- Item 3's snapshot-timing question (creation vs. `ready_for_production`
  entry) is resolved — see item 3 above: snapshot (and re-snapshot) on
  entry to `ready_for_production`.
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
