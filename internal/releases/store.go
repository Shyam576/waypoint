package releases

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

// releaseColumns / scanReleaseRow centralize the release row shape so
// every method below (and future ones) stays in sync automatically.
const releaseColumns = `id, project_id, owner_id, name, target_date, risk_level, risk_categories, summary, rollback_plan, support_period_days, stage, support_ends_at, created_by, created_at, updated_at`

func scanReleaseRow(row pgx.Row) (Release, error) {
	var r Release
	err := row.Scan(&r.ID, &r.ProjectID, &r.OwnerID, &r.Name, &r.TargetDate, &r.RiskLevel, &r.RiskCategories, &r.Summary, &r.RollbackPlan, &r.SupportPeriodDays, &r.Stage, &r.SupportEndsAt, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

type CreateParams struct {
	ProjectID         uuid.UUID
	OwnerID           uuid.UUID
	Name              string
	TargetDate        *time.Time
	RiskLevel         RiskLevel
	RiskCategories    []string
	Summary           string
	RollbackPlan      string
	SupportPeriodDays int
	CreatedBy         uuid.UUID
}

func (s *Store) Create(ctx context.Context, q dbx.Querier, p CreateParams) (Release, error) {
	if p.RiskCategories == nil {
		p.RiskCategories = []string{}
	}
	row := q.QueryRow(ctx, `
		INSERT INTO releases (project_id, owner_id, name, target_date, risk_level, risk_categories, summary, rollback_plan, support_period_days, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+releaseColumns,
		p.ProjectID, p.OwnerID, p.Name, p.TargetDate, p.RiskLevel, p.RiskCategories, p.Summary, p.RollbackPlan, p.SupportPeriodDays, p.CreatedBy,
	)
	return scanReleaseRow(row)
}

func (s *Store) GetByID(ctx context.Context, q dbx.Querier, id uuid.UUID) (Release, error) {
	row := q.QueryRow(ctx, `SELECT `+releaseColumns+` FROM releases WHERE id = $1`, id)
	return scanReleaseRow(row)
}

func (s *Store) ListByProject(ctx context.Context, q dbx.Querier, projectID uuid.UUID, stage *Stage) ([]Release, error) {
	var rows pgx.Rows
	var err error
	if stage != nil {
		rows, err = q.Query(ctx, `SELECT `+releaseColumns+` FROM releases WHERE project_id = $1 AND stage = $2 ORDER BY created_at DESC`, projectID, *stage)
	} else {
		rows, err = q.Query(ctx, `SELECT `+releaseColumns+` FROM releases WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReleases(rows)
}

func scanReleases(rows pgx.Rows) ([]Release, error) {
	out := []Release{}
	for rows.Next() {
		r, err := scanReleaseRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type UpdateParams struct {
	Name              string
	TargetDate        *time.Time
	RiskLevel         RiskLevel
	RiskCategories    []string
	Summary           string
	RollbackPlan      string
	SupportPeriodDays int
}

func (s *Store) UpdateMeta(ctx context.Context, q dbx.Querier, id uuid.UUID, p UpdateParams) (Release, error) {
	if p.RiskCategories == nil {
		p.RiskCategories = []string{}
	}
	row := q.QueryRow(ctx, `
		UPDATE releases
		SET name = $2, target_date = $3, risk_level = $4, risk_categories = $5, summary = $6, rollback_plan = $7, support_period_days = $8, updated_at = now()
		WHERE id = $1
		RETURNING `+releaseColumns,
		id, p.Name, p.TargetDate, p.RiskLevel, p.RiskCategories, p.Summary, p.RollbackPlan, p.SupportPeriodDays,
	)
	return scanReleaseRow(row)
}

func (s *Store) UpdateStage(ctx context.Context, q dbx.Querier, id uuid.UUID, stage Stage, supportEndsAt *time.Time) (Release, error) {
	row := q.QueryRow(ctx, `
		UPDATE releases SET stage = $2, support_ends_at = $3, updated_at = now()
		WHERE id = $1
		RETURNING `+releaseColumns,
		id, stage, supportEndsAt,
	)
	return scanReleaseRow(row)
}

func (s *Store) CreateLink(ctx context.Context, q dbx.Querier, releaseID uuid.UUID, linkType LinkType, url string, label *string, createdBy uuid.UUID) (Link, error) {
	var l Link
	err := q.QueryRow(ctx, `
		INSERT INTO release_links (release_id, link_type, url, label, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, release_id, link_type, url, label, created_by, created_at`,
		releaseID, linkType, url, label, createdBy,
	).Scan(&l.ID, &l.ReleaseID, &l.LinkType, &l.URL, &l.Label, &l.CreatedBy, &l.CreatedAt)
	return l, err
}

func (s *Store) ListLinks(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) ([]Link, error) {
	rows, err := q.Query(ctx, `
		SELECT id, release_id, link_type, url, label, created_by, created_at
		FROM release_links WHERE release_id = $1 ORDER BY created_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.ReleaseID, &l.LinkType, &l.URL, &l.Label, &l.CreatedBy, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// UpsertPolicySnapshot records (or re-records, on a later re-entry into
// ready_for_production) the policy snapshot for one release.
const policySnapshotColumns = `release_id, required_evidence_types, required_approvals_count, block_on_open_rejection, require_candidate_for_approval, require_checklist_completion, extra_required_approvals_count, extra_required_evidence_types, require_risk_acceptance, snapshotted_at`

func scanPolicySnapshot(row pgx.Row) (PolicySnapshot, error) {
	var p PolicySnapshot
	err := row.Scan(&p.ReleaseID, &p.RequiredEvidenceTypes, &p.RequiredApprovalsCount, &p.BlockOnOpenRejection, &p.RequireCandidateForApproval,
		&p.RequireChecklistCompletion, &p.ExtraRequiredApprovalsCount, &p.ExtraRequiredEvidenceTypes, &p.RequireRiskAcceptance, &p.SnapshottedAt)
	return p, err
}

func (s *Store) UpsertPolicySnapshot(ctx context.Context, q dbx.Querier, releaseID uuid.UUID, in PolicySnapshotInput) (PolicySnapshot, error) {
	if in.ExtraRequiredEvidenceTypes == nil {
		in.ExtraRequiredEvidenceTypes = []string{}
	}
	row := q.QueryRow(ctx, `
		INSERT INTO release_policy_snapshots (release_id, required_evidence_types, required_approvals_count, block_on_open_rejection, require_candidate_for_approval, require_checklist_completion, extra_required_approvals_count, extra_required_evidence_types, require_risk_acceptance)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (release_id) DO UPDATE SET
			required_evidence_types = EXCLUDED.required_evidence_types,
			required_approvals_count = EXCLUDED.required_approvals_count,
			block_on_open_rejection = EXCLUDED.block_on_open_rejection,
			require_candidate_for_approval = EXCLUDED.require_candidate_for_approval,
			require_checklist_completion = EXCLUDED.require_checklist_completion,
			extra_required_approvals_count = EXCLUDED.extra_required_approvals_count,
			extra_required_evidence_types = EXCLUDED.extra_required_evidence_types,
			require_risk_acceptance = EXCLUDED.require_risk_acceptance,
			snapshotted_at = now()
		RETURNING `+policySnapshotColumns,
		releaseID, in.RequiredEvidenceTypes, in.RequiredApprovalsCount, in.BlockOnOpenRejection, in.RequireCandidateForApproval,
		in.RequireChecklistCompletion, in.ExtraRequiredApprovalsCount, in.ExtraRequiredEvidenceTypes, in.RequireRiskAcceptance,
	)
	return scanPolicySnapshot(row)
}

// GetPolicySnapshot returns the release's policy snapshot, or (nil, nil)
// if it has never entered ready_for_production (so no snapshot exists
// yet and the live project policy still applies).
func (s *Store) GetPolicySnapshot(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) (*PolicySnapshot, error) {
	row := q.QueryRow(ctx, `SELECT `+policySnapshotColumns+` FROM release_policy_snapshots WHERE release_id = $1`, releaseID)
	p, err := scanPolicySnapshot(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// UpsertRiskAcceptance records (or re-records) the single "risk was
// accepted" row for a release. One row per release: accepting again
// overwrites who/when/why, which is fine because audit_logs (written by
// the caller in the same transaction) is what preserves the history.
func (s *Store) UpsertRiskAcceptance(ctx context.Context, q dbx.Querier, releaseID, acceptedBy uuid.UUID, note string) (RiskAcceptance, error) {
	var a RiskAcceptance
	err := q.QueryRow(ctx, `
		INSERT INTO release_risk_acceptances (release_id, accepted_by, note)
		VALUES ($1, $2, $3)
		ON CONFLICT (release_id) DO UPDATE SET accepted_by = EXCLUDED.accepted_by, note = EXCLUDED.note, accepted_at = now()
		RETURNING release_id, accepted_by, note, accepted_at`,
		releaseID, acceptedBy, note,
	).Scan(&a.ReleaseID, &a.AcceptedBy, &a.Note, &a.AcceptedAt)
	return a, err
}

// GetRiskAcceptance returns (nil, nil) if the release's risk has never
// been explicitly accepted.
func (s *Store) GetRiskAcceptance(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) (*RiskAcceptance, error) {
	var a RiskAcceptance
	err := q.QueryRow(ctx, `SELECT release_id, accepted_by, note, accepted_at FROM release_risk_acceptances WHERE release_id = $1`, releaseID).
		Scan(&a.ReleaseID, &a.AcceptedBy, &a.Note, &a.AcceptedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}
