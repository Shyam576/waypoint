package authn

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/dbx"
)

type Store struct {
	pool dbx.Querier
}

func NewStore(pool dbx.Querier) *Store {
	return &Store{pool: pool}
}

func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, tokenHash, userAgent string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, user_agent, expires_at)
		VALUES ($1, $2, $3, $4)`, userID, tokenHash, userAgent, expiresAt)
	return err
}

type sessionRow struct {
	UserID    uuid.UUID
	ExpiresAt time.Time
}

func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash string) (sessionRow, error) {
	var row sessionRow
	err := s.pool.QueryRow(ctx, `
		SELECT user_id, expires_at FROM sessions WHERE token_hash = $1`, tokenHash,
	).Scan(&row.UserID, &row.ExpiresAt)
	return row, err
}

func (s *Store) DeleteSessionByTokenHash(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

type MCPToken struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

func (s *Store) CreateMCPToken(ctx context.Context, userID uuid.UUID, label, tokenHash string) (MCPToken, error) {
	var t MCPToken
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mcp_tokens (user_id, label, token_hash)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, label, created_at, last_used_at, revoked_at`,
		userID, label, tokenHash,
	).Scan(&t.ID, &t.UserID, &t.Label, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt)
	return t, err
}

func (s *Store) ListMCPTokens(ctx context.Context, userID uuid.UUID) ([]MCPToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, label, created_at, last_used_at, revoked_at
		FROM mcp_tokens WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MCPToken{}
	for rows.Next() {
		var t MCPToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.Label, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) RevokeMCPToken(ctx context.Context, id, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE mcp_tokens SET revoked_at = now() WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

type mcpTokenLookup struct {
	UserID    uuid.UUID
	RevokedAt *time.Time
}

func (s *Store) GetMCPTokenByHash(ctx context.Context, tokenHash string) (mcpTokenLookup, uuid.UUID, error) {
	var row mcpTokenLookup
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, revoked_at FROM mcp_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&id, &row.UserID, &row.RevokedAt)
	return row, id, err
}

func (s *Store) TouchMCPToken(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE mcp_tokens SET last_used_at = now() WHERE id = $1`, id)
	return err
}

type CIToken struct {
	ID        uuid.UUID  `json:"id"`
	ProjectID uuid.UUID  `json:"project_id"`
	Label     string     `json:"label"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

func (s *Store) CreateCIToken(ctx context.Context, projectID, createdBy uuid.UUID, label, tokenHash string) (CIToken, error) {
	var t CIToken
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ci_tokens (project_id, label, token_hash, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, project_id, label, created_at, revoked_at`,
		projectID, label, tokenHash, createdBy,
	).Scan(&t.ID, &t.ProjectID, &t.Label, &t.CreatedAt, &t.RevokedAt)
	return t, err
}

func (s *Store) ListCITokens(ctx context.Context, projectID uuid.UUID) ([]CIToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, label, created_at, revoked_at
		FROM ci_tokens WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CIToken{}
	for rows.Next() {
		var t CIToken
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Label, &t.CreatedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) RevokeCIToken(ctx context.Context, id, projectID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE ci_tokens SET revoked_at = now() WHERE id = $1 AND project_id = $2`, id, projectID)
	return err
}

type ciTokenLookup struct {
	ProjectID uuid.UUID
	RevokedAt *time.Time
}

func (s *Store) GetCITokenByHash(ctx context.Context, tokenHash string) (ciTokenLookup, uuid.UUID, error) {
	var row ciTokenLookup
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, revoked_at FROM ci_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&id, &row.ProjectID, &row.RevokedAt)
	return row, id, err
}
