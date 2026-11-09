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
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

func (r RiskLevel) Valid() bool {
	switch r {
	case RiskLow, RiskMedium, RiskHigh:
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
	Summary           string     `json:"summary"`
	RollbackPlan      string     `json:"rollback_plan"`
	SupportPeriodDays int        `json:"support_period_days"`
	Stage             Stage      `json:"stage"`
	SupportEndsAt     *time.Time `json:"support_ends_at,omitempty"`
	CreatedBy         uuid.UUID  `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
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
