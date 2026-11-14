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
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/users"
)

type Service struct {
	pool    *pgxpool.Pool
	store   *Store
	audit   *audit.Store
	usersSt *users.Store
}

func NewService(pool *pgxpool.Pool, store *Store, auditStore *audit.Store, usersStore *users.Store) *Service {
	return &Service{pool: pool, store: store, audit: auditStore, usersSt: usersStore}
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
func (s *Service) canManage(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error) {
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

	after, err := s.store.UpdatePolicy(ctx, tx, projectID, in.RequiredEvidenceTypes, in.RequiredApprovalsCount, in.BlockOnOpenRejection, in.RequireCandidateForApproval, identity.User.ID)
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
