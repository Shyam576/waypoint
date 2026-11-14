// Package evidence stores the artifacts a release must collect before it
// is eligible for production (test results, UAT sign-off, rollback plan,
// security review, change summary). Which types are required is decided
// per project by internal/projects' Policy, not here; this package only
// stores submissions and lists them.
package evidence

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/dbx"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/releases"
)

type Type string

const (
	TypeChangeSummary  Type = "change_summary"
	TypeTestResults    Type = "test_results"
	TypeUATSignoff     Type = "uat_signoff"
	TypeRollbackPlan   Type = "rollback_plan"
	TypeSecurityReview Type = "security_review"
	// TypeConfigComparison, TypeSmokeTestResults, and
	// TypePaymentConfigCheck support the brief's high-risk/payment change
	// asks (docs/architecture-audit.md Finding 4): a configuration diff,
	// business-critical smoke tests, and a narrowly-scoped,
	// machine-oriented check that test values/gateway config weren't
	// accidentally promoted to production. Submitting this evidence is
	// no different from any other type here — nothing in this package
	// runs an actual automated check; a CI step is expected to perform
	// the check itself and submit the result as this evidence type. This
	// is deliberately not a general "detect wrong business values"
	// feature.
	TypeConfigComparison   Type = "config_comparison"
	TypeSmokeTestResults   Type = "smoke_test_results"
	TypePaymentConfigCheck Type = "payment_config_check"
	TypeOther              Type = "other"
)

func (t Type) Valid() bool {
	switch t {
	case TypeChangeSummary, TypeTestResults, TypeUATSignoff, TypeRollbackPlan, TypeSecurityReview,
		TypeConfigComparison, TypeSmokeTestResults, TypePaymentConfigCheck, TypeOther:
		return true
	}
	return false
}

type Evidence struct {
	ID            uuid.UUID `json:"id"`
	ReleaseID     uuid.UUID `json:"release_id"`
	Type          Type      `json:"type"`
	Title         string    `json:"title"`
	Notes         string    `json:"notes"`
	AttachmentURL *string   `json:"attachment_url,omitempty"`
	SubmittedBy   uuid.UUID `json:"submitted_by"`
	SubmittedAt   time.Time `json:"submitted_at"`
}

type Store struct{}

func NewStore() *Store { return &Store{} }

func (s *Store) Create(ctx context.Context, q dbx.Querier, releaseID uuid.UUID, typ Type, title, notes string, attachmentURL *string, submittedBy uuid.UUID) (Evidence, error) {
	var e Evidence
	err := q.QueryRow(ctx, `
		INSERT INTO evidence (release_id, type, title, notes, attachment_url, submitted_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, release_id, type, title, notes, attachment_url, submitted_by, submitted_at`,
		releaseID, typ, title, notes, attachmentURL, submittedBy,
	).Scan(&e.ID, &e.ReleaseID, &e.Type, &e.Title, &e.Notes, &e.AttachmentURL, &e.SubmittedBy, &e.SubmittedAt)
	return e, err
}

func (s *Store) ListByRelease(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) ([]Evidence, error) {
	rows, err := q.Query(ctx, `
		SELECT id, release_id, type, title, notes, attachment_url, submitted_by, submitted_at
		FROM evidence WHERE release_id = $1 ORDER BY submitted_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Evidence{}
	for rows.Next() {
		var e Evidence
		if err := rows.Scan(&e.ID, &e.ReleaseID, &e.Type, &e.Title, &e.Notes, &e.AttachmentURL, &e.SubmittedBy, &e.SubmittedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PresentTypes returns the distinct evidence types submitted for a
// release, used by the gate to check which required types are missing.
func (s *Store) PresentTypes(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) (map[string]bool, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT type FROM evidence WHERE release_id = $1`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out[t] = true
	}
	return out, rows.Err()
}

// SubmittedByUser reports whether this user has submitted any evidence
// for this release, for internal/approvals' optional
// separation-of-duties check (docs/architecture-audit.md Finding 9).
func (s *Store) SubmittedByUser(ctx context.Context, q dbx.Querier, releaseID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM evidence WHERE release_id = $1 AND submitted_by = $2)`, releaseID, userID).Scan(&exists)
	return exists, err
}

// ReleaseAccess is the narrow slice of the releases package's Service
// that evidence needs: fetching a release while enforcing project
// visibility.
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

func (s *Service) Submit(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, typ Type, title, notes string, attachmentURL *string) (Evidence, error) {
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return Evidence{}, err
	}
	if !typ.Valid() {
		return Evidence{}, httpx.BadRequest("unknown evidence type")
	}
	if title == "" {
		return Evidence{}, httpx.BadRequest("title is required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Evidence{}, err
	}
	defer tx.Rollback(ctx)

	e, err := s.store.Create(ctx, tx, releaseID, typ, title, notes, attachmentURL, identity.User.ID)
	if err != nil {
		return Evidence{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", releaseID, "evidence_submitted", identity.AuditActor(), nil, e, string(typ)); err != nil {
		return Evidence{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Evidence{}, err
	}
	return e, nil
}

func (s *Service) ListByRelease(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) ([]Evidence, error) {
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return nil, err
	}
	return s.store.ListByRelease(ctx, s.pool, releaseID)
}
