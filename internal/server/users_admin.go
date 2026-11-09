package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/users"
)

// These handlers cover user and token administration: creating accounts
// (admin only), and issuing the two scoped service tokens described in
// the brief (a per-user MCP token, a per-project CI token). They live in
// server rather than internal/users because they need authn's token
// issuance alongside the user store.

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Users.List(r.Context(), s.Pool)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

type createUserRequest struct {
	Email    string     `json:"email"`
	Name     string     `json:"name"`
	Password string     `json:"password"`
	Role     users.Role `json:"role"`
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	identity, _ := authn.IdentityFromContext(r.Context())
	var req createUserRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.Email == "" || req.Name == "" || len(req.Password) < 8 {
		httpx.Error(w, r, httpx.BadRequest("email, name, and a password of at least 8 characters are required"))
		return
	}
	if !req.Role.Valid() {
		httpx.Error(w, r, httpx.BadRequest("role must be admin, developer, approver, release_coordinator, or support"))
		return
	}
	hash, err := authn.HashPassword(req.Password)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	u, err := s.Users.Create(r.Context(), s.Pool, req.Email, req.Name, hash, req.Role)
	if err != nil {
		httpx.Error(w, r, httpx.Conflict("a user with this email already exists"))
		return
	}
	_ = s.Audit.Record(r.Context(), s.Pool, "user", u.ID, "user_created", identity.AuditActor(), nil, u, "")
	httpx.JSON(w, http.StatusCreated, u)
}

type issueMCPTokenRequest struct {
	Label string `json:"label"`
}

// issueMCPToken lets a user mint their own MCP token (admins may mint
// for anyone). The raw token is returned exactly once in this response.
func (s *Server) issueMCPToken(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	targetUserID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid userID"))
		return
	}
	if targetUserID != identity.User.ID && identity.User.Role != users.RoleAdmin {
		httpx.Error(w, r, httpx.Forbidden("you can only issue mcp tokens for yourself"))
		return
	}
	var req issueMCPTokenRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.Label == "" {
		req.Label = "claude-code"
	}
	t, raw, err := s.Auth.IssueMCPToken(r.Context(), targetUserID, req.Label)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	_ = s.Audit.Record(r.Context(), s.Pool, "user", targetUserID, "mcp_token_issued", identity.AuditActor(), nil, t, "")
	httpx.JSON(w, http.StatusCreated, map[string]any{"token": t, "raw_token": raw})
}

type issueCITokenRequest struct {
	Label string `json:"label"`
}

func (s *Server) issueCIToken(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid projectID"))
		return
	}
	canManage, err := s.Projects.CanManage(r.Context(), identity, projectID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if !canManage {
		httpx.Error(w, r, httpx.Forbidden("only admins and release coordinators on this project can issue ci tokens"))
		return
	}
	var req issueCITokenRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.Label == "" {
		req.Label = "github-actions"
	}
	t, raw, err := s.Auth.IssueCIToken(r.Context(), projectID, identity.User.ID, req.Label)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	_ = s.Audit.Record(r.Context(), s.Pool, "project", projectID, "ci_token_issued", identity.AuditActor(), nil, t, req.Label)
	httpx.JSON(w, http.StatusCreated, map[string]any{"token": t, "raw_token": raw})
}

func (s *Server) listMCPTokens(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	targetUserID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid userID"))
		return
	}
	if targetUserID != identity.User.ID && identity.User.Role != users.RoleAdmin {
		httpx.Error(w, r, httpx.Forbidden("you can only view your own mcp tokens"))
		return
	}
	list, err := s.Auth.ListMCPTokens(r.Context(), targetUserID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

func (s *Server) revokeMCPToken(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "tokenID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid tokenID"))
		return
	}
	if err := s.Auth.RevokeMCPToken(r.Context(), id, identity.User.ID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) listCITokens(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid projectID"))
		return
	}
	canManage, err := s.Projects.CanManage(r.Context(), identity, projectID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if !canManage {
		httpx.Error(w, r, httpx.Forbidden("only admins and release coordinators on this project can view ci tokens"))
		return
	}
	list, err := s.Auth.ListCITokens(r.Context(), projectID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

func (s *Server) revokeCIToken(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid projectID"))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "tokenID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid tokenID"))
		return
	}
	canManage, err := s.Projects.CanManage(r.Context(), identity, projectID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if !canManage {
		httpx.Error(w, r, httpx.Forbidden("only admins and release coordinators on this project can revoke ci tokens"))
		return
	}
	if err := s.Auth.RevokeCIToken(r.Context(), id, projectID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) releaseActivity(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "releaseID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid releaseID"))
		return
	}
	if _, err := s.Releases.Get(r.Context(), identity, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	entries, err := s.Audit.ForEntity(r.Context(), s.Pool, "release", id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, nonNilActivity(entries))
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	d, err := s.Dashboard.Build(r.Context(), identity)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}

func (s *Server) mcpWhoami(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("mcp token required"))
		return
	}
	httpx.JSON(w, http.StatusOK, identity.User)
}
