package authn

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/dbx"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/users"
)

type Handlers struct {
	Svc                 *Service
	Users               *users.Store
	Audit               *audit.Store
	Pool                dbx.Querier
	CookieName          string
	CookieSecure        bool
	SessionTTL          time.Duration
	AdminBootstrapToken string
}

func (h *Handlers) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(h.SessionTTL),
	})
}

func (h *Handlers) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.Email == "" || req.Password == "" {
		httpx.Error(w, r, httpx.BadRequest("email and password are required"))
		return
	}

	u, token, err := h.Svc.Login(r.Context(), req.Email, req.Password, r.UserAgent())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	h.setSessionCookie(w, token)
	httpx.JSON(w, http.StatusOK, u)
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(h.CookieName); err == nil {
		_ = h.Svc.Logout(r.Context(), cookie.Value)
	}
	h.clearSessionCookie(w)
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	httpx.JSON(w, http.StatusOK, identity.User)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword is the self-service path (any signed-in user changing
// their own password); it requires the current password. See
// docs/architecture-audit.md Finding 10.
func (h *Handlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	var req changePasswordRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.Svc.ChangePassword(r.Context(), identity.User.ID, req.CurrentPassword, req.NewPassword); err != nil {
		httpx.Error(w, r, err)
		return
	}
	_ = h.Audit.Record(r.Context(), h.Pool, "user", identity.User.ID, "password_changed", identity.AuditActor(), nil, nil, "")
	h.clearSessionCookie(w)
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "password changed; please sign in again"})
}

type bootstrapRequest struct {
	Token    string `json:"token"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// BootstrapAdmin creates the very first admin account. It only ever
// succeeds once: it refuses if any user already exists, and it requires
// ADMIN_BOOTSTRAP_TOKEN to be set and matched, so it cannot be used
// against a database that already has users, and is a no-op if the
// operator never configured a bootstrap token.
func (h *Handlers) BootstrapAdmin(w http.ResponseWriter, r *http.Request) {
	if h.AdminBootstrapToken == "" {
		httpx.Error(w, r, httpx.Forbidden("admin bootstrap is not enabled on this server"))
		return
	}

	var req bootstrapRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	// Constant-time comparison: a plain != leaks timing information
	// proportional to how many leading bytes match, a classical (if
	// impractical over typical network jitter) side channel for a
	// secret token. See docs/architecture-audit.md Finding 13.
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(h.AdminBootstrapToken)) != 1 {
		httpx.Error(w, r, httpx.Forbidden("invalid bootstrap token"))
		return
	}
	if req.Email == "" || req.Name == "" || len(req.Password) < 8 {
		httpx.Error(w, r, httpx.BadRequest("email, name, and a password of at least 8 characters are required"))
		return
	}

	count, err := h.Users.Count(r.Context(), h.Pool)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if count > 0 {
		httpx.Error(w, r, httpx.Conflict("an admin already exists; bootstrap is only available on an empty database"))
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	u, err := h.Users.Create(r.Context(), h.Pool, req.Email, req.Name, hash, users.RoleAdmin)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	_ = h.Audit.Record(r.Context(), h.Pool, "user", u.ID, "bootstrap_admin_created", audit.Actor{Label: "bootstrap"}, nil, u, "first admin account created via local bootstrap")
	httpx.JSON(w, http.StatusCreated, u)
}
