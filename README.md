# Waypoint

Waypoint is an internal release coordination and evidence tool. It tracks a
change from development and UAT through production and post-go-live support,
so a team can see what changed, why, who approved it, what artifact was
deployed, and what happened after release.

This is an MVP built for local/internal use. See [Limitations](#limitations)
for what is intentionally simplified.

## Contents

- [Stack](#stack)
- [Quickstart (Docker Compose)](#quickstart-docker-compose)
- [Local development without Docker](#local-development-without-docker)
- [Creating the first admin account](#creating-the-first-admin-account)
- [Roles and permissions](#roles-and-permissions)
- [Main user flow](#main-user-flow)
- [API overview](#api-overview)
- [The production gate](#the-production-gate)
- [GitHub Actions integration](#github-actions-integration)
- [Claude Code / MCP setup](#claude-code--mcp-setup)
- [Environment variables](#environment-variables)
- [Testing](#testing)
- [Limitations](#limitations)

## Stack

- **Backend**: Go, [chi](https://github.com/go-chi/chi) for HTTP routing/middleware, [pgx](https://github.com/jackc/pgx) for PostgreSQL.
- **Database**: PostgreSQL, with checked-in SQL migrations under `migrations/` (applied automatically on API startup).
- **Frontend**: Next.js (App Router) + TypeScript + Tailwind CSS, under `web/`.
- **MCP server**: a separate Go binary (`cmd/mcp`) using the official [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk), for Claude Code integration.
- **Local dev**: Docker Compose for Postgres + API + web.

### Repository layout

```
cmd/api       HTTP API entrypoint
cmd/mcp       MCP server entrypoint (for Claude Code)
internal/     business logic, one package per domain area
  config      environment configuration
  dbx         pgx pool + embedded SQL migration runner
  httpx       shared JSON/error helpers
  authn       passwords, sessions, MCP tokens, CI tokens, middleware
  audit       append-only audit log
  users       accounts and roles
  projects    projects, release policy, membership
  releases    release record and stage transition rules
  evidence    evidence submissions (test results, sign-off, ...)
  candidates  release candidates: the immutable build identity (commit, image digest, ...) a release currently points at
  checklists  structured go-live checklists: versioned templates, per-release verification/sign-off/waiver
  approvals   approve/reject decisions
  gate        the production eligibility check
  deployments deployment records, CI-token-protected endpoints
  support     post-go-live support items
  mcpnotes    MCP submission staging + review, change notes, design decisions
  server      wires everything into one chi.Router + dashboard/aggregation
migrations/   checked-in SQL migrations (embedded into the api binary)
web/          Next.js frontend
deploy/       Dockerfiles, docker-compose is at the repo root, GitHub Actions sample
```

## Quickstart (Docker Compose)

Prerequisites: Docker and Docker Compose.

```bash
cp .env.example .env
# Edit .env and set ADMIN_BOOTSTRAP_TOKEN to any random string.

docker compose up -d --build
```

This starts three services: `db` (Postgres), `api` (the Go API on
`:8080`), and `web` (the Next.js app on `:3000`, proxying `/api/*` to the
`api` service).

If you already run something on port 8080 or 3000 locally, override the
published host ports instead of editing the compose file:

```bash
WAYPOINT_API_HOST_PORT=18080 WAYPOINT_WEB_HOST_PORT=13000 docker compose up -d --build
```

Once it's up, create the first admin account (one-time; see
[below](#creating-the-first-admin-account)), then open
`http://localhost:3000` (or your overridden port) and sign in.

> **Note on rebuilding `web`:** the API URL the frontend proxies to is
> baked into the `web` image at build time (Next.js resolves
> `next.config.js`'s `rewrites()` during `next build`, not per request).
> If you change where `api` runs, rebuild `web` — `docker compose build
> web` — rather than just restarting the container.

## Local development without Docker

Prerequisites: Go 1.26+, Node.js 22+, a running PostgreSQL instance.

```bash
# 1. Start Postgres however you like, e.g.:
docker run -d --name waypoint-db -e POSTGRES_USER=waypoint \
  -e POSTGRES_PASSWORD=waypoint -e POSTGRES_DB=waypoint -p 5432:5432 postgres:16-alpine

# 2. Run the API (applies migrations automatically on startup)
export DATABASE_URL="postgres://waypoint:waypoint@localhost:5432/waypoint?sslmode=disable"
export ADMIN_BOOTSTRAP_TOKEN="any-random-string-for-local-use"
go run ./cmd/api
# -> listening on :8080

# 3. In another terminal, run the frontend
cd web
npm install
WAYPOINT_API_URL=http://localhost:8080 npm run dev
# -> http://localhost:3000
```

## Creating the first admin account

There is no default production password. On a fresh database, with
`ADMIN_BOOTSTRAP_TOKEN` set, call the one-time bootstrap endpoint:

```bash
curl -X POST http://localhost:8080/api/v1/bootstrap/admin \
  -H 'Content-Type: application/json' \
  -d '{
    "token": "the ADMIN_BOOTSTRAP_TOKEN value",
    "email": "you@example.com",
    "name": "Your Name",
    "password": "a password of at least 8 characters"
  }'
```

This only ever succeeds once: it refuses if the `users` table already
has any row, regardless of the token. Once your admin account exists,
sign in at the web app and create further accounts under **Admin ->
Users** (or `POST /api/v1/users`), choosing a role for each.

**No SSO.** This MVP only implements local email/password sign-in with
HTTP-only session cookies. An SSO integration (SAML/OIDC against your
company IdP) would replace `internal/authn`'s password check with a
token exchange and is not implemented here.

## Roles and permissions

Roles are global per user account: `admin`, `developer`, `approver`,
`release_coordinator`, `support`. All authorization is enforced in the
backend (`internal/authn` middleware + per-service checks), never only
by hiding frontend buttons.

| Role | Can do |
|---|---|
| `admin` | Everything; bypasses project-membership checks. |
| `release_coordinator` | Manage projects/policy/members on projects they're a member of; move release stages; close releases; record manual deployments; review MCP submissions. |
| `developer` | Create/edit releases, links, evidence, change notes, design decisions on projects they're a member of; move most release stages; review MCP submissions. |
| `approver` | Approve or reject releases (never their own) on projects they're a member of. |
| `support` | Create/update post-go-live support items. |

A user must be a **member** of a project (see "Members" on a project
page, or `POST /api/v1/projects/{id}/members`) to act on its releases,
except admins, who can always act. The project creator is added as a
member automatically.

## Main user flow

1. Sign in and select (or create) a project.
2. Create a release: owner, name/version, target date, risk level,
   summary, rollback plan, support period.
3. Attach links (work items, PRs, commits, CI runs), evidence (test
   results, UAT sign-off, rollback plan, security review, change
   summary), change notes, and design decisions.
4. Move the release through `draft -> development -> uat ->
   ready_for_production` (see [`internal/releases/model.go`](internal/releases/model.go)
   for the full transition table — invalid transitions are rejected
   with a clear reason).
5. An approver approves or rejects; every decision is recorded and shown
   in the release's activity timeline.
6. A CI/CD pipeline checks `GET /api/v1/ci/releases/{id}/eligibility`,
   then — once it has built the artifact and knows its exact digest —
   obtains a short-lived, single-use deployment authorization from
   `POST /api/v1/ci/releases/{id}/deployment-authorizations` (only
   issued when the release is currently eligible, and bound to that one
   release and digest). The deployment-recording endpoint requires and
   consumes that authorization for any CI-recorded production
   deployment, and independently re-checks the same gate again — so
   neither a stale client-side check nor a stale authorization can
   bypass it. On a successful production deployment, the release stage
   automatically becomes `production`. See
   [`docs/deployment-security.md`](docs/deployment-security.md) for what
   this does and does not enforce, and the infrastructure controls
   needed around it.
7. During post-release support, link incidents/fixes/follow-ups to the
   same release, then close it once the support period is handled.

## API overview

All endpoints are under `/api/v1`. Three authentication schemes, each
checked by its own middleware:

- **Session cookie** (`waypoint_session`, HTTP-only) — the web app.
- **Per-user MCP token** (`Authorization: Bearer ...`) — the MCP server, under `/api/v1/mcp/*`.
- **Per-project CI token** (`Authorization: Bearer ...`) — CI/CD pipelines, under `/api/v1/ci/*`.

Selected endpoints (see `internal/server/router.go` for the full list):

```
POST   /api/v1/auth/login
GET    /api/v1/auth/me
POST   /api/v1/projects
GET    /api/v1/projects/{projectID}/releases
POST   /api/v1/projects/{projectID}/releases
GET    /api/v1/releases/{releaseID}/full          # summary+changes+evidence+approvals+deployments+support+activity
POST   /api/v1/releases/{releaseID}/transition
POST   /api/v1/releases/{releaseID}/evidence
POST   /api/v1/releases/{releaseID}/approvals
GET    /api/v1/releases/{releaseID}/eligibility
POST   /api/v1/releases/{releaseID}/support-items
GET    /api/v1/releases/{releaseID}/mcp-submissions?status=pending
POST   /api/v1/mcp-submissions/{id}/accept
POST   /api/v1/mcp-submissions/{id}/reject

GET    /api/v1/mcp/releases/{releaseID}/context           (MCP token)
POST   /api/v1/mcp/releases/{releaseID}/change-notes       (MCP token)

GET    /api/v1/releases/{releaseID}/candidates
POST   /api/v1/releases/{releaseID}/candidates

POST   /api/v1/projects/{projectID}/checklist-templates
GET    /api/v1/projects/{projectID}/checklist-templates/active
GET    /api/v1/projects/{projectID}/checklist-templates
GET    /api/v1/releases/{releaseID}/checklist-items
POST   /api/v1/checklist-items/{itemID}/owner
POST   /api/v1/checklist-items/{itemID}/verify
POST   /api/v1/checklist-items/{itemID}/reject
POST   /api/v1/checklist-items/{itemID}/waive

GET    /api/v1/ci/releases/{releaseID}/eligibility                      (CI token)
POST   /api/v1/ci/releases/{releaseID}/candidates                      (CI token)
POST   /api/v1/ci/releases/{releaseID}/deployment-authorizations        (CI token)
POST   /api/v1/ci/releases/{releaseID}/deployments                      (CI token)

GET    /healthz
GET    /readyz
```

Errors are a consistent JSON envelope: `{"error": {"code", "message", "details"}}`.

## The production gate

`internal/gate.Evaluate` is the **only** place eligibility is computed,
used by both the human-readable `.../eligibility` endpoints and the
deployment-recording endpoints. A release is eligible for production
when:

- its stage is `ready_for_production` (or already `production`, for a redeploy), **and**
- every evidence type required by the project's policy has at least one submission, **and**
- the number of distinct approvers whose *most recent* decision is `approved` meets the policy's required count, **and**
- (if the policy's `block_on_open_rejection` is set, the default) the single most recent approval decision on the release — across all approvers — is not a rejection.

Required evidence types and the approval count are configurable per
project (`GET`/`PATCH /api/v1/projects/{id}/policy`); a sensible default
is seeded when a project is created (`change_summary`, `test_results`,
`uat_signoff`, `rollback_plan`; 1 required approval).

**"The project's policy" means a frozen snapshot once a release has
reached `ready_for_production`, not whatever the policy currently says.**
Every time a release enters `ready_for_production` (including a later
re-entry, e.g. after being sent back to `uat` for a fix), the project's
current policy is captured onto that release
(`release_policy_snapshots`), and `gate.Evaluate` reads that snapshot
from then on — `GET .../eligibility`'s `policy_snapshot_at` field is set
once this has happened. Editing the project's policy afterward (e.g.
lowering the required approval count) has no effect on a release that
already has a snapshot; it only affects releases that haven't reached
`ready_for_production` yet. This closes
[`docs/architecture-audit.md`](docs/architecture-audit.md) Finding 3 —
previously, a policy edit could silently change the requirements an
in-progress release was being held to.

Eligibility alone is **not** what gates a CI-recorded production
deployment — see the next section.

## Deployment authorization

`GET .../eligibility` is a read-only, human-readable check: it answers
"would a production deployment be accepted right now," but calling it
does not authorize anything. The actual gate for a CI-recorded
production deployment is a short-lived, single-use **deployment
authorization**, minted only when the release is eligible and bound to
one exact release + artifact digest:

1. Build your artifact and know its immutable digest (e.g. `sha256:...`).
2. `POST /api/v1/ci/releases/{id}/deployment-authorizations` with
   `{"artifact_digest": "sha256:..."}`. This re-runs the same
   `gate.Evaluate` the eligibility endpoint uses; it fails with `409` and
   the current blockers if the release isn't eligible. On success it
   returns a `token`, shown exactly once, that expires after
   `WAYPOINT_DEPLOY_AUTH_TTL_SECONDS` (default 900s / 15 minutes).
3. Deploy the artifact.
4. `POST /api/v1/ci/releases/{id}/deployments` with
   `"environment": "production"` and
   `"deployment_authorization_token"` set to that token, alongside the
   same `artifact_digest`. The token is consumed atomically on first use
   (concurrent attempts with the same token can never both succeed), and
   is rejected if it's missing, expired, already used, or minted for a
   different release or digest than the one being recorded.

Non-production deployments, and deployments recorded manually from the
web app by an admin/release coordinator, do not require or use this
token — see [`docs/deployment-security.md`](docs/deployment-security.md)
for exactly what this does and does not close, and what has to exist
outside Waypoint's own code (branch protection, environment reviewers,
cluster/registry access control) for it to be meaningful rather than
advisory.

## Release candidates and artifact integrity

A release candidate (`internal/candidates`) is the immutable build
identity — git repo/branch, commit SHA, build ID, image digest, app
version, config/migration checksums, build provenance URL — a release
currently points at. A release's **current candidate** is simply the
most recently registered one for it; there's no separate pointer to keep
in sync.

- `POST /api/v1/releases/{id}/candidates` (web) or
  `POST /api/v1/ci/releases/{id}/candidates` (CI token) registers one.
  Only `image_digest` is required.
- Whenever a candidate is registered, a deployment authorization
  (above) can only be minted for that exact digest — requesting one for
  any other digest is rejected, regardless of project policy.
- Each approval decision (`POST .../approvals`) is stamped with whichever
  candidate was current at the moment of the decision.
- A project can additionally set
  `require_candidate_for_approval: true` on its policy
  (`PATCH /api/v1/projects/{id}/policy`, default `false`). When set, the
  gate (`internal/gate.Evaluate`) only counts an approval toward the
  required-approvals count while it still matches the release's current
  candidate — registering a *new* candidate (the artifact changed)
  silently drops every prior approval from the count, with no separate
  "invalidate" step, forcing a fresh approval against the new build
  before the release is eligible again. With the flag unset (the
  default), approvals count the same way they always have, so existing
  projects are unaffected.

This closes
[`docs/architecture-audit.md`](docs/architecture-audit.md) Finding 2 for
any project that registers candidates; it is opt-in (beyond the
always-on authorization/digest check above) rather than mandatory, to
avoid silently changing behavior for projects that don't use it.

## Structured go-live checklists

`internal/checklists` extends (not replaces — both exist side by side)
the free-text evidence system with a versioned, per-project checklist:

- A project has one **active template version** at a time
  (`checklist_templates` + `checklist_template_items`), grouped into 4
  fixed categories: `development`, `uat`, `production_readiness`,
  `post_deployment`. Every new project is auto-seeded with a starter
  template (the categories/items the brief suggests) so the feature
  works out of the box; `POST /api/v1/projects/{id}/checklist-templates`
  creates a new version (deactivating the previous one — existing
  releases are unaffected).
- When a release is **created**, it is assigned the project's
  currently-active template version by copying each item into its own
  row (`release_checklist_items`). A later template version change never
  retroactively alters a release already created — verified by a
  dedicated test.
- Each item has `is_required`, a `verification_method`
  (`human_attestation` or `automated_check` — descriptive only in this
  release; see Limitations), an assignable `owner_id`
  (`POST /api/v1/checklist-items/{id}/owner`), and a status:
  `pending` → `verified` (`POST .../verify`, reviewer sign-off + an
  optional evidence reference), `rejected` (`POST .../reject`, reason
  required), or `waived` (`POST .../waive`, reason required — a
  controlled exception, held to a stricter permission bar: project
  management, not just edit permission). Every transition is audited.
- A project can set `require_checklist_completion: true` on its policy
  (default `false`) to make the gate actually block on incomplete
  required items; `GET .../eligibility`'s `incomplete_checklist_items`
  lists outstanding item titles regardless of the flag, for visibility.

Closes [`docs/architecture-audit.md`](docs/architecture-audit.md)
Finding 5 for checklists specifically (evidence itself is still
free-text and unbound to a candidate — see Limitations).

## GitHub Actions integration

See [`deploy/github-actions/deploy.yml`](deploy/github-actions/deploy.yml)
for a sample workflow: it calls the CI eligibility endpoint, builds the
artifact, obtains a deployment authorization for that artifact's exact
digest (stopping the job if the release isn't eligible), runs your real
deploy step, and records the result (success or failure) with the
deployment's immutable artifact digest, commit SHA, and the workflow
run's own URL — consuming the authorization in the same call. Copy it
into the target service's own repository and fill in the real
build/deploy steps.

Mint a CI token (admin or release coordinator on the project):

```bash
curl -X POST http://localhost:8080/api/v1/projects/{projectID}/ci-tokens \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{"label": "github-actions"}'
```

The response's `raw_token` is shown exactly once — store it as a
repository secret (`WAYPOINT_CI_TOKEN`). Never commit it.

## Claude Code / MCP setup

`cmd/mcp` exposes four tools, scoped narrowly to release coordination
notes:

- `get_release_context` — read a release's status, evidence checklist, approvals, linked work, and recent notes.
- `submit_change_note` — what changed, why, affected service/files, tests run, linked PR/commit.
- `submit_design_decision` — the decision, rationale, alternatives considered, impact, linked PR/commit.
- `submit_support_note` — a post-go-live issue, investigation, or fix note.

**Every submit_\* call lands as a pending submission.** It never becomes
accepted release evidence by itself — a developer must review and
confirm it in the web app (or reject it) first. Claude's transcripts are
never stored; only the structured fields the tool call sends.

### 1. Mint a per-user MCP token

Sign in to the web app as the developer who will use Claude Code, then:

```bash
curl -X POST http://localhost:8080/api/v1/users/{yourUserID}/mcp-tokens \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{"label": "claude-code-laptop"}'
```

The response's `raw_token` is shown exactly once. Treat it like a
password — it authenticates as you.

### 2. Register the server with Claude Code

Copy [`.mcp.json.example`](.mcp.json.example) to `.mcp.json` (git-ignored
by convention — never commit a real token) and fill in the absolute path
to this repository and your token:

```json
{
  "mcpServers": {
    "waypoint": {
      "command": "go",
      "args": ["run", "./cmd/mcp"],
      "cwd": "/absolute/path/to/waypoint",
      "env": {
        "WAYPOINT_API_BASE_URL": "http://localhost:8080",
        "WAYPOINT_MCP_TOKEN": "your-raw-token-here"
      }
    }
  }
}
```

Then run `claude mcp add` or restart Claude Code so it picks up the
server (see the current official Claude Code MCP documentation for your
Claude Code version's exact registration command/flow, as this can
change between releases). Claude Code will then be able to call the four
tools above against your local Waypoint instance.

## Environment variables

See [`.env.example`](.env.example) for the full list with defaults. No
real secrets are committed anywhere in this repository.

| Variable | Used by | Purpose |
|---|---|---|
| `DATABASE_URL` | api | Postgres connection string |
| `WAYPOINT_HTTP_ADDR` | api | listen address, default `:8080` |
| `WAYPOINT_SESSION_COOKIE` | api | session cookie name |
| `WAYPOINT_SESSION_TTL_HOURS` | api | session lifetime |
| `WAYPOINT_COOKIE_SECURE` | api | set `true` behind HTTPS |
| `WAYPOINT_CORS_ORIGIN` | api | allowed origin (not needed in the Docker Compose setup, where the browser never calls the API cross-origin) |
| `ADMIN_BOOTSTRAP_TOKEN` | api | enables the one-time `/bootstrap/admin` endpoint; unset disables it |
| `WAYPOINT_DEPLOY_AUTH_TTL_SECONDS` | api | how long a minted deployment authorization stays valid before it must be re-issued; default `900` (15 min), max `3600` |
| `WAYPOINT_API_URL` | web (build-time) | backend URL the Next.js proxy forwards to |
| `WAYPOINT_API_BASE_URL`, `WAYPOINT_MCP_TOKEN` | cmd/mcp | backend URL + the developer's MCP token |

## Testing

```bash
# Fast unit tests (no database needed)
go test ./internal/releases/... ./internal/config/...

# Full suite, including DB-backed integration tests for the release
# transition rules, the production gate's decisions, authorization
# (self-approval, roles, project scoping), and the MCP review flow.
# Needs a disposable Postgres instance — the docker-compose `db` service
# works if you temporarily publish its port, or run a throwaway container:
docker run -d --rm --name waypoint-test-db -e POSTGRES_USER=waypoint \
  -e POSTGRES_PASSWORD=waypoint -e POSTGRES_DB=waypoint -p 55432:5432 postgres:16-alpine

WAYPOINT_TEST_DATABASE_URL="postgres://waypoint:waypoint@localhost:55432/waypoint?sslmode=disable" \
  go test ./...

# Frontend type check
cd web && npm run typecheck
```

Without `WAYPOINT_TEST_DATABASE_URL` set, the integration tests skip
(and `go test ./...` still passes) rather than failing.

## Limitations

Explicit, deliberate MVP simplifications — flagged here rather than
silently assumed:

- **Roles are global per user, not per project.** A real multi-tenant
  deployment would likely want per-project roles; here, project
  *membership* gates visibility/action, but a user's role (e.g.
  `approver`) is the same across every project they're a member of.
- **"Named approvers" = any project member with role `approver` or
  `admin`**, not a separate per-release approver assignment list.
- **No SSO.** Local email/password only; see
  [above](#creating-the-first-admin-account).
- **No file storage for evidence attachments.** `evidence.attachment_url`
  is a free-text URL field; there is no upload endpoint or object
  storage integration in this MVP.
- **The web app's manual "record deployment" form** (for
  admins/coordinators) exists for completeness and backfilling, but the
  primary, intended path for production deployments is the CI-token
  endpoint from a pipeline.
- **No real GitHub App / webhook integration.** Work item, PR, commit,
  and CI run links are manually pasted URLs, per the brief.
- **Rate limiting is not implemented** on any endpoint.
- **Candidate-bound approvals are opt-in, not mandatory.** A project
  must set `require_candidate_for_approval: true` (see "Release
  candidates and artifact integrity" above) for approvals to actually be
  invalidated when the artifact changes; with the (default) flag unset,
  approvals count exactly as they did before release candidates existed.
  This was a deliberate choice to avoid silently changing gate behavior
  for every existing project — see `docs/architecture-audit.md` Finding 2
  and `docs/implementation-roadmap.md` item 2 for the reasoning.
- **The original free-text evidence system is unchanged and still
  unverified.** Anyone with project visibility can still submit any
  `evidence` type as free text with no owner or verification status.
  The structured checklist engine (`internal/checklists`, roadmap item
  4) is a separate, richer, opt-in layer alongside it — it does not
  retrofit ownership or verification onto the original evidence
  records.
- **Checklist `verification_method` is descriptive only.** An item
  marked `automated_check` documents that it's ideally machine-verified,
  but nothing in this release wires up a channel for an automated
  system to actually report a result — every item, regardless of this
  field, is still verified/rejected/waived by a human through the web
  API today.
- **Checklist categories are a fixed 4-value enum** (`development`,
  `uat`, `production_readiness`, `post_deployment`), matching the
  brief's suggested categories; only the items within each category are
  project-configurable, not the category taxonomy itself.
- **Making the authorization check unavoidable also requires controls
  outside this repository** (GitHub Environment protection rules,
  branch protection on the workflow file, scoped cluster/registry
  credentials) — see
  [`docs/deployment-security.md`](docs/deployment-security.md). Nothing
  in this codebase can prevent a deployment that never calls Waypoint's
  API at all; it can only make the API path the correct, auditable one.
