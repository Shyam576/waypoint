package support

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/dbx"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/releases"
	"github.com/Shyam576/waypoint/internal/users"
)

type ReleaseAccess interface {
	GetForService(ctx context.Context, identity authn.Identity, id uuid.UUID) (releases.Release, error)
}

type Service struct {
	pool     *pgxpool.Pool
	store    *Store
	audit    *audit.Store
	releases ReleaseAccess
}

func NewService(pool *pgxpool.Pool, store *Store, auditStore *audit.Store, releaseAccess ReleaseAccess) *Service {
	return &Service{pool: pool, store: store, audit: auditStore, releases: releaseAccess}
}

type CreateInput struct {
	Summary         string
	Severity        Severity
	OwnerID         *uuid.UUID
	LinkedTicketURL *string
	LinkedPRURL     *string
}

func (s *Service) Create(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, in CreateInput) (Item, error) {
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return Item{}, err
	}
	if in.Summary == "" {
		return Item{}, httpx.BadRequest("summary is required")
	}
	if !in.Severity.Valid() {
		return Item{}, httpx.BadRequest("severity must be low, medium, high, or critical")
	}

	return s.createWithSource(ctx, identity, releaseID, CreateParams{
		ReleaseID: releaseID, Summary: in.Summary, Severity: in.Severity, OwnerID: in.OwnerID,
		LinkedTicketURL: in.LinkedTicketURL, LinkedPRURL: in.LinkedPRURL, Source: SourceWeb, CreatedBy: identity.User.ID,
	})
}

// CreateFromMCP is used only by internal/mcpnotes when a pending MCP
// support-note submission is accepted; it runs inside the caller's
// transaction so the mcp_submissions row and the resulting support item
// commit together.
func (s *Service) CreateFromMCP(ctx context.Context, tx dbx.Querier, identity authn.Identity, p CreateParams) (Item, error) {
	p.Source = SourceMCP
	it, err := s.store.Create(ctx, tx, p)
	if err != nil {
		return Item{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", p.ReleaseID, "support_item_created", identity.AuditActor(), nil, it, "via mcp submission"); err != nil {
		return Item{}, err
	}
	return it, nil
}

func (s *Service) createWithSource(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, p CreateParams) (Item, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback(ctx)

	it, err := s.store.Create(ctx, tx, p)
	if err != nil {
		return Item{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", releaseID, "support_item_created", identity.AuditActor(), nil, it, ""); err != nil {
		return Item{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Item{}, err
	}
	return it, nil
}

func (s *Service) ListByRelease(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) ([]Item, error) {
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return nil, err
	}
	return s.store.ListByRelease(ctx, s.pool, releaseID)
}

type UpdateInput struct {
	Severity        Severity
	Status          ItemStatus
	OwnerID         *uuid.UUID
	LinkedTicketURL *string
	LinkedPRURL     *string
	ResolutionNote  string
}

// Update is restricted to admins, support, and release coordinators, or
// the item's own creator/owner, since support items can carry sensitive
// incident detail.
func (s *Service) Update(ctx context.Context, identity authn.Identity, id uuid.UUID, in UpdateInput) (Item, error) {
	before, err := s.store.GetByID(ctx, s.pool, id)
	if err != nil {
		return Item{}, httpx.NotFound("support item not found")
	}
	if _, err := s.releases.GetForService(ctx, identity, before.ReleaseID); err != nil {
		return Item{}, err
	}

	privileged := identity.User.Role == users.RoleAdmin || identity.User.Role == users.RoleSupport || identity.User.Role == users.RoleReleaseCoordinator
	isCreatorOrOwner := before.CreatedBy == identity.User.ID || (before.OwnerID != nil && *before.OwnerID == identity.User.ID)
	if !privileged && !isCreatorOrOwner {
		return Item{}, httpx.Forbidden("you do not have permission to update this support item")
	}
	if !in.Severity.Valid() {
		return Item{}, httpx.BadRequest("severity must be low, medium, high, or critical")
	}
	if !in.Status.Valid() {
		return Item{}, httpx.BadRequest("status must be open, investigating, resolved, or closed")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback(ctx)

	after, err := s.store.Update(ctx, tx, id, UpdateParams{
		Severity: in.Severity, Status: in.Status, OwnerID: in.OwnerID,
		LinkedTicketURL: in.LinkedTicketURL, LinkedPRURL: in.LinkedPRURL, ResolutionNote: in.ResolutionNote,
	})
	if err != nil {
		return Item{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", before.ReleaseID, "support_item_updated", identity.AuditActor(), before, after, ""); err != nil {
		return Item{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Item{}, err
	}
	return after, nil
}
