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
// table's CHECK constraint (see internal/evidence and migrations/0004).
// Policy.RequiredEvidenceTypes must only contain these values.
var ValidEvidenceTypes = map[string]bool{
	"change_summary":  true,
	"test_results":    true,
	"uat_signoff":     true,
	"rollback_plan":   true,
	"security_review": true,
	"other":           true,
}

type Policy struct {
	ID                     uuid.UUID  `json:"id"`
	ProjectID              uuid.UUID  `json:"project_id"`
	RequiredEvidenceTypes  []string   `json:"required_evidence_types"`
	RequiredApprovalsCount int        `json:"required_approvals_count"`
	BlockOnOpenRejection   bool       `json:"block_on_open_rejection"`
	UpdatedBy              *uuid.UUID `json:"updated_by,omitempty"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type Member struct {
	ProjectID uuid.UUID `json:"project_id"`
	UserID    uuid.UUID `json:"user_id"`
	AddedAt   time.Time `json:"added_at"`
}
