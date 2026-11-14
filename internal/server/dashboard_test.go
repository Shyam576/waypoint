package server

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/config"
	"github.com/Shyam576/waypoint/internal/dbx"
	"github.com/Shyam576/waypoint/internal/deployments"
	"github.com/Shyam576/waypoint/internal/projects"
	"github.com/Shyam576/waypoint/internal/releases"
	"github.com/Shyam576/waypoint/internal/support"
	"github.com/Shyam576/waypoint/internal/users"
)

// setupTestServer wires a full Server exactly as production does
// (New), against a real Postgres database. Requires
// WAYPOINT_TEST_DATABASE_URL; skipped otherwise.
func setupTestServer(t *testing.T) (context.Context, *Server) {
	t.Helper()
	url := os.Getenv("WAYPOINT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("WAYPOINT_TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	pool, err := dbx.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	if err := dbx.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	t.Cleanup(pool.Close)
	cfg := config.Config{
		SessionTTL: time.Hour, DeployAuthTTL: 15 * time.Minute,
		MCPTokenTTL: 180 * 24 * time.Hour, CITokenTTL: 90 * 24 * time.Hour,
	}
	return ctx, New(cfg, pool)
}

func createTestUser(t *testing.T, ctx context.Context, srv *Server, role users.Role) authn.Identity {
	t.Helper()
	email := fmt.Sprintf("%s-%d@test.local", role, time.Now().UnixNano())
	u, err := srv.Users.Create(ctx, srv.Pool, email, string(role), "x", role)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return authn.Identity{User: u, Via: authn.ViaWeb}
}

func TestDashboardSurfacesEmergencyReleasesOpenIncidentsAndDeploymentHealth(t *testing.T) {
	ctx, srv := setupTestServer(t)
	admin := createTestUser(t, ctx, srv, users.RoleAdmin)
	dev := createTestUser(t, ctx, srv, users.RoleDeveloper)

	proj, err := srv.Projects.Create(ctx, admin, projects.CreateInput{
		Name: fmt.Sprintf("Dashboard Test %d", time.Now().UnixNano()), OwningTeam: "QA",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := srv.Projects.AddMember(ctx, admin, proj.ID, dev.User.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}

	rel, err := srv.Releases.Create(ctx, dev, proj.ID, releases.CreateInput{
		Name: "v1.0.0", RiskLevel: releases.RiskLow, Summary: "dashboard test release", RollbackPlan: "revert",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}

	if _, err := srv.Releases.DeclareEmergency(ctx, admin, rel.ID, "checkout is down"); err != nil {
		t.Fatalf("declare emergency: %v", err)
	}

	if _, err := srv.Support.Create(ctx, dev, rel.ID, support.CreateInput{
		Summary: "elevated error rate after deploy", Severity: support.SeverityHigh,
	}); err != nil {
		t.Fatalf("create support item: %v", err)
	}

	failedDep, err := srv.Deployments.RecordFromWeb(ctx, admin, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvDevelopment, ArtifactDigest: "sha256:bad", WorkflowRunURL: "https://ci.example/run",
		Result: deployments.ResultFailure,
	})
	if err != nil {
		t.Fatalf("record failed deployment: %v", err)
	}

	pendingVerificationDep, err := srv.Deployments.RecordFromWeb(ctx, admin, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvDevelopment, ArtifactDigest: "sha256:good", WorkflowRunURL: "https://ci.example/run2",
		Result: deployments.ResultSuccess,
	})
	if err != nil {
		t.Fatalf("record successful deployment: %v", err)
	}

	dash, err := srv.Dashboard.Build(ctx, admin)
	if err != nil {
		t.Fatalf("build dashboard: %v", err)
	}

	foundEmergency := false
	for _, r := range dash.EmergencyReleases {
		if r.ID == rel.ID {
			foundEmergency = true
		}
	}
	if !foundEmergency {
		t.Error("expected the emergency release to appear in EmergencyReleases")
	}

	foundIncident := false
	for _, item := range dash.OpenIncidents {
		if item.ReleaseID == rel.ID {
			foundIncident = true
		}
	}
	if !foundIncident {
		t.Error("expected the open support item to appear in OpenIncidents")
	}

	foundFailed := false
	for _, d := range dash.FailedDeployments {
		if d.ID == failedDep.ID {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Error("expected the failed deployment to appear in FailedDeployments")
	}

	foundAwaiting := false
	for _, d := range dash.AwaitingVerificationDeployments {
		if d.ID == pendingVerificationDep.ID {
			foundAwaiting = true
		}
	}
	if !foundAwaiting {
		t.Error("expected the unverified successful deployment to appear in AwaitingVerificationDeployments")
	}

	// Once verified, it should drop off the awaiting-verification list.
	if _, err := srv.Deployments.SubmitVerificationFromWeb(ctx, dev, rel.ID, pendingVerificationDep.ID, deployments.SubmitVerificationInput{
		Status: deployments.VerificationPassed, Note: "looks fine",
	}); err != nil {
		t.Fatalf("submit verification: %v", err)
	}
	dash, err = srv.Dashboard.Build(ctx, admin)
	if err != nil {
		t.Fatalf("rebuild dashboard: %v", err)
	}
	for _, d := range dash.AwaitingVerificationDeployments {
		if d.ID == pendingVerificationDep.ID {
			t.Error("expected the now-verified deployment to no longer appear in AwaitingVerificationDeployments")
		}
	}
}
