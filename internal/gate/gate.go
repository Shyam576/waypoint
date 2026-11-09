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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/approvals"
	"github.com/Shyam576/waypoint/internal/evidence"
	"github.com/Shyam576/waypoint/internal/projects"
	"github.com/Shyam576/waypoint/internal/releases"
)

type Result struct {
	Eligible                      bool     `json:"eligible"`
	Blockers                      []string `json:"blockers"`
	Stage                         string   `json:"stage"`
	RequiredEvidenceTypes         []string `json:"required_evidence_types"`
	MissingEvidenceTypes          []string `json:"missing_evidence_types"`
	RequiredApprovalsCount        int      `json:"required_approvals_count"`
	ApprovedCount                 int      `json:"approved_count"`
	MostRecentDecisionIsRejection bool     `json:"most_recent_decision_is_rejection"`
}

type Service struct {
	pool      *pgxpool.Pool
	releases  *releases.Store
	evidence  *evidence.Store
	approvals *approvals.Store
	projects  *projects.Store
}

func NewService(pool *pgxpool.Pool, releasesStore *releases.Store, evidenceStore *evidence.Store, approvalsStore *approvals.Store, projectsStore *projects.Store) *Service {
	return &Service{pool: pool, releases: releasesStore, evidence: evidenceStore, approvals: approvalsStore, projects: projectsStore}
}

// Evaluate computes production eligibility for one release. It performs
// no authorization check itself; callers (the web handler and the CI
// handler) are responsible for verifying the caller may see this release
// before calling Evaluate.
func (s *Service) Evaluate(ctx context.Context, releaseID uuid.UUID) (Result, error) {
	rel, err := s.releases.GetByID(ctx, s.pool, releaseID)
	if err != nil {
		return Result{}, err
	}
	policy, err := s.projects.GetPolicy(ctx, s.pool, rel.ProjectID)
	if err != nil {
		return Result{}, err
	}
	present, err := s.evidence.PresentTypes(ctx, s.pool, releaseID)
	if err != nil {
		return Result{}, err
	}
	latest, err := s.approvals.LatestPerApprover(ctx, s.pool, releaseID)
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

	return Result{
		Eligible:                      len(blockers) == 0,
		Blockers:                      blockers,
		Stage:                         string(rel.Stage),
		RequiredEvidenceTypes:         policy.RequiredEvidenceTypes,
		MissingEvidenceTypes:          missing,
		RequiredApprovalsCount:        policy.RequiredApprovalsCount,
		ApprovedCount:                 approvedCount,
		MostRecentDecisionIsRejection: mostRecentIsRejection,
	}, nil
}
