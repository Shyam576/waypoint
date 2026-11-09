package authn

import (
	"net/http"

	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/users"
)

const CookieName = "waypoint_session"

// RequireSession authenticates browser requests via the session cookie
// and stores the resulting Identity on the request context.
func RequireSession(svc *Service, cookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(cookieName)
			if err != nil || cookie.Value == "" {
				httpx.Error(w, r, httpx.Unauthorized("sign in required"))
				return
			}
			identity, err := svc.ResolveSession(r.Context(), cookie.Value)
			if err != nil {
				httpx.Error(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), identity)))
		})
	}
}

// RequireMCPToken authenticates requests from the MCP server using a
// per-user bearer token.
func RequireMCPToken(svc *Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				httpx.Error(w, r, httpx.Unauthorized("mcp token required"))
				return
			}
			identity, err := svc.ResolveMCPToken(r.Context(), token)
			if err != nil {
				httpx.Error(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), identity)))
		})
	}
}

// RequireCIToken authenticates CI/CD pipeline requests using a
// project-scoped bearer token.
func RequireCIToken(svc *Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				httpx.Error(w, r, httpx.Unauthorized("ci token required"))
				return
			}
			identity, err := svc.ResolveCIToken(r.Context(), token)
			if err != nil {
				httpx.Error(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), identity)))
		})
	}
}

// RequireRole must run after RequireSession or RequireMCPToken. It checks
// authorization on the server; the frontend hiding a button is never
// sufficient on its own.
func RequireRole(roles ...users.Role) func(http.Handler) http.Handler {
	allowed := make(map[users.Role]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := IdentityFromContext(r.Context())
			if !ok {
				httpx.Error(w, r, httpx.Unauthorized("sign in required"))
				return
			}
			if !allowed[identity.User.Role] {
				httpx.Error(w, r, httpx.Forbidden("your role does not permit this action"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && h[:len(prefix)] == prefix {
		return h[len(prefix):]
	}
	return ""
}
