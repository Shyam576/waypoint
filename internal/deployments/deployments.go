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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
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

type Deployment struct {
	ID                  uuid.UUID   `json:"id"`
	ReleaseID           uuid.UUID   `json:"release_id"`
	Environment         Environment `json:"environment"`
	ArtifactDigest      string      `json:"artifact_digest"`
	ArtifactRef         *string     `json:"artifact_ref,omitempty"`
	CommitSHA           *string     `json:"commit_sha,omitempty"`
	WorkflowRunURL      string      `json:"workflow_run_url"`
	Result              Result      `json:"result"`
	RecordedVia         RecordedVia `json:"recorded_via"`
	RecordedBy          *uuid.UUID  `json:"recorded_by,omitempty"`
	RecordedByCITokenID *uuid.UUID  `json:"recorded_by_ci_token_id,omitempty"`
	OccurredAt          time.Time   `json:"occurred_at"`
	CreatedAt           time.Time   `json:"created_at"`
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
	OccurredAt          time.Time
}

func (s *Store) Create(ctx context.Context, q dbx.Querier, p CreateParams) (Deployment, error) {
	var d Deployment
	err := q.QueryRow(ctx, `
		INSERT INTO deployments (release_id, environment, artifact_digest, artifact_ref, commit_sha, workflow_run_url, result, recorded_via, recorded_by, recorded_by_ci_token_id, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, release_id, environment, artifact_digest, artifact_ref, commit_sha, workflow_run_url, result, recorded_via, recorded_by, recorded_by_ci_token_id, occurred_at, created_at`,
		p.ReleaseID, p.Environment, p.ArtifactDigest, p.ArtifactRef, p.CommitSHA, p.WorkflowRunURL, p.Result, p.RecordedVia, p.RecordedBy, p.RecordedByCITokenID, p.OccurredAt,
	).Scan(&d.ID, &d.ReleaseID, &d.Environment, &d.ArtifactDigest, &d.ArtifactRef, &d.CommitSHA, &d.WorkflowRunURL, &d.Result, &d.RecordedVia, &d.RecordedBy, &d.RecordedByCITokenID, &d.OccurredAt, &d.CreatedAt)
	return d, err
}

func (s *Store) ListByRelease(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) ([]Deployment, error) {
	rows, err := q.Query(ctx, `
		SELECT id, release_id, environment, artifact_digest, artifact_ref, commit_sha, workflow_run_url, result, recorded_via, recorded_by, recorded_by_ci_token_id, occurred_at, created_at
		FROM deployments WHERE release_id = $1 ORDER BY occurred_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		var d Deployment
		if err := rows.Scan(&d.ID, &d.ReleaseID, &d.Environment, &d.ArtifactDigest, &d.ArtifactRef, &d.CommitSHA, &d.WorkflowRunURL, &d.Result, &d.RecordedVia, &d.RecordedBy, &d.RecordedByCITokenID, &d.OccurredAt, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListRecent returns the most recent deployments across every release
// the caller can see, for the dashboard's "recent deployments" panel.
func (s *Store) ListRecent(ctx context.Context, q dbx.Querier, projectIDs []uuid.UUID, limit int) ([]Deployment, error) {
	rows, err := q.Query(ctx, `
		SELECT d.id, d.release_id, d.environment, d.artifact_digest, d.artifact_ref, d.commit_sha, d.workflow_run_url, d.result, d.recorded_via, d.recorded_by, d.recorded_by_ci_token_id, d.occurred_at, d.created_at
		FROM deployments d
		JOIN releases r ON r.id = d.release_id
		WHERE r.project_id = ANY($1)
		ORDER BY d.occurred_at DESC
		LIMIT $2`, projectIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		var d Deployment
		if err := rows.Scan(&d.ID, &d.ReleaseID, &d.Environment, &d.ArtifactDigest, &d.ArtifactRef, &d.CommitSHA, &d.WorkflowRunURL, &d.Result, &d.RecordedVia, &d.RecordedBy, &d.RecordedByCITokenID, &d.OccurredAt, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type ReleaseAccess interface {
	GetForService(ctx context.Context, identity authn.Identity, id uuid.UUID) (releases.Release, error)
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
}

func NewService(pool *pgxpool.Pool, store *Store, auditStore *audit.Store, gateSvc *gate.Service, releasesStore *releases.Store, promoter ReleasePromoter, releaseAccess ReleaseAccess, projectAccess ProjectAccess) *Service {
	return &Service{pool: pool, store: store, audit: auditStore, gate: gateSvc, releases: releasesStore, promoter: promoter, releaseAcc: releaseAccess, projects: projectAccess}
}

type RecordInput struct {
	Environment    Environment
	ArtifactDigest string
	ArtifactRef    *string
	CommitSHA      *string
	WorkflowRunURL string
	Result         Result
	OccurredAt     time.Time
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
// project-scoped CI token. It is the enforcement point the main user
// flow describes: the pipeline must check eligibility first and this
// call independently re-checks it for any production deployment, so a
// stale client-side check can never bypass the gate.
func (s *Service) RecordFromCI(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, in RecordInput) (Deployment, error) {
	if identity.Via != authn.ViaCI || identity.CITokenID == nil {
		return Deployment{}, httpx.Unauthorized("ci token required")
	}
	if err := in.validate(); err != nil {
		return Deployment{}, err
	}
	rel, err := s.releases.GetByID(ctx, s.pool, releaseID)
	if err != nil {
		return Deployment{}, httpx.NotFound("release not found")
	}
	if identity.CIProjectID == nil || *identity.CIProjectID != rel.ProjectID {
		return Deployment{}, httpx.Forbidden("this ci token is not scoped to this release's project")
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

func (s *Service) record(ctx context.Context, identity authn.Identity, rel releases.Release, in RecordInput, via RecordedVia, recordedBy, ciTokenID *uuid.UUID) (Deployment, error) {
	if in.Environment == EnvProduction {
		result, err := s.gate.Evaluate(ctx, rel.ID)
		if err != nil {
			return Deployment{}, err
		}
		if !result.Eligible {
			return Deployment{}, httpx.Conflict("release is not eligible for production deployment").WithDetails(result)
		}
	}
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now()
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, err
	}
	defer tx.Rollback(ctx)

	d, err := s.store.Create(ctx, tx, CreateParams{
		ReleaseID: rel.ID, Environment: in.Environment, ArtifactDigest: in.ArtifactDigest, ArtifactRef: in.ArtifactRef,
		CommitSHA: in.CommitSHA, WorkflowRunURL: in.WorkflowRunURL, Result: in.Result, RecordedVia: via,
		RecordedBy: recordedBy, RecordedByCITokenID: ciTokenID, OccurredAt: in.OccurredAt,
	})
	if err != nil {
		return Deployment{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", rel.ID, "deployment_recorded", identity.AuditActor(), nil, d, ""); err != nil {
		return Deployment{}, err
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
	if identity.Via != authn.ViaCI {
		return gate.Result{}, httpx.Unauthorized("ci token required")
	}
	rel, err := s.releases.GetByID(ctx, s.pool, releaseID)
	if err != nil {
		return gate.Result{}, httpx.NotFound("release not found")
	}
	if identity.CIProjectID == nil || *identity.CIProjectID != rel.ProjectID {
		return gate.Result{}, httpx.Forbidden("this ci token is not scoped to this release's project")
	}
	return s.gate.Evaluate(ctx, releaseID)
}
