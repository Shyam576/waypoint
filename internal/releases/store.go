package releases

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Shyam576/waypoint/internal/dbx"
)

type Store struct{}

func NewStore() *Store { return &Store{} }

type CreateParams struct {
	ProjectID         uuid.UUID
	OwnerID           uuid.UUID
	Name              string
	TargetDate        *time.Time
	RiskLevel         RiskLevel
	Summary           string
	RollbackPlan      string
	SupportPeriodDays int
	CreatedBy         uuid.UUID
}

func (s *Store) Create(ctx context.Context, q dbx.Querier, p CreateParams) (Release, error) {
	var r Release
	err := q.QueryRow(ctx, `
		INSERT INTO releases (project_id, owner_id, name, target_date, risk_level, summary, rollback_plan, support_period_days, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, project_id, owner_id, name, target_date, risk_level, summary, rollback_plan, support_period_days, stage, support_ends_at, created_by, created_at, updated_at`,
		p.ProjectID, p.OwnerID, p.Name, p.TargetDate, p.RiskLevel, p.Summary, p.RollbackPlan, p.SupportPeriodDays, p.CreatedBy,
	).Scan(&r.ID, &r.ProjectID, &r.OwnerID, &r.Name, &r.TargetDate, &r.RiskLevel, &r.Summary, &r.RollbackPlan, &r.SupportPeriodDays, &r.Stage, &r.SupportEndsAt, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (s *Store) GetByID(ctx context.Context, q dbx.Querier, id uuid.UUID) (Release, error) {
	var r Release
	err := q.QueryRow(ctx, `
		SELECT id, project_id, owner_id, name, target_date, risk_level, summary, rollback_plan, support_period_days, stage, support_ends_at, created_by, created_at, updated_at
		FROM releases WHERE id = $1`, id,
	).Scan(&r.ID, &r.ProjectID, &r.OwnerID, &r.Name, &r.TargetDate, &r.RiskLevel, &r.Summary, &r.RollbackPlan, &r.SupportPeriodDays, &r.Stage, &r.SupportEndsAt, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (s *Store) ListByProject(ctx context.Context, q dbx.Querier, projectID uuid.UUID, stage *Stage) ([]Release, error) {
	var rows pgx.Rows
	var err error
	if stage != nil {
		rows, err = q.Query(ctx, `
			SELECT id, project_id, owner_id, name, target_date, risk_level, summary, rollback_plan, support_period_days, stage, support_ends_at, created_by, created_at, updated_at
			FROM releases WHERE project_id = $1 AND stage = $2 ORDER BY created_at DESC`, projectID, *stage)
	} else {
		rows, err = q.Query(ctx, `
			SELECT id, project_id, owner_id, name, target_date, risk_level, summary, rollback_plan, support_period_days, stage, support_ends_at, created_by, created_at, updated_at
			FROM releases WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
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
		var r Release
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.OwnerID, &r.Name, &r.TargetDate, &r.RiskLevel, &r.Summary, &r.RollbackPlan, &r.SupportPeriodDays, &r.Stage, &r.SupportEndsAt, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
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
	Summary           string
	RollbackPlan      string
	SupportPeriodDays int
}

func (s *Store) UpdateMeta(ctx context.Context, q dbx.Querier, id uuid.UUID, p UpdateParams) (Release, error) {
	var r Release
	err := q.QueryRow(ctx, `
		UPDATE releases
		SET name = $2, target_date = $3, risk_level = $4, summary = $5, rollback_plan = $6, support_period_days = $7, updated_at = now()
		WHERE id = $1
		RETURNING id, project_id, owner_id, name, target_date, risk_level, summary, rollback_plan, support_period_days, stage, support_ends_at, created_by, created_at, updated_at`,
		id, p.Name, p.TargetDate, p.RiskLevel, p.Summary, p.RollbackPlan, p.SupportPeriodDays,
	).Scan(&r.ID, &r.ProjectID, &r.OwnerID, &r.Name, &r.TargetDate, &r.RiskLevel, &r.Summary, &r.RollbackPlan, &r.SupportPeriodDays, &r.Stage, &r.SupportEndsAt, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (s *Store) UpdateStage(ctx context.Context, q dbx.Querier, id uuid.UUID, stage Stage, supportEndsAt *time.Time) (Release, error) {
	var r Release
	err := q.QueryRow(ctx, `
		UPDATE releases SET stage = $2, support_ends_at = $3, updated_at = now()
		WHERE id = $1
		RETURNING id, project_id, owner_id, name, target_date, risk_level, summary, rollback_plan, support_period_days, stage, support_ends_at, created_by, created_at, updated_at`,
		id, stage, supportEndsAt,
	).Scan(&r.ID, &r.ProjectID, &r.OwnerID, &r.Name, &r.TargetDate, &r.RiskLevel, &r.Summary, &r.RollbackPlan, &r.SupportPeriodDays, &r.Stage, &r.SupportEndsAt, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	return r, err
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
