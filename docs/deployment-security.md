# Deployment Security

What the deployment authorization mechanism (`internal/deployments`,
`migrations/0009_deployment_authorizations.sql`) actually enforces, what
it does not, and what has to exist outside this repository for it to
matter. Written alongside that implementation, closing
[`docs/architecture-audit.md`](architecture-audit.md) Finding 1 and
Finding 7 and roadmap items 1/1b.

## What this closes

Before this change, `gate.Evaluate()` was only consulted at the moment a
deployment was *recorded*. Nothing bound that check to a specific,
consumable credential, so "check eligibility, then deploy, then record"
was three independent steps a pipeline (or a human) could reorder, skip
the first of, or substitute a different artifact into between steps two
and three.

Now, a CI-recorded production deployment requires a **deployment
authorization**: a single-use, time-boxed token that

- is minted only when `gate.Evaluate()` currently says the release is
  eligible (`POST .../deployment-authorizations`),
- is bound to one exact release ID and one exact artifact digest at
  mint time,
- expires on its own (`WAYPOINT_DEPLOY_AUTH_TTL_SECONDS`, default 900s)
  if never used,
- is consumed atomically on first use — a single SQL
  `UPDATE ... WHERE consumed_at IS NULL AND revoked_at IS NULL AND
  expires_at > now() RETURNING ...` inside the same transaction as the
  deployment insert, so concurrent requests presenting the same token
  can never both succeed (see
  `TestDeploymentAuthorizationConcurrentConsumptionIsSingleWinner`, which
  exercises exactly this with 8 concurrent attempts and asserts exactly
  one wins),
- is rejected if the artifact digest being recorded doesn't match the one
  it was minted for,
- and is re-validated against a fresh `gate.Evaluate()` call at the
  moment it's minted, and the deployment-recording call still performs
  its own fresh `gate.Evaluate()` as well — so a release whose
  approval was revoked (a later rejection decision recorded) or whose
  evidence became stale between mint and consumption is caught at
  consumption time even though the token itself hasn't expired. This is
  how "approval revocation or material release changes must invalidate
  pending authorization" is satisfied without a separate invalidation
  mechanism: the gate re-check already fails if the state that made the
  release eligible has changed.

Every issuance and consumption is audited (`deployment_authorization_issued`,
`deployment_authorization_consumed`) on the release's activity timeline,
attributed to the CI identity that acted, alongside the existing
`deployment_recorded` entry.

## What this does not close

**This is still not a physical deployment gate.** It is a credential that
a cooperating pipeline must request and present. Nothing in this
repository's code can stop a deployment that never calls Waypoint's API
at all — direct `kubectl apply`, a direct image push, a modified or
entirely different CI workflow, or anyone holding standing production
credentials can still deploy with zero interaction with Waypoint. Closing
*that* gap is an infrastructure and process problem, not a code problem,
and is described below rather than something this repository can enforce
on its own.

**Artifact integrity beyond the digest match is still open.** The
authorization binds to a digest the CI pipeline *claims* it built, and
nothing yet verifies that digest traces back through a specific commit,
build, and set of approvals (there is no `release_candidates` table yet
— that's roadmap item 2). An approver today is approving "this release,"
not "this specific artifact"; the authorization only guarantees "the
artifact being deployed is the same one the authorization was minted
for," not "the artifact was itself independently verified."

## Required controls outside this repository

For the deployment-authorization check to be unavoidable rather than
advisory, the following have to be configured in GitHub and in the target
infrastructure — none of this is, or can be, enforced by Waypoint's own
code:

- **Branch protection on the workflow file.** If anyone can edit
  `.github/workflows/deploy.yml` on an arbitrary branch and still trigger
  it, they can remove the authorization step entirely. Require the
  workflow file itself to only be changeable via a reviewed PR into the
  default branch, and ideally require a CODEOWNERS review from whoever
  owns deployment tooling.
- **Required reviewers on the `production` GitHub Environment.** The
  sample workflow sets `environment: production`; configure that
  Environment in the repository's settings with required reviewers (and,
  ideally, a wait timer) so triggering the job at all needs a second
  person's sign-off independent of Waypoint's own approval gate — this is
  a second, independent control, not a replacement for the first.
- **OIDC-scoped, short-lived cloud credentials restricted to this
  workflow.** The job's deploy step should authenticate via your cloud
  provider's OIDC federation scoped to this specific repository +
  workflow + environment, not a long-lived static credential that could
  be used from anywhere.
- **Kubernetes RBAC / registry access that does not grant ordinary
  developers direct production deploy rights.** If any developer's
  personal `kubectl` context or registry credentials can push to
  production directly, the entire CI-token-and-authorization flow is
  optional for them specifically. The authorization mechanism only
  matters for identities that don't have a simpler path around it.
- **Audit what actually deployed, not just what Waypoint recorded.**
  Waypoint's deployment records are only as trustworthy as the claim that
  they correspond to reality (see Finding 12 / roadmap item 8, not yet
  implemented: there is no independent post-deployment verification that
  the recorded artifact is what's actually running).

## Operational notes

- A disposable local Postgres container is sufficient to run the
  authorization tests; see the Testing section of the README.
- `WAYPOINT_DEPLOY_AUTH_TTL_SECONDS` is capped at 3600 (1 hour) by
  `internal/config.Load`; there is no way to configure an unbounded or
  very long-lived authorization short of editing that cap in code, which
  is deliberate.
- Non-production deployments (`development`, `uat`, `staging`) and
  deployments recorded manually from the web app by an admin or release
  coordinator (`RecordFromWeb`, for backfilling/correcting a record) do
  not require or consume an authorization token. The manual web path
  remains gated on role + project-management permission and still
  re-runs the gate check for production; it is a deliberately separate,
  human-operated escape hatch, not a path a CI pipeline uses.
