// Package releases implements the release record and its stage machine.
// Stage and approval status are deliberately kept separate: stage says
// where the release is in the delivery process, while approvals (see
// internal/approvals) and the production gate (see internal/gate) decide
// whether it is allowed to progress into production.
package releases

import (
	"time"

	"github.com/google/uuid"
)

type Stage string

const (
	StageDraft              Stage = "draft"
	StageDevelopment        Stage = "development"
	StageUAT                Stage = "uat"
	StageReadyForProduction Stage = "ready_for_production"
	StageProduction         Stage = "production"
	StagePostReleaseSupport Stage = "post_release_support"
	StageClosed             Stage = "closed"
)

func (s Stage) Valid() bool {
	switch s {
	case StageDraft, StageDevelopment, StageUAT, StageReadyForProduction, StageProduction, StagePostReleaseSupport, StageClosed:
		return true
	}
	return false
}

// allowedTransitions is the single source of truth for which stage moves
// are legal. Every transition request is checked against this map; there
// is no other path in the codebase that writes to releases.stage.
var allowedTransitions = map[Stage][]Stage{
	StageDraft:              {StageDevelopment, StageClosed},
	StageDevelopment:        {StageUAT, StageDraft, StageClosed},
	StageUAT:                {StageReadyForProduction, StageDevelopment, StageClosed},
	StageReadyForProduction: {StageProduction, StageUAT, StageClosed},
	StageProduction:         {StagePostReleaseSupport, StageClosed},
	StagePostReleaseSupport: {StageClosed},
	StageClosed:             {},
}

// CanTransition reports whether moving from `from` to `to` is a legal
// manual transition.
func CanTransition(from, to Stage) bool {
	for _, s := range allowedTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

func (r RiskLevel) Valid() bool {
	switch r {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	}
	return false
}

// RiskCategory tags a release with a sensitive area it touches, per the
// brief's examples (a payment incident was the motivating scenario).
// This is classification/visibility only — it does not by itself change
// gate requirements; RiskLevel is what internal/gate consults via
// internal/projects' per-tier risk policy. See
// docs/architecture-audit.md Finding 4.
type RiskCategory string

const (
	RiskCategoryPayment         RiskCategory = "payment"
	RiskCategoryAuthentication  RiskCategory = "authentication"
	RiskCategorySchemaMigration RiskCategory = "schema_migration"
	RiskCategoryInfraConfig     RiskCategory = "infra_config"
	RiskCategorySecrets         RiskCategory = "secrets"
	RiskCategorySensitiveAPI    RiskCategory = "sensitive_api"
	RiskCategoryOther           RiskCategory = "other"
)

func (c RiskCategory) Valid() bool {
	switch c {
	case RiskCategoryPayment, RiskCategoryAuthentication, RiskCategorySchemaMigration, RiskCategoryInfraConfig, RiskCategorySecrets, RiskCategorySensitiveAPI, RiskCategoryOther:
		return true
	}
	return false
}

type Release struct {
	ID                uuid.UUID  `json:"id"`
	ProjectID         uuid.UUID  `json:"project_id"`
	OwnerID           uuid.UUID  `json:"owner_id"`
	Name              string     `json:"name"`
	TargetDate        *time.Time `json:"target_date,omitempty"`
	RiskLevel         RiskLevel  `json:"risk_level"`
	RiskCategories    []string   `json:"risk_categories"`
	Summary           string     `json:"summary"`
	RollbackPlan      string     `json:"rollback_plan"`
	SupportPeriodDays int        `json:"support_period_days"`
	Stage             Stage      `json:"stage"`
	SupportEndsAt     *time.Time `json:"support_ends_at,omitempty"`
	CreatedBy         uuid.UUID  `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// RiskAcceptance is an explicit, named acknowledgement that a release's
// risk was considered and accepted, distinct from an ordinary approval.
// internal/gate requires one before eligibility only when the matching
// project_risk_policies row sets RequireRiskAcceptance.
type RiskAcceptance struct {
	ReleaseID  uuid.UUID `json:"release_id"`
	AcceptedBy uuid.UUID `json:"accepted_by"`
	Note       string    `json:"note"`
	AcceptedAt time.Time `json:"accepted_at"`
}

type LinkType string

const (
	LinkWorkItem    LinkType = "work_item"
	LinkPullRequest LinkType = "pull_request"
	LinkCommit      LinkType = "commit"
	LinkCIRun       LinkType = "ci_run"
)

func (t LinkType) Valid() bool {
	switch t {
	case LinkWorkItem, LinkPullRequest, LinkCommit, LinkCIRun:
		return true
	}
	return false
}

type Link struct {
	ID        uuid.UUID `json:"id"`
	ReleaseID uuid.UUID `json:"release_id"`
	LinkType  LinkType  `json:"link_type"`
	URL       string    `json:"url"`
	Label     *string   `json:"label,omitempty"`
	CreatedBy uuid.UUID `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// PolicySnapshotInput is the shape of a project policy captured onto a
// release. Defined here (rather than importing internal/projects.Policy)
// so this package doesn't need to depend on the projects package; the
// ProjectAccess interface below asks for these fields as plain values,
// and internal/projects.Service.CurrentPolicy supplies them.
type PolicySnapshotInput struct {
	RequiredEvidenceTypes       []string
	RequiredApprovalsCount      int
	BlockOnOpenRejection        bool
	RequireCandidateForApproval bool
	RequireChecklistCompletion  bool
	// The following three are resolved for this release's own risk_level
	// (internal/projects.Service.RiskPolicyFor) at snapshot time, not
	// re-read live afterward — same freezing principle as the rest of
	// this snapshot.
	ExtraRequiredApprovalsCount int
	ExtraRequiredEvidenceTypes  []string
	RequireRiskAcceptance       bool
}

// PolicySnapshot is the frozen policy internal/gate reads once a release
// has entered ready_for_production at least once. See
// migrations/0011_release_policy_snapshots.sql.
type PolicySnapshot struct {
	ReleaseID                   uuid.UUID `json:"release_id"`
	RequiredEvidenceTypes       []string  `json:"required_evidence_types"`
	RequiredApprovalsCount      int       `json:"required_approvals_count"`
	BlockOnOpenRejection        bool      `json:"block_on_open_rejection"`
	RequireCandidateForApproval bool      `json:"require_candidate_for_approval"`
	RequireChecklistCompletion  bool      `json:"require_checklist_completion"`
	ExtraRequiredApprovalsCount int       `json:"extra_required_approvals_count"`
	ExtraRequiredEvidenceTypes  []string  `json:"extra_required_evidence_types"`
	RequireRiskAcceptance       bool      `json:"require_risk_acceptance"`
	SnapshottedAt               time.Time `json:"snapshotted_at"`
}
