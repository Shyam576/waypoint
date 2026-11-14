// Package server wires every domain package's services and HTTP handlers
// together into one chi.Router, plus a small amount of its own
// read-aggregation logic (the dashboard and the release detail/MCP
// context composites) that doesn't belong to any single domain package.
package server

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/approvals"
	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/candidates"
	"github.com/Shyam576/waypoint/internal/checklists"
	"github.com/Shyam576/waypoint/internal/config"
	"github.com/Shyam576/waypoint/internal/deployments"
	"github.com/Shyam576/waypoint/internal/evidence"
	"github.com/Shyam576/waypoint/internal/gate"
	"github.com/Shyam576/waypoint/internal/mcpnotes"
	"github.com/Shyam576/waypoint/internal/projects"
	"github.com/Shyam576/waypoint/internal/releases"
	"github.com/Shyam576/waypoint/internal/support"
	"github.com/Shyam576/waypoint/internal/users"
)

type Server struct {
	Cfg  config.Config
	Pool *pgxpool.Pool

	Audit *audit.Store
	Users *users.Store

	Auth         *authn.Service
	AuthHandlers *authn.Handlers

	Projects         *projects.Service
	ProjectsHandlers *projects.Handlers

	Releases         *releases.Service
	ReleasesStore    *releases.Store
	ReleasesHandlers *releases.Handlers

	Evidence         *evidence.Service
	EvidenceStore    *evidence.Store
	EvidenceHandlers *evidence.Handlers

	Approvals         *approvals.Service
	ApprovalsStore    *approvals.Store
	ApprovalsHandlers *approvals.Handlers

	Gate         *gate.Service
	GateHandlers *gate.Handlers

	Deployments         *deployments.Service
	DeploymentsStore    *deployments.Store
	DeploymentsHandlers *deployments.Handlers

	Support         *support.Service
	SupportStore    *support.Store
	SupportHandlers *support.Handlers

	MCPNotes         *mcpnotes.Service
	MCPNotesStore    *mcpnotes.Store
	MCPNotesHandlers *mcpnotes.Handlers

	Candidates         *candidates.Service
	CandidatesStore    *candidates.Store
	CandidatesHandlers *candidates.Handlers

	Checklists         *checklists.Service
	ChecklistsStore    *checklists.Store
	ChecklistsHandlers *checklists.Handlers

	Dashboard *DashboardService
}

func New(cfg config.Config, pool *pgxpool.Pool) *Server {
	auditStore := audit.NewStore()
	usersStore := users.NewStore()

	authStore := authn.NewStore(pool)
	authSvc := authn.NewService(authStore, usersStore, pool, cfg.SessionTTL)
	authHandlers := &authn.Handlers{
		Svc: authSvc, Users: usersStore, Audit: auditStore, Pool: pool,
		CookieName: cfg.SessionCookieName, CookieSecure: cfg.CookieSecure, SessionTTL: cfg.SessionTTL,
		AdminBootstrapToken: cfg.AdminBootstrapToken,
	}

	checklistsStore := checklists.NewStore()

	projectsStore := projects.NewStore()
	projectsSvc := projects.NewService(pool, projectsStore, auditStore, usersStore, checklistsStore)
	projectsHandlers := projects.NewHandlers(projectsSvc)

	releasesStore := releases.NewStore()
	releasesSvc := releases.NewService(pool, releasesStore, auditStore, projectsSvc, checklistsStore)
	releasesHandlers := releases.NewHandlers(releasesSvc)

	checklistsSvc := checklists.NewService(pool, checklistsStore, auditStore, releasesSvc, projectsSvc)
	checklistsHandlers := checklists.NewHandlers(checklistsSvc)

	evidenceStore := evidence.NewStore()
	evidenceSvc := evidence.NewService(pool, evidenceStore, auditStore, releasesSvc)
	evidenceHandlers := evidence.NewHandlers(evidenceSvc)

	candidatesStore := candidates.NewStore()
	candidatesSvc := candidates.NewService(pool, candidatesStore, auditStore, releasesStore, releasesSvc)
	candidatesHandlers := candidates.NewHandlers(candidatesSvc)

	approvalsStore := approvals.NewStore()
	approvalsSvc := approvals.NewService(pool, approvalsStore, auditStore, releasesSvc, projectsSvc, candidatesStore)
	approvalsHandlers := approvals.NewHandlers(approvalsSvc)

	gateSvc := gate.NewService(pool, releasesStore, evidenceStore, approvalsStore, projectsStore, candidatesStore, checklistsStore)
	gateHandlers := gate.NewHandlers(gateSvc, releasesSvc)

	deploymentsStore := deployments.NewStore()
	deploymentsSvc := deployments.NewService(pool, deploymentsStore, auditStore, gateSvc, releasesStore, releasesSvc, releasesSvc, projectsSvc, candidatesStore, cfg.DeployAuthTTL)
	deploymentsHandlers := deployments.NewHandlers(deploymentsSvc)

	supportStore := support.NewStore()
	supportSvc := support.NewService(pool, supportStore, auditStore, releasesSvc)
	supportHandlers := support.NewHandlers(supportSvc)

	mcpNotesStore := mcpnotes.NewStore()
	mcpNotesSvc := mcpnotes.NewService(pool, mcpNotesStore, auditStore, releasesSvc, supportSvc)
	mcpNotesHandlers := mcpnotes.NewHandlers(mcpNotesSvc)

	dashboard := NewDashboardService(pool, projectsStore, releasesStore, approvalsStore, deploymentsStore, mcpNotesStore, gateSvc)

	return &Server{
		Cfg: cfg, Pool: pool,
		Audit: auditStore, Users: usersStore,
		Auth: authSvc, AuthHandlers: authHandlers,
		Projects: projectsSvc, ProjectsHandlers: projectsHandlers,
		Releases: releasesSvc, ReleasesStore: releasesStore, ReleasesHandlers: releasesHandlers,
		Evidence: evidenceSvc, EvidenceStore: evidenceStore, EvidenceHandlers: evidenceHandlers,
		Approvals: approvalsSvc, ApprovalsStore: approvalsStore, ApprovalsHandlers: approvalsHandlers,
		Gate: gateSvc, GateHandlers: gateHandlers,
		Deployments: deploymentsSvc, DeploymentsStore: deploymentsStore, DeploymentsHandlers: deploymentsHandlers,
		Support: supportSvc, SupportStore: supportStore, SupportHandlers: supportHandlers,
		MCPNotes: mcpNotesSvc, MCPNotesStore: mcpNotesStore, MCPNotesHandlers: mcpNotesHandlers,
		Candidates: candidatesSvc, CandidatesStore: candidatesStore, CandidatesHandlers: candidatesHandlers,
		Checklists: checklistsSvc, ChecklistsStore: checklistsStore, ChecklistsHandlers: checklistsHandlers,
		Dashboard: dashboard,
	}
}
