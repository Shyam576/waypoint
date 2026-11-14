package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/approvals"
	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/candidates"
	"github.com/Shyam576/waypoint/internal/deployments"
	"github.com/Shyam576/waypoint/internal/evidence"
	"github.com/Shyam576/waypoint/internal/gate"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/mcpnotes"
	"github.com/Shyam576/waypoint/internal/releases"
	"github.com/Shyam576/waypoint/internal/support"
)

// ReleaseDetail is the single payload the release detail page fetches:
// summary, changes, evidence, approvals, deployments, support items, and
// activity history, all in one request instead of eight.
type ReleaseDetail struct {
	Release         releases.Release          `json:"release"`
	Links           []releases.Link           `json:"links"`
	Candidates      []candidates.Candidate    `json:"candidates"`
	Evidence        []evidence.Evidence       `json:"evidence"`
	ChangeNotes     []mcpnotes.ChangeNote     `json:"change_notes"`
	DesignDecisions []mcpnotes.DesignDecision `json:"design_decisions"`
	Approvals       []approvals.Approval      `json:"approvals"`
	Deployments     []deployments.Deployment  `json:"deployments"`
	SupportItems    []support.Item            `json:"support_items"`
	PendingMCP      []mcpnotes.Submission     `json:"pending_mcp_submissions"`
	Eligibility     gate.Result               `json:"eligibility"`
	Activity        []audit.Entry             `json:"activity"`
}

func (s *Server) releaseDetail(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "releaseID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid releaseID"))
		return
	}

	rel, err := s.Releases.Get(r.Context(), identity, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	links, err := s.ReleasesStore.ListLinks(r.Context(), s.Pool, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	cands, err := s.CandidatesStore.ListByRelease(r.Context(), s.Pool, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	ev, err := s.EvidenceStore.ListByRelease(r.Context(), s.Pool, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	changeNotes, err := s.MCPNotesStore.ListChangeNotesByRelease(r.Context(), s.Pool, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	designDecisions, err := s.MCPNotesStore.ListDesignDecisionsByRelease(r.Context(), s.Pool, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	apprs, err := s.ApprovalsStore.ListByRelease(r.Context(), s.Pool, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	deps, err := s.DeploymentsStore.ListByRelease(r.Context(), s.Pool, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	supportItems, err := s.SupportStore.ListByRelease(r.Context(), s.Pool, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	pendingStatus := mcpnotes.SubmissionPending
	pending, err := s.MCPNotesStore.ListSubmissionsByRelease(r.Context(), s.Pool, id, &pendingStatus)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	eligibility, err := s.Gate.Evaluate(r.Context(), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	activity, err := s.Audit.ForEntity(r.Context(), s.Pool, "release", id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, ReleaseDetail{
		Release: rel, Links: nonNilLinks(links), Candidates: nonNilCandidates(cands), Evidence: nonNilEvidence(ev),
		ChangeNotes: nonNilChangeNotes(changeNotes), DesignDecisions: nonNilDesignDecisions(designDecisions),
		Approvals: nonNilApprovals(apprs), Deployments: nonNilDeployments(deps), SupportItems: nonNilSupport(supportItems),
		PendingMCP: nonNilSubmissions(pending), Eligibility: eligibility, Activity: nonNilActivity(activity),
	})
}

func nonNilLinks(v []releases.Link) []releases.Link {
	if v == nil {
		return []releases.Link{}
	}
	return v
}
func nonNilEvidence(v []evidence.Evidence) []evidence.Evidence {
	if v == nil {
		return []evidence.Evidence{}
	}
	return v
}
func nonNilCandidates(v []candidates.Candidate) []candidates.Candidate {
	if v == nil {
		return []candidates.Candidate{}
	}
	return v
}
func nonNilChangeNotes(v []mcpnotes.ChangeNote) []mcpnotes.ChangeNote {
	if v == nil {
		return []mcpnotes.ChangeNote{}
	}
	return v
}
func nonNilDesignDecisions(v []mcpnotes.DesignDecision) []mcpnotes.DesignDecision {
	if v == nil {
		return []mcpnotes.DesignDecision{}
	}
	return v
}
func nonNilApprovals(v []approvals.Approval) []approvals.Approval {
	if v == nil {
		return []approvals.Approval{}
	}
	return v
}
func nonNilDeployments(v []deployments.Deployment) []deployments.Deployment {
	if v == nil {
		return []deployments.Deployment{}
	}
	return v
}
func nonNilSupport(v []support.Item) []support.Item {
	if v == nil {
		return []support.Item{}
	}
	return v
}
func nonNilSubmissions(v []mcpnotes.Submission) []mcpnotes.Submission {
	if v == nil {
		return []mcpnotes.Submission{}
	}
	return v
}
func nonNilActivity(v []audit.Entry) []audit.Entry {
	if v == nil {
		return []audit.Entry{}
	}
	return v
}
