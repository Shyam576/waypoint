// Package integrationtest exercises the release transition rules, the
// production gate's decisions, authorization (self-approval, roles,
// project membership), and the MCP submission review flow against a
// real Postgres database, the way the manual smoke test in the README
// does but as repeatable assertions.
//
// These tests require WAYPOINT_TEST_DATABASE_URL (a disposable Postgres
// instance; docker-compose.yml's `db` service works) and are skipped
// otherwise, so `go test ./...` without a database still passes.
package integrationtest

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/approvals"
	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/candidates"
	"github.com/Shyam576/waypoint/internal/checklists"
	"github.com/Shyam576/waypoint/internal/dbx"
	"github.com/Shyam576/waypoint/internal/deployments"
	"github.com/Shyam576/waypoint/internal/evidence"
	"github.com/Shyam576/waypoint/internal/gate"
	"github.com/Shyam576/waypoint/internal/mcpnotes"
	"github.com/Shyam576/waypoint/internal/projects"
	"github.com/Shyam576/waypoint/internal/releases"
	"github.com/Shyam576/waypoint/internal/support"
	"github.com/Shyam576/waypoint/internal/users"
)

func setup(t *testing.T) (context.Context, *pgxpool.Pool) {
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
	return ctx, pool
}

// harness wires every service exactly as internal/server does, so these
// tests exercise the same code path production runs.
type harness struct {
	pool           *pgxpool.Pool
	usersStore     *users.Store
	authSvc        *authn.Service
	projectsSvc    *projects.Service
	releasesSvc    *releases.Service
	evidenceSvc    *evidence.Service
	candidatesSvc  *candidates.Service
	checklistsSvc  *checklists.Service
	approvalsSvc   *approvals.Service
	gateSvc        *gate.Service
	deploymentsSvc *deployments.Service
	supportSvc     *support.Service
	mcpNotesSvc    *mcpnotes.Service
}

func newHarness(pool *pgxpool.Pool) *harness {
	auditStore := audit.NewStore()
	usersStore := users.NewStore()
	authStore := authn.NewStore(pool)
	authSvc := authn.NewService(authStore, usersStore, pool, time.Hour, 180*24*time.Hour, 90*24*time.Hour)
	checklistsStore := checklists.NewStore()
	projectsStore := projects.NewStore()
	projectsSvc := projects.NewService(pool, projectsStore, auditStore, usersStore, checklistsStore)
	releasesStore := releases.NewStore()
	releasesSvc := releases.NewService(pool, releasesStore, auditStore, projectsSvc, checklistsStore)
	checklistsSvc := checklists.NewService(pool, checklistsStore, auditStore, releasesSvc, projectsSvc)
	evidenceStore := evidence.NewStore()
	evidenceSvc := evidence.NewService(pool, evidenceStore, auditStore, releasesSvc)
	candidatesStore := candidates.NewStore()
	candidatesSvc := candidates.NewService(pool, candidatesStore, auditStore, releasesStore, releasesSvc)
	approvalsStore := approvals.NewStore()
	approvalsSvc := approvals.NewService(pool, approvalsStore, auditStore, releasesSvc, projectsSvc, candidatesStore, evidenceStore)
	gateSvc := gate.NewService(pool, releasesStore, evidenceStore, approvalsStore, projectsStore, candidatesStore, checklistsStore)
	deploymentsStore := deployments.NewStore()
	deploymentsSvc := deployments.NewService(pool, deploymentsStore, auditStore, gateSvc, releasesStore, releasesSvc, releasesSvc, projectsSvc, candidatesStore, 15*time.Minute)
	supportStore := support.NewStore()
	supportSvc := support.NewService(pool, supportStore, auditStore, releasesSvc)
	mcpNotesStore := mcpnotes.NewStore()
	mcpNotesSvc := mcpnotes.NewService(pool, mcpNotesStore, auditStore, releasesSvc, supportSvc)

	return &harness{
		pool: pool, usersStore: usersStore, authSvc: authSvc, projectsSvc: projectsSvc, releasesSvc: releasesSvc,
		checklistsSvc: checklistsSvc,
		evidenceSvc:   evidenceSvc, candidatesSvc: candidatesSvc, approvalsSvc: approvalsSvc, gateSvc: gateSvc,
		deploymentsSvc: deploymentsSvc, supportSvc: supportSvc, mcpNotesSvc: mcpNotesSvc,
	}
}

func (h *harness) issueCIIdentity(t *testing.T, ctx context.Context, admin authn.Identity, projectID uuid.UUID) authn.Identity {
	t.Helper()
	tok, _, err := h.authSvc.IssueCIToken(ctx, projectID, admin.User.ID, "test-ci-token")
	if err != nil {
		t.Fatalf("issue ci token: %v", err)
	}
	return authn.Identity{Via: authn.ViaCI, CITokenID: &tok.ID, CIProjectID: &projectID}
}

func (h *harness) createUser(t *testing.T, ctx context.Context, role users.Role) authn.Identity {
	t.Helper()
	email := fmt.Sprintf("%s-%d@test.local", role, time.Now().UnixNano())
	u, err := h.usersStore.Create(ctx, h.pool, email, string(role), "x", role)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return authn.Identity{User: u, Via: authn.ViaWeb}
}

func (h *harness) createProject(t *testing.T, ctx context.Context, admin authn.Identity, members ...authn.Identity) projects.Project {
	t.Helper()
	name := fmt.Sprintf("Test Project %d", time.Now().UnixNano())
	p, err := h.projectsSvc.Create(ctx, admin, projects.CreateInput{Name: name, OwningTeam: "QA"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	for _, m := range members {
		if err := h.projectsSvc.AddMember(ctx, admin, p.ID, m.User.ID); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
	return p
}

func (h *harness) createRelease(t *testing.T, ctx context.Context, owner authn.Identity, projectID uuid.UUID) releases.Release {
	t.Helper()
	r, err := h.releasesSvc.Create(ctx, owner, projectID, releases.CreateInput{
		Name: "v1.0.0", RiskLevel: releases.RiskLow, Summary: "test release", RollbackPlan: "revert",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	return r
}

func (h *harness) advanceToReadyForProduction(t *testing.T, ctx context.Context, owner authn.Identity, releaseID uuid.UUID) {
	t.Helper()
	for _, stage := range []releases.Stage{releases.StageDevelopment, releases.StageUAT, releases.StageReadyForProduction} {
		if _, err := h.releasesSvc.Transition(ctx, owner, releaseID, stage, ""); err != nil {
			t.Fatalf("transition to %s: %v", stage, err)
		}
	}
}

// satisfyGateAndDeployProduction advances rel through evidence and
// approval so the gate's normal requirements are met, then issues a CI
// authorization and records a successful production deployment of the
// given digest. It returns the CI identity used (so callers can reuse
// it to record further deployments on the same release) and the
// resulting deployment.
func (h *harness) satisfyGateAndDeployProduction(t *testing.T, ctx context.Context, admin, dev, approver authn.Identity, proj projects.Project, rel releases.Release, digest string) (authn.Identity, deployments.Deployment) {
	t.Helper()
	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence %s: %v", typ, err)
		}
	}
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	ci := h.issueCIIdentity(t, ctx, admin, proj.ID)
	_, token, err := h.deploymentsSvc.IssueAuthorization(ctx, ci, rel.ID, digest)
	if err != nil {
		t.Fatalf("issue authorization: %v", err)
	}
	d, err := h.deploymentsSvc.RecordFromCI(ctx, ci, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: digest, WorkflowRunURL: "https://ci.example/run/1",
		Result: deployments.ResultSuccess, DeploymentAuthorization: token,
	})
	if err != nil {
		t.Fatalf("record production deployment: %v", err)
	}
	return ci, d
}

func TestReleaseStageMachineRejectsInvalidTransitions(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	proj := h.createProject(t, ctx, admin, dev)
	rel := h.createRelease(t, ctx, dev, proj.ID)

	if _, err := h.releasesSvc.Transition(ctx, dev, rel.ID, releases.StagePostReleaseSupport, ""); err == nil {
		t.Error("expected skipping straight to post_release_support to be rejected")
	}
	if _, err := h.releasesSvc.Transition(ctx, dev, rel.ID, releases.StageProduction, ""); err == nil {
		t.Error("expected manual transition into production to be rejected (it is automatic-only)")
	}
	if _, err := h.releasesSvc.Transition(ctx, dev, rel.ID, releases.StageDevelopment, ""); err != nil {
		t.Errorf("expected draft -> development to succeed: %v", err)
	}
}

func TestGateBlocksMissingEvidenceAndApprovals(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)

	result, err := h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected release with no evidence or approvals to be ineligible")
	}
	if len(result.MissingEvidenceTypes) != 4 {
		t.Errorf("expected 4 missing evidence types, got %d: %v", len(result.MissingEvidenceTypes), result.MissingEvidenceTypes)
	}

	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence %s: %v", typ, err)
		}
	}

	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected release with evidence but no approval to still be ineligible")
	}
	if len(result.Blockers) != 1 {
		t.Errorf("expected exactly one blocker (missing approval), got: %v", result.Blockers)
	}

	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "looks good"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result.Eligible {
		t.Fatalf("expected release to be eligible after evidence and approval, blockers: %v", result.Blockers)
	}
}

func TestGateBlocksOnMostRecentRejection(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}

	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "initial approval"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	result, err := h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil || !result.Eligible {
		t.Fatalf("expected eligible after approval, got eligible=%v err=%v", result.Eligible, err)
	}

	// A second, later rejection from the same approver should re-block the
	// gate even though an approval exists in the history.
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionRejected, "found a regression"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected a subsequent rejection to re-block the gate")
	}
	if !result.MostRecentDecisionIsRejection {
		t.Error("expected MostRecentDecisionIsRejection to be true")
	}
}

func TestApprovalRejectsSelfApprovalAndWrongRole(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)

	if _, err := h.approvalsSvc.Decide(ctx, dev, rel.ID, approvals.DecisionApproved, "self approve"); err == nil {
		t.Error("expected the release owner to be forbidden from approving their own release")
	}

	otherDev := h.createUser(t, ctx, users.RoleDeveloper)
	if err := h.projectsSvc.AddMember(ctx, admin, proj.ID, otherDev.User.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := h.approvalsSvc.Decide(ctx, otherDev, rel.ID, approvals.DecisionApproved, "wrong role"); err == nil {
		t.Error("expected a developer (non-approver, non-admin) to be forbidden from approving")
	}
}

func TestProductionDeploymentEnforcesGateAndPromotesStage(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)

	ciIdentity := h.issueCIIdentity(t, ctx, admin, proj.ID)

	if _, _, err := h.deploymentsSvc.IssueAuthorization(ctx, ciIdentity, rel.ID, "sha256:deadbeef"); err == nil {
		t.Fatal("expected authorization mint to be rejected: release is not eligible yet")
	}

	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	_, authToken, err := h.deploymentsSvc.IssueAuthorization(ctx, ciIdentity, rel.ID, "sha256:deadbeef")
	if err != nil {
		t.Fatalf("expected authorization mint to succeed once eligible: %v", err)
	}

	d, err := h.deploymentsSvc.RecordFromCI(ctx, ciIdentity, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:deadbeef", WorkflowRunURL: "https://ci.example/run/2",
		Result: deployments.ResultSuccess, DeploymentAuthorization: authToken,
	})
	if err != nil {
		t.Fatalf("expected eligible production deployment to succeed: %v", err)
	}
	if d.ArtifactDigest != "sha256:deadbeef" {
		t.Errorf("artifact digest not stored correctly: %s", d.ArtifactDigest)
	}
	if d.AuthorizationID == nil {
		t.Error("expected the deployment to record which authorization it consumed")
	}

	after, err := h.releasesSvc.GetForService(ctx, admin, rel.ID)
	if err != nil {
		t.Fatalf("get release: %v", err)
	}
	if after.Stage != releases.StageProduction {
		t.Errorf("expected release to auto-promote to production, got stage=%s", after.Stage)
	}

	otherProjAdmin := h.createUser(t, ctx, users.RoleAdmin)
	otherProj := h.createProject(t, ctx, otherProjAdmin)
	wrongScopeCI := h.issueCIIdentity(t, ctx, otherProjAdmin, otherProj.ID)
	if _, _, err := h.deploymentsSvc.IssueAuthorization(ctx, wrongScopeCI, rel.ID, "sha256:other"); err == nil {
		t.Error("expected a ci token scoped to a different project to be rejected when minting an authorization")
	}
}

func TestDeploymentAuthorizationRequiredForCIProduction(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	ciIdentity := h.issueCIIdentity(t, ctx, admin, proj.ID)

	// Even though the release is eligible, recording a CI production
	// deployment with no authorization token at all must be rejected: the
	// gate being satisfied is necessary but not sufficient — presenting a
	// token minted for this exact artifact is also required.
	if _, err := h.deploymentsSvc.RecordFromCI(ctx, ciIdentity, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:nodigesttoken", WorkflowRunURL: "https://ci.example/run/1", Result: deployments.ResultSuccess,
	}); err == nil {
		t.Error("expected a CI production deployment with no authorization token to be rejected")
	}

	// Non-production environments never required an authorization and
	// still don't.
	if _, err := h.deploymentsSvc.RecordFromCI(ctx, ciIdentity, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvStaging, ArtifactDigest: "sha256:staging", WorkflowRunURL: "https://ci.example/run/2", Result: deployments.ResultSuccess,
	}); err != nil {
		t.Errorf("expected a staging deployment with no authorization to succeed: %v", err)
	}
}

func TestDeploymentAuthorizationIsSingleUseAndArtifactBound(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	ciIdentity := h.issueCIIdentity(t, ctx, admin, proj.ID)

	_, authToken, err := h.deploymentsSvc.IssueAuthorization(ctx, ciIdentity, rel.ID, "sha256:approved")
	if err != nil {
		t.Fatalf("issue authorization: %v", err)
	}

	// A token minted for one digest must not authorize recording a
	// deployment of a different digest.
	if _, err := h.deploymentsSvc.RecordFromCI(ctx, ciIdentity, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:swapped", WorkflowRunURL: "https://ci.example/run/1",
		Result: deployments.ResultSuccess, DeploymentAuthorization: authToken,
	}); err == nil {
		t.Error("expected a mismatched artifact_digest to be rejected even with a valid token")
	}

	// The same token, used correctly, succeeds exactly once.
	if _, err := h.deploymentsSvc.RecordFromCI(ctx, ciIdentity, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:approved", WorkflowRunURL: "https://ci.example/run/2",
		Result: deployments.ResultSuccess, DeploymentAuthorization: authToken,
	}); err != nil {
		t.Fatalf("expected the correctly-matched token to succeed: %v", err)
	}

	// Reusing the same (now-consumed) token must be rejected.
	if _, err := h.deploymentsSvc.RecordFromCI(ctx, ciIdentity, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:approved", WorkflowRunURL: "https://ci.example/run/3",
		Result: deployments.ResultSuccess, DeploymentAuthorization: authToken,
	}); err == nil {
		t.Error("expected reusing an already-consumed authorization token to be rejected")
	}
}

func TestDeploymentAuthorizationConcurrentConsumptionIsSingleWinner(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	ciIdentity := h.issueCIIdentity(t, ctx, admin, proj.ID)

	_, authToken, err := h.deploymentsSvc.IssueAuthorization(ctx, ciIdentity, rel.ID, "sha256:race")
	if err != nil {
		t.Fatalf("issue authorization: %v", err)
	}

	const attempts = 8
	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := h.deploymentsSvc.RecordFromCI(ctx, ciIdentity, rel.ID, deployments.RecordInput{
				Environment: deployments.EnvProduction, ArtifactDigest: "sha256:race",
				WorkflowRunURL: fmt.Sprintf("https://ci.example/run/race-%d", n),
				Result:         deployments.ResultSuccess, DeploymentAuthorization: authToken,
			})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Errorf("expected exactly 1 of %d concurrent attempts with the same single-use token to succeed, got %d", attempts, successes)
	}

	deps, err := h.deploymentsSvc.ListByRelease(ctx, admin, rel.ID)
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if len(deps) != 1 {
		t.Errorf("expected exactly 1 deployment recorded from the race, got %d", len(deps))
	}
}

func TestDeploymentAuthorizationExpires(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// A harness-local deployments service with a near-zero authorization
	// TTL, so the token is already expired by the time we try to use it,
	// without the test needing to sleep for a realistic TTL.
	shortTTLSvc := deployments.NewService(h.pool, deployments.NewStore(), audit.NewStore(), h.gateSvc, releases.NewStore(), h.releasesSvc, h.releasesSvc, h.projectsSvc, candidates.NewStore(), time.Nanosecond)
	ciIdentity := h.issueCIIdentity(t, ctx, admin, proj.ID)

	_, authToken, err := shortTTLSvc.IssueAuthorization(ctx, ciIdentity, rel.ID, "sha256:expired")
	if err != nil {
		t.Fatalf("issue authorization: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	if _, err := shortTTLSvc.RecordFromCI(ctx, ciIdentity, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:expired", WorkflowRunURL: "https://ci.example/run/1",
		Result: deployments.ResultSuccess, DeploymentAuthorization: authToken,
	}); err == nil {
		t.Error("expected an expired authorization token to be rejected")
	}
}

func TestMCPSubmissionAcceptMaterializesRejectDoesNot(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	proj := h.createProject(t, ctx, admin, dev)
	rel := h.createRelease(t, ctx, dev, proj.ID)

	mcpIdentity := dev
	mcpIdentity.Via = authn.ViaMCP

	sub, err := h.mcpNotesSvc.SubmitChangeNote(ctx, mcpIdentity, rel.ID, mcpnotes.ChangeNotePayload{Summary: "did a thing", TestsStatus: mcpnotes.TestsNotRun})
	if err != nil {
		t.Fatalf("submit change note via mcp: %v", err)
	}
	if sub.Status != mcpnotes.SubmissionPending {
		t.Fatalf("expected submission to start pending, got %s", sub.Status)
	}

	notes, err := h.mcpNotesSvc.ListChangeNotes(ctx, dev, rel.ID)
	if err != nil {
		t.Fatalf("list change notes: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no accepted change notes before review, got %d", len(notes))
	}

	if _, err := h.mcpNotesSvc.Accept(ctx, dev, sub.ID, "looks right"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	notes, err = h.mcpNotesSvc.ListChangeNotes(ctx, dev, rel.ID)
	if err != nil {
		t.Fatalf("list change notes: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 accepted change note, got %d", len(notes))
	}
	if notes[0].Source != mcpnotes.SourceMCP {
		t.Errorf("expected materialized note to be attributed to mcp, got %s", notes[0].Source)
	}

	sub2, err := h.mcpNotesSvc.SubmitSupportNote(ctx, mcpIdentity, rel.ID, mcpnotes.SupportNotePayload{Summary: "weird bug", Severity: "low"})
	if err != nil {
		t.Fatalf("submit support note via mcp: %v", err)
	}
	if _, err := h.mcpNotesSvc.Reject(ctx, dev, sub2.ID, "duplicate"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	items, err := h.supportSvc.ListByRelease(ctx, dev, rel.ID)
	if err != nil {
		t.Fatalf("list support items: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected a rejected mcp submission to never materialize, got %d support items", len(items))
	}

	if _, err := h.mcpNotesSvc.Accept(ctx, dev, sub.ID, ""); err == nil {
		t.Error("expected accepting an already-reviewed submission to fail")
	}
}

func TestDeploymentAuthorizationRequiresMatchingCandidateDigest(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	if _, err := h.candidatesSvc.RegisterFromWeb(ctx, dev, rel.ID, candidates.RegisterInput{CommitSHA: "abc123", ImageDigest: "sha256:candidate-digest"}); err != nil {
		t.Fatalf("register candidate: %v", err)
	}
	ciIdentity := h.issueCIIdentity(t, ctx, admin, proj.ID)

	// A registered candidate exists: minting an authorization for a
	// different digest must be rejected even though the release is
	// otherwise eligible.
	if _, _, err := h.deploymentsSvc.IssueAuthorization(ctx, ciIdentity, rel.ID, "sha256:some-other-digest"); err == nil {
		t.Error("expected an authorization request for a digest that doesn't match the registered candidate to be rejected")
	}

	// The candidate's own digest is authorized normally.
	if _, _, err := h.deploymentsSvc.IssueAuthorization(ctx, ciIdentity, rel.ID, "sha256:candidate-digest"); err != nil {
		t.Errorf("expected an authorization request matching the registered candidate's digest to succeed: %v", err)
	}
}

func TestGateRequiresCandidateMatchingApprovalWhenPolicyEnabled(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)

	policy, err := h.projectsSvc.GetPolicy(ctx, admin, proj.ID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if _, err := h.projectsSvc.UpdatePolicy(ctx, admin, proj.ID, projects.UpdatePolicyInput{
		RequiredEvidenceTypes: policy.RequiredEvidenceTypes, RequiredApprovalsCount: policy.RequiredApprovalsCount,
		BlockOnOpenRejection: policy.BlockOnOpenRejection, RequireCandidateForApproval: true,
	}); err != nil {
		t.Fatalf("enable require_candidate_for_approval: %v", err)
	}

	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}

	// No candidate registered yet and the policy requires one: the gate
	// must block with a clear reason, even with a satisfied approval
	// count requirement about to be met.
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	result, err := h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected the gate to block when no candidate is registered but policy requires one")
	}

	// Register the candidate the approval above did NOT see (it predates
	// the candidate), so it must not count.
	if _, err := h.candidatesSvc.RegisterFromWeb(ctx, dev, rel.ID, candidates.RegisterInput{ImageDigest: "sha256:v1"}); err != nil {
		t.Fatalf("register candidate v1: %v", err)
	}
	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected the gate to still block: the only approval predates the candidate and must not count")
	}

	// Approve again now that a candidate exists: this decision is stamped
	// with the current candidate and should satisfy the gate.
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "go, now that there's a candidate"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result.Eligible {
		t.Fatalf("expected the gate to be eligible once approved against the current candidate, blockers: %v", result.Blockers)
	}

	// Registering a NEW candidate (the artifact changed) must silently
	// drop the old approval from the count without any separate
	// invalidation step: this is the revalidation-after-change guarantee.
	if _, err := h.candidatesSvc.RegisterFromWeb(ctx, dev, rel.ID, candidates.RegisterInput{ImageDigest: "sha256:v2"}); err != nil {
		t.Fatalf("register candidate v2: %v", err)
	}
	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected changing the candidate to invalidate the prior approval and re-block the gate")
	}
	if result.ApprovedCount != 0 {
		t.Errorf("expected 0 approvals to count against the new candidate, got %d", result.ApprovedCount)
	}
}

func TestGateUsesPolicySnapshotNotLivePolicyAfterReadyForProduction(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)

	policy, err := h.projectsSvc.GetPolicy(ctx, admin, proj.ID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if policy.RequiredApprovalsCount != 1 {
		t.Fatalf("expected the default policy to require 1 approval, got %d", policy.RequiredApprovalsCount)
	}

	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)

	// Weaken the project's live policy to require 0 approvals AFTER the
	// release has already entered ready_for_production.
	if _, err := h.projectsSvc.UpdatePolicy(ctx, admin, proj.ID, projects.UpdatePolicyInput{
		RequiredEvidenceTypes: policy.RequiredEvidenceTypes, RequiredApprovalsCount: 0,
		BlockOnOpenRejection: policy.BlockOnOpenRejection, RequireCandidateForApproval: policy.RequireCandidateForApproval,
	}); err != nil {
		t.Fatalf("weaken policy: %v", err)
	}

	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}

	result, err := h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected the gate to still require the snapshotted 1-approval policy, not the weakened live policy")
	}
	if result.RequiredApprovalsCount != 1 {
		t.Errorf("expected the snapshotted required_approvals_count of 1, got %d (live policy leaked through)", result.RequiredApprovalsCount)
	}
	if result.PolicySnapshotAt == nil {
		t.Error("expected a policy snapshot to have been taken on entering ready_for_production")
	}

	// A fresh project policy change is still unrestricted for a release
	// that has NOT yet reached ready_for_production.
	rel2 := h.createRelease(t, ctx, dev, proj.ID)
	result2, err := h.gateSvc.Evaluate(ctx, rel2.ID)
	if err != nil {
		t.Fatalf("evaluate rel2: %v", err)
	}
	if result2.RequiredApprovalsCount != 0 {
		t.Errorf("expected a release that never reached ready_for_production to read the live (weakened) policy, got required_approvals_count=%d", result2.RequiredApprovalsCount)
	}
	if result2.PolicySnapshotAt != nil {
		t.Error("expected no policy snapshot for a release that has never entered ready_for_production")
	}

	// Approving against the snapshotted policy makes it eligible.
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result.Eligible {
		t.Fatalf("expected eligible after satisfying the snapshotted policy, blockers: %v", result.Blockers)
	}

	// Sending the release back to uat and re-promoting it refreshes the
	// snapshot to whatever the live policy says now.
	if _, err := h.releasesSvc.Transition(ctx, dev, rel.ID, releases.StageUAT, "found an issue"); err != nil {
		t.Fatalf("transition back to uat: %v", err)
	}
	if _, err := h.releasesSvc.Transition(ctx, dev, rel.ID, releases.StageReadyForProduction, "fixed"); err != nil {
		t.Fatalf("re-transition to ready_for_production: %v", err)
	}
	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.RequiredApprovalsCount != 0 {
		t.Errorf("expected re-entering ready_for_production to refresh the snapshot to the current live policy (0), got %d", result.RequiredApprovalsCount)
	}
	if !result.Eligible {
		t.Errorf("expected the release to be eligible again with 0 required approvals, blockers: %v", result.Blockers)
	}
}

func TestChecklistDefaultTemplateMaterializedOnReleaseCreation(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	proj := h.createProject(t, ctx, admin, dev)
	rel := h.createRelease(t, ctx, dev, proj.ID)

	items, err := h.checklistsSvc.ListByRelease(ctx, dev, rel.ID)
	if err != nil {
		t.Fatalf("list checklist items: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected the project's default checklist template to be materialized onto the release")
	}
	categories := map[checklists.Category]int{}
	for _, it := range items {
		if it.Status != checklists.ItemPending {
			t.Errorf("expected a freshly materialized item to start pending, got %s for %q", it.Status, it.Title)
		}
		categories[it.Category]++
	}
	for _, cat := range []checklists.Category{checklists.CategoryDevelopment, checklists.CategoryUAT, checklists.CategoryProductionReadiness, checklists.CategoryPostDeployment} {
		if categories[cat] == 0 {
			t.Errorf("expected at least one default item in category %s", cat)
		}
	}
}

func TestChecklistItemLifecycle(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	proj := h.createProject(t, ctx, admin, dev)
	rel := h.createRelease(t, ctx, dev, proj.ID)

	items, err := h.checklistsSvc.ListByRelease(ctx, dev, rel.ID)
	if err != nil || len(items) < 2 {
		t.Fatalf("expected at least 2 default items, got %d items, err=%v", len(items), err)
	}
	toVerify, toReject := items[0], items[1]

	verified, err := h.checklistsSvc.Verify(ctx, dev, toVerify.ID, "https://ci.example/build/42")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verified.Status != checklists.ItemVerified || verified.EvidenceRef != "https://ci.example/build/42" || verified.DecidedBy == nil {
		t.Errorf("unexpected verified item state: %+v", verified)
	}

	if _, err := h.checklistsSvc.Reject(ctx, dev, toReject.ID, ""); err == nil {
		t.Error("expected rejecting with no reason to fail")
	}
	rejected, err := h.checklistsSvc.Reject(ctx, dev, toReject.ID, "not actually done yet")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != checklists.ItemRejected || rejected.RejectionReason != "not actually done yet" {
		t.Errorf("unexpected rejected item state: %+v", rejected)
	}

	// Waiving (a controlled exception) requires project management
	// permission, not just edit permission.
	if _, err := h.checklistsSvc.Waive(ctx, dev, toReject.ID, "not applicable to this release"); err == nil {
		t.Error("expected a developer (non-admin, non-coordinator) to be forbidden from waiving a checklist item")
	}
	waived, err := h.checklistsSvc.Waive(ctx, admin, toReject.ID, "not applicable to this release")
	if err != nil {
		t.Fatalf("waive: %v", err)
	}
	if waived.Status != checklists.ItemWaived || waived.WaiverReason != "not applicable to this release" {
		t.Errorf("unexpected waived item state: %+v", waived)
	}

	otherDev := h.createUser(t, ctx, users.RoleDeveloper)
	assigned, err := h.checklistsSvc.AssignOwner(ctx, dev, toVerify.ID, otherDev.User.ID)
	if err != nil {
		t.Fatalf("assign owner: %v", err)
	}
	if assigned.OwnerID == nil || *assigned.OwnerID != otherDev.User.ID {
		t.Errorf("expected owner to be assigned, got %+v", assigned.OwnerID)
	}
}

func TestChecklistTemplateVersioningRetainedByRelease(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	proj := h.createProject(t, ctx, admin, dev)

	relV1 := h.createRelease(t, ctx, dev, proj.ID)
	itemsV1, err := h.checklistsSvc.ListByRelease(ctx, dev, relV1.ID)
	if err != nil {
		t.Fatalf("list v1 items: %v", err)
	}
	if len(itemsV1) == 0 {
		t.Fatal("expected the default template's items on the first release")
	}

	newTemplate, err := h.checklistsSvc.CreateTemplateVersion(ctx, admin, proj.ID, []checklists.TemplateItemInput{
		{Category: checklists.CategoryDevelopment, Title: "Custom v2 item", IsRequired: true, VerificationMethod: checklists.MethodHumanAttestation},
	})
	if err != nil {
		t.Fatalf("create new template version: %v", err)
	}
	if newTemplate.Version != 2 {
		t.Errorf("expected the new template to be version 2, got %d", newTemplate.Version)
	}

	relV2 := h.createRelease(t, ctx, dev, proj.ID)
	itemsV2, err := h.checklistsSvc.ListByRelease(ctx, dev, relV2.ID)
	if err != nil {
		t.Fatalf("list v2 items: %v", err)
	}
	if len(itemsV2) != 1 || itemsV2[0].Title != "Custom v2 item" {
		t.Errorf("expected the new release to get exactly the new template's 1 item, got %+v", itemsV2)
	}

	// The first release keeps whatever it was assigned, unaffected by the
	// new template version.
	itemsV1Again, err := h.checklistsSvc.ListByRelease(ctx, dev, relV1.ID)
	if err != nil {
		t.Fatalf("re-list v1 items: %v", err)
	}
	if len(itemsV1Again) != len(itemsV1) {
		t.Errorf("expected the first release's checklist to be unaffected by the new template version, had %d items, now %d", len(itemsV1), len(itemsV1Again))
	}
}

func TestGateRequiresChecklistCompletionWhenPolicyEnabled(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)

	policy, err := h.projectsSvc.GetPolicy(ctx, admin, proj.ID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if _, err := h.projectsSvc.UpdatePolicy(ctx, admin, proj.ID, projects.UpdatePolicyInput{
		RequiredEvidenceTypes: policy.RequiredEvidenceTypes, RequiredApprovalsCount: policy.RequiredApprovalsCount,
		BlockOnOpenRejection: policy.BlockOnOpenRejection, RequireChecklistCompletion: true,
	}); err != nil {
		t.Fatalf("enable require_checklist_completion: %v", err)
	}

	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	result, err := h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected the gate to block on incomplete required checklist items")
	}
	if len(result.IncompleteChecklistItems) == 0 {
		t.Error("expected IncompleteChecklistItems to list the outstanding items")
	}

	items, err := h.checklistsSvc.ListByRelease(ctx, dev, rel.ID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	for _, it := range items {
		if !it.IsRequired {
			continue
		}
		if _, err := h.checklistsSvc.Verify(ctx, dev, it.ID, ""); err != nil {
			t.Fatalf("verify item %q: %v", it.Title, err)
		}
	}

	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result.Eligible {
		t.Fatalf("expected the gate to be eligible once every required checklist item is verified, blockers: %v", result.Blockers)
	}
	if len(result.IncompleteChecklistItems) != 0 {
		t.Errorf("expected no incomplete checklist items, got %v", result.IncompleteChecklistItems)
	}
}

func TestDefaultRiskPoliciesSeededForHighAndCritical(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	proj := h.createProject(t, ctx, admin)

	policies, err := h.projectsSvc.ListRiskPolicies(ctx, admin, proj.ID)
	if err != nil {
		t.Fatalf("list risk policies: %v", err)
	}
	byTier := map[string]projects.RiskPolicy{}
	for _, p := range policies {
		byTier[p.RiskLevel] = p
	}
	high, ok := byTier["high"]
	if !ok {
		t.Fatal("expected a seeded risk policy for 'high'")
	}
	if high.ExtraRequiredApprovalsCount != 1 || high.RequireRiskAcceptance {
		t.Errorf("unexpected default 'high' risk policy: %+v", high)
	}
	critical, ok := byTier["critical"]
	if !ok {
		t.Fatal("expected a seeded risk policy for 'critical'")
	}
	if critical.ExtraRequiredApprovalsCount != 1 || !critical.RequireRiskAcceptance {
		t.Errorf("unexpected default 'critical' risk policy: %+v", critical)
	}
	foundSecurityReview := false
	for _, e := range critical.ExtraRequiredEvidenceTypes {
		if e == "security_review" {
			foundSecurityReview = true
		}
	}
	if !foundSecurityReview {
		t.Errorf("expected 'critical' to default to requiring security_review evidence, got %v", critical.ExtraRequiredEvidenceTypes)
	}
	if _, ok := byTier["low"]; ok {
		t.Error("expected no seeded risk policy for 'low'")
	}
}

func TestGateAppliesExtraApprovalsForHighRiskRelease(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver1 := h.createUser(t, ctx, users.RoleApprover)
	approver2 := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver1, approver2)

	rel, err := h.releasesSvc.Create(ctx, dev, proj.ID, releases.CreateInput{
		Name: "v-high-risk", RiskLevel: releases.RiskHigh, Summary: "high risk release", RollbackPlan: "revert",
	})
	if err != nil {
		t.Fatalf("create high-risk release: %v", err)
	}
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}

	if _, err := h.approvalsSvc.Decide(ctx, approver1, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve 1: %v", err)
	}
	result, err := h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected a high-risk release to need 2 approvals (1 base + 1 extra), not 1")
	}
	if result.RequiredApprovalsCount != 2 {
		t.Errorf("expected RequiredApprovalsCount to reflect the extra requirement (2), got %d", result.RequiredApprovalsCount)
	}

	if _, err := h.approvalsSvc.Decide(ctx, approver2, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve 2: %v", err)
	}
	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result.Eligible {
		t.Fatalf("expected eligible with 2 approvals, blockers: %v", result.Blockers)
	}
}

func TestGateRequiresRiskAcceptanceAndExtraEvidenceForCriticalRelease(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver1 := h.createUser(t, ctx, users.RoleApprover)
	approver2 := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver1, approver2)

	rel, err := h.releasesSvc.Create(ctx, dev, proj.ID, releases.CreateInput{
		Name: "v-critical-risk", RiskLevel: releases.RiskCritical, RiskCategories: []string{"payment"},
		Summary: "critical payment release", RollbackPlan: "revert",
	})
	if err != nil {
		t.Fatalf("create critical release: %v", err)
	}
	if len(rel.RiskCategories) != 1 || rel.RiskCategories[0] != "payment" {
		t.Errorf("expected risk_categories to be stored, got %v", rel.RiskCategories)
	}

	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}
	if _, err := h.approvalsSvc.Decide(ctx, approver1, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve 1: %v", err)
	}
	if _, err := h.approvalsSvc.Decide(ctx, approver2, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve 2: %v", err)
	}

	result, err := h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected a critical release to still be blocked: missing security_review evidence and risk acceptance")
	}
	if !result.RiskAcceptanceRequired || result.RiskAccepted {
		t.Errorf("expected risk acceptance to be required and not yet accepted, got required=%v accepted=%v", result.RiskAcceptanceRequired, result.RiskAccepted)
	}

	if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, evidence.TypeSecurityReview, "security review", "ok", nil); err != nil {
		t.Fatalf("submit security review evidence: %v", err)
	}

	if _, err := h.releasesSvc.AcceptRisk(ctx, dev, rel.ID, "trying as a developer"); err == nil {
		t.Error("expected a developer (non-admin, non-coordinator) to be forbidden from accepting risk")
	}
	if _, err := h.releasesSvc.AcceptRisk(ctx, admin, rel.ID, "reviewed and accepted"); err != nil {
		t.Fatalf("accept risk as admin: %v", err)
	}

	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result.Eligible {
		t.Fatalf("expected eligible once security review is submitted and risk is accepted, blockers: %v", result.Blockers)
	}
	if !result.RiskAccepted {
		t.Error("expected RiskAccepted to be true")
	}
}

func TestMCPAndCITokensExpire(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	proj := h.createProject(t, ctx, admin)

	// A harness-local auth service with a near-zero token TTL, so issued
	// tokens are already expired by the time we try to use them, without
	// sleeping for a realistic TTL (same technique as
	// TestDeploymentAuthorizationExpires).
	shortTTLAuth := authn.NewService(authn.NewStore(pool), h.usersStore, pool, time.Hour, time.Nanosecond, time.Nanosecond)

	_, mcpToken, err := shortTTLAuth.IssueMCPToken(ctx, admin.User.ID, "test")
	if err != nil {
		t.Fatalf("issue mcp token: %v", err)
	}
	_, ciToken, err := shortTTLAuth.IssueCIToken(ctx, proj.ID, admin.User.ID, "test")
	if err != nil {
		t.Fatalf("issue ci token: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	if _, err := shortTTLAuth.ResolveMCPToken(ctx, mcpToken); err == nil {
		t.Error("expected an expired mcp token to be rejected")
	}
	if _, err := shortTTLAuth.ResolveCIToken(ctx, ciToken); err == nil {
		t.Error("expected an expired ci token to be rejected")
	}

	// A token issued with the harness's normal (long) TTL still works.
	_, longMCPToken, err := h.authSvc.IssueMCPToken(ctx, admin.User.ID, "test-long")
	if err != nil {
		t.Fatalf("issue long-lived mcp token: %v", err)
	}
	if _, err := h.authSvc.ResolveMCPToken(ctx, longMCPToken); err != nil {
		t.Errorf("expected a freshly issued, long-TTL mcp token to resolve: %v", err)
	}
}

func TestChangePasswordRequiresCurrentPasswordAndInvalidatesSessions(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	email := fmt.Sprintf("changepw-%d@test.local", time.Now().UnixNano())
	hash, err := authn.HashPassword("originalpass123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u, err := h.usersStore.Create(ctx, h.pool, email, "Change PW", hash, users.RoleDeveloper)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	_, sessionToken, err := h.authSvc.Login(ctx, email, "originalpass123", "test-agent")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := h.authSvc.ResolveSession(ctx, sessionToken); err != nil {
		t.Fatalf("expected the fresh session to resolve: %v", err)
	}

	if err := h.authSvc.ChangePassword(ctx, u.ID, "wrongpassword", "newpass456"); err == nil {
		t.Error("expected changing password with the wrong current password to fail")
	}
	if err := h.authSvc.ChangePassword(ctx, u.ID, "originalpass123", "newpass456"); err != nil {
		t.Fatalf("change password: %v", err)
	}

	if _, err := h.authSvc.ResolveSession(ctx, sessionToken); err == nil {
		t.Error("expected the old session to be invalidated after a password change")
	}
	if _, _, err := h.authSvc.Login(ctx, email, "newpass456", "test-agent"); err != nil {
		t.Errorf("expected login with the new password to succeed: %v", err)
	}
}

func TestAdminSetPasswordAndDeactivateUser(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	email := fmt.Sprintf("admin-managed-%d@test.local", time.Now().UnixNano())
	hash, err := authn.HashPassword("originalpass123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u, err := h.usersStore.Create(ctx, h.pool, email, "Admin Managed", hash, users.RoleDeveloper)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if err := h.authSvc.AdminSetPassword(ctx, u.ID, "adminsetpass789"); err != nil {
		t.Fatalf("admin set password: %v", err)
	}
	if _, _, err := h.authSvc.Login(ctx, email, "adminsetpass789", "test-agent"); err != nil {
		t.Errorf("expected login with the admin-set password to succeed: %v", err)
	}

	if _, err := h.usersStore.SetActive(ctx, h.pool, u.ID, false); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}
	if _, _, err := h.authSvc.Login(ctx, email, "adminsetpass789", "test-agent"); err == nil {
		t.Error("expected login for a deactivated user to fail")
	}

	if _, err := h.usersStore.SetActive(ctx, h.pool, u.ID, true); err != nil {
		t.Fatalf("reactivate user: %v", err)
	}
	if _, _, err := h.authSvc.Login(ctx, email, "adminsetpass789", "test-agent"); err != nil {
		t.Errorf("expected login for a reactivated user to succeed: %v", err)
	}
}

func TestApproverNotSubmitterSeparationOfDuties(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approverWhoSubmitted := h.createUser(t, ctx, users.RoleApprover)
	otherApprover := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approverWhoSubmitted, otherApprover)

	policy, err := h.projectsSvc.GetPolicy(ctx, admin, proj.ID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if _, err := h.projectsSvc.UpdatePolicy(ctx, admin, proj.ID, projects.UpdatePolicyInput{
		RequiredEvidenceTypes: policy.RequiredEvidenceTypes, RequiredApprovalsCount: policy.RequiredApprovalsCount,
		BlockOnOpenRejection: policy.BlockOnOpenRejection, RequireApproverNotSubmitter: true,
	}); err != nil {
		t.Fatalf("enable require_approver_not_submitter: %v", err)
	}

	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)

	if _, err := h.evidenceSvc.Submit(ctx, approverWhoSubmitted, rel.ID, evidence.TypeTestResults, "tests", "ok", nil); err != nil {
		t.Fatalf("submit evidence: %v", err)
	}

	if _, err := h.approvalsSvc.Decide(ctx, approverWhoSubmitted, rel.ID, approvals.DecisionApproved, "self-submitted"); err == nil {
		t.Error("expected an approver who submitted evidence for this release to be forbidden from approving it")
	}
	if _, err := h.approvalsSvc.Decide(ctx, otherApprover, rel.ID, approvals.DecisionApproved, "did not submit evidence"); err != nil {
		t.Errorf("expected an approver who did not submit evidence to be allowed to approve: %v", err)
	}
}

func TestRollbackRequiresProvenSuccessfulProductionTarget(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	_, original := h.satisfyGateAndDeployProduction(t, ctx, admin, dev, approver, proj, rel, "sha256:original")

	// A failed production deployment is not a valid rollback target: it
	// was never proven safe.
	failed, err := h.deploymentsSvc.RecordFromWeb(ctx, admin, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:bad", WorkflowRunURL: "https://ci.example/run/2",
		Result: deployments.ResultFailure,
	})
	if err != nil {
		t.Fatalf("record failed deployment: %v", err)
	}
	if _, err := h.deploymentsSvc.InitiateRollback(ctx, admin, rel.ID, deployments.InitiateRollbackInput{
		OriginalDeploymentID: original.ID, TargetDeploymentID: failed.ID, Reason: "testing provenance",
	}); err == nil {
		t.Error("expected rollback to a failed deployment to be rejected")
	}

	// A non-production deployment is also not a valid rollback target.
	devDeployment, err := h.deploymentsSvc.RecordFromWeb(ctx, admin, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvDevelopment, ArtifactDigest: "sha256:dev", WorkflowRunURL: "https://ci.example/run/3",
		Result: deployments.ResultSuccess,
	})
	if err != nil {
		t.Fatalf("record dev deployment: %v", err)
	}
	if _, err := h.deploymentsSvc.InitiateRollback(ctx, admin, rel.ID, deployments.InitiateRollbackInput{
		OriginalDeploymentID: original.ID, TargetDeploymentID: devDeployment.ID, Reason: "testing provenance",
	}); err == nil {
		t.Error("expected rollback to a non-production deployment to be rejected")
	}

	// A prior successful production deployment is a valid target.
	if _, err := h.deploymentsSvc.InitiateRollback(ctx, admin, rel.ID, deployments.InitiateRollbackInput{
		OriginalDeploymentID: original.ID, TargetDeploymentID: original.ID, Reason: "rolling back to itself is allowed by provenance",
	}); err != nil {
		t.Errorf("expected rollback targeting a proven successful production deployment to be accepted: %v", err)
	}
}

func TestRollbackAuthorizationBypassesGateOnlyForThatDeployment(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)

	relA := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, relA.ID)
	_, goodDeployment := h.satisfyGateAndDeployProduction(t, ctx, admin, dev, approver, proj, relA, "sha256:good")

	relB := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, relB.ID)
	ciB, brokenDeployment := h.satisfyGateAndDeployProduction(t, ctx, admin, dev, approver, proj, relB, "sha256:broken")

	// Something is found wrong with relB's deployment after the fact: a
	// reviewer rejects it, which re-blocks the gate.
	if _, err := h.approvalsSvc.Decide(ctx, approver, relB.ID, approvals.DecisionRejected, "found a regression in production"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	result, err := h.gateSvc.Evaluate(ctx, relB.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected relB to be ineligible after the rejection")
	}

	// A normal authorization still cannot be minted while ineligible.
	if _, _, err := h.deploymentsSvc.IssueAuthorization(ctx, ciB, relB.ID, "sha256:another"); err == nil {
		t.Error("expected normal authorization issuance to still be blocked by the gate")
	}

	// But an admin can initiate a rollback to the proven-safe artifact
	// from relA, bypassing the gate for that one authorized deployment.
	out, err := h.deploymentsSvc.InitiateRollback(ctx, admin, relB.ID, deployments.InitiateRollbackInput{
		OriginalDeploymentID: brokenDeployment.ID, TargetDeploymentID: goodDeployment.ID, Reason: "relB's deployment caused a regression",
	})
	if err != nil {
		t.Fatalf("initiate rollback: %v", err)
	}
	if out.Token == "" {
		t.Fatal("expected a deployment authorization token")
	}

	d, err := h.deploymentsSvc.RecordFromCI(ctx, ciB, relB.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:good", WorkflowRunURL: "https://ci.example/rollback",
		Result: deployments.ResultSuccess, DeploymentAuthorization: out.Token,
	})
	if err != nil {
		t.Fatalf("expected rollback-authorized deployment to bypass the gate: %v", err)
	}
	if d.AuthorizationID == nil {
		t.Error("expected the rollback deployment to record which authorization it consumed")
	}

	rollbacks, err := h.deploymentsSvc.ListRollbacksByRelease(ctx, admin, relB.ID)
	if err != nil {
		t.Fatalf("list rollbacks: %v", err)
	}
	if len(rollbacks) != 1 || rollbacks[0].Status != deployments.RollbackCompleted {
		t.Fatalf("expected one completed rollback, got: %+v", rollbacks)
	}
	if rollbacks[0].ResultingDeploymentID == nil || *rollbacks[0].ResultingDeploymentID != d.ID {
		t.Error("expected rollback's resulting_deployment_id to point at the new deployment")
	}

	// The rollback-linked token was single-use like any other; a normal
	// (non-rollback) authorization is still blocked by the still-open
	// rejection.
	if _, _, err := h.deploymentsSvc.IssueAuthorization(ctx, ciB, relB.ID, "sha256:another"); err == nil {
		t.Error("expected normal authorization issuance to remain blocked after the rollback")
	}
}

func TestRollbackResultMarksStatusFailed(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	ci, original := h.satisfyGateAndDeployProduction(t, ctx, admin, dev, approver, proj, rel, "sha256:v1")

	out, err := h.deploymentsSvc.InitiateRollback(ctx, admin, rel.ID, deployments.InitiateRollbackInput{
		OriginalDeploymentID: original.ID, TargetDeploymentID: original.ID, Reason: "drill",
	})
	if err != nil {
		t.Fatalf("initiate rollback: %v", err)
	}

	d, err := h.deploymentsSvc.RecordFromCI(ctx, ci, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:v1", WorkflowRunURL: "https://ci.example/rollback-fail",
		Result: deployments.ResultFailure, DeploymentAuthorization: out.Token,
	})
	if err != nil {
		t.Fatalf("expected the rollback deployment attempt itself to be recorded even though it failed: %v", err)
	}

	rollbacks, err := h.deploymentsSvc.ListRollbacksByRelease(ctx, admin, rel.ID)
	if err != nil {
		t.Fatalf("list rollbacks: %v", err)
	}
	if len(rollbacks) != 1 || rollbacks[0].Status != deployments.RollbackFailed {
		t.Fatalf("expected one failed rollback, got: %+v", rollbacks)
	}
	if rollbacks[0].ResultingDeploymentID == nil || *rollbacks[0].ResultingDeploymentID != d.ID {
		t.Error("expected resulting_deployment_id to be set even for a failed rollback attempt")
	}
}

func TestVerifyRollbackRequiresExecutedRollback(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	ci, original := h.satisfyGateAndDeployProduction(t, ctx, admin, dev, approver, proj, rel, "sha256:v1")

	out, err := h.deploymentsSvc.InitiateRollback(ctx, admin, rel.ID, deployments.InitiateRollbackInput{
		OriginalDeploymentID: original.ID, TargetDeploymentID: original.ID, Reason: "drill",
	})
	if err != nil {
		t.Fatalf("initiate rollback: %v", err)
	}

	if _, err := h.deploymentsSvc.VerifyRollback(ctx, admin, rel.ID, out.Rollback.ID, "looks fine"); err == nil {
		t.Error("expected verifying a not-yet-executed rollback to be rejected")
	}

	if _, err := h.deploymentsSvc.RecordFromCI(ctx, ci, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:v1", WorkflowRunURL: "https://ci.example/rollback",
		Result: deployments.ResultSuccess, DeploymentAuthorization: out.Token,
	}); err != nil {
		t.Fatalf("record rollback deployment: %v", err)
	}

	if _, err := h.deploymentsSvc.VerifyRollback(ctx, admin, rel.ID, out.Rollback.ID, ""); err == nil {
		t.Error("expected an empty verification note to be rejected")
	}

	verified, err := h.deploymentsSvc.VerifyRollback(ctx, admin, rel.ID, out.Rollback.ID, "confirmed the regression is gone")
	if err != nil {
		t.Fatalf("verify rollback: %v", err)
	}
	if verified.VerifiedBy == nil || *verified.VerifiedBy != admin.User.ID {
		t.Error("expected verified_by to be set to the verifying user")
	}
	if verified.VerifiedAt == nil {
		t.Error("expected verified_at to be set")
	}
}

func TestInitiateRollbackRequiresManagePermission(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)
	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)
	_, original := h.satisfyGateAndDeployProduction(t, ctx, admin, dev, approver, proj, rel, "sha256:v1")

	if _, err := h.deploymentsSvc.InitiateRollback(ctx, dev, rel.ID, deployments.InitiateRollbackInput{
		OriginalDeploymentID: original.ID, TargetDeploymentID: original.ID, Reason: "drill",
	}); err == nil {
		t.Error("expected a developer (not an admin or release coordinator) to be forbidden from initiating a rollback")
	}
}

func TestEmergencyPolicyFloorEnforced(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	proj := h.createProject(t, ctx, admin)

	if _, err := h.projectsSvc.UpdateEmergencyPolicy(ctx, admin, proj.ID, projects.UpdateEmergencyPolicyInput{
		RequiredApprovalsCount: 0, RequireRetrospective: true,
	}); err == nil {
		t.Error("expected required_approvals_count of 0 to be rejected: an emergency changes the bar, it never removes it")
	}

	ep, err := h.projectsSvc.UpdateEmergencyPolicy(ctx, admin, proj.ID, projects.UpdateEmergencyPolicyInput{
		RequiredApprovalsCount: 2, RequireRetrospective: false,
	})
	if err != nil {
		t.Fatalf("update emergency policy: %v", err)
	}
	if ep.RequiredApprovalsCount != 2 || ep.RequireRetrospective {
		t.Errorf("unexpected emergency policy after update: %+v", ep)
	}
}

func TestEmergencyDeclarationOverridesRequiredApprovalsLive(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	approver := h.createUser(t, ctx, users.RoleApprover)
	proj := h.createProject(t, ctx, admin, dev, approver)

	if _, err := h.projectsSvc.UpdatePolicy(ctx, admin, proj.ID, projects.UpdatePolicyInput{
		RequiredEvidenceTypes: []string{}, RequiredApprovalsCount: 2,
	}); err != nil {
		t.Fatalf("update policy: %v", err)
	}

	rel := h.createRelease(t, ctx, dev, proj.ID)
	h.advanceToReadyForProduction(t, ctx, dev, rel.ID)

	result, err := h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Eligible {
		t.Fatal("expected a release needing 2 approvals with 0 to be ineligible")
	}
	if result.RequiredApprovalsCount != 2 {
		t.Errorf("expected required approvals count 2 before any emergency declaration, got %d", result.RequiredApprovalsCount)
	}

	// Declaring an emergency on an already-ready_for_production release
	// (whose policy is already frozen into a snapshot) takes effect
	// immediately, because IsEmergency is read live, never from the
	// snapshot.
	if _, err := h.releasesSvc.DeclareEmergency(ctx, admin, rel.ID, "prod checkout is down, customers cannot pay"); err != nil {
		t.Fatalf("declare emergency: %v", err)
	}

	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result.IsEmergency {
		t.Error("expected the gate result to reflect emergency status")
	}
	if result.RequiredApprovalsCount != 1 {
		t.Errorf("expected emergency to drop required approvals to the floor of 1, got %d", result.RequiredApprovalsCount)
	}
	if result.Eligible {
		t.Fatal("expected still-ineligible with 0 approvals even at the emergency floor of 1")
	}

	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "emergency approval"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	result, err = h.gateSvc.Evaluate(ctx, rel.ID)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !result.Eligible {
		t.Fatalf("expected eligible after 1 approval under the emergency floor, blockers: %v", result.Blockers)
	}
}

func TestEmergencyReleaseCannotCloseWithoutRetrospective(t *testing.T) {
	ctx, pool := setup(t)
	h := newHarness(pool)
	admin := h.createUser(t, ctx, users.RoleAdmin)
	dev := h.createUser(t, ctx, users.RoleDeveloper)
	proj := h.createProject(t, ctx, admin, dev)
	rel := h.createRelease(t, ctx, dev, proj.ID)

	if _, err := h.releasesSvc.DeclareEmergency(ctx, admin, rel.ID, "sev1 outage"); err != nil {
		t.Fatalf("declare emergency: %v", err)
	}

	if _, err := h.releasesSvc.Close(ctx, admin, rel.ID, "done"); err == nil {
		t.Error("expected close to be blocked until the emergency retrospective is completed")
	}

	if _, err := h.releasesSvc.CompleteRetrospective(ctx, admin, rel.ID, "root cause: bad config push, fixed by revert"); err != nil {
		t.Fatalf("complete retrospective: %v", err)
	}

	if _, err := h.releasesSvc.Close(ctx, admin, rel.ID, "done"); err != nil {
		t.Fatalf("expected close to succeed after retrospective completed: %v", err)
	}
}
