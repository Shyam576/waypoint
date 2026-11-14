// Package deployments records what was actually deployed and where. The
// artifact digest, not a reusable tag, is the deployment's identity.
// Recording a production deployment always re-runs the same eligibility
// check the GET .../eligibility endpoint exposes (internal/gate); there
// is no path that skips it, including this package's own web-facing
// "manual record" handler for admins/coordinators.
package deployments

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/candidates"
	"github.com/Shyam576/waypoint/internal/dbx"
	"github.com/Shyam576/waypoint/internal/gate"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/releases"
	"github.com/Shyam576/waypoint/internal/users"
)

type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvUAT         Environment = "uat"
	EnvStaging     Environment = "staging"
	EnvProduction  Environment = "production"
)

func (e Environment) Valid() bool {
	switch e {
	case EnvDevelopment, EnvUAT, EnvStaging, EnvProduction:
		return true
	}
	return false
}

type Result string

const (
	ResultSuccess Result = "success"
	ResultFailure Result = "failure"
)

func (r Result) Valid() bool { return r == ResultSuccess || r == ResultFailure }

type RecordedVia string

const (
	RecordedViaCI  RecordedVia = "ci"
	RecordedViaWeb RecordedVia = "web"
)

// VerificationStatus is distinct from Result: Result records whether
// the deploy step itself exited 0; VerificationStatus records whether
// the application was actually confirmed healthy afterward — the two
// are never allowed to be confused. See
// docs/architecture-audit.md Finding 12.
type VerificationStatus string

const (
	VerificationPending VerificationStatus = "pending"
	VerificationPassed  VerificationStatus = "passed"
	VerificationFailed  VerificationStatus = "failed"
)

// VerifiedVia is who/what reported the verification result: an
// automated CI-run health check, or a human confirming manually.
type VerifiedVia string

const (
	VerifiedViaCI  VerifiedVia = "ci"
	VerifiedViaWeb VerifiedVia = "web"
)

type Deployment struct {
	ID                  uuid.UUID          `json:"id"`
	ReleaseID           uuid.UUID          `json:"release_id"`
	Environment         Environment        `json:"environment"`
	ArtifactDigest      string             `json:"artifact_digest"`
	ArtifactRef         *string            `json:"artifact_ref,omitempty"`
	CommitSHA           *string            `json:"commit_sha,omitempty"`
	WorkflowRunURL      string             `json:"workflow_run_url"`
	Result              Result             `json:"result"`
	RecordedVia         RecordedVia        `json:"recorded_via"`
	RecordedBy          *uuid.UUID         `json:"recorded_by,omitempty"`
	RecordedByCITokenID *uuid.UUID         `json:"recorded_by_ci_token_id,omitempty"`
	AuthorizationID     *uuid.UUID         `json:"authorization_id,omitempty"`
	OccurredAt          time.Time          `json:"occurred_at"`
	CreatedAt           time.Time          `json:"created_at"`
	VerificationStatus  VerificationStatus `json:"verification_status"`
	VerifiedBy          *uuid.UUID         `json:"verified_by,omitempty"`
	VerifiedVia         *VerifiedVia       `json:"verified_via,omitempty"`
	VerifiedAt          *time.Time         `json:"verified_at,omitempty"`
	VerificationNote    string             `json:"verification_note"`
}

type Store struct{}

func NewStore() *Store { return &Store{} }

type CreateParams struct {
	ReleaseID           uuid.UUID
	Environment         Environment
	ArtifactDigest      string
	ArtifactRef         *string
	CommitSHA           *string
	WorkflowRunURL      string
	Result              Result
	RecordedVia         RecordedVia
	RecordedBy          *uuid.UUID
	RecordedByCITokenID *uuid.UUID
	AuthorizationID     *uuid.UUID
	OccurredAt          time.Time
}

const deploymentColumns = `id, release_id, environment, artifact_digest, artifact_ref, commit_sha, workflow_run_url, result, recorded_via, recorded_by, recorded_by_ci_token_id, authorization_id, occurred_at, created_at, verification_status, verified_by, verified_via, verified_at, verification_note`

func scanDeployment(row pgx.Row) (Deployment, error) {
	var d Deployment
	err := row.Scan(&d.ID, &d.ReleaseID, &d.Environment, &d.ArtifactDigest, &d.ArtifactRef, &d.CommitSHA, &d.WorkflowRunURL,
		&d.Result, &d.RecordedVia, &d.RecordedBy, &d.RecordedByCITokenID, &d.AuthorizationID, &d.OccurredAt, &d.CreatedAt,
		&d.VerificationStatus, &d.VerifiedBy, &d.VerifiedVia, &d.VerifiedAt, &d.VerificationNote)
	return d, err
}

func (s *Store) Create(ctx context.Context, q dbx.Querier, p CreateParams) (Deployment, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO deployments (release_id, environment, artifact_digest, artifact_ref, commit_sha, workflow_run_url, result, recorded_via, recorded_by, recorded_by_ci_token_id, authorization_id, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+deploymentColumns,
		p.ReleaseID, p.Environment, p.ArtifactDigest, p.ArtifactRef, p.CommitSHA, p.WorkflowRunURL, p.Result, p.RecordedVia, p.RecordedBy, p.RecordedByCITokenID, p.AuthorizationID, p.OccurredAt,
	)
	return scanDeployment(row)
}

func (s *Store) GetByID(ctx context.Context, q dbx.Querier, id uuid.UUID) (Deployment, error) {
	row := q.QueryRow(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE id = $1`, id)
	return scanDeployment(row)
}

func (s *Store) ListByRelease(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) ([]Deployment, error) {
	rows, err := q.Query(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE release_id = $1 ORDER BY occurred_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// deploymentJoinedColumns is deploymentColumns qualified for a query
// that joins deployments to releases (to filter by project), in the
// same order, so scanDeployment can be reused unchanged.
const deploymentJoinedColumns = `d.id, d.release_id, d.environment, d.artifact_digest, d.artifact_ref, d.commit_sha, d.workflow_run_url, d.result, d.recorded_via, d.recorded_by, d.recorded_by_ci_token_id, d.authorization_id, d.occurred_at, d.created_at, d.verification_status, d.verified_by, d.verified_via, d.verified_at, d.verification_note`

func (s *Store) queryJoined(ctx context.Context, q dbx.Querier, extraWhere string, projectIDs []uuid.UUID, limit int) ([]Deployment, error) {
	rows, err := q.Query(ctx, `
		SELECT `+deploymentJoinedColumns+`
		FROM deployments d
		JOIN releases r ON r.id = d.release_id
		WHERE r.project_id = ANY($1)`+extraWhere+`
		ORDER BY d.occurred_at DESC
		LIMIT $2`, projectIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListRecent returns the most recent deployments across every release
// the caller can see, for the dashboard's "recent deployments" panel.
func (s *Store) ListRecent(ctx context.Context, q dbx.Querier, projectIDs []uuid.UUID, limit int) ([]Deployment, error) {
	return s.queryJoined(ctx, q, "", projectIDs, limit)
}

// ListFailed returns the most recent deployments whose own result was
// failure, across every project the caller can see, for the
// dashboard's "failed deployments" panel.
func (s *Store) ListFailed(ctx context.Context, q dbx.Querier, projectIDs []uuid.UUID, limit int) ([]Deployment, error) {
	return s.queryJoined(ctx, q, " AND d.result = 'failure'", projectIDs, limit)
}

// ListAwaitingVerification returns the most recent successful
// deployments whose verification_status is still pending — the deploy
// step exited 0, but nobody has yet confirmed the application is
// actually healthy. See docs/architecture-audit.md Finding 12.
func (s *Store) ListAwaitingVerification(ctx context.Context, q dbx.Querier, projectIDs []uuid.UUID, limit int) ([]Deployment, error) {
	return s.queryJoined(ctx, q, " AND d.result = 'success' AND d.verification_status = 'pending'", projectIDs, limit)
}

type ReleaseAccess interface {
	GetForService(ctx context.Context, identity authn.Identity, id uuid.UUID) (releases.Release, error)
	// CanEdit backs SubmitVerificationFromWeb: the same bar
	// internal/checklists uses for confirming (not waiving) a checklist
	// item — a developer/coordinator/admin who can see the project, not
	// just anyone with release visibility.
	CanEdit(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error)
}

type ReleasePromoter interface {
	PromoteToProduction(ctx context.Context, tx dbx.Querier, id uuid.UUID, actor audit.Actor) error
}

type ProjectAccess interface {
	CanManage(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error)
}

type Service struct {
	pool       *pgxpool.Pool
	store      *Store
	audit      *audit.Store
	gate       *gate.Service
	releases   *releases.Store
	promoter   ReleasePromoter
	releaseAcc ReleaseAccess
	projects   ProjectAccess
	candidates *candidates.Store
	authTTL    time.Duration
}

// NewService wires a deployments Service. authTTL bounds how long a
// minted deployment authorization (see authorization.go) stays valid
// before it must be re-issued; it has no effect on anything else.
func NewService(pool *pgxpool.Pool, store *Store, auditStore *audit.Store, gateSvc *gate.Service, releasesStore *releases.Store, promoter ReleasePromoter, releaseAccess ReleaseAccess, projectAccess ProjectAccess, candidatesStore *candidates.Store, authTTL time.Duration) *Service {
	return &Service{pool: pool, store: store, audit: auditStore, gate: gateSvc, releases: releasesStore, promoter: promoter, releaseAcc: releaseAccess, projects: projectAccess, candidates: candidatesStore, authTTL: authTTL}
}

type RecordInput struct {
	Environment             Environment
	ArtifactDigest          string
	ArtifactRef             *string
	CommitSHA               *string
	WorkflowRunURL          string
	Result                  Result
	OccurredAt              time.Time
	DeploymentAuthorization string
}

func (in RecordInput) validate() error {
	if !in.Environment.Valid() {
		return httpx.BadRequest("environment must be one of development, uat, staging, production")
	}
	if in.ArtifactDigest == "" {
		return httpx.BadRequest("artifact_digest is required and must be an immutable digest, not a reusable tag")
	}
	if in.WorkflowRunURL == "" {
		return httpx.BadRequest("workflow_run_url is required")
	}
	if !in.Result.Valid() {
		return httpx.BadRequest("result must be success or failure")
	}
	return nil
}

// RecordFromCI records a deployment submitted by a CI/CD pipeline using a
// project-scoped CI token. For a production deployment, it also requires
// and consumes a deployment authorization (see authorization.go) that
// must have been minted for this exact release and artifact digest: the
// pipeline obtains one from IssueAuthorization (which only succeeds when
// the gate currently says the release is eligible), and this call
// independently re-checks the gate again and verifies the authorization
// matches before accepting the record, so neither a stale client-side
// check nor a stale authorization can bypass the gate.
func (s *Service) RecordFromCI(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, in RecordInput) (Deployment, error) {
	if identity.CITokenID == nil {
		return Deployment{}, httpx.Unauthorized("ci token required")
	}
	if err := in.validate(); err != nil {
		return Deployment{}, err
	}
	rel, err := s.resolveCIRelease(ctx, identity, releaseID)
	if err != nil {
		return Deployment{}, err
	}
	return s.record(ctx, identity, rel, in, RecordedViaCI, nil, identity.CITokenID)
}

// RecordFromWeb lets an admin or release coordinator record a deployment
// manually from the web app (e.g. to backfill or correct a record). It
// goes through the identical gate check as the CI path.
func (s *Service) RecordFromWeb(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, in RecordInput) (Deployment, error) {
	rel, err := s.releaseAcc.GetForService(ctx, identity, releaseID)
	if err != nil {
		return Deployment{}, err
	}
	if identity.User.Role != users.RoleAdmin && identity.User.Role != users.RoleReleaseCoordinator {
		return Deployment{}, httpx.Forbidden("only admins and release coordinators can record deployments manually")
	}
	canManage, err := s.projects.CanManage(ctx, identity, rel.ProjectID)
	if err != nil {
		return Deployment{}, err
	}
	if !canManage {
		return Deployment{}, httpx.Forbidden("you do not have permission to manage this project")
	}
	if err := in.validate(); err != nil {
		return Deployment{}, err
	}
	return s.record(ctx, identity, rel, in, RecordedViaWeb, &identity.User.ID, nil)
}

// requireAuthorization reports whether recording this deployment must
// present and consume a deployment authorization: only CI-recorded
// production deployments go through the authorization flow. A manual
// web-recorded entry (RecordFromWeb, for admins/coordinators backfilling
// or correcting a record) is unaffected — it is already gated on role and
// project-management permission, and still re-checks the gate below.
func requireAuthorization(via RecordedVia, env Environment) bool {
	return via == RecordedViaCI && env == EnvProduction
}

func (s *Service) record(ctx context.Context, identity authn.Identity, rel releases.Release, in RecordInput, via RecordedVia, recordedBy, ciTokenID *uuid.UUID) (Deployment, error) {
	if requireAuthorization(via, in.Environment) && in.DeploymentAuthorization == "" {
		return Deployment{}, httpx.BadRequest("deployment_authorization_token is required for a CI-recorded production deployment; obtain one from POST .../deployment-authorizations first")
	}
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now()
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, err
	}
	defer tx.Rollback(ctx)

	// A row lock on the release is taken inside this transaction before
	// anything else, so a concurrent approval/policy change cannot land
	// in the gap between "the gate said eligible" and "the deployment
	// got recorded" — see docs/architecture-audit.md Finding 11. A
	// failed check anywhere below still just returns; the deferred
	// rollback above discards the lock and anything else done so far.
	if in.Environment == EnvProduction {
		if err := s.releases.LockForUpdate(ctx, tx, rel.ID); err != nil {
			return Deployment{}, err
		}
	}

	// Authorization is consumed before the gate re-check (not after) so
	// that a rollback-linked authorization (see rollback.go) can be
	// detected here and substituted for the normal gate check below.
	// Consuming inside this same transaction keeps the safety property
	// documented on ConsumeAuthorization: if anything after this point
	// fails, the deferred rollback undoes the consumption too, so a
	// token is never burned by a failed attempt.
	var authorizationID *uuid.UUID
	var consumedAuth Authorization
	if requireAuthorization(via, in.Environment) {
		auth, err := s.store.ConsumeAuthorization(ctx, tx, hashAuthToken(in.DeploymentAuthorization))
		if err != nil {
			return Deployment{}, httpx.Forbidden("deployment authorization is invalid, expired, revoked, or already used")
		}
		if auth.ReleaseID != rel.ID {
			return Deployment{}, httpx.Forbidden("deployment authorization was not issued for this release")
		}
		if auth.ArtifactDigest != in.ArtifactDigest {
			return Deployment{}, httpx.Forbidden("deployment authorization does not match the artifact_digest being recorded")
		}
		authorizationID = &auth.ID
		consumedAuth = auth
	}

	// A rollback authorization substitutes its own provenance check
	// (done up front in InitiateRollback: the target must already have
	// been a prior successful production deployment) for the normal
	// gate re-check. Every other production deployment still re-runs
	// the gate here, with no exceptions.
	if in.Environment == EnvProduction && consumedAuth.RollbackID == nil {
		result, err := s.gate.EvaluateTx(ctx, tx, rel.ID)
		if err != nil {
			return Deployment{}, err
		}
		if !result.Eligible {
			return Deployment{}, httpx.Conflict("release is not eligible for production deployment").WithDetails(result)
		}
	}

	d, err := s.store.Create(ctx, tx, CreateParams{
		ReleaseID: rel.ID, Environment: in.Environment, ArtifactDigest: in.ArtifactDigest, ArtifactRef: in.ArtifactRef,
		CommitSHA: in.CommitSHA, WorkflowRunURL: in.WorkflowRunURL, Result: in.Result, RecordedVia: via,
		RecordedBy: recordedBy, RecordedByCITokenID: ciTokenID, AuthorizationID: authorizationID, OccurredAt: in.OccurredAt,
	})
	if err != nil {
		return Deployment{}, err
	}
	if authorizationID != nil {
		if err := s.audit.Record(ctx, tx, "release", rel.ID, "deployment_authorization_consumed", identity.AuditActor(), nil, map[string]any{"authorization_id": authorizationID}, ""); err != nil {
			return Deployment{}, err
		}
	}
	if err := s.audit.Record(ctx, tx, "release", rel.ID, "deployment_recorded", identity.AuditActor(), nil, d, ""); err != nil {
		return Deployment{}, err
	}

	if consumedAuth.RollbackID != nil {
		rb, err := s.store.MarkRollbackResult(ctx, tx, *consumedAuth.RollbackID, in.Result, d.ID)
		if err != nil {
			return Deployment{}, err
		}
		if err := s.audit.Record(ctx, tx, "release", rel.ID, "rollback_result_recorded", identity.AuditActor(), nil, rb, ""); err != nil {
			return Deployment{}, err
		}
	}

	if in.Environment == EnvProduction && in.Result == ResultSuccess && rel.Stage != releases.StageProduction {
		if err := s.promoter.PromoteToProduction(ctx, tx, rel.ID, identity.AuditActor()); err != nil {
			return Deployment{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, err
	}
	return d, nil
}

func (s *Service) ListByRelease(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) ([]Deployment, error) {
	if _, err := s.releaseAcc.GetForService(ctx, identity, releaseID); err != nil {
		return nil, err
	}
	return s.store.ListByRelease(ctx, s.pool, releaseID)
}

// EligibilityForCI is the CI-token-scoped equivalent of the web
// eligibility endpoint: same Evaluate call, different authorization path.
func (s *Service) EligibilityForCI(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) (gate.Result, error) {
	rel, err := s.resolveCIRelease(ctx, identity, releaseID)
	if err != nil {
		return gate.Result{}, err
	}
	return s.gate.Evaluate(ctx, rel.ID)
}
