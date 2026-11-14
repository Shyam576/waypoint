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

**7. Rollback and emergency release workflows (brief's Phase Seven). — DONE.**
Implemented: migration `0015_rollback_and_emergency.sql`
(`project_emergency_policies`, `rollbacks`, emergency columns on
`releases`, `rollback_id` on `deployment_authorizations` with
`issued_by_ci_token_id` loosened to nullable + a CHECK that every
authorization is traceable to a CI token or a rollback).

- **Rollback** deliberately reuses the existing single-use deployment
  authorization mechanism from item 1 rather than a new execution path:
  `deployments.Service.InitiateRollback` (web, admin/coordinator) mints
  an authorization linked via `rollback_id`, bypassing the normal gate
  check — substituted with its own provenance check that
  `target_deployment_id` was itself a prior *successful production*
  deployment. CI consumes it through the existing
  `POST .../deployments` endpoint with no new CI-facing route; `record()`
  detects the rollback link (after consuming the authorization, before
  the gate check it would otherwise run) and skips the gate re-check for
  that one deployment only, then writes the outcome back onto the
  rollback (`status: completed`/`failed`, `resulting_deployment_id`) as
  part of the same transaction. `VerifyRollback` records a human
  confirmation afterward; it refuses a still-`pending` rollback.
- **Emergency** is a live-read override, not a frozen policy-snapshot
  field: `releases.IsEmergency` is read directly from the release row by
  `gate.EvaluateTx` every time, specifically so declaring an emergency on
  a release already sitting in `ready_for_production` (whose policy
  already has a snapshot per item 3) takes effect immediately — that's
  the situation emergency declaration exists for. When true, it
  *replaces* the required-approvals count with the project's
  `project_emergency_policies.required_approvals_count`, which has a
  floor of 1 enforced at both the DB (`CHECK`) and Go level — resolving
  the open question below with the recommended floor. `DeclareEmergency`
  requires a justification and the same permission bar as risk
  acceptance; `releases.Service.Close` is refused with `409` until
  `CompleteRetrospective` has been called, when the project's emergency
  policy requires one (the default).
- Verified against a disposable Postgres instance
  (`internal/integrationtest`: 8 new tests covering rollback provenance
  validation, the gate bypass being scoped to exactly the rollback
  deployment, rollback status following the deployment result,
  verification requiring an executed rollback, the manage-permission
  bar, the emergency floor validation, the live override taking effect
  on an already-snapshotted release, and the close-requires-retrospective
  gate) and live end-to-end over HTTP against the Docker dev stack.
- Not done this pass: a web UI for either workflow (API-only; see
  README Limitations), and any automatic time limit or escalation for
  an open emergency declaration.

**8. Post-deployment verification states (closes Finding 12, brief's Phase Eight). — DONE.**
Implemented: migration `0016_deployment_verification.sql` adds
`verification_status` (`pending`/`passed`/`failed`, default `pending`),
`verified_by`, `verified_via` (`ci`/`web`), `verified_at`, and
`verification_note` to `deployments`.
`deployments.Service.SubmitVerificationFromCI` (CI token) and
`SubmitVerificationFromWeb` (same permission bar as confirming a
checklist item — developer/coordinator/admin, not the stricter bar for
recording a deployment) both accept a `status` + required `note`;
either can only verify a deployment whose own `result` was `success`
(`400` otherwise — there's nothing to confirm healthy about a
deployment that never went out), and a verification can be
resubmitted, each submission fully audited.

The `rollback_initiated`/`rollback_completed`/`rollback_failed` half of
this item's original description is already satisfied by item 7's
design, not duplicated here: a rollback's lifecycle lives on its own
`rollbacks.status` column (`pending`/`completed`/`failed`), not as a
state on the `deployments` row, since a rollback is its own first-class
entity with its own provenance and verification. Adding a second,
overlapping status enum on `deployments` for the same concept would
have been redundant.

Deliberately out of scope this pass (see README Limitations):
verification does not block the gate, a stage transition, or a later
deployment authorization — it is a recorded, visible fact only.

## Low-risk, can happen any time — not blocked on anything above

**9. Dashboard and operational visibility (brief's Phase Nine). — DONE.**
Implemented: `Dashboard` (`internal/server/dashboard.go`) gains
`emergency_releases`, `open_incidents`, `failed_deployments`, and
`awaiting_verification_deployments`, each pure read-side aggregation
following the exact pattern the existing `needs_my_action`/`blocked`/
`upcoming_production`/`recent_deployments` fields already use —
`upcoming_production` already covered "upcoming release windows," so
nothing new was needed there. `deployments.Store` gains `ListFailed`
and `ListAwaitingVerification`, sharing a new `queryJoined` helper with
`ListRecent` to avoid tripling the JOIN query.

While wiring this, found and fixed a latent bug from item 8:
`ListRecent`'s own inline column list was never updated when
`deploymentColumns`/`scanDeployment` gained the five verification
columns, so every dashboard load would have hit a column-count
mismatch panic the first time it ran against a deployment row. Caught
by writing this item's own test rather than by any existing test,
since `internal/server` had no test file before this — see
`internal/server/dashboard_test.go` (new), which wires a full `Server`
via `New()` against a real Postgres database, the same way
`internal/integrationtest` wires services by hand.

Frontend: `web/app/dashboard/page.tsx` gains "Emergency releases,"
"Open incidents," "Failed deployments," and "Awaiting verification"
sections, reusing the existing `Section`/`ReleaseRow`/`DeploymentRow`
components (one new `IncidentRow`); `web/lib/types.ts` gains the
`verification_status` field on `Deployment` (added to the API in item
8 but never added to the frontend type until now) and the new
`Dashboard` fields. `tsc --noEmit` passes. Not visually confirmed in a
rendered browser this pass — no browser automation tool was available
in this session; verified instead via the typecheck, a new backend
test, and live `curl` against the real API with seeded data (emergency
release, open incident, failed deployment, and an unverified
successful deployment all appeared in the right buckets).

Deliberately deferred, per the brief's own warning to "ensure metrics
are derived from actual recorded events": deployment
frequency/failure-rate/MTTR/cycle-time metrics. Computing them from a
handful of rows would be more misleading than useful; revisit once
there is enough real recorded-event history.

**10. MCP/AI integration enhancements (brief's Phase Ten). — DONE.**
Implemented:

- **Richer `get_release_context`** (`internal/server/mcp_context.go`):
  `MCPReleaseContext` gains `checklist_items` (full
  `[]checklists.ReleaseItem`, including status) and `current_candidate`
  (`*candidates.Candidate`, nil if none registered), now that items 2
  and 4 both exist. Read-only, same visibility rules as every other
  field already returned.
- **Hardened the "nothing reachable from an MCP token can approve a
  release, change a policy, or authorize/record a production
  deployment" rule with an explicit, defense-in-depth check**, rather
  than leaving it true only because no MCP route currently wires to
  those handlers. `approvals.Service.Decide` now refuses any
  `identity.Via == authn.ViaMCP` outright.
  `projects.Service.canManage` — the single chokepoint shared by
  `UpdatePolicy`, `UpdateRiskPolicy`, `UpdateEmergencyPolicy`,
  `AddMember`, `releases.Service.DeclareEmergency`/`AcceptRisk`,
  `checklists.Service.Waive`, and
  `deployments.Service.InitiateRollback`/`VerifyRollback`/
  `RecordFromWeb` — does the same. The CI-side equivalent
  (`resolveCIRelease` checking `identity.Via != authn.ViaCI`) already
  existed from item 1 and needed no change. Found no gap in the
  audit's "already solid" assessment — this is deliberately *stronger*
  than what the audit required, closing the gap where a future routing
  mistake (an MCP route accidentally wired to one of these handlers)
  would otherwise have silently worked for any MCP token whose owner
  happened to hold the right role.
- Tests (`internal/server/mcp_context_test.go`, new): richer context
  returned correctly over real HTTP via `httptest`, and a direct
  regression test proving an MCP-via identity is refused by both
  `UpdatePolicy` and `Decide` even when its owning user is an
  admin/approver — while the same user's ordinary web identity still
  works, proving the check isn't overbroad. Verified live against the
  Docker dev stack: no MCP route exists for either action (`404`), and
  the MCP token is rejected outright on the web-only route (`401`,
  since it authenticates by bearer token, not session cookie).

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
  Resolved in item 7: a floor of one, enforced at both the DB (`CHECK`)
  and Go level, with the emergency classification itself requiring a
  named authorizer and justification — "emergency" changes the bar, it
  never removes it.

## What I am doing next

Unless redirected, the next session of work implements item 1 (deployment
authorization) and item 1b (the accompanying documentation) together,
since item 1 is both the audit's top finding and the brief's own stated
highest priority, and ships with tests for: issuance only when eligible,
single-use consumption, expiry, and rejection of a mismatched artifact
digest.
