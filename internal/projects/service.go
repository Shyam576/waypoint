// Package projects implements project CRUD, per-project release policy,
// and project membership, which is this MVP's authorization boundary for
// "can this user see or act on this project's releases at all."
package projects

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/checklists"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/users"
)

type Service struct {
	pool       *pgxpool.Pool
	store      *Store
	audit      *audit.Store
	usersSt    *users.Store
	checklists *checklists.Store
}

func NewService(pool *pgxpool.Pool, store *Store, auditStore *audit.Store, usersStore *users.Store, checklistsStore *checklists.Store) *Service {
	return &Service{pool: pool, store: store, audit: auditStore, usersSt: usersStore, checklists: checklistsStore}
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = slugRe.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "project"
	}
	return s
}

// canManage is true for admins, and for project members whose global role
// is release_coordinator. Everyone else may view but not edit.
//
// An MCP token is never allowed to manage a project — change its
// policy, add a member, declare an emergency, accept risk, waive a
// checklist item, or initiate/verify a rollback — regardless of the
// token owner's own role. Every one of those actions routes through
// this one function, so this is the single place that needs to say
// so: today this is already true because no MCP route wires to any of
// them, but this makes it true independent of routing too, which is
// the explicit, must-stay-true rule from the brief (see
// docs/implementation-roadmap.md item 10).
func (s *Service) canManage(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error) {
	if identity.Via == authn.ViaMCP {
		return false, nil
	}
	if identity.User.Role == users.RoleAdmin {
		return true, nil
	}
	if identity.User.Role != users.RoleReleaseCoordinator {
		return false, nil
	}
	return s.store.IsMember(ctx, s.pool, projectID, identity.User.ID)
}

func (s *Service) canView(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error) {
	if identity.User.Role == users.RoleAdmin {
		return true, nil
	}
	return s.store.IsMember(ctx, s.pool, projectID, identity.User.ID)
}

type CreateInput struct {
	Name       string
	OwningTeam string
	RepoURL    *string
}

func (s *Service) Create(ctx context.Context, identity authn.Identity, in CreateInput) (Project, error) {
	if identity.User.Role != users.RoleAdmin && identity.User.Role != users.RoleReleaseCoordinator {
		return Project{}, httpx.Forbidden("only admins and release coordinators can create projects")
	}
	if strings.TrimSpace(in.Name) == "" {
		return Project{}, httpx.BadRequest("name is required")
	}
	if strings.TrimSpace(in.OwningTeam) == "" {
		return Project{}, httpx.BadRequest("owning_team is required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback(ctx)

	slug := slugify(in.Name)
	for attempt := 0; attempt < 5; attempt++ {
		candidate := slug
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", slug, attempt+1)
		}
		p, createErr := s.store.Create(ctx, tx, Project{
			Name: in.Name, Slug: candidate, OwningTeam: in.OwningTeam, RepoURL: in.RepoURL, CreatedBy: identity.User.ID,
		})
		if createErr == nil {
			if _, err := s.store.CreatePolicy(ctx, tx, p.ID); err != nil {
				return Project{}, err
			}
			if err := s.store.AddMember(ctx, tx, p.ID, identity.User.ID); err != nil {
				return Project{}, err
			}
			if err := s.checklists.SeedDefaultTemplate(ctx, tx, p.ID, &identity.User.ID); err != nil {
				return Project{}, err
			}
			// Sensible starter risk policy: 'high' needs one more
			// approval than the base policy; 'critical' additionally
			// requires a security review and explicit risk acceptance.
			// 'low'/'medium' get no row (no extra requirements).
			if _, err := s.store.UpsertRiskPolicy(ctx, tx, p.ID, "high", 1, nil, false, &identity.User.ID); err != nil {
				return Project{}, err
			}
			if _, err := s.store.UpsertRiskPolicy(ctx, tx, p.ID, "critical", 1, []string{"security_review"}, true, &identity.User.ID); err != nil {
				return Project{}, err
			}
			// Default emergency policy: the floor of 1 required approval,
			// and a retrospective is required before an emergency release
			// can be closed.
			if _, err := s.store.UpsertEmergencyPolicy(ctx, tx, p.ID, 1, true, &identity.User.ID); err != nil {
				return Project{}, err
			}
			if err := s.audit.Record(ctx, tx, "project", p.ID, "project_created", identity.AuditActor(), nil, p, ""); err != nil {
				return Project{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return Project{}, err
			}
			return p, nil
		}
		if !strings.Contains(createErr.Error(), "duplicate key") {
			return Project{}, createErr
		}
	}
	return Project{}, httpx.Conflict("could not generate a unique project slug; try a different name")
}

func (s *Service) List(ctx context.Context, identity authn.Identity) ([]Project, error) {
	if identity.User.Role == users.RoleAdmin {
		return s.store.ListAll(ctx, s.pool)
	}
	return s.store.ListForUser(ctx, s.pool, identity.User.ID)
}

func (s *Service) Get(ctx context.Context, identity authn.Identity, id uuid.UUID) (Project, error) {
	p, err := s.store.GetByID(ctx, s.pool, id)
	if err != nil {
		return Project{}, httpx.NotFound("project not found")
	}
	ok, err := s.canView(ctx, identity, id)
	if err != nil {
		return Project{}, err
	}
	if !ok {
		return Project{}, httpx.Forbidden("you do not have access to this project")
	}
	return p, nil
}

type UpdateInput struct {
	Name       string
	OwningTeam string
	RepoURL    *string
	Status     Status
}

func (s *Service) Update(ctx context.Context, identity authn.Identity, id uuid.UUID, in UpdateInput) (Project, error) {
	before, err := s.store.GetByID(ctx, s.pool, id)
	if err != nil {
		return Project{}, httpx.NotFound("project not found")
	}
	ok, err := s.canManage(ctx, identity, id)
	if err != nil {
		return Project{}, err
	}
	if !ok {
		return Project{}, httpx.Forbidden("only admins and release coordinators on this project can edit it")
	}
	if in.Status != StatusActive && in.Status != StatusArchived {
		return Project{}, httpx.BadRequest("status must be 'active' or 'archived'")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback(ctx)

	after, err := s.store.Update(ctx, tx, id, in.Name, in.OwningTeam, in.RepoURL, in.Status)
	if err != nil {
		return Project{}, err
	}
	if err := s.audit.Record(ctx, tx, "project", id, "project_updated", identity.AuditActor(), before, after, ""); err != nil {
		return Project{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Project{}, err
	}
	return after, nil
}

func (s *Service) GetPolicy(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (Policy, error) {
	ok, err := s.canView(ctx, identity, projectID)
	if err != nil {
		return Policy{}, err
	}
	if !ok {
		return Policy{}, httpx.Forbidden("you do not have access to this project")
	}
	p, err := s.store.GetPolicy(ctx, s.pool, projectID)
	if err != nil {
		return Policy{}, httpx.NotFound("policy not found")
	}
	return p, nil
}

type UpdatePolicyInput struct {
	RequiredEvidenceTypes       []string
	RequiredApprovalsCount      int
	BlockOnOpenRejection        bool
	RequireCandidateForApproval bool
	RequireChecklistCompletion  bool
	RequireApproverNotSubmitter bool
}

func (s *Service) UpdatePolicy(ctx context.Context, identity authn.Identity, projectID uuid.UUID, in UpdatePolicyInput) (Policy, error) {
	ok, err := s.canManage(ctx, identity, projectID)
	if err != nil {
		return Policy{}, err
	}
	if !ok {
		return Policy{}, httpx.Forbidden("only admins and release coordinators on this project can edit its policy")
	}
	if in.RequiredApprovalsCount < 0 {
		return Policy{}, httpx.BadRequest("required_approvals_count cannot be negative")
	}
	for _, t := range in.RequiredEvidenceTypes {
		if !ValidEvidenceTypes[t] {
			return Policy{}, httpx.BadRequest("unknown evidence type: " + t)
		}
	}

	before, err := s.store.GetPolicy(ctx, s.pool, projectID)
	if err != nil {
		return Policy{}, httpx.NotFound("policy not found")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Policy{}, err
	}
	defer tx.Rollback(ctx)

	after, err := s.store.UpdatePolicy(ctx, tx, projectID, UpdatePolicyParams{
		RequiredEvidenceTypes: in.RequiredEvidenceTypes, RequiredApprovalsCount: in.RequiredApprovalsCount,
		BlockOnOpenRejection: in.BlockOnOpenRejection, RequireCandidateForApproval: in.RequireCandidateForApproval,
		RequireChecklistCompletion: in.RequireChecklistCompletion, RequireApproverNotSubmitter: in.RequireApproverNotSubmitter,
		UpdatedBy: identity.User.ID,
	})
	if err != nil {
		return Policy{}, err
	}
	if err := s.audit.Record(ctx, tx, "project", projectID, "policy_updated", identity.AuditActor(), before, after, ""); err != nil {
		return Policy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Policy{}, err
	}
	return after, nil
}

func (s *Service) AddMember(ctx context.Context, identity authn.Identity, projectID, userID uuid.UUID) error {
	ok, err := s.canManage(ctx, identity, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.Forbidden("only admins and release coordinators on this project can add members")
	}
	if _, err := s.usersSt.GetByID(ctx, s.pool, userID); err != nil {
		return httpx.BadRequest("user not found")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := s.store.AddMember(ctx, tx, projectID, userID); err != nil {
		return err
	}
	if err := s.audit.Record(ctx, tx, "project", projectID, "member_added", identity.AuditActor(), nil, map[string]any{"user_id": userID}, ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) ListMembers(ctx context.Context, identity authn.Identity, projectID uuid.UUID) ([]users.User, error) {
	ok, err := s.canView(ctx, identity, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httpx.Forbidden("you do not have access to this project")
	}
	ids, err := s.store.ListMemberIDs(ctx, s.pool, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]users.User, 0, len(ids))
	for _, id := range ids {
		u, err := s.usersSt.GetByID(ctx, s.pool, id)
		if err != nil {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// CanView is exported so other packages (releases, evidence, ...) can
// reuse the same project-visibility check instead of duplicating it.
func (s *Service) CanView(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error) {
	return s.canView(ctx, identity, projectID)
}

// CanManage is exported for the same reason as CanView.
func (s *Service) CanManage(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error) {
	return s.canManage(ctx, identity, projectID)
}

// CurrentPolicy returns the project's live policy as plain values, with
// no authorization check of its own: it exists so internal/releases can
// snapshot a policy onto a release without this package depending on
// releases, or releases depending on this package's Policy type. Callers
// that already hold a Release (and so have already proven project
// visibility through it) are the only intended callers.
func (s *Service) CurrentPolicy(ctx context.Context, projectID uuid.UUID) ([]string, int, bool, bool, bool, error) {
	p, err := s.store.GetPolicy(ctx, s.pool, projectID)
	if err != nil {
		return nil, 0, false, false, false, err
	}
	return p.RequiredEvidenceTypes, p.RequiredApprovalsCount, p.BlockOnOpenRejection, p.RequireCandidateForApproval, p.RequireChecklistCompletion, nil
}

// RequireApproverNotSubmitter reports whether this project's live policy
// currently requires the separation-of-duties rule internal/approvals
// enforces at decision time (not frozen into the policy snapshot: this
// is an authorization check on the approver, evaluated live, the same
// way the "not the release owner" rule already is — not a gate
// eligibility requirement that needs to stay stable over the release's
// lifetime). See docs/architecture-audit.md Finding 9.
func (s *Service) RequireApproverNotSubmitter(ctx context.Context, projectID uuid.UUID) (bool, error) {
	p, err := s.store.GetPolicy(ctx, s.pool, projectID)
	if err != nil {
		return false, err
	}
	return p.RequireApproverNotSubmitter, nil
}

// RiskPolicyFor returns the extra requirements this project defines for
// one risk tier, or all-zero values if it has none — no authorization
// check of its own, for the same reason and the same callers as
// CurrentPolicy (internal/releases, internal/gate).
func (s *Service) RiskPolicyFor(ctx context.Context, projectID uuid.UUID, riskLevel string) (int, []string, bool, error) {
	p, err := s.store.GetRiskPolicy(ctx, s.pool, projectID, riskLevel)
	if err != nil {
		return 0, nil, false, err
	}
	if p == nil {
		return 0, nil, false, nil
	}
	return p.ExtraRequiredApprovalsCount, p.ExtraRequiredEvidenceTypes, p.RequireRiskAcceptance, nil
}

func (s *Service) GetRiskPolicy(ctx context.Context, identity authn.Identity, projectID uuid.UUID, riskLevel string) (*RiskPolicy, error) {
	ok, err := s.canView(ctx, identity, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httpx.Forbidden("you do not have access to this project")
	}
	return s.store.GetRiskPolicy(ctx, s.pool, projectID, riskLevel)
}

func (s *Service) ListRiskPolicies(ctx context.Context, identity authn.Identity, projectID uuid.UUID) ([]RiskPolicy, error) {
	ok, err := s.canView(ctx, identity, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httpx.Forbidden("you do not have access to this project")
	}
	return s.store.ListRiskPolicies(ctx, s.pool, projectID)
}

type UpdateRiskPolicyInput struct {
	RiskLevel                   string
	ExtraRequiredApprovalsCount int
	ExtraRequiredEvidenceTypes  []string
	RequireRiskAcceptance       bool
}

func (s *Service) UpdateRiskPolicy(ctx context.Context, identity authn.Identity, projectID uuid.UUID, in UpdateRiskPolicyInput) (RiskPolicy, error) {
	ok, err := s.canManage(ctx, identity, projectID)
	if err != nil {
		return RiskPolicy{}, err
	}
	if !ok {
		return RiskPolicy{}, httpx.Forbidden("only admins and release coordinators on this project can edit its risk policy")
	}
	if !ValidRiskLevels[in.RiskLevel] {
		return RiskPolicy{}, httpx.BadRequest("risk_level must be low, medium, high, or critical")
	}
	if in.ExtraRequiredApprovalsCount < 0 {
		return RiskPolicy{}, httpx.BadRequest("extra_required_approvals_count cannot be negative")
	}
	for _, t := range in.ExtraRequiredEvidenceTypes {
		if !ValidEvidenceTypes[t] {
			return RiskPolicy{}, httpx.BadRequest("unknown evidence type: " + t)
		}
	}

	before, err := s.store.GetRiskPolicy(ctx, s.pool, projectID, in.RiskLevel)
	if err != nil {
		return RiskPolicy{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RiskPolicy{}, err
	}
	defer tx.Rollback(ctx)

	after, err := s.store.UpsertRiskPolicy(ctx, tx, projectID, in.RiskLevel, in.ExtraRequiredApprovalsCount, in.ExtraRequiredEvidenceTypes, in.RequireRiskAcceptance, &identity.User.ID)
	if err != nil {
		return RiskPolicy{}, err
	}
	if err := s.audit.Record(ctx, tx, "project", projectID, "risk_policy_updated", identity.AuditActor(), before, after, ""); err != nil {
		return RiskPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RiskPolicy{}, err
	}
	return after, nil
}

// EmergencyPolicyFor returns this project's emergency-release policy as
// plain values (required-approvals floor and whether a retrospective is
// required), or the floor-of-1-plus-retrospective default if the
// project was created before this feature existed and has no row yet.
// No authorization check of its own, for the same reason and callers as
// CurrentPolicy/RiskPolicyFor (internal/releases, internal/gate).
func (s *Service) EmergencyPolicyFor(ctx context.Context, projectID uuid.UUID) (int, bool, error) {
	p, err := s.store.GetEmergencyPolicy(ctx, s.pool, projectID)
	if err != nil {
		return 0, false, err
	}
	if p == nil {
		return 1, true, nil
	}
	return p.RequiredApprovalsCount, p.RequireRetrospective, nil
}

func (s *Service) GetEmergencyPolicy(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (EmergencyPolicy, error) {
	ok, err := s.canView(ctx, identity, projectID)
	if err != nil {
		return EmergencyPolicy{}, err
	}
	if !ok {
		return EmergencyPolicy{}, httpx.Forbidden("you do not have access to this project")
	}
	p, err := s.store.GetEmergencyPolicy(ctx, s.pool, projectID)
	if err != nil {
		return EmergencyPolicy{}, err
	}
	if p == nil {
		return EmergencyPolicy{ProjectID: projectID, RequiredApprovalsCount: 1, RequireRetrospective: true}, nil
	}
	return *p, nil
}

type UpdateEmergencyPolicyInput struct {
	RequiredApprovalsCount int
	RequireRetrospective   bool
}

func (s *Service) UpdateEmergencyPolicy(ctx context.Context, identity authn.Identity, projectID uuid.UUID, in UpdateEmergencyPolicyInput) (EmergencyPolicy, error) {
	ok, err := s.canManage(ctx, identity, projectID)
	if err != nil {
		return EmergencyPolicy{}, err
	}
	if !ok {
		return EmergencyPolicy{}, httpx.Forbidden("only admins and release coordinators on this project can edit its emergency policy")
	}
	if in.RequiredApprovalsCount < 1 {
		return EmergencyPolicy{}, httpx.BadRequest("required_approvals_count must be at least 1: an emergency changes the bar, it never removes it")
	}

	before, err := s.store.GetEmergencyPolicy(ctx, s.pool, projectID)
	if err != nil {
		return EmergencyPolicy{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EmergencyPolicy{}, err
	}
	defer tx.Rollback(ctx)

	after, err := s.store.UpsertEmergencyPolicy(ctx, tx, projectID, in.RequiredApprovalsCount, in.RequireRetrospective, &identity.User.ID)
	if err != nil {
		return EmergencyPolicy{}, err
	}
	if err := s.audit.Record(ctx, tx, "project", projectID, "emergency_policy_updated", identity.AuditActor(), before, after, ""); err != nil {
		return EmergencyPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EmergencyPolicy{}, err
	}
	return after, nil
}
