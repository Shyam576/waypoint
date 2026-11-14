package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/ratelimit"
	"github.com/Shyam576/waypoint/internal/users"
)

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(corsMiddleware(s.Cfg.CORSOrigin))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Pool.Ping(r.Context()); err != nil {
			httpx.Error(w, r, httpx.NewAPIError(http.StatusServiceUnavailable, "not_ready", "database is not reachable"))
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	requireSession := authn.RequireSession(s.Auth, s.Cfg.SessionCookieName)
	requireMCP := authn.RequireMCPToken(s.Auth)
	requireCI := authn.RequireCIToken(s.Auth)
	requireAdmin := authn.RequireRole(users.RoleAdmin)
	// Rate limiting on the two endpoints that accept credentials before
	// any authentication exists to key on. See
	// docs/architecture-audit.md Finding 8.
	authRateLimit := ratelimit.PerIP(s.Cfg.AuthRateLimitPerMin, time.Minute)

	r.Route("/api/v1", func(api chi.Router) {
		api.With(authRateLimit).Post("/auth/login", s.AuthHandlers.Login)
		api.With(authRateLimit).Post("/bootstrap/admin", s.AuthHandlers.BootstrapAdmin)

		api.Group(func(web chi.Router) {
			web.Use(requireSession)

			web.Post("/auth/logout", s.AuthHandlers.Logout)
			web.Get("/auth/me", s.AuthHandlers.Me)
			web.Post("/auth/change-password", s.AuthHandlers.ChangePassword)

			web.Get("/dashboard", s.dashboard)

			web.Group(func(admin chi.Router) {
				admin.Use(requireAdmin)
				admin.Get("/users", s.listUsers)
				admin.Post("/users", s.createUser)
				admin.Post("/users/{userID}/password", s.adminSetPassword)
				admin.Patch("/users/{userID}/status", s.setUserActive)
			})

			web.Post("/users/{userID}/mcp-tokens", s.issueMCPToken)
			web.Get("/users/{userID}/mcp-tokens", s.listMCPTokens)
			web.Delete("/mcp-tokens/{tokenID}", s.revokeMCPToken)

			web.Post("/projects", s.ProjectsHandlers.Create)
			web.Get("/projects", s.ProjectsHandlers.List)
			web.Get("/projects/{projectID}", s.ProjectsHandlers.Get)
			web.Patch("/projects/{projectID}", s.ProjectsHandlers.Update)
			web.Get("/projects/{projectID}/policy", s.ProjectsHandlers.GetPolicy)
			web.Patch("/projects/{projectID}/policy", s.ProjectsHandlers.UpdatePolicy)
			web.Get("/projects/{projectID}/risk-policies", s.ProjectsHandlers.ListRiskPolicies)
			web.Patch("/projects/{projectID}/risk-policies", s.ProjectsHandlers.UpdateRiskPolicy)
			web.Get("/projects/{projectID}/emergency-policy", s.ProjectsHandlers.GetEmergencyPolicy)
			web.Patch("/projects/{projectID}/emergency-policy", s.ProjectsHandlers.UpdateEmergencyPolicy)
			web.Post("/projects/{projectID}/members", s.ProjectsHandlers.AddMember)
			web.Get("/projects/{projectID}/members", s.ProjectsHandlers.ListMembers)
			web.Post("/projects/{projectID}/ci-tokens", s.issueCIToken)
			web.Get("/projects/{projectID}/ci-tokens", s.listCITokens)
			web.Delete("/projects/{projectID}/ci-tokens/{tokenID}", s.revokeCIToken)

			web.Post("/projects/{projectID}/releases", s.ReleasesHandlers.Create)
			web.Get("/projects/{projectID}/releases", s.ReleasesHandlers.ListByProject)

			web.Get("/releases/{releaseID}", s.ReleasesHandlers.Get)
			web.Patch("/releases/{releaseID}", s.ReleasesHandlers.Update)
			web.Post("/releases/{releaseID}/transition", s.ReleasesHandlers.Transition)
			web.Post("/releases/{releaseID}/close", s.ReleasesHandlers.Close)
			web.Post("/releases/{releaseID}/risk-acceptance", s.ReleasesHandlers.AcceptRisk)
			web.Get("/releases/{releaseID}/risk-acceptance", s.ReleasesHandlers.GetRiskAcceptance)
			web.Post("/releases/{releaseID}/emergency", s.ReleasesHandlers.DeclareEmergency)
			web.Post("/releases/{releaseID}/emergency-retrospective", s.ReleasesHandlers.CompleteRetrospective)
			web.Get("/releases/{releaseID}/full", s.releaseDetail)
			web.Get("/releases/{releaseID}/activity", s.releaseActivity)

			web.Post("/releases/{releaseID}/links", s.ReleasesHandlers.CreateLink)
			web.Get("/releases/{releaseID}/links", s.ReleasesHandlers.ListLinks)

			web.Post("/releases/{releaseID}/evidence", s.EvidenceHandlers.Submit)
			web.Get("/releases/{releaseID}/evidence", s.EvidenceHandlers.ListByRelease)

			web.Post("/releases/{releaseID}/candidates", s.CandidatesHandlers.RegisterFromWeb)
			web.Get("/releases/{releaseID}/candidates", s.CandidatesHandlers.ListByRelease)

			web.Post("/projects/{projectID}/checklist-templates", s.ChecklistsHandlers.CreateTemplateVersion)
			web.Get("/projects/{projectID}/checklist-templates/active", s.ChecklistsHandlers.GetActiveTemplate)
			web.Get("/projects/{projectID}/checklist-templates", s.ChecklistsHandlers.ListTemplateVersions)

			web.Get("/releases/{releaseID}/checklist-items", s.ChecklistsHandlers.ListByRelease)
			web.Post("/checklist-items/{itemID}/owner", s.ChecklistsHandlers.AssignOwner)
			web.Post("/checklist-items/{itemID}/verify", s.ChecklistsHandlers.Verify)
			web.Post("/checklist-items/{itemID}/reject", s.ChecklistsHandlers.Reject)
			web.Post("/checklist-items/{itemID}/waive", s.ChecklistsHandlers.Waive)

			web.Post("/releases/{releaseID}/approvals", s.ApprovalsHandlers.Decide)
			web.Get("/releases/{releaseID}/approvals", s.ApprovalsHandlers.ListByRelease)

			web.Get("/releases/{releaseID}/eligibility", s.GateHandlers.Eligibility)

			web.Post("/releases/{releaseID}/deployments", s.DeploymentsHandlers.RecordFromWeb)
			web.Get("/releases/{releaseID}/deployments", s.DeploymentsHandlers.ListByRelease)
			web.Post("/releases/{releaseID}/rollbacks", s.DeploymentsHandlers.InitiateRollback)
			web.Get("/releases/{releaseID}/rollbacks", s.DeploymentsHandlers.ListRollbacksByRelease)
			web.Post("/releases/{releaseID}/rollbacks/{rollbackID}/verify", s.DeploymentsHandlers.VerifyRollback)

			web.Post("/releases/{releaseID}/support-items", s.SupportHandlers.Create)
			web.Get("/releases/{releaseID}/support-items", s.SupportHandlers.ListByRelease)
			web.Patch("/support-items/{supportItemID}", s.SupportHandlers.Update)

			web.Post("/releases/{releaseID}/change-notes", s.MCPNotesHandlers.CreateChangeNoteWeb)
			web.Get("/releases/{releaseID}/change-notes", s.MCPNotesHandlers.ListChangeNotes)
			web.Post("/releases/{releaseID}/design-decisions", s.MCPNotesHandlers.CreateDesignDecisionWeb)
			web.Get("/releases/{releaseID}/design-decisions", s.MCPNotesHandlers.ListDesignDecisions)

			web.Get("/releases/{releaseID}/mcp-submissions", s.MCPNotesHandlers.ListPending)
			web.Post("/mcp-submissions/{submissionID}/accept", s.MCPNotesHandlers.Accept)
			web.Post("/mcp-submissions/{submissionID}/reject", s.MCPNotesHandlers.Reject)
		})

		api.Group(func(mcp chi.Router) {
			mcp.Use(requireMCP)
			mcp.Get("/mcp/whoami", s.mcpWhoami)
			mcp.Get("/mcp/releases/{releaseID}/context", s.mcpReleaseContext)
			mcp.Post("/mcp/releases/{releaseID}/change-notes", s.MCPNotesHandlers.SubmitChangeNoteMCP)
			mcp.Post("/mcp/releases/{releaseID}/design-decisions", s.MCPNotesHandlers.SubmitDesignDecisionMCP)
			mcp.Post("/mcp/releases/{releaseID}/support-notes", s.MCPNotesHandlers.SubmitSupportNoteMCP)
		})

		api.Group(func(ci chi.Router) {
			ci.Use(requireCI)
			ci.Get("/ci/releases/{releaseID}/eligibility", s.DeploymentsHandlers.EligibilityForCI)
			ci.Post("/ci/releases/{releaseID}/candidates", s.CandidatesHandlers.RegisterFromCI)
			ci.Post("/ci/releases/{releaseID}/deployment-authorizations", s.DeploymentsHandlers.IssueAuthorization)
			ci.Post("/ci/releases/{releaseID}/deployments", s.DeploymentsHandlers.RecordFromCI)
		})
	})

	return r
}

func corsMiddleware(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
