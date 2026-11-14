package projects

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Shyam576/waypoint/internal/dbx"
)

type Store struct{}

func NewStore() *Store { return &Store{} }

func (s *Store) Create(ctx context.Context, q dbx.Querier, p Project) (Project, error) {
	var out Project
	err := q.QueryRow(ctx, `
		INSERT INTO projects (name, slug, owning_team, repo_url, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, name, slug, owning_team, repo_url, status, created_by, created_at, updated_at`,
		p.Name, p.Slug, p.OwningTeam, p.RepoURL, p.CreatedBy,
	).Scan(&out.ID, &out.Name, &out.Slug, &out.OwningTeam, &out.RepoURL, &out.Status, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt)
	return out, err
}

func (s *Store) GetByID(ctx context.Context, q dbx.Querier, id uuid.UUID) (Project, error) {
	var out Project
	err := q.QueryRow(ctx, `
		SELECT id, name, slug, owning_team, repo_url, status, created_by, created_at, updated_at
		FROM projects WHERE id = $1`, id,
	).Scan(&out.ID, &out.Name, &out.Slug, &out.OwningTeam, &out.RepoURL, &out.Status, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt)
	return out, err
}

func (s *Store) ListAll(ctx context.Context, q dbx.Querier) ([]Project, error) {
	rows, err := q.Query(ctx, `
		SELECT id, name, slug, owning_team, repo_url, status, created_by, created_at, updated_at
		FROM projects ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProjects(rows)
}

func (s *Store) ListForUser(ctx context.Context, q dbx.Querier, userID uuid.UUID) ([]Project, error) {
	rows, err := q.Query(ctx, `
		SELECT p.id, p.name, p.slug, p.owning_team, p.repo_url, p.status, p.created_by, p.created_at, p.updated_at
		FROM projects p
		JOIN project_members m ON m.project_id = p.id
		WHERE m.user_id = $1
		ORDER BY p.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProjects(rows)
}

func scanProjects(rows pgx.Rows) ([]Project, error) {
	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.OwningTeam, &p.RepoURL, &p.Status, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Update(ctx context.Context, q dbx.Querier, id uuid.UUID, name, owningTeam string, repoURL *string, status Status) (Project, error) {
	var out Project
	err := q.QueryRow(ctx, `
		UPDATE projects SET name = $2, owning_team = $3, repo_url = $4, status = $5, updated_at = now()
		WHERE id = $1
		RETURNING id, name, slug, owning_team, repo_url, status, created_by, created_at, updated_at`,
		id, name, owningTeam, repoURL, status,
	).Scan(&out.ID, &out.Name, &out.Slug, &out.OwningTeam, &out.RepoURL, &out.Status, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt)
	return out, err
}

const policyColumns = `id, project_id, required_evidence_types, required_approvals_count, block_on_open_rejection, require_candidate_for_approval, require_checklist_completion, require_approver_not_submitter, updated_by, updated_at`

func scanPolicy(row pgx.Row) (Policy, error) {
	var p Policy
	err := row.Scan(&p.ID, &p.ProjectID, &p.RequiredEvidenceTypes, &p.RequiredApprovalsCount, &p.BlockOnOpenRejection,
		&p.RequireCandidateForApproval, &p.RequireChecklistCompletion, &p.RequireApproverNotSubmitter, &p.UpdatedBy, &p.UpdatedAt)
	return p, err
}

func (s *Store) CreatePolicy(ctx context.Context, q dbx.Querier, projectID uuid.UUID) (Policy, error) {
	row := q.QueryRow(ctx, `INSERT INTO project_policies (project_id) VALUES ($1) RETURNING `+policyColumns, projectID)
	return scanPolicy(row)
}

func (s *Store) GetPolicy(ctx context.Context, q dbx.Querier, projectID uuid.UUID) (Policy, error) {
	row := q.QueryRow(ctx, `SELECT `+policyColumns+` FROM project_policies WHERE project_id = $1`, projectID)
	return scanPolicy(row)
}

type UpdatePolicyParams struct {
	RequiredEvidenceTypes       []string
	RequiredApprovalsCount      int
	BlockOnOpenRejection        bool
	RequireCandidateForApproval bool
	RequireChecklistCompletion  bool
	RequireApproverNotSubmitter bool
	UpdatedBy                   uuid.UUID
}

func (s *Store) UpdatePolicy(ctx context.Context, q dbx.Querier, projectID uuid.UUID, p UpdatePolicyParams) (Policy, error) {
	row := q.QueryRow(ctx, `
		UPDATE project_policies
		SET required_evidence_types = $2, required_approvals_count = $3, block_on_open_rejection = $4,
		    require_candidate_for_approval = $5, require_checklist_completion = $6, require_approver_not_submitter = $7,
		    updated_by = $8, updated_at = now()
		WHERE project_id = $1
		RETURNING `+policyColumns,
		projectID, p.RequiredEvidenceTypes, p.RequiredApprovalsCount, p.BlockOnOpenRejection,
		p.RequireCandidateForApproval, p.RequireChecklistCompletion, p.RequireApproverNotSubmitter, p.UpdatedBy,
	)
	return scanPolicy(row)
}

const riskPolicyColumns = `id, project_id, risk_level, extra_required_approvals_count, extra_required_evidence_types, require_risk_acceptance, updated_by, updated_at`

func scanRiskPolicy(row pgx.Row) (RiskPolicy, error) {
	var p RiskPolicy
	err := row.Scan(&p.ID, &p.ProjectID, &p.RiskLevel, &p.ExtraRequiredApprovalsCount, &p.ExtraRequiredEvidenceTypes, &p.RequireRiskAcceptance, &p.UpdatedBy, &p.UpdatedAt)
	return p, err
}

func (s *Store) UpsertRiskPolicy(ctx context.Context, q dbx.Querier, projectID uuid.UUID, riskLevel string, extraApprovals int, extraEvidenceTypes []string, requireRiskAcceptance bool, updatedBy *uuid.UUID) (RiskPolicy, error) {
	if extraEvidenceTypes == nil {
		extraEvidenceTypes = []string{}
	}
	row := q.QueryRow(ctx, `
		INSERT INTO project_risk_policies (project_id, risk_level, extra_required_approvals_count, extra_required_evidence_types, require_risk_acceptance, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (project_id, risk_level) DO UPDATE SET
			extra_required_approvals_count = EXCLUDED.extra_required_approvals_count,
			extra_required_evidence_types = EXCLUDED.extra_required_evidence_types,
			require_risk_acceptance = EXCLUDED.require_risk_acceptance,
			updated_by = EXCLUDED.updated_by,
			updated_at = now()
		RETURNING `+riskPolicyColumns,
		projectID, riskLevel, extraApprovals, extraEvidenceTypes, requireRiskAcceptance, updatedBy,
	)
	return scanRiskPolicy(row)
}

// GetRiskPolicy returns (nil, nil) if the project has no extra
// requirements defined for this risk tier.
func (s *Store) GetRiskPolicy(ctx context.Context, q dbx.Querier, projectID uuid.UUID, riskLevel string) (*RiskPolicy, error) {
	row := q.QueryRow(ctx, `SELECT `+riskPolicyColumns+` FROM project_risk_policies WHERE project_id = $1 AND risk_level = $2`, projectID, riskLevel)
	p, err := scanRiskPolicy(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (s *Store) ListRiskPolicies(ctx context.Context, q dbx.Querier, projectID uuid.UUID) ([]RiskPolicy, error) {
	rows, err := q.Query(ctx, `SELECT `+riskPolicyColumns+` FROM project_risk_policies WHERE project_id = $1 ORDER BY risk_level`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RiskPolicy{}
	for rows.Next() {
		p, err := scanRiskPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) AddMember(ctx context.Context, q dbx.Querier, projectID, userID uuid.UUID) error {
	_, err := q.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id) VALUES ($1, $2)
		ON CONFLICT (project_id, user_id) DO NOTHING`, projectID, userID)
	return err
}

func (s *Store) IsMember(ctx context.Context, q dbx.Querier, projectID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM project_members WHERE project_id = $1 AND user_id = $2)`,
		projectID, userID,
	).Scan(&exists)
	return exists, err
}

func (s *Store) ListMemberIDs(ctx context.Context, q dbx.Querier, projectID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT user_id FROM project_members WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
