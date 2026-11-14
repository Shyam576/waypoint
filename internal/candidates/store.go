package candidates

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Shyam576/waypoint/internal/dbx"
)

type Store struct{}

func NewStore() *Store { return &Store{} }

type CreateParams struct {
	ReleaseID          uuid.UUID
	GitRepo            string
	GitBranch          string
	CommitSHA          string
	BuildID            string
	ImageDigest        string
	AppVersion         string
	ConfigChecksum     string
	MigrationChecksum  string
	BuildProvenanceURL string
	CreatedVia         CreatedVia
	CreatedBy          *uuid.UUID
	CreatedByCITokenID *uuid.UUID
}

const candidateColumns = `id, release_id, git_repo, git_branch, commit_sha, build_id, image_digest, app_version, config_checksum, migration_checksum, build_provenance_url, created_via, created_by, created_by_ci_token_id, created_at`

func scanCandidate(row pgx.Row) (Candidate, error) {
	var c Candidate
	err := row.Scan(&c.ID, &c.ReleaseID, &c.GitRepo, &c.GitBranch, &c.CommitSHA, &c.BuildID, &c.ImageDigest, &c.AppVersion, &c.ConfigChecksum, &c.MigrationChecksum, &c.BuildProvenanceURL, &c.CreatedVia, &c.CreatedBy, &c.CreatedByCITokenID, &c.CreatedAt)
	return c, err
}

func (s *Store) Create(ctx context.Context, q dbx.Querier, p CreateParams) (Candidate, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO release_candidates (release_id, git_repo, git_branch, commit_sha, build_id, image_digest, app_version, config_checksum, migration_checksum, build_provenance_url, created_via, created_by, created_by_ci_token_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING `+candidateColumns,
		p.ReleaseID, p.GitRepo, p.GitBranch, p.CommitSHA, p.BuildID, p.ImageDigest, p.AppVersion, p.ConfigChecksum, p.MigrationChecksum, p.BuildProvenanceURL, p.CreatedVia, p.CreatedBy, p.CreatedByCITokenID,
	)
	return scanCandidate(row)
}

// GetCurrentByRelease returns the most recently registered candidate for
// a release, or (nil, nil) if none has been registered yet.
func (s *Store) GetCurrentByRelease(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) (*Candidate, error) {
	row := q.QueryRow(ctx, `
		SELECT `+candidateColumns+`
		FROM release_candidates WHERE release_id = $1
		ORDER BY created_at DESC LIMIT 1`, releaseID)
	c, err := scanCandidate(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (s *Store) ListByRelease(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) ([]Candidate, error) {
	rows, err := q.Query(ctx, `
		SELECT `+candidateColumns+`
		FROM release_candidates WHERE release_id = $1
		ORDER BY created_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Candidate{}
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
