// Package approvals records approve/reject decisions from named
// approvers. A user may never approve their own release, and every
// decision is kept (not overwritten) so the history is auditable; the
// production gate (internal/gate) only looks at each approver's most
// recent decision.
package approvals

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/candidates"
	"github.com/Shyam576/waypoint/internal/dbx"
	"github.com/Shyam576/waypoint/internal/evidence"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/releases"
	"github.com/Shyam576/waypoint/internal/users"
)

type Decision string

const (
	DecisionApproved Decision = "approved"
	DecisionRejected Decision = "rejected"
)

func (d Decision) Valid() bool {
	return d == DecisionApproved || d == DecisionRejected
}

type Approval struct {
	ID          uuid.UUID  `json:"id"`
	ReleaseID   uuid.UUID  `json:"release_id"`
	ApproverID  uuid.UUID  `json:"approver_id"`
	Decision    Decision   `json:"decision"`
	Comment     string     `json:"comment"`
	CandidateID *uuid.UUID `json:"candidate_id,omitempty"`
	DecidedAt   time.Time  `json:"decided_at"`
}

type Store struct{}

func NewStore() *Store { return &Store{} }

// Create records one approve/reject decision. candidateID is the
// release's current candidate (see internal/candidates) at the moment of
// decision, or nil if the release has no registered candidate — it is
// bookkeeping only here; internal/gate decides whether a mismatch
// matters, per project policy.
func (s *Store) Create(ctx context.Context, q dbx.Querier, releaseID, approverID uuid.UUID, decision Decision, comment string, candidateID *uuid.UUID) (Approval, error) {
	var a Approval
	err := q.QueryRow(ctx, `
		INSERT INTO approvals (release_id, approver_id, decision, comment, candidate_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, release_id, approver_id, decision, comment, candidate_id, decided_at`,
		releaseID, approverID, decision, comment, candidateID,
	).Scan(&a.ID, &a.ReleaseID, &a.ApproverID, &a.Decision, &a.Comment, &a.CandidateID, &a.DecidedAt)
	return a, err
}

func (s *Store) ListByRelease(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) ([]Approval, error) {
	rows, err := q.Query(ctx, `
		SELECT id, release_id, approver_id, decision, comment, candidate_id, decided_at
		FROM approvals WHERE release_id = $1 ORDER BY decided_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		var a Approval
		if err := rows.Scan(&a.ID, &a.ReleaseID, &a.ApproverID, &a.Decision, &a.Comment, &a.CandidateID, &a.DecidedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// LatestPerApprover returns, for each approver who has ever decided on
// this release, only their most recent decision. This is the view the
// production gate uses to count current approvals.
func (s *Store) LatestPerApprover(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) ([]Approval, error) {
	rows, err := q.Query(ctx, `
		SELECT DISTINCT ON (approver_id) id, release_id, approver_id, decision, comment, candidate_id, decided_at
		FROM approvals WHERE release_id = $1
		ORDER BY approver_id, decided_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		var a Approval
		if err := rows.Scan(&a.ID, &a.ReleaseID, &a.ApproverID, &a.Decision, &a.Comment, &a.CandidateID, &a.DecidedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type ReleaseAccess interface {
	GetForService(ctx context.Context, identity authn.Identity, id uuid.UUID) (releases.Release, error)
}

type ProjectAccess interface {
	CanView(ctx context.Context, identity authn.Identity, projectID uuid.UUID) (bool, error)
	// RequireApproverNotSubmitter reports whether this project's live
	// policy requires the extra separation-of-duties rule below, beyond
	// the always-on "not the release owner" check. See
	// docs/architecture-audit.md Finding 9.
	RequireApproverNotSubmitter(ctx context.Context, projectID uuid.UUID) (bool, error)
}

type Service struct {
	pool       *pgxpool.Pool
	store      *Store
	audit      *audit.Store
	releases   ReleaseAccess
	projects   ProjectAccess
	candidates *candidates.Store
	evidence   *evidence.Store
}

func NewService(pool *pgxpool.Pool, store *Store, auditStore *audit.Store, releaseAccess ReleaseAccess, projectAccess ProjectAccess, candidatesStore *candidates.Store, evidenceStore *evidence.Store) *Service {
	return &Service{pool: pool, store: store, audit: auditStore, releases: releaseAccess, projects: projectAccess, candidates: candidatesStore, evidence: evidenceStore}
}

// Decide records one approve/reject decision. It enforces, on the
// server: the actor is an eligible approver (role approver or admin and
// a member of the project, unless they are an admin), and the actor is
// never the release's own owner.
func (s *Service) Decide(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, decision Decision, comment string) (Approval, error) {
	// Nothing reachable from an MCP token may approve or reject a
	// release — already true today because no MCP route wires here,
	// but checked explicitly too, independent of routing. See
	// docs/implementation-roadmap.md item 10.
	if identity.Via == authn.ViaMCP {
		return Approval{}, httpx.Forbidden("mcp tokens cannot approve or reject a release")
	}
	rel, err := s.releases.GetForService(ctx, identity, releaseID)
	if err != nil {
		return Approval{}, err
	}
	if !decision.Valid() {
		return Approval{}, httpx.BadRequest("decision must be approved or rejected")
	}
	if identity.User.ID == rel.OwnerID {
		return Approval{}, httpx.Forbidden("you cannot approve or reject a release you own")
	}
	if identity.User.Role != users.RoleAdmin && identity.User.Role != users.RoleApprover {
		return Approval{}, httpx.Forbidden("only approvers and admins can approve or reject a release")
	}
	if identity.User.Role != users.RoleAdmin {
		isMember, err := s.projects.CanView(ctx, identity, rel.ProjectID)
		if err != nil {
			return Approval{}, err
		}
		if !isMember {
			return Approval{}, httpx.Forbidden("you must be a member of this project to approve its releases")
		}
	}

	// Configurable separation of duties, beyond the always-on "not the
	// release owner" check above: a project may additionally require
	// that an approver did not submit evidence for the release they're
	// approving. See docs/architecture-audit.md Finding 9.
	requireNotSubmitter, err := s.projects.RequireApproverNotSubmitter(ctx, rel.ProjectID)
	if err != nil {
		return Approval{}, err
	}
	if requireNotSubmitter {
		submitted, err := s.evidence.SubmittedByUser(ctx, s.pool, releaseID, identity.User.ID)
		if err != nil {
			return Approval{}, err
		}
		if submitted {
			return Approval{}, httpx.Forbidden("you submitted evidence for this release and cannot also approve it under this project's separation-of-duties policy")
		}
	}

	current, err := s.candidates.GetCurrentByRelease(ctx, s.pool, releaseID)
	if err != nil {
		return Approval{}, err
	}
	var candidateID *uuid.UUID
	if current != nil {
		candidateID = &current.ID
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Approval{}, err
	}
	defer tx.Rollback(ctx)

	a, err := s.store.Create(ctx, tx, releaseID, identity.User.ID, decision, comment, candidateID)
	if err != nil {
		return Approval{}, err
	}
	action := "release_approved"
	if decision == DecisionRejected {
		action = "release_rejected"
	}
	if err := s.audit.Record(ctx, tx, "release", releaseID, action, identity.AuditActor(), nil, a, comment); err != nil {
		return Approval{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Approval{}, err
	}
	return a, nil
}

func (s *Service) ListByRelease(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) ([]Approval, error) {
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return nil, err
	}
	return s.store.ListByRelease(ctx, s.pool, releaseID)
}
