package candidates

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/releases"
)

// ReleaseAccess is the subset of releases.Service candidates needs for
// the web registration/listing path: fetching the release while
// enforcing project visibility, and checking whether this identity may
// make changes on it (the same bar as editing the release itself).
type ReleaseAccess interface {
	GetForService(ctx context.Context, identity authn.Identity, id uuid.UUID) (releases.Release, error)
	CanEdit(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error)
}

type Service struct {
	pool       *pgxpool.Pool
	store      *Store
	audit      *audit.Store
	releases   *releases.Store
	releaseAcc ReleaseAccess
}

func NewService(pool *pgxpool.Pool, store *Store, auditStore *audit.Store, releasesStore *releases.Store, releaseAccess ReleaseAccess) *Service {
	return &Service{pool: pool, store: store, audit: auditStore, releases: releasesStore, releaseAcc: releaseAccess}
}

// RegisterInput is the build identity a candidate records. ImageDigest is
// the only required field — everything else is best-effort metadata a
// caller may not always have.
type RegisterInput struct {
	GitRepo            string
	GitBranch          string
	CommitSHA          string
	BuildID            string
	ImageDigest        string
	AppVersion         string
	ConfigChecksum     string
	MigrationChecksum  string
	BuildProvenanceURL string
}

func (in RegisterInput) validate() error {
	if in.ImageDigest == "" {
		return httpx.BadRequest("image_digest is required and must be an immutable digest, not a reusable tag")
	}
	return nil
}

// RegisterFromWeb lets a project member who can edit this release record
// a new candidate (e.g. a developer pasting the commit/digest once a
// build is known). Registering a new candidate makes it the release's
// current candidate; any approval decision made against an older
// candidate stops counting toward eligibility for a project whose policy
// requires a matching candidate (internal/gate).
func (s *Service) RegisterFromWeb(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, in RegisterInput) (Candidate, error) {
	rel, err := s.releaseAcc.GetForService(ctx, identity, releaseID)
	if err != nil {
		return Candidate{}, err
	}
	ok, err := s.releaseAcc.CanEdit(ctx, identity, rel.ProjectID)
	if err != nil {
		return Candidate{}, err
	}
	if !ok {
		return Candidate{}, httpx.Forbidden("you do not have permission to register a release candidate for this release")
	}
	if err := in.validate(); err != nil {
		return Candidate{}, err
	}
	return s.register(ctx, identity, rel.ID, in, CreatedViaWeb, &identity.User.ID, nil)
}

// RegisterFromCI lets a project-scoped CI token register a candidate,
// typically right after a pipeline builds and pushes the artifact and
// before it requests a deployment authorization for that same digest.
func (s *Service) RegisterFromCI(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, in RegisterInput) (Candidate, error) {
	if identity.Via != authn.ViaCI || identity.CITokenID == nil {
		return Candidate{}, httpx.Unauthorized("ci token required")
	}
	rel, err := s.releases.GetByID(ctx, s.pool, releaseID)
	if err != nil {
		return Candidate{}, httpx.NotFound("release not found")
	}
	if identity.CIProjectID == nil || *identity.CIProjectID != rel.ProjectID {
		return Candidate{}, httpx.Forbidden("this ci token is not scoped to this release's project")
	}
	if err := in.validate(); err != nil {
		return Candidate{}, err
	}
	return s.register(ctx, identity, rel.ID, in, CreatedViaCI, nil, identity.CITokenID)
}

func (s *Service) register(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, in RegisterInput, via CreatedVia, createdBy, ciTokenID *uuid.UUID) (Candidate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Candidate{}, err
	}
	defer tx.Rollback(ctx)

	c, err := s.store.Create(ctx, tx, CreateParams{
		ReleaseID: releaseID, GitRepo: in.GitRepo, GitBranch: in.GitBranch, CommitSHA: in.CommitSHA, BuildID: in.BuildID,
		ImageDigest: in.ImageDigest, AppVersion: in.AppVersion, ConfigChecksum: in.ConfigChecksum, MigrationChecksum: in.MigrationChecksum,
		BuildProvenanceURL: in.BuildProvenanceURL, CreatedVia: via, CreatedBy: createdBy, CreatedByCITokenID: ciTokenID,
	})
	if err != nil {
		return Candidate{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", releaseID, "release_candidate_registered", identity.AuditActor(), nil, c, "image_digest="+in.ImageDigest); err != nil {
		return Candidate{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Candidate{}, err
	}
	return c, nil
}

func (s *Service) ListByRelease(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) ([]Candidate, error) {
	if _, err := s.releaseAcc.GetForService(ctx, identity, releaseID); err != nil {
		return nil, err
	}
	return s.store.ListByRelease(ctx, s.pool, releaseID)
}
