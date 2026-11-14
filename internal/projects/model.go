package projects

import (
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusArchived Status = "archived"
)

type Project struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Slug       string    `json:"slug"`
	OwningTeam string    `json:"owning_team"`
	RepoURL    *string   `json:"repo_url,omitempty"`
	Status     Status    `json:"status"`
	CreatedBy  uuid.UUID `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// EvidenceType mirrors the fixed vocabulary enforced on the evidence
// table's CHECK constraint (see internal/evidence and
// migrations/0004 + 0013). Policy.RequiredEvidenceTypes and
// RiskPolicy.ExtraRequiredEvidenceTypes must only contain these values.
var ValidEvidenceTypes = map[string]bool{
	"change_summary":       true,
	"test_results":         true,
	"uat_signoff":          true,
	"rollback_plan":        true,
	"security_review":      true,
	"config_comparison":    true,
	"smoke_test_results":   true,
	"payment_config_check": true,
	"other":                true,
}

// ValidRiskLevels mirrors internal/releases.RiskLevel's vocabulary. Kept
// as a plain map here (rather than importing internal/releases) to
// match how the rest of this file already treats evidence types: a
// local copy of a small, stable, shared vocabulary.
var ValidRiskLevels = map[string]bool{
	"low": true, "medium": true, "high": true, "critical": true,
}

type Policy struct {
	ID                          uuid.UUID `json:"id"`
	ProjectID                   uuid.UUID `json:"project_id"`
	RequiredEvidenceTypes       []string  `json:"required_evidence_types"`
	RequiredApprovalsCount      int       `json:"required_approvals_count"`
	BlockOnOpenRejection        bool      `json:"block_on_open_rejection"`
	RequireCandidateForApproval bool      `json:"require_candidate_for_approval"`
	RequireChecklistCompletion  bool      `json:"require_checklist_completion"`
	// RequireApproverNotSubmitter is a configurable separation-of-duties
	// rule beyond the always-on "not the release's own owner" check
	// (internal/approvals): when set, an approver who submitted any
	// evidence for a release cannot also approve it. Default false,
	// same opt-in convention as the flags above. See
	// docs/architecture-audit.md Finding 9.
	RequireApproverNotSubmitter bool       `json:"require_approver_not_submitter"`
	UpdatedBy                   *uuid.UUID `json:"updated_by,omitempty"`
	UpdatedAt                   time.Time  `json:"updated_at"`
}

// RiskPolicy is the extra requirements a project layers on top of its
// base Policy for releases at one specific risk tier. Only 'high' and
// 'critical' get a seeded default on project creation (see
// internal/checklists-style seeding in Service.Create); 'low'/'medium'
// having no row (or a project having none at all) means no extra
// requirements for that tier. See docs/architecture-audit.md Finding 4.
type RiskPolicy struct {
	ID                          uuid.UUID  `json:"id"`
	ProjectID                   uuid.UUID  `json:"project_id"`
	RiskLevel                   string     `json:"risk_level"`
	ExtraRequiredApprovalsCount int        `json:"extra_required_approvals_count"`
	ExtraRequiredEvidenceTypes  []string   `json:"extra_required_evidence_types"`
	RequireRiskAcceptance       bool       `json:"require_risk_acceptance"`
	UpdatedBy                   *uuid.UUID `json:"updated_by,omitempty"`
	UpdatedAt                   time.Time  `json:"updated_at"`
}

type Member struct {
	ProjectID uuid.UUID `json:"project_id"`
	UserID    uuid.UUID `json:"user_id"`
	AddedAt   time.Time `json:"added_at"`
}
