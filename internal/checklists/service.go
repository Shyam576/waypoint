package checklists

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/httpx"
)

// ReleaseAccess deliberately returns only a release's project ID (not
// internal/releases.Release) so this package doesn't need to import
// internal/releases: that package depends on this one directly (to
// materialize a new release's checklist at creation time), and the
// reverse import would be a cycle.
type ReleaseAccess interface {
	ProjectIDForRelease(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) (uuid.UUID, error)
	CanEdit(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error)
}

type ProjectAccess interface {
	CanView(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error)
	CanManage(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error)
}

type Service struct {
	pool     *pgxpool.Pool
	store    *Store
	audit    *audit.Store
	releases ReleaseAccess
	projects ProjectAccess
}

func NewService(pool *pgxpool.Pool, store *Store, auditStore *audit.Store, releaseAccess ReleaseAccess, projectAccess ProjectAccess) *Service {
	return &Service{pool: pool, store: store, audit: auditStore, releases: releaseAccess, projects: projectAccess}
}

// --- Templates (project-level configuration) ---

func validateTemplateItems(items []TemplateItemInput) error {
	if len(items) == 0 {
		return httpx.BadRequest("a checklist template must have at least one item")
	}
	for _, it := range items {
		if !it.Category.Valid() {
			return httpx.BadRequest("category must be development, uat, production_readiness, or post_deployment")
		}
		if it.Title == "" {
			return httpx.BadRequest("every checklist item needs a title")
		}
		if it.VerificationMethod == "" {
			it.VerificationMethod = MethodHumanAttestation
		}
		if !it.VerificationMethod.Valid() {
			return httpx.BadRequest("verification_method must be human_attestation or automated_check")
		}
	}
	return nil
}

// CreateTemplateVersion creates a new template version for a project
// (version = previous max + 1) and deactivates any previously active
// version, so only one is ever active at a time. Existing releases keep
// pointing at whatever version they were assigned; only releases created
// after this call pick up the new one.
func (s *Service) CreateTemplateVersion(ctx context.Context, identity authn.Identity, projectID uuid.UUID, items []TemplateItemInput) (TemplateWithItems, error) {
	ok, err := s.projects.CanManage(ctx, identity, projectID)
	if err != nil {
		return TemplateWithItems{}, err
	}
	if !ok {
		return TemplateWithItems{}, httpx.Forbidden("only admins and release coordinators on this project can manage its checklist template")
	}
	if err := validateTemplateItems(items); err != nil {
		return TemplateWithItems{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TemplateWithItems{}, err
	}
	defer tx.Rollback(ctx)

	if err := s.store.DeactivateAllTemplates(ctx, tx, projectID); err != nil {
		return TemplateWithItems{}, err
	}
	maxVersion, err := s.store.MaxTemplateVersion(ctx, tx, projectID)
	if err != nil {
		return TemplateWithItems{}, err
	}
	t, err := s.store.CreateTemplate(ctx, tx, projectID, maxVersion+1, &identity.User.ID)
	if err != nil {
		return TemplateWithItems{}, err
	}
	out := TemplateWithItems{Template: t, Items: []TemplateItem{}}
	for _, in := range items {
		if in.VerificationMethod == "" {
			in.VerificationMethod = MethodHumanAttestation
		}
		it, err := s.store.CreateTemplateItem(ctx, tx, t.ID, in)
		if err != nil {
			return TemplateWithItems{}, err
		}
		out.Items = append(out.Items, it)
	}
	if err := s.audit.Record(ctx, tx, "project", projectID, "checklist_template_created", identity.AuditActor(), nil, out, ""); err != nil {
		return TemplateWithItems{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TemplateWithItems{}, err
	}
	return out, nil
}

func (s *Service) GetActiveTemplate(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (*TemplateWithItems, error) {
	ok, err := s.projects.CanView(ctx, identity, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httpx.Forbidden("you do not have access to this project")
	}
	t, err := s.store.GetActiveTemplate(ctx, s.pool, projectID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, nil
	}
	items, err := s.store.ListTemplateItems(ctx, s.pool, t.ID)
	if err != nil {
		return nil, err
	}
	return &TemplateWithItems{Template: *t, Items: items}, nil
}

func (s *Service) ListTemplateVersions(ctx context.Context, identity authn.Identity, projectID uuid.UUID) ([]Template, error) {
	ok, err := s.projects.CanView(ctx, identity, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httpx.Forbidden("you do not have access to this project")
	}
	return s.store.ListTemplateVersions(ctx, s.pool, projectID)
}

// --- Release checklist items ---

func (s *Service) ListByRelease(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) ([]ReleaseItem, error) {
	if _, err := s.releases.ProjectIDForRelease(ctx, identity, releaseID); err != nil {
		return nil, err
	}
	return s.store.ListByRelease(ctx, s.pool, releaseID)
}

// resolveItemRelease returns the item and its release's project ID,
// after checking the caller may see that release.
func (s *Service) resolveItemRelease(ctx context.Context, identity authn.Identity, itemID uuid.UUID) (ReleaseItem, uuid.UUID, error) {
	item, err := s.store.GetItemByID(ctx, s.pool, itemID)
	if err != nil {
		return ReleaseItem{}, uuid.UUID{}, httpx.NotFound("checklist item not found")
	}
	projectID, err := s.releases.ProjectIDForRelease(ctx, identity, item.ReleaseID)
	if err != nil {
		return ReleaseItem{}, uuid.UUID{}, err
	}
	return item, projectID, nil
}

func (s *Service) AssignOwner(ctx context.Context, identity authn.Identity, itemID, ownerID uuid.UUID) (ReleaseItem, error) {
	item, projectID, err := s.resolveItemRelease(ctx, identity, itemID)
	if err != nil {
		return ReleaseItem{}, err
	}
	ok, err := s.releases.CanEdit(ctx, identity, projectID)
	if err != nil {
		return ReleaseItem{}, err
	}
	if !ok {
		return ReleaseItem{}, httpx.Forbidden("you do not have permission to assign checklist item owners on this release")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReleaseItem{}, err
	}
	defer tx.Rollback(ctx)

	after, err := s.store.AssignOwner(ctx, tx, itemID, ownerID)
	if err != nil {
		return ReleaseItem{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", item.ReleaseID, "checklist_item_owner_assigned", identity.AuditActor(), item, after, ""); err != nil {
		return ReleaseItem{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReleaseItem{}, err
	}
	return after, nil
}

func (s *Service) Verify(ctx context.Context, identity authn.Identity, itemID uuid.UUID, evidenceRef string) (ReleaseItem, error) {
	return s.decide(ctx, identity, itemID, DecideParams{Status: ItemVerified, EvidenceRef: evidenceRef, DecidedBy: identity.User.ID}, "checklist_item_verified", false)
}

func (s *Service) Reject(ctx context.Context, identity authn.Identity, itemID uuid.UUID, reason string) (ReleaseItem, error) {
	if reason == "" {
		return ReleaseItem{}, httpx.BadRequest("a rejection reason is required")
	}
	return s.decide(ctx, identity, itemID, DecideParams{Status: ItemRejected, RejectionReason: reason, DecidedBy: identity.User.ID}, "checklist_item_rejected", false)
}

// Waive records a controlled exception: the item is treated as complete
// without being verified, because it doesn't apply or was explicitly
// accepted as a risk. Held to a higher bar than Verify/Reject (project
// management permission, not just edit permission) since it is a
// deliberate override of a requirement rather than confirming it was
// met.
func (s *Service) Waive(ctx context.Context, identity authn.Identity, itemID uuid.UUID, reason string) (ReleaseItem, error) {
	if reason == "" {
		return ReleaseItem{}, httpx.BadRequest("a waiver reason is required for a controlled exception")
	}
	return s.decide(ctx, identity, itemID, DecideParams{Status: ItemWaived, WaiverReason: reason, DecidedBy: identity.User.ID}, "checklist_item_waived", true)
}

func (s *Service) decide(ctx context.Context, identity authn.Identity, itemID uuid.UUID, params DecideParams, auditAction string, requireManage bool) (ReleaseItem, error) {
	item, projectID, err := s.resolveItemRelease(ctx, identity, itemID)
	if err != nil {
		return ReleaseItem{}, err
	}
	var ok bool
	if requireManage {
		ok, err = s.projects.CanManage(ctx, identity, projectID)
	} else {
		ok, err = s.releases.CanEdit(ctx, identity, projectID)
	}
	if err != nil {
		return ReleaseItem{}, err
	}
	if !ok {
		if requireManage {
			return ReleaseItem{}, httpx.Forbidden("only admins and release coordinators on this project can waive a checklist item")
		}
		return ReleaseItem{}, httpx.Forbidden("you do not have permission to update this checklist item")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReleaseItem{}, err
	}
	defer tx.Rollback(ctx)

	after, err := s.store.Decide(ctx, tx, itemID, params)
	if err != nil {
		return ReleaseItem{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", item.ReleaseID, auditAction, identity.AuditActor(), item, after, params.RejectionReason+params.WaiverReason); err != nil {
		return ReleaseItem{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReleaseItem{}, err
	}
	return after, nil
}
