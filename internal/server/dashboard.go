package server

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/approvals"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/deployments"
	"github.com/Shyam576/waypoint/internal/gate"
	"github.com/Shyam576/waypoint/internal/mcpnotes"
	"github.com/Shyam576/waypoint/internal/projects"
	"github.com/Shyam576/waypoint/internal/releases"
	"github.com/Shyam576/waypoint/internal/users"
)

// DashboardService composes read models across packages for the "what
// needs my attention" home screen. It is intentionally its own small
// service (rather than living in any one domain package) because it is
// purely a read-side aggregation with no business rules of its own.
type DashboardService struct {
	pool        *pgxpool.Pool
	projects    *projects.Store
	releases    *releases.Store
	approvals   *approvals.Store
	deployments *deployments.Store
	submissions *mcpnotes.Store
	gate        *gate.Service
}

func NewDashboardService(pool *pgxpool.Pool, projectsStore *projects.Store, releasesStore *releases.Store, approvalsStore *approvals.Store, deploymentsStore *deployments.Store, submissionsStore *mcpnotes.Store, gateSvc *gate.Service) *DashboardService {
	return &DashboardService{pool: pool, projects: projectsStore, releases: releasesStore, approvals: approvalsStore, deployments: deploymentsStore, submissions: submissionsStore, gate: gateSvc}
}

type DashboardRelease struct {
	releases.Release
	ProjectName string `json:"project_name"`
	Reason      string `json:"reason,omitempty"`
}

type Dashboard struct {
	NeedsMyAction      []DashboardRelease       `json:"needs_my_action"`
	Blocked            []DashboardRelease       `json:"blocked"`
	UpcomingProduction []DashboardRelease       `json:"upcoming_production"`
	RecentDeployments  []deployments.Deployment `json:"recent_deployments"`
}

func (s *DashboardService) visibleProjects(ctx context.Context, identity authn.Identity) ([]projects.Project, error) {
	if identity.User.Role == users.RoleAdmin {
		return s.projects.ListAll(ctx, s.pool)
	}
	return s.projects.ListForUser(ctx, s.pool, identity.User.ID)
}

func (s *DashboardService) Build(ctx context.Context, identity authn.Identity) (Dashboard, error) {
	projs, err := s.visibleProjects(ctx, identity)
	if err != nil {
		return Dashboard{}, err
	}
	projectName := map[uuid.UUID]string{}
	projectIDs := make([]uuid.UUID, 0, len(projs))
	for _, p := range projs {
		projectName[p.ID] = p.Name
		projectIDs = append(projectIDs, p.ID)
	}

	var allReleases []releases.Release
	for _, pid := range projectIDs {
		rs, err := s.releases.ListByProject(ctx, s.pool, pid, nil)
		if err != nil {
			return Dashboard{}, err
		}
		allReleases = append(allReleases, rs...)
	}

	canApprove := identity.User.Role == users.RoleAdmin || identity.User.Role == users.RoleApprover
	canReview := identity.User.Role == users.RoleAdmin || identity.User.Role == users.RoleDeveloper || identity.User.Role == users.RoleReleaseCoordinator

	var needsAction, blocked, upcoming []DashboardRelease
	now := time.Now()

	for _, r := range allReleases {
		if r.Stage == releases.StageClosed {
			continue
		}

		if r.Stage == releases.StageReadyForProduction {
			if canApprove && r.OwnerID != identity.User.ID {
				latest, err := s.approvals.LatestPerApprover(ctx, s.pool, r.ID)
				if err != nil {
					return Dashboard{}, err
				}
				decided := false
				for _, a := range latest {
					if a.ApproverID == identity.User.ID {
						decided = true
					}
				}
				if !decided {
					needsAction = append(needsAction, DashboardRelease{Release: r, ProjectName: projectName[r.ProjectID], Reason: "awaiting your approval decision"})
				}
			}

			result, err := s.gate.Evaluate(ctx, r.ID)
			if err != nil {
				return Dashboard{}, err
			}
			if !result.Eligible {
				blocked = append(blocked, DashboardRelease{Release: r, ProjectName: projectName[r.ProjectID], Reason: joinBlockers(result.Blockers)})
			}

			if r.TargetDate != nil && r.TargetDate.After(now.AddDate(0, 0, -1)) {
				upcoming = append(upcoming, DashboardRelease{Release: r, ProjectName: projectName[r.ProjectID]})
			}
		}

		if canReview {
			pending, err := s.submissions.ListSubmissionsByRelease(ctx, s.pool, r.ID, statusPtr(mcpnotes.SubmissionPending))
			if err != nil {
				return Dashboard{}, err
			}
			if len(pending) > 0 {
				needsAction = append(needsAction, DashboardRelease{Release: r, ProjectName: projectName[r.ProjectID], Reason: "has pending MCP submissions to review"})
			}
		}
	}

	recent, err := s.deployments.ListRecent(ctx, s.pool, projectIDs, 20)
	if err != nil {
		return Dashboard{}, err
	}
	if recent == nil {
		recent = []deployments.Deployment{}
	}

	return Dashboard{
		NeedsMyAction:      nonNil(needsAction),
		Blocked:            nonNil(blocked),
		UpcomingProduction: nonNil(upcoming),
		RecentDeployments:  recent,
	}, nil
}

func statusPtr(s mcpnotes.SubmissionStatus) *mcpnotes.SubmissionStatus { return &s }

func nonNil(in []DashboardRelease) []DashboardRelease {
	if in == nil {
		return []DashboardRelease{}
	}
	return in
}

func joinBlockers(blockers []string) string {
	if len(blockers) == 0 {
		return ""
	}
	if len(blockers) == 1 {
		return blockers[0]
	}
	return blockers[0] + " (+" + strconv.Itoa(len(blockers)-1) + " more)"
}
