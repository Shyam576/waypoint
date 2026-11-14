// Package users holds the account model and permission roles shared by
// every other package. Roles are global per user for this MVP (not
// per-project); project-level visibility is layered on top in
// internal/projects via project membership.
package users

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/dbx"
)

type Role string

const (
	RoleAdmin              Role = "admin"
	RoleDeveloper          Role = "developer"
	RoleApprover           Role = "approver"
	RoleReleaseCoordinator Role = "release_coordinator"
	RoleSupport            Role = "support"
)

func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleDeveloper, RoleApprover, RoleReleaseCoordinator, RoleSupport:
		return true
	}
	return false
}

type User struct {
	ID           uuid.UUID `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Store is stateless: every method takes the dbx.Querier to run against,
// so callers can pass either the pool or an open transaction.
type Store struct{}

func NewStore() *Store { return &Store{} }

func (s *Store) Create(ctx context.Context, q dbx.Querier, email, name, passwordHash string, role Role) (User, error) {
	var u User
	err := q.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id, email, name, password_hash, role, is_active, created_at, updated_at`,
		email, name, passwordHash, role,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func (s *Store) GetByEmail(ctx context.Context, q dbx.Querier, email string) (User, error) {
	var u User
	err := q.QueryRow(ctx, `
		SELECT id, email, name, password_hash, role, is_active, created_at, updated_at
		FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func (s *Store) GetByID(ctx context.Context, q dbx.Querier, id uuid.UUID) (User, error) {
	var u User
	err := q.QueryRow(ctx, `
		SELECT id, email, name, password_hash, role, is_active, created_at, updated_at
		FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func (s *Store) List(ctx context.Context, q dbx.Querier) ([]User, error) {
	rows, err := q.Query(ctx, `
		SELECT id, email, name, password_hash, role, is_active, created_at, updated_at
		FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) Count(ctx context.Context, q dbx.Querier) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) UpdatePassword(ctx context.Context, q dbx.Querier, id uuid.UUID, passwordHash string) error {
	_, err := q.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, passwordHash)
	return err
}

// SetActive flips a user's is_active flag. Session and MCP/CI token
// resolution already check IsActive on every request, so deactivating a
// user takes effect immediately — no separate session-revocation step
// is needed the way it is for a password change. See
// docs/architecture-audit.md Finding 10.
func (s *Store) SetActive(ctx context.Context, q dbx.Querier, id uuid.UUID, active bool) (User, error) {
	var u User
	err := q.QueryRow(ctx, `
		UPDATE users SET is_active = $2, updated_at = now() WHERE id = $1
		RETURNING id, email, name, password_hash, role, is_active, created_at, updated_at`,
		id, active,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}
