package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Shyam576/waypoint/internal/approvals"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/candidates"
	"github.com/Shyam576/waypoint/internal/projects"
	"github.com/Shyam576/waypoint/internal/releases"
	"github.com/Shyam576/waypoint/internal/users"
)

func TestMCPReleaseContextIncludesChecklistAndCandidateState(t *testing.T) {
	ctx, srv := setupTestServer(t)
	admin := createTestUser(t, ctx, srv, users.RoleAdmin)
	dev := createTestUser(t, ctx, srv, users.RoleDeveloper)

	proj, err := srv.Projects.Create(ctx, admin, projects.CreateInput{
		Name: fmt.Sprintf("MCP Context Test %d", time.Now().UnixNano()), OwningTeam: "QA",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := srv.Projects.AddMember(ctx, admin, proj.ID, dev.User.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}

	rel, err := srv.Releases.Create(ctx, dev, proj.ID, releases.CreateInput{
		Name: "v1.0.0", RiskLevel: releases.RiskLow, Summary: "mcp context test", RollbackPlan: "revert",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}

	if _, err := srv.Candidates.RegisterFromWeb(ctx, dev, rel.ID, candidates.RegisterInput{ImageDigest: "sha256:v1"}); err != nil {
		t.Fatalf("register candidate: %v", err)
	}

	_, rawToken, err := srv.Auth.IssueMCPToken(ctx, dev.User.ID, "test-mcp-token")
	if err != nil {
		t.Fatalf("issue mcp token: %v", err)
	}

	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/mcp/releases/"+rel.ID.String()+"/context", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+rawToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out MCPReleaseContext
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.ChecklistItems) == 0 {
		t.Error("expected checklist_items to be populated from the project's default template")
	}
	if out.CurrentCandidate == nil || out.CurrentCandidate.ImageDigest != "sha256:v1" {
		t.Errorf("expected current_candidate to reflect the registered candidate, got %+v", out.CurrentCandidate)
	}
}

// TestMCPTokenCannotManageOrApprove is a regression test for the
// explicit, must-stay-true rule (brief / roadmap item 10) that nothing
// reachable from an MCP token can approve a release or change a
// policy. No MCP route wires to either action, so this calls the
// service methods directly with an MCP-via identity to prove the
// defense holds independent of routing too.
func TestMCPTokenCannotManageOrApprove(t *testing.T) {
	ctx, srv := setupTestServer(t)
	admin := createTestUser(t, ctx, srv, users.RoleAdmin)
	approver := createTestUser(t, ctx, srv, users.RoleApprover)

	proj, err := srv.Projects.Create(ctx, admin, projects.CreateInput{
		Name: fmt.Sprintf("MCP Boundary Test %d", time.Now().UnixNano()), OwningTeam: "QA",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := srv.Projects.AddMember(ctx, admin, proj.ID, approver.User.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}

	dev := createTestUser(t, ctx, srv, users.RoleDeveloper)
	if err := srv.Projects.AddMember(ctx, admin, proj.ID, dev.User.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	rel, err := srv.Releases.Create(ctx, dev, proj.ID, releases.CreateInput{
		Name: "v1.0.0", RiskLevel: releases.RiskLow, Summary: "mcp boundary test", RollbackPlan: "revert",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	for _, stage := range []releases.Stage{releases.StageDevelopment, releases.StageUAT, releases.StageReadyForProduction} {
		if _, err := srv.Releases.Transition(ctx, dev, rel.ID, stage, ""); err != nil {
			t.Fatalf("transition to %s: %v", stage, err)
		}
	}

	mcpAdmin := admin
	mcpAdmin.Via = authn.ViaMCP
	mcpApprover := approver
	mcpApprover.Via = authn.ViaMCP

	// Change a policy: admin's own MCP identity must still be refused.
	if _, err := srv.Projects.UpdatePolicy(ctx, mcpAdmin, proj.ID, projects.UpdatePolicyInput{RequiredEvidenceTypes: []string{}, RequiredApprovalsCount: 2}); err == nil {
		t.Error("expected an mcp-via identity to be forbidden from updating project policy, even for an admin user")
	}

	// Approve a release: an approver's own MCP identity must still be
	// refused.
	if _, err := srv.Approvals.Decide(ctx, mcpApprover, rel.ID, approvals.DecisionApproved, "approved via mcp"); err == nil {
		t.Error("expected an mcp-via identity to be forbidden from approving a release, even for an approver user")
	}

	// Confirm the same users can still do both things through their
	// normal web identity — the mcp check isn't accidentally blocking
	// everyone.
	if _, err := srv.Projects.UpdatePolicy(ctx, admin, proj.ID, projects.UpdatePolicyInput{RequiredEvidenceTypes: []string{}, RequiredApprovalsCount: 1}); err != nil {
		t.Errorf("expected the admin's web identity to still be able to update policy: %v", err)
	}
	if _, err := srv.Approvals.Decide(ctx, approver, rel.ID, approvals.DecisionApproved, "approved via web"); err != nil {
		t.Errorf("expected the approver's web identity to still be able to approve: %v", err)
	}
}
