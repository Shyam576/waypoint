package authn

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/dbx"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/users"
)

type Service struct {
	store      *Store
	users      *users.Store
	pool       dbx.Querier
	sessionTTL time.Duration
}

func NewService(store *Store, usersStore *users.Store, pool dbx.Querier, sessionTTL time.Duration) *Service {
	return &Service{store: store, users: usersStore, pool: pool, sessionTTL: sessionTTL}
}

// Login verifies credentials and issues a new opaque session token. The
// caller is responsible for setting it as an HTTP-only cookie.
func (s *Service) Login(ctx context.Context, email, password, userAgent string) (users.User, string, error) {
	u, err := s.users.GetByEmail(ctx, s.pool, email)
	if err != nil {
		return users.User{}, "", httpx.Unauthorized("invalid email or password")
	}
	if !u.IsActive {
		return users.User{}, "", httpx.Unauthorized("account is disabled")
	}
	if !CheckPassword(u.PasswordHash, password) {
		return users.User{}, "", httpx.Unauthorized("invalid email or password")
	}

	token, err := generateToken()
	if err != nil {
		return users.User{}, "", err
	}
	expiresAt := time.Now().Add(s.sessionTTL)
	if err := s.store.CreateSession(ctx, u.ID, hashToken(token), userAgent, expiresAt); err != nil {
		return users.User{}, "", err
	}
	return u, token, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSessionByTokenHash(ctx, hashToken(token))
}

// ResolveSession turns a raw session cookie value into an Identity, or a
// 401 APIError if it is missing, expired, or belongs to a disabled user.
func (s *Service) ResolveSession(ctx context.Context, token string) (Identity, error) {
	row, err := s.store.GetSessionByTokenHash(ctx, hashToken(token))
	if err != nil {
		return Identity{}, httpx.Unauthorized("session is invalid or expired")
	}
	if time.Now().After(row.ExpiresAt) {
		return Identity{}, httpx.Unauthorized("session is invalid or expired")
	}
	u, err := s.users.GetByID(ctx, s.pool, row.UserID)
	if err != nil || !u.IsActive {
		return Identity{}, httpx.Unauthorized("session is invalid or expired")
	}
	return Identity{User: u, Via: ViaWeb}, nil
}

// IssueMCPToken creates a new per-user MCP token and returns its raw
// value. The raw value is never retrievable again after this call.
func (s *Service) IssueMCPToken(ctx context.Context, userID uuid.UUID, label string) (MCPToken, string, error) {
	token, err := generateToken()
	if err != nil {
		return MCPToken{}, "", err
	}
	t, err := s.store.CreateMCPToken(ctx, userID, label, hashToken(token))
	return t, token, err
}

func (s *Service) ResolveMCPToken(ctx context.Context, token string) (Identity, error) {
	lookup, id, err := s.store.GetMCPTokenByHash(ctx, hashToken(token))
	if err != nil {
		return Identity{}, httpx.Unauthorized("mcp token is invalid")
	}
	if lookup.RevokedAt != nil {
		return Identity{}, httpx.Unauthorized("mcp token has been revoked")
	}
	u, err := s.users.GetByID(ctx, s.pool, lookup.UserID)
	if err != nil || !u.IsActive {
		return Identity{}, httpx.Unauthorized("mcp token is invalid")
	}
	_ = s.store.TouchMCPToken(ctx, id)
	return Identity{User: u, Via: ViaMCP, MCPTokenID: &id}, nil
}

// IssueCIToken creates a new project-scoped CI token and returns its raw
// value. The raw value is never retrievable again after this call.
func (s *Service) IssueCIToken(ctx context.Context, projectID, createdBy uuid.UUID, label string) (CIToken, string, error) {
	token, err := generateToken()
	if err != nil {
		return CIToken{}, "", err
	}
	t, err := s.store.CreateCIToken(ctx, projectID, createdBy, label, hashToken(token))
	return t, token, err
}

func (s *Service) ListMCPTokens(ctx context.Context, userID uuid.UUID) ([]MCPToken, error) {
	return s.store.ListMCPTokens(ctx, userID)
}

func (s *Service) RevokeMCPToken(ctx context.Context, id, userID uuid.UUID) error {
	return s.store.RevokeMCPToken(ctx, id, userID)
}

func (s *Service) ListCITokens(ctx context.Context, projectID uuid.UUID) ([]CIToken, error) {
	return s.store.ListCITokens(ctx, projectID)
}

func (s *Service) RevokeCIToken(ctx context.Context, id, projectID uuid.UUID) error {
	return s.store.RevokeCIToken(ctx, id, projectID)
}

func (s *Service) ResolveCIToken(ctx context.Context, token string) (Identity, error) {
	lookup, id, err := s.store.GetCITokenByHash(ctx, hashToken(token))
	if err != nil {
		return Identity{}, httpx.Unauthorized("ci token is invalid")
	}
	if lookup.RevokedAt != nil {
		return Identity{}, httpx.Unauthorized("ci token has been revoked")
	}
	return Identity{Via: ViaCI, CITokenID: &id, CIProjectID: &lookup.ProjectID}, nil
}
