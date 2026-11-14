package checklists

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Shyam576/waypoint/internal/dbx"
)

type Store struct{}

func NewStore() *Store { return &Store{} }

// --- Templates ---

func (s *Store) CreateTemplate(ctx context.Context, q dbx.Querier, projectID uuid.UUID, version int, createdBy *uuid.UUID) (Template, error) {
	var t Template
	err := q.QueryRow(ctx, `
		INSERT INTO checklist_templates (project_id, version, created_by)
		VALUES ($1, $2, $3)
		RETURNING id, project_id, version, is_active, created_by, created_at`,
		projectID, version, createdBy,
	).Scan(&t.ID, &t.ProjectID, &t.Version, &t.IsActive, &t.CreatedBy, &t.CreatedAt)
	return t, err
}

func (s *Store) DeactivateAllTemplates(ctx context.Context, q dbx.Querier, projectID uuid.UUID) error {
	_, err := q.Exec(ctx, `UPDATE checklist_templates SET is_active = false WHERE project_id = $1 AND is_active`, projectID)
	return err
}

func (s *Store) MaxTemplateVersion(ctx context.Context, q dbx.Querier, projectID uuid.UUID) (int, error) {
	var max *int
	err := q.QueryRow(ctx, `SELECT MAX(version) FROM checklist_templates WHERE project_id = $1`, projectID).Scan(&max)
	if err != nil {
		return 0, err
	}
	if max == nil {
		return 0, nil
	}
	return *max, nil
}

func (s *Store) CreateTemplateItem(ctx context.Context, q dbx.Querier, templateID uuid.UUID, in TemplateItemInput) (TemplateItem, error) {
	var it TemplateItem
	err := q.QueryRow(ctx, `
		INSERT INTO checklist_template_items (template_id, category, title, description, is_required, verification_method, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, template_id, category, title, description, is_required, verification_method, sort_order`,
		templateID, in.Category, in.Title, in.Description, in.IsRequired, in.VerificationMethod, in.SortOrder,
	).Scan(&it.ID, &it.TemplateID, &it.Category, &it.Title, &it.Description, &it.IsRequired, &it.VerificationMethod, &it.SortOrder)
	return it, err
}

func (s *Store) GetActiveTemplate(ctx context.Context, q dbx.Querier, projectID uuid.UUID) (*Template, error) {
	var t Template
	err := q.QueryRow(ctx, `
		SELECT id, project_id, version, is_active, created_by, created_at
		FROM checklist_templates WHERE project_id = $1 AND is_active`, projectID,
	).Scan(&t.ID, &t.ProjectID, &t.Version, &t.IsActive, &t.CreatedBy, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (s *Store) ListTemplateVersions(ctx context.Context, q dbx.Querier, projectID uuid.UUID) ([]Template, error) {
	rows, err := q.Query(ctx, `
		SELECT id, project_id, version, is_active, created_by, created_at
		FROM checklist_templates WHERE project_id = $1 ORDER BY version DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Version, &t.IsActive, &t.CreatedBy, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// defaultTemplateItems mirrors the brief's suggested checklist
// categories and items. "Required automated tests passed", "Deployment
// result verified", and "Smoke tests completed" are marked
// automated_check to document that they're ideally machine-verified,
// even though nothing in this release wires up a channel for a machine
// to actually report that result yet (see the package doc comment).
var defaultTemplateItems = []TemplateItemInput{
	{Category: CategoryDevelopment, Title: "Code reviewed", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 1},
	{Category: CategoryDevelopment, Title: "Changes documented", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 2},
	{Category: CategoryDevelopment, Title: "Required automated tests passed", IsRequired: true, VerificationMethod: MethodAutomatedCheck, SortOrder: 3},
	{Category: CategoryDevelopment, Title: "Dependencies reviewed", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 4},

	{Category: CategoryUAT, Title: "Functional testing completed", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 1},
	{Category: CategoryUAT, Title: "Regression testing completed", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 2},
	{Category: CategoryUAT, Title: "Stakeholder acceptance recorded", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 3},

	{Category: CategoryProductionReadiness, Title: "Production configuration verified", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 1},
	{Category: CategoryProductionReadiness, Title: "Secrets and environment variables checked", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 2},
	{Category: CategoryProductionReadiness, Title: "Database migration reviewed", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 3},
	{Category: CategoryProductionReadiness, Title: "Backup and recovery readiness verified", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 4},
	{Category: CategoryProductionReadiness, Title: "Rollback plan documented", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 5},
	{Category: CategoryProductionReadiness, Title: "Monitoring and alerting readiness confirmed", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 6},
	{Category: CategoryProductionReadiness, Title: "Security review completed where required", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 7},

	{Category: CategoryPostDeployment, Title: "Deployment result verified", IsRequired: true, VerificationMethod: MethodAutomatedCheck, SortOrder: 1},
	{Category: CategoryPostDeployment, Title: "Smoke tests completed", IsRequired: true, VerificationMethod: MethodAutomatedCheck, SortOrder: 2},
	{Category: CategoryPostDeployment, Title: "Critical business functions tested", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 3},
	{Category: CategoryPostDeployment, Title: "Monitoring reviewed", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 4},
	{Category: CategoryPostDeployment, Title: "Release handover completed", IsRequired: true, VerificationMethod: MethodHumanAttestation, SortOrder: 5},
}

// SeedDefaultTemplate creates version 1 of a project's checklist
// template, populated with the brief's suggested categories and items,
// so the feature is usable without any extra setup. Intended to run
// inside the same transaction as project creation
// (internal/projects.Service.Create); it has no authorization check of
// its own since the caller has already decided this project is being
// created.
func (s *Store) SeedDefaultTemplate(ctx context.Context, q dbx.Querier, projectID uuid.UUID, createdBy *uuid.UUID) error {
	t, err := s.CreateTemplate(ctx, q, projectID, 1, createdBy)
	if err != nil {
		return err
	}
	for _, item := range defaultTemplateItems {
		if _, err := s.CreateTemplateItem(ctx, q, t.ID, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListTemplateItems(ctx context.Context, q dbx.Querier, templateID uuid.UUID) ([]TemplateItem, error) {
	rows, err := q.Query(ctx, `
		SELECT id, template_id, category, title, description, is_required, verification_method, sort_order
		FROM checklist_template_items WHERE template_id = $1 ORDER BY sort_order, title`, templateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TemplateItem{}
	for rows.Next() {
		var it TemplateItem
		if err := rows.Scan(&it.ID, &it.TemplateID, &it.Category, &it.Title, &it.Description, &it.IsRequired, &it.VerificationMethod, &it.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// --- Release checklists ---

const releaseItemColumns = `id, release_id, template_item_id, category, title, description, is_required, verification_method, owner_id, status, evidence_ref, rejection_reason, waiver_reason, decided_by, decided_at, created_at, updated_at`

func scanReleaseItem(row pgx.Row) (ReleaseItem, error) {
	var it ReleaseItem
	err := row.Scan(&it.ID, &it.ReleaseID, &it.TemplateItemID, &it.Category, &it.Title, &it.Description, &it.IsRequired, &it.VerificationMethod,
		&it.OwnerID, &it.Status, &it.EvidenceRef, &it.RejectionReason, &it.WaiverReason, &it.DecidedBy, &it.DecidedAt, &it.CreatedAt, &it.UpdatedAt)
	return it, err
}

// MaterializeForRelease copies the project's active template's items
// into their own rows for this release, if the project has one. It is a
// no-op (ok=false, no error) for a project that has never defined a
// checklist template, keeping the feature fully backward compatible.
// Intended to run inside the same transaction as release creation.
func (s *Store) MaterializeForRelease(ctx context.Context, q dbx.Querier, releaseID, projectID uuid.UUID) (ok bool, err error) {
	template, err := s.GetActiveTemplate(ctx, q, projectID)
	if err != nil {
		return false, err
	}
	if template == nil {
		return false, nil
	}
	if _, err := q.Exec(ctx, `INSERT INTO release_checklists (release_id, template_id) VALUES ($1, $2)`, releaseID, template.ID); err != nil {
		return false, err
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO release_checklist_items (release_id, template_item_id, category, title, description, is_required, verification_method)
		SELECT $1, id, category, title, description, is_required, verification_method
		FROM checklist_template_items WHERE template_id = $2`, releaseID, template.ID,
	); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ListByRelease(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) ([]ReleaseItem, error) {
	rows, err := q.Query(ctx, `
		SELECT `+releaseItemColumns+`
		FROM release_checklist_items WHERE release_id = $1 ORDER BY category, title`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReleaseItem{}
	for rows.Next() {
		it, err := scanReleaseItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) GetItemByID(ctx context.Context, q dbx.Querier, id uuid.UUID) (ReleaseItem, error) {
	row := q.QueryRow(ctx, `SELECT `+releaseItemColumns+` FROM release_checklist_items WHERE id = $1`, id)
	return scanReleaseItem(row)
}

type DecideParams struct {
	Status          ItemStatus
	OwnerID         *uuid.UUID
	EvidenceRef     string
	RejectionReason string
	WaiverReason    string
	DecidedBy       uuid.UUID
}

// Decide applies a status change (verify/reject/waive) to one item,
// stamping who decided it and when. AssignOwner (below) is the only
// mutation that doesn't go through here, since assigning an owner isn't
// itself a verification decision.
func (s *Store) Decide(ctx context.Context, q dbx.Querier, id uuid.UUID, p DecideParams) (ReleaseItem, error) {
	now := time.Now()
	row := q.QueryRow(ctx, `
		UPDATE release_checklist_items
		SET status = $2, evidence_ref = $3, rejection_reason = $4, waiver_reason = $5, decided_by = $6, decided_at = $7, updated_at = now()
		WHERE id = $1
		RETURNING `+releaseItemColumns,
		id, p.Status, p.EvidenceRef, p.RejectionReason, p.WaiverReason, p.DecidedBy, now,
	)
	return scanReleaseItem(row)
}

func (s *Store) AssignOwner(ctx context.Context, q dbx.Querier, id uuid.UUID, ownerID uuid.UUID) (ReleaseItem, error) {
	row := q.QueryRow(ctx, `
		UPDATE release_checklist_items SET owner_id = $2, updated_at = now()
		WHERE id = $1
		RETURNING `+releaseItemColumns,
		id, ownerID,
	)
	return scanReleaseItem(row)
}
