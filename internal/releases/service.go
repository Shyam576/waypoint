package releases

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/checklists"
	"github.com/Shyam576/waypoint/internal/dbx"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/users"
)

// ProjectAccess is the subset of the projects package's Service that
// releases needs, kept as an interface to avoid an import cycle
// (projects -> ... -> releases is not needed, but keeping this narrow
// makes the dependency explicit and easy to test).
type ProjectAccess interface {
	CanView(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error)
	CanManage(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error)
	// CurrentPolicy returns the project's live policy as plain values, so
	// Transition can snapshot it onto a release entering
	// ready_for_production without this package importing
	// internal/projects for the Policy type.
	CurrentPolicy(ctx context.Context, projectID uuid.UUID) (requiredEvidenceTypes []string, requiredApprovalsCount int, blockOnOpenRejection, requireCandidateForApproval, requireChecklistCompletion bool, err error)
	// RiskPolicyFor returns the extra requirements a project's policy
	// defines for one specific risk tier (zero values if none are
	// defined for it), so Transition can fold them into the policy
	// snapshot alongside the base policy. See
	// docs/architecture-audit.md Finding 4.
	RiskPolicyFor(ctx context.Context, projectID uuid.UUID, riskLevel string) (extraRequiredApprovalsCount int, extraRequiredEvidenceTypes []string, requireRiskAcceptance bool, err error)
}

type Service struct {
	pool       *pgxpool.Pool
	store      *Store
	audit      *audit.Store
	projects   ProjectAccess
	checklists *checklists.Store
}

func NewService(pool *pgxpool.Pool, store *Store, auditStore *audit.Store, projectAccess ProjectAccess, checklistsStore *checklists.Store) *Service {
	return &Service{pool: pool, store: store, audit: auditStore, projects: projectAccess, checklists: checklistsStore}
}

// canEdit mirrors project management rights: admins, and release
// coordinators/developers who are members of the owning project. Any
// project member may create and edit releases; stage transitions into
// production and beyond have additional checks layered in Transition.
func (s *Service) canEdit(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error) {
	if identity.User.Role == users.RoleAdmin {
		return true, nil
	}
	switch identity.User.Role {
	case users.RoleDeveloper, users.RoleReleaseCoordinator:
		return s.projects.CanView(ctx, identity, projectID)
	default:
		return false, nil
	}
}

type CreateInput struct {
	OwnerID           uuid.UUID
	Name              string
	TargetDate        *time.Time
	RiskLevel         RiskLevel
	RiskCategories    []string
	Summary           string
	RollbackPlan      string
	SupportPeriodDays int
}

func validateRiskCategories(categories []string) error {
	for _, c := range categories {
		if !RiskCategory(c).Valid() {
			return httpx.BadRequest("unknown risk category: " + c)
		}
	}
	return nil
}

func (s *Service) Create(ctx context.Context, identity authn.Identity, projectID uuid.UUID, in CreateInput) (Release, error) {
	ok, err := s.canEdit(ctx, identity, projectID)
	if err != nil {
		return Release{}, err
	}
	if !ok {
		return Release{}, httpx.Forbidden("you do not have permission to create releases in this project")
	}
	if in.Name == "" {
		return Release{}, httpx.BadRequest("name is required")
	}
	if !in.RiskLevel.Valid() {
		return Release{}, httpx.BadRequest("risk_level must be low, medium, high, or critical")
	}
	if err := validateRiskCategories(in.RiskCategories); err != nil {
		return Release{}, err
	}
	if in.OwnerID == uuid.Nil {
		in.OwnerID = identity.User.ID
	}
	if in.SupportPeriodDays <= 0 {
		in.SupportPeriodDays = 14
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Release{}, err
	}
	defer tx.Rollback(ctx)

	r, err := s.store.Create(ctx, tx, CreateParams{
		ProjectID: projectID, OwnerID: in.OwnerID, Name: in.Name, TargetDate: in.TargetDate,
		RiskLevel: in.RiskLevel, RiskCategories: in.RiskCategories, Summary: in.Summary, RollbackPlan: in.RollbackPlan,
		SupportPeriodDays: in.SupportPeriodDays, CreatedBy: identity.User.ID,
	})
	if err != nil {
		return Release{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", r.ID, "release_created", identity.AuditActor(), nil, r, ""); err != nil {
		return Release{}, err
	}
	// Assigns the project's currently-active checklist template to this
	// release, if it has one (a no-op for a project that has never
	// defined one). See docs/architecture-audit.md Finding 5.
	if _, err := s.checklists.MaterializeForRelease(ctx, tx, r.ID, projectID); err != nil {
		return Release{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Release{}, err
	}
	return r, nil
}

func (s *Service) Get(ctx context.Context, identity authn.Identity, id uuid.UUID) (Release, error) {
	r, err := s.store.GetByID(ctx, s.pool, id)
	if err != nil {
		return Release{}, httpx.NotFound("release not found")
	}
	ok, err := s.projects.CanView(ctx, identity, r.ProjectID)
	if err != nil {
		return Release{}, err
	}
	if !ok {
		return Release{}, httpx.Forbidden("you do not have access to this release")
	}
	return r, nil
}

// GetForService is used by other packages (evidence, approvals, gate,
// deployments, support, mcpnotes) that need the release row and its
// visibility check without importing the HTTP handlers.
func (s *Service) GetForService(ctx context.Context, identity authn.Identity, id uuid.UUID) (Release, error) {
	return s.Get(ctx, identity, id)
}

// CanEdit is exported so other packages (candidates) can reuse the same
// "may this identity create/change things on this project's releases"
// check instead of duplicating it, the same way projects.Service exports
// CanView/CanManage.
func (s *Service) CanEdit(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error) {
	return s.canEdit(ctx, identity, projectID)
}

// ProjectIDForRelease returns the project a release belongs to, after
// the same visibility check as Get/GetForService. It exists for
// packages this package itself depends on (internal/checklists, for
// materializing a release's checklist at creation time) so they can
// check release visibility without importing this package's Release
// type, which would create an import cycle.
func (s *Service) ProjectIDForRelease(ctx context.Context, identity authn.Identity, id uuid.UUID) (uuid.UUID, error) {
	r, err := s.Get(ctx, identity, id)
	if err != nil {
		return uuid.UUID{}, err
	}
	return r.ProjectID, nil
}

func (s *Service) ListByProject(ctx context.Context, identity authn.Identity, projectID uuid.UUID, stage *Stage) ([]Release, error) {
	ok, err := s.projects.CanView(ctx, identity, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httpx.Forbidden("you do not have access to this project")
	}
	return s.store.ListByProject(ctx, s.pool, projectID, stage)
}

func (s *Service) UpdateMeta(ctx context.Context, identity authn.Identity, id uuid.UUID, p UpdateParams) (Release, error) {
	before, err := s.store.GetByID(ctx, s.pool, id)
	if err != nil {
		return Release{}, httpx.NotFound("release not found")
	}
	ok, err := s.canEdit(ctx, identity, before.ProjectID)
	if err != nil {
		return Release{}, err
	}
	if !ok {
		return Release{}, httpx.Forbidden("you do not have permission to edit this release")
	}
	if !p.RiskLevel.Valid() {
		return Release{}, httpx.BadRequest("risk_level must be low, medium, high, or critical")
	}
	if err := validateRiskCategories(p.RiskCategories); err != nil {
		return Release{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Release{}, err
	}
	defer tx.Rollback(ctx)

	after, err := s.store.UpdateMeta(ctx, tx, id, p)
	if err != nil {
		return Release{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", id, "release_updated", identity.AuditActor(), before, after, ""); err != nil {
		return Release{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Release{}, err
	}
	return after, nil
}

// Transition moves a release to a new stage after checking the stage
// machine and the actor's permission for that specific move. Promotion
// into Production is never performed here manually; it only happens as a
// side effect of a successful production deployment (see
// internal/deployments) so the recorded deployment and the stage change
// are always consistent with each other.
func (s *Service) Transition(ctx context.Context, identity authn.Identity, id uuid.UUID, to Stage, reason string) (Release, error) {
	before, err := s.store.GetByID(ctx, s.pool, id)
	if err != nil {
		return Release{}, httpx.NotFound("release not found")
	}
	if to == StageProduction {
		return Release{}, httpx.BadRequest("production stage is only set automatically by a successful production deployment")
	}
	if !to.Valid() {
		return Release{}, httpx.BadRequest("unknown stage")
	}
	if !CanTransition(before.Stage, to) {
		return Release{}, httpx.Unprocessable("cannot move a release from " + string(before.Stage) + " to " + string(to))
	}

	ok, err := s.canEdit(ctx, identity, before.ProjectID)
	if err != nil {
		return Release{}, err
	}
	if !ok {
		return Release{}, httpx.Forbidden("you do not have permission to change this release's stage")
	}

	var supportEndsAt *time.Time
	if to == StagePostReleaseSupport {
		t := time.Now().AddDate(0, 0, before.SupportPeriodDays)
		supportEndsAt = &t
	}

	// Entering ready_for_production freezes the project's current policy
	// onto this release (re-freezing it on a later re-entry, e.g. after
	// being sent back to uat for fixes); internal/gate prefers this
	// snapshot over the live project policy from here on, so an edit to
	// the project's policy afterward can no longer silently change what
	// this release is held to. See docs/architecture-audit.md Finding 3.
	var snapshotInput *PolicySnapshotInput
	if to == StageReadyForProduction {
		reqTypes, reqCount, blockReject, reqCandidate, reqChecklist, err := s.projects.CurrentPolicy(ctx, before.ProjectID)
		if err != nil {
			return Release{}, err
		}
		extraApprovals, extraEvidence, requireRiskAccept, err := s.projects.RiskPolicyFor(ctx, before.ProjectID, string(before.RiskLevel))
		if err != nil {
			return Release{}, err
		}
		snapshotInput = &PolicySnapshotInput{
			RequiredEvidenceTypes: reqTypes, RequiredApprovalsCount: reqCount,
			BlockOnOpenRejection: blockReject, RequireCandidateForApproval: reqCandidate,
			RequireChecklistCompletion:  reqChecklist,
			ExtraRequiredApprovalsCount: extraApprovals, ExtraRequiredEvidenceTypes: extraEvidence,
			RequireRiskAcceptance: requireRiskAccept,
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Release{}, err
	}
	defer tx.Rollback(ctx)

	after, err := s.store.UpdateStage(ctx, tx, id, to, supportEndsAt)
	if err != nil {
		return Release{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", id, "stage_transition", identity.AuditActor(), before.Stage, after.Stage, reason); err != nil {
		return Release{}, err
	}
	if snapshotInput != nil {
		snap, err := s.store.UpsertPolicySnapshot(ctx, tx, id, *snapshotInput)
		if err != nil {
			return Release{}, err
		}
		if err := s.audit.Record(ctx, tx, "release", id, "policy_snapshot_taken", identity.AuditActor(), nil, snap, ""); err != nil {
			return Release{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Release{}, err
	}
	return after, nil
}

// Close moves a release to Closed from any non-terminal stage. It is the
// explicit "close after support period" action from the main user flow,
// kept as its own method so the audit action name is specific.
func (s *Service) Close(ctx context.Context, identity authn.Identity, id uuid.UUID, reason string) (Release, error) {
	before, err := s.store.GetByID(ctx, s.pool, id)
	if err != nil {
		return Release{}, httpx.NotFound("release not found")
	}
	if before.Stage == StageClosed {
		return Release{}, httpx.Conflict("release is already closed")
	}
	ok, err := s.canEdit(ctx, identity, before.ProjectID)
	if err != nil {
		return Release{}, err
	}
	if !ok {
		return Release{}, httpx.Forbidden("you do not have permission to close this release")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Release{}, err
	}
	defer tx.Rollback(ctx)

	after, err := s.store.UpdateStage(ctx, tx, id, StageClosed, before.SupportEndsAt)
	if err != nil {
		return Release{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", id, "release_closed", identity.AuditActor(), before.Stage, after.Stage, reason); err != nil {
		return Release{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Release{}, err
	}
	return after, nil
}

// PromoteToProduction is called by internal/deployments immediately after
// it records a successful production deployment, inside the same
// transaction, so the stage change and the deployment record are atomic.
func (s *Service) PromoteToProduction(ctx context.Context, tx dbx.Querier, id uuid.UUID, actor audit.Actor) error {
	_, err := s.store.UpdateStage(ctx, tx, id, StageProduction, nil)
	if err != nil {
		return err
	}
	return s.audit.Record(ctx, tx, "release", id, "stage_transition", actor, nil, StageProduction, "automatic promotion after successful production deployment")
}

func (s *Service) CreateLink(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, linkType LinkType, url string, label *string) (Link, error) {
	r, err := s.store.GetByID(ctx, s.pool, releaseID)
	if err != nil {
		return Link{}, httpx.NotFound("release not found")
	}
	ok, err := s.canEdit(ctx, identity, r.ProjectID)
	if err != nil {
		return Link{}, err
	}
	if !ok {
		return Link{}, httpx.Forbidden("you do not have permission to edit this release")
	}
	if !linkType.Valid() {
		return Link{}, httpx.BadRequest("link_type must be work_item, pull_request, commit, or ci_run")
	}
	if url == "" {
		return Link{}, httpx.BadRequest("url is required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Link{}, err
	}
	defer tx.Rollback(ctx)

	l, err := s.store.CreateLink(ctx, tx, releaseID, linkType, url, label, identity.User.ID)
	if err != nil {
		return Link{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", releaseID, "link_added", identity.AuditActor(), nil, l, ""); err != nil {
		return Link{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Link{}, err
	}
	return l, nil
}

// AcceptRisk records an explicit, named acceptance of a release's risk.
// Held to the same bar as internal/checklists' Waive (project
// management permission, not just edit permission) since it is a
// deliberate override, not a routine action, and internal/gate requires
// one before eligibility whenever the matching risk policy sets
// RequireRiskAcceptance.
func (s *Service) AcceptRisk(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, note string) (RiskAcceptance, error) {
	rel, err := s.store.GetByID(ctx, s.pool, releaseID)
	if err != nil {
		return RiskAcceptance{}, httpx.NotFound("release not found")
	}
	ok, err := s.projects.CanManage(ctx, identity, rel.ProjectID)
	if err != nil {
		return RiskAcceptance{}, err
	}
	if !ok {
		return RiskAcceptance{}, httpx.Forbidden("only admins and release coordinators on this project can accept a release's risk")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RiskAcceptance{}, err
	}
	defer tx.Rollback(ctx)

	a, err := s.store.UpsertRiskAcceptance(ctx, tx, releaseID, identity.User.ID, note)
	if err != nil {
		return RiskAcceptance{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", releaseID, "risk_accepted", identity.AuditActor(), nil, a, note); err != nil {
		return RiskAcceptance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RiskAcceptance{}, err
	}
	return a, nil
}

func (s *Service) GetRiskAcceptance(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) (*RiskAcceptance, error) {
	if _, err := s.Get(ctx, identity, releaseID); err != nil {
		return nil, err
	}
	return s.store.GetRiskAcceptance(ctx, s.pool, releaseID)
}

func (s *Service) ListLinks(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) ([]Link, error) {
	if _, err := s.Get(ctx, identity, releaseID); err != nil {
		return nil, err
	}
	return s.store.ListLinks(ctx, s.pool, releaseID)
}
