// Package checklists implements structured go-live checklists: a
// project defines a versioned template of items grouped into fixed
// categories (development, uat, production_readiness, post_deployment);
// a release is assigned the project's currently-active template version
// at creation time, by copying each item into its own row, so a later
// template change never retroactively alters a release already in
// flight. This extends (it does not replace) the existing free-text
// evidence system in internal/evidence. See
// docs/architecture-audit.md Finding 5.
package checklists

import (
	"time"

	"github.com/google/uuid"
)

type Category string

const (
	CategoryDevelopment         Category = "development"
	CategoryUAT                 Category = "uat"
	CategoryProductionReadiness Category = "production_readiness"
	CategoryPostDeployment      Category = "post_deployment"
)

func (c Category) Valid() bool {
	switch c {
	case CategoryDevelopment, CategoryUAT, CategoryProductionReadiness, CategoryPostDeployment:
		return true
	}
	return false
}

type VerificationMethod string

const (
	MethodHumanAttestation VerificationMethod = "human_attestation"
	MethodAutomatedCheck   VerificationMethod = "automated_check"
)

func (m VerificationMethod) Valid() bool {
	switch m {
	case MethodHumanAttestation, MethodAutomatedCheck:
		return true
	}
	return false
}

type ItemStatus string

const (
	ItemPending  ItemStatus = "pending"
	ItemVerified ItemStatus = "verified"
	ItemRejected ItemStatus = "rejected"
	ItemWaived   ItemStatus = "waived"
)

type Template struct {
	ID        uuid.UUID  `json:"id"`
	ProjectID uuid.UUID  `json:"project_id"`
	Version   int        `json:"version"`
	IsActive  bool       `json:"is_active"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type TemplateItem struct {
	ID                 uuid.UUID          `json:"id"`
	TemplateID         uuid.UUID          `json:"template_id"`
	Category           Category           `json:"category"`
	Title              string             `json:"title"`
	Description        string             `json:"description"`
	IsRequired         bool               `json:"is_required"`
	VerificationMethod VerificationMethod `json:"verification_method"`
	SortOrder          int                `json:"sort_order"`
}

type TemplateWithItems struct {
	Template
	Items []TemplateItem `json:"items"`
}

// ReleaseItem is one release's own copy of a template item, with its
// live verification state.
type ReleaseItem struct {
	ID                 uuid.UUID          `json:"id"`
	ReleaseID          uuid.UUID          `json:"release_id"`
	TemplateItemID     *uuid.UUID         `json:"template_item_id,omitempty"`
	Category           Category           `json:"category"`
	Title              string             `json:"title"`
	Description        string             `json:"description"`
	IsRequired         bool               `json:"is_required"`
	VerificationMethod VerificationMethod `json:"verification_method"`
	OwnerID            *uuid.UUID         `json:"owner_id,omitempty"`
	Status             ItemStatus         `json:"status"`
	EvidenceRef        string             `json:"evidence_ref,omitempty"`
	RejectionReason    string             `json:"rejection_reason,omitempty"`
	WaiverReason       string             `json:"waiver_reason,omitempty"`
	DecidedBy          *uuid.UUID         `json:"decided_by,omitempty"`
	DecidedAt          *time.Time         `json:"decided_at,omitempty"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// TemplateItemInput is one item in a new template version, as supplied
// by the caller creating it.
type TemplateItemInput struct {
	Category           Category
	Title              string
	Description        string
	IsRequired         bool
	VerificationMethod VerificationMethod
	SortOrder          int
}
