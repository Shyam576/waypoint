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
	authSvc := authn.NewService(authStore, usersStore, pool, time.Hour)
	projectsStore := projects.NewStore()
	projectsSvc := projects.NewService(pool, projectsStore, auditStore, usersStore)
	releasesStore := releases.NewStore()
	releasesSvc := releases.NewService(pool, releasesStore, auditStore, projectsSvc)
	evidenceStore := evidence.NewStore()
	evidenceSvc := evidence.NewService(pool, evidenceStore, auditStore, releasesSvc)
	candidatesStore := candidates.NewStore()
	candidatesSvc := candidates.NewService(pool, candidatesStore, auditStore, releasesStore, releasesSvc)
	approvalsStore := approvals.NewStore()
	approvalsSvc := approvals.NewService(pool, approvalsStore, auditStore, releasesSvc, projectsSvc, candidatesStore)
	gateSvc := gate.NewService(pool, releasesStore, evidenceStore, approvalsStore, projectsStore, candidatesStore)
	deploymentsStore := deployments.NewStore()
	deploymentsSvc := deployments.NewService(pool, deploymentsStore, auditStore, gateSvc, releasesStore, releasesSvc, releasesSvc, projectsSvc, candidatesStore, 15*time.Minute)
	supportStore := support.NewStore()
	supportSvc := support.NewService(pool, supportStore, auditStore, releasesSvc)
	mcpNotesStore := mcpnotes.NewStore()
	mcpNotesSvc := mcpnotes.NewService(pool, mcpNotesStore, auditStore, releasesSvc, supportSvc)

	return &harness{
		pool: pool, usersStore: usersStore, authSvc: authSvc, projectsSvc: projectsSvc, releasesSvc: releasesSvc,
		evidenceSvc: evidenceSvc, candidatesSvc: candidatesSvc, approvalsSvc: approvalsSvc, gateSvc: gateSvc,
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
