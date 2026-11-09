package mcpnotes

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/releases"
	"github.com/Shyam576/waypoint/internal/support"
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
	support  *support.Service
}

func NewService(pool *pgxpool.Pool, store *Store, auditStore *audit.Store, releaseAccess ReleaseAccess, supportSvc *support.Service) *Service {
	return &Service{pool: pool, store: store, audit: auditStore, releases: releaseAccess, support: supportSvc}
}

// --- Direct web authorship (no MCP staging involved) ---

func (s *Service) CreateChangeNoteWeb(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, p ChangeNotePayload) (ChangeNote, error) {
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return ChangeNote{}, err
	}
	if err := validateChangeNotePayload(p); err != nil {
		return ChangeNote{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ChangeNote{}, err
	}
	defer tx.Rollback(ctx)

	c, err := s.store.CreateChangeNote(ctx, tx, CreateChangeNoteParams{
		ReleaseID: releaseID, AuthorID: identity.User.ID, Summary: p.Summary, Rationale: p.Rationale,
		AffectedService: p.AffectedService, TestsRun: p.TestsRun, TestsStatus: p.TestsStatus,
		LinkedPRURL: p.LinkedPRURL, LinkedCommitSHA: p.LinkedCommitSHA, Source: SourceWeb,
	})
	if err != nil {
		return ChangeNote{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", releaseID, "change_note_added", identity.AuditActor(), nil, c, ""); err != nil {
		return ChangeNote{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ChangeNote{}, err
	}
	return c, nil
}

func (s *Service) ListChangeNotes(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) ([]ChangeNote, error) {
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return nil, err
	}
	return s.store.ListChangeNotesByRelease(ctx, s.pool, releaseID)
}

func (s *Service) CreateDesignDecisionWeb(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, p DesignDecisionPayload) (DesignDecision, error) {
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return DesignDecision{}, err
	}
	if err := validateDesignDecisionPayload(p); err != nil {
		return DesignDecision{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DesignDecision{}, err
	}
	defer tx.Rollback(ctx)

	d, err := s.store.CreateDesignDecision(ctx, tx, CreateDesignDecisionParams{
		ReleaseID: releaseID, AuthorID: identity.User.ID, Decision: p.Decision, Rationale: p.Rationale,
		AlternativesConsidered: p.AlternativesConsidered, Impact: p.Impact,
		LinkedPRURL: p.LinkedPRURL, LinkedCommitSHA: p.LinkedCommitSHA, Source: SourceWeb,
	})
	if err != nil {
		return DesignDecision{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", releaseID, "design_decision_added", identity.AuditActor(), nil, d, ""); err != nil {
		return DesignDecision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DesignDecision{}, err
	}
	return d, nil
}

func (s *Service) ListDesignDecisions(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) ([]DesignDecision, error) {
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return nil, err
	}
	return s.store.ListDesignDecisionsByRelease(ctx, s.pool, releaseID)
}

// --- MCP submission intake (always pending) ---

func validateChangeNotePayload(p ChangeNotePayload) error {
	if p.Summary == "" {
		return httpx.BadRequest("summary is required")
	}
	if p.TestsStatus == "" {
		p.TestsStatus = TestsNotRun
	}
	if !p.TestsStatus.Valid() {
		return httpx.BadRequest("tests_status must be passed, failed, or not_run")
	}
	return nil
}

func validateDesignDecisionPayload(p DesignDecisionPayload) error {
	if p.Decision == "" {
		return httpx.BadRequest("decision is required")
	}
	return nil
}

func validateSupportNotePayload(p SupportNotePayload) error {
	if p.Summary == "" {
		return httpx.BadRequest("summary is required")
	}
	switch support.Severity(p.Severity) {
	case support.SeverityLow, support.SeverityMedium, support.SeverityHigh, support.SeverityCritical:
	case "":
	default:
		return httpx.BadRequest("severity must be low, medium, high, or critical")
	}
	return nil
}

// SubmitChangeNote stores a change_note submission from Claude via MCP.
// It never writes to change_notes directly; it is pending review.
func (s *Service) SubmitChangeNote(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, p ChangeNotePayload) (Submission, error) {
	if err := s.requireMCP(identity); err != nil {
		return Submission{}, err
	}
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return Submission{}, err
	}
	if err := validateChangeNotePayload(p); err != nil {
		return Submission{}, err
	}
	if p.TestsStatus == "" {
		p.TestsStatus = TestsNotRun
	}
	return s.submit(ctx, identity, releaseID, SubmissionChangeNote, p)
}

func (s *Service) SubmitDesignDecision(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, p DesignDecisionPayload) (Submission, error) {
	if err := s.requireMCP(identity); err != nil {
		return Submission{}, err
	}
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return Submission{}, err
	}
	if err := validateDesignDecisionPayload(p); err != nil {
		return Submission{}, err
	}
	return s.submit(ctx, identity, releaseID, SubmissionDesignDecision, p)
}

func (s *Service) SubmitSupportNote(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, p SupportNotePayload) (Submission, error) {
	if err := s.requireMCP(identity); err != nil {
		return Submission{}, err
	}
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return Submission{}, err
	}
	if err := validateSupportNotePayload(p); err != nil {
		return Submission{}, err
	}
	if p.Severity == "" {
		p.Severity = string(support.SeverityMedium)
	}
	return s.submit(ctx, identity, releaseID, SubmissionSupportNote, p)
}

func (s *Service) requireMCP(identity authn.Identity) error {
	if identity.Via != authn.ViaMCP {
		return httpx.Forbidden("this action requires an MCP token")
	}
	return nil
}

func (s *Service) submit(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, typ SubmissionType, payload any) (Submission, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Submission{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Submission{}, err
	}
	defer tx.Rollback(ctx)

	sub, err := s.store.CreateSubmission(ctx, tx, releaseID, typ, raw, identity.User.ID)
	if err != nil {
		return Submission{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", releaseID, "mcp_submission_received", identity.AuditActor(), nil, sub, string(typ)); err != nil {
		return Submission{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Submission{}, err
	}
	return sub, nil
}

// --- Review (web, by a developer/coordinator/admin) ---

func (s *Service) canReview(identity authn.Identity) bool {
	switch identity.User.Role {
	case users.RoleAdmin, users.RoleDeveloper, users.RoleReleaseCoordinator:
		return true
	default:
		return false
	}
}

func (s *Service) ListPending(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, status *SubmissionStatus) ([]Submission, error) {
	if _, err := s.releases.GetForService(ctx, identity, releaseID); err != nil {
		return nil, err
	}
	return s.store.ListSubmissionsByRelease(ctx, s.pool, releaseID, status)
}

// Accept materializes a pending submission into its real table
// (change_notes, design_decisions, or support_items), attributed to the
// original MCP submitter, and marks the submission accepted. Both writes
// commit together.
func (s *Service) Accept(ctx context.Context, identity authn.Identity, submissionID uuid.UUID, reviewNote string) (Submission, error) {
	sub, err := s.store.GetSubmission(ctx, s.pool, submissionID)
	if err != nil {
		return Submission{}, httpx.NotFound("submission not found")
	}
	if _, err := s.releases.GetForService(ctx, identity, sub.ReleaseID); err != nil {
		return Submission{}, err
	}
	if sub.Status != SubmissionPending {
		return Submission{}, httpx.Conflict("submission has already been reviewed")
	}
	if !s.canReview(identity) {
		return Submission{}, httpx.Forbidden("only developers, release coordinators, and admins can review mcp submissions")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Submission{}, err
	}
	defer tx.Rollback(ctx)

	switch sub.Type {
	case SubmissionChangeNote:
		var p ChangeNotePayload
		if err := json.Unmarshal(sub.Payload, &p); err != nil {
			return Submission{}, err
		}
		c, err := s.store.CreateChangeNote(ctx, tx, CreateChangeNoteParams{
			ReleaseID: sub.ReleaseID, AuthorID: sub.SubmittedBy, Summary: p.Summary, Rationale: p.Rationale,
			AffectedService: p.AffectedService, TestsRun: p.TestsRun, TestsStatus: p.TestsStatus,
			LinkedPRURL: p.LinkedPRURL, LinkedCommitSHA: p.LinkedCommitSHA, Source: SourceMCP, MCPSubmissionID: &sub.ID,
		})
		if err != nil {
			return Submission{}, err
		}
		if err := s.audit.Record(ctx, tx, "release", sub.ReleaseID, "change_note_added", identity.AuditActor(), nil, c, "accepted from mcp submission"); err != nil {
			return Submission{}, err
		}
	case SubmissionDesignDecision:
		var p DesignDecisionPayload
		if err := json.Unmarshal(sub.Payload, &p); err != nil {
			return Submission{}, err
		}
		d, err := s.store.CreateDesignDecision(ctx, tx, CreateDesignDecisionParams{
			ReleaseID: sub.ReleaseID, AuthorID: sub.SubmittedBy, Decision: p.Decision, Rationale: p.Rationale,
			AlternativesConsidered: p.AlternativesConsidered, Impact: p.Impact,
			LinkedPRURL: p.LinkedPRURL, LinkedCommitSHA: p.LinkedCommitSHA, Source: SourceMCP, MCPSubmissionID: &sub.ID,
		})
		if err != nil {
			return Submission{}, err
		}
		if err := s.audit.Record(ctx, tx, "release", sub.ReleaseID, "design_decision_added", identity.AuditActor(), nil, d, "accepted from mcp submission"); err != nil {
			return Submission{}, err
		}
	case SubmissionSupportNote:
		var p SupportNotePayload
		if err := json.Unmarshal(sub.Payload, &p); err != nil {
			return Submission{}, err
		}
		severity := support.Severity(p.Severity)
		if severity == "" {
			severity = support.SeverityMedium
		}
		_, err := s.support.CreateFromMCP(ctx, tx, identity, support.CreateParams{
			ReleaseID: sub.ReleaseID, Summary: p.Summary, Severity: severity,
			LinkedTicketURL: p.LinkedTicketURL, LinkedPRURL: p.LinkedPRURL, MCPSubmissionID: &sub.ID, CreatedBy: sub.SubmittedBy,
		})
		if err != nil {
			return Submission{}, err
		}
	}

	if err := s.store.MarkReviewed(ctx, tx, sub.ID, SubmissionAccepted, identity.User.ID, reviewNote); err != nil {
		return Submission{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", sub.ReleaseID, "mcp_submission_accepted", identity.AuditActor(), nil, nil, reviewNote); err != nil {
		return Submission{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Submission{}, err
	}

	sub.Status = SubmissionAccepted
	return sub, nil
}

func (s *Service) Reject(ctx context.Context, identity authn.Identity, submissionID uuid.UUID, reviewNote string) (Submission, error) {
	sub, err := s.store.GetSubmission(ctx, s.pool, submissionID)
	if err != nil {
		return Submission{}, httpx.NotFound("submission not found")
	}
	if _, err := s.releases.GetForService(ctx, identity, sub.ReleaseID); err != nil {
		return Submission{}, err
	}
	if sub.Status != SubmissionPending {
		return Submission{}, httpx.Conflict("submission has already been reviewed")
	}
	if !s.canReview(identity) {
		return Submission{}, httpx.Forbidden("only developers, release coordinators, and admins can review mcp submissions")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Submission{}, err
	}
	defer tx.Rollback(ctx)

	if err := s.store.MarkReviewed(ctx, tx, sub.ID, SubmissionRejected, identity.User.ID, reviewNote); err != nil {
		return Submission{}, err
	}
	if err := s.audit.Record(ctx, tx, "release", sub.ReleaseID, "mcp_submission_rejected", identity.AuditActor(), nil, nil, reviewNote); err != nil {
		return Submission{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Submission{}, err
	}

	sub.Status = SubmissionRejected
	return sub, nil
}
