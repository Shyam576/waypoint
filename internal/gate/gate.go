// Package gate implements the production eligibility check: the single
// place that decides whether a release is allowed to deploy to
// production. internal/deployments calls Evaluate and refuses to record
// a production deployment if it is not eligible; the same Evaluate
// backs the human-readable GET .../eligibility endpoint. There is no
// other path that performs this check, by design, so the rule can never
// drift between "what the UI shows" and "what the pipeline enforces."
package gate

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/approvals"
	"github.com/Shyam576/waypoint/internal/candidates"
	"github.com/Shyam576/waypoint/internal/checklists"
	"github.com/Shyam576/waypoint/internal/evidence"
	"github.com/Shyam576/waypoint/internal/projects"
	"github.com/Shyam576/waypoint/internal/releases"
)

type Result struct {
	Eligible                      bool       `json:"eligible"`
	Blockers                      []string   `json:"blockers"`
	Stage                         string     `json:"stage"`
	RequiredEvidenceTypes         []string   `json:"required_evidence_types"`
	MissingEvidenceTypes          []string   `json:"missing_evidence_types"`
	RequiredApprovalsCount        int        `json:"required_approvals_count"`
	ApprovedCount                 int        `json:"approved_count"`
	MostRecentDecisionIsRejection bool       `json:"most_recent_decision_is_rejection"`
	CurrentCandidateID            *uuid.UUID `json:"current_candidate_id,omitempty"`
	// IncompleteChecklistItems lists the titles of required checklist
	// items that are neither verified nor waived yet. Always populated
	// for visibility; only actually blocks eligibility when the
	// effective policy's RequireChecklistCompletion is set.
	IncompleteChecklistItems []string `json:"incomplete_checklist_items,omitempty"`
	// PolicySnapshotAt is set once this release has entered
	// ready_for_production at least once: from that point on, this
	// evaluation uses the policy as it was at that moment, not whatever
	// the project's live policy currently says. Nil means no snapshot
	// exists yet and the live project policy is in effect.
	PolicySnapshotAt *time.Time `json:"policy_snapshot_at,omitempty"`
}

type Service struct {
	pool       *pgxpool.Pool
	releases   *releases.Store
	evidence   *evidence.Store
	approvals  *approvals.Store
	projects   *projects.Store
	candidates *candidates.Store
	checklists *checklists.Store
}

func NewService(pool *pgxpool.Pool, releasesStore *releases.Store, evidenceStore *evidence.Store, approvalsStore *approvals.Store, projectsStore *projects.Store, candidatesStore *candidates.Store, checklistsStore *checklists.Store) *Service {
	return &Service{pool: pool, releases: releasesStore, evidence: evidenceStore, approvals: approvalsStore, projects: projectsStore, candidates: candidatesStore, checklists: checklistsStore}
}

// Evaluate computes production eligibility for one release. It performs
// no authorization check itself; callers (the web handler and the CI
// handler) are responsible for verifying the caller may see this release
// before calling Evaluate.
// effectivePolicy is whichever policy actually governs this evaluation:
// the release's frozen snapshot if it has one (see
// migrations/0011_release_policy_snapshots.sql), otherwise the project's
// current live policy. Kept as its own small type (rather than reusing
// projects.Policy directly) so this package doesn't care which source it
// came from past this point.
type effectivePolicy struct {
	RequiredEvidenceTypes       []string
	RequiredApprovalsCount      int
	BlockOnOpenRejection        bool
	RequireCandidateForApproval bool
	RequireChecklistCompletion  bool
}

func (s *Service) Evaluate(ctx context.Context, releaseID uuid.UUID) (Result, error) {
	rel, err := s.releases.GetByID(ctx, s.pool, releaseID)
	if err != nil {
		return Result{}, err
	}
	snapshot, err := s.releases.GetPolicySnapshot(ctx, s.pool, releaseID)
	if err != nil {
		return Result{}, err
	}
	var policy effectivePolicy
	var policySnapshotAt *time.Time
	if snapshot != nil {
		policy = effectivePolicy{
			RequiredEvidenceTypes: snapshot.RequiredEvidenceTypes, RequiredApprovalsCount: snapshot.RequiredApprovalsCount,
			BlockOnOpenRejection: snapshot.BlockOnOpenRejection, RequireCandidateForApproval: snapshot.RequireCandidateForApproval,
			RequireChecklistCompletion: snapshot.RequireChecklistCompletion,
		}
		policySnapshotAt = &snapshot.SnapshottedAt
	} else {
		live, err := s.projects.GetPolicy(ctx, s.pool, rel.ProjectID)
		if err != nil {
			return Result{}, err
		}
		policy = effectivePolicy{
			RequiredEvidenceTypes: live.RequiredEvidenceTypes, RequiredApprovalsCount: live.RequiredApprovalsCount,
			BlockOnOpenRejection: live.BlockOnOpenRejection, RequireCandidateForApproval: live.RequireCandidateForApproval,
			RequireChecklistCompletion: live.RequireChecklistCompletion,
		}
	}
	present, err := s.evidence.PresentTypes(ctx, s.pool, releaseID)
	if err != nil {
		return Result{}, err
	}
	latest, err := s.approvals.LatestPerApprover(ctx, s.pool, releaseID)
	if err != nil {
		return Result{}, err
	}
	currentCandidate, err := s.candidates.GetCurrentByRelease(ctx, s.pool, releaseID)
	if err != nil {
		return Result{}, err
	}
	checklistItems, err := s.checklists.ListByRelease(ctx, s.pool, releaseID)
	if err != nil {
		return Result{}, err
	}

	var blockers []string

	if rel.Stage != releases.StageReadyForProduction && rel.Stage != releases.StageProduction {
		blockers = append(blockers, fmt.Sprintf("release must be in the Ready for Production stage (currently: %s)", rel.Stage))
	}

	var missing []string
	for _, t := range policy.RequiredEvidenceTypes {
		if !present[t] {
			missing = append(missing, t)
		}
	}
	for _, t := range missing {
		blockers = append(blockers, "missing required evidence: "+t)
	}

	// When a project requires approvals to be bound to a specific
	// candidate, an approval decided against any candidate other than the
	// current one (or decided before any candidate existed) silently
	// stops counting — this is what forces revalidation after the
	// artifact changes, with no separate "invalidate" step needed. See
	// docs/architecture-audit.md Finding 2.
	var candidateID *uuid.UUID
	if currentCandidate != nil {
		candidateID = &currentCandidate.ID
	}
	if policy.RequireCandidateForApproval {
		if currentCandidate == nil {
			blockers = append(blockers, "project policy requires a release candidate to be registered before approvals count, but none has been registered for this release")
		}
		filtered := latest[:0]
		for _, a := range latest {
			if currentCandidate != nil && a.CandidateID != nil && *a.CandidateID == currentCandidate.ID {
				filtered = append(filtered, a)
			}
		}
		latest = filtered
	}

	approvedCount := 0
	var mostRecent *approvals.Approval
	for i := range latest {
		a := latest[i]
		if a.Decision == approvals.DecisionApproved {
			approvedCount++
		}
		if mostRecent == nil || a.DecidedAt.After(mostRecent.DecidedAt) {
			mostRecent = &a
		}
	}
	if approvedCount < policy.RequiredApprovalsCount {
		blockers = append(blockers, fmt.Sprintf("needs %d approval(s), has %d", policy.RequiredApprovalsCount, approvedCount))
	}

	mostRecentIsRejection := false
	if policy.BlockOnOpenRejection && mostRecent != nil && mostRecent.Decision == approvals.DecisionRejected {
		mostRecentIsRejection = true
		blockers = append(blockers, "the most recent approval decision on this release is a rejection; it must be resolved with a new approval before proceeding")
	}

	// A required checklist item counts as incomplete unless it has been
	// verified or waived (a waiver is a deliberate, audited exception —
	// see internal/checklists.Service.Waive — and counts as satisfying
	// the requirement). This is always computed for visibility; it only
	// becomes a blocker when the effective policy opts in. See
	// docs/architecture-audit.md Finding 5.
	var incomplete []string
	for _, item := range checklistItems {
		if !item.IsRequired {
			continue
		}
		if item.Status != checklists.ItemVerified && item.Status != checklists.ItemWaived {
			incomplete = append(incomplete, item.Title)
		}
	}
	if policy.RequireChecklistCompletion {
		for _, title := range incomplete {
			blockers = append(blockers, "checklist item not verified: "+title)
		}
	}

	return Result{
		Eligible:                      len(blockers) == 0,
		Blockers:                      blockers,
		Stage:                         string(rel.Stage),
		RequiredEvidenceTypes:         policy.RequiredEvidenceTypes,
		MissingEvidenceTypes:          missing,
		RequiredApprovalsCount:        policy.RequiredApprovalsCount,
		ApprovedCount:                 approvedCount,
		MostRecentDecisionIsRejection: mostRecentIsRejection,
		CurrentCandidateID:            candidateID,
		IncompleteChecklistItems:      incomplete,
		PolicySnapshotAt:              policySnapshotAt,
	}, nil
}
