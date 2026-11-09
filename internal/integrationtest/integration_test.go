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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shyam576/waypoint/internal/approvals"
	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
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
	approvalsStore := approvals.NewStore()
	approvalsSvc := approvals.NewService(pool, approvalsStore, auditStore, releasesSvc, projectsSvc)
	gateSvc := gate.NewService(pool, releasesStore, evidenceStore, approvalsStore, projectsStore)
	deploymentsStore := deployments.NewStore()
	deploymentsSvc := deployments.NewService(pool, deploymentsStore, auditStore, gateSvc, releasesStore, releasesSvc, releasesSvc, projectsSvc)
	supportStore := support.NewStore()
	supportSvc := support.NewService(pool, supportStore, auditStore, releasesSvc)
	mcpNotesStore := mcpnotes.NewStore()
	mcpNotesSvc := mcpnotes.NewService(pool, mcpNotesStore, auditStore, releasesSvc, supportSvc)

	return &harness{
		pool: pool, usersStore: usersStore, authSvc: authSvc, projectsSvc: projectsSvc, releasesSvc: releasesSvc,
		evidenceSvc: evidenceSvc, approvalsSvc: approvalsSvc, gateSvc: gateSvc,
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

	_, err := h.deploymentsSvc.RecordFromCI(ctx, ciIdentity, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:deadbeef", WorkflowRunURL: "https://ci.example/run/1", Result: deployments.ResultSuccess,
	})
	if err == nil {
		t.Fatal("expected production deployment to be rejected: release is not eligible yet")
	}

	for _, typ := range []evidence.Type{evidence.TypeChangeSummary, evidence.TypeTestResults, evidence.TypeUATSignoff, evidence.TypeRollbackPlan} {
		if _, err := h.evidenceSvc.Submit(ctx, dev, rel.ID, typ, string(typ), "ok", nil); err != nil {
			t.Fatalf("submit evidence: %v", err)
		}
	}
	if _, err := h.approvalsSvc.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "go"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	d, err := h.deploymentsSvc.RecordFromCI(ctx, ciIdentity, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:deadbeef", WorkflowRunURL: "https://ci.example/run/2", Result: deployments.ResultSuccess,
	})
	if err != nil {
		t.Fatalf("expected eligible production deployment to succeed: %v", err)
	}
	if d.ArtifactDigest != "sha256:deadbeef" {
		t.Errorf("artifact digest not stored correctly: %s", d.ArtifactDigest)
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
	if _, err := h.deploymentsSvc.RecordFromCI(ctx, wrongScopeCI, rel.ID, deployments.RecordInput{
		Environment: deployments.EnvProduction, ArtifactDigest: "sha256:other", WorkflowRunURL: "https://ci.example/run/3", Result: deployments.ResultSuccess,
	}); err == nil {
		t.Error("expected a ci token scoped to a different project to be rejected")
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
