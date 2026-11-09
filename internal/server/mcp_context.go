package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/mcpnotes"
	"github.com/Shyam576/waypoint/internal/releases"
)

// MCPReleaseContext is the payload behind the MCP server's
// get_release_context tool: enough for Claude to understand where a
// release stands and what is already linked to it, without exposing raw
// audit internals or anything beyond what the authenticated developer
// could already see in the web app.
type MCPReleaseContext struct {
	Release                releases.Release          `json:"release"`
	Links                  []releases.Link           `json:"links"`
	EvidenceTypesSubmitted []string                  `json:"evidence_types_submitted"`
	ApprovedCount          int                       `json:"approved_count"`
	Eligibility            map[string]any            `json:"eligibility"`
	ChangeNotes            []mcpnotes.ChangeNote     `json:"change_notes"`
	DesignDecisions        []mcpnotes.DesignDecision `json:"design_decisions"`
	PendingSubmissions     int                       `json:"pending_submissions"`
}

// mcpReleaseContext handles GET /api/v1/mcp/releases/{releaseID}/context,
// authenticated with a per-user MCP token.
func (s *Server) mcpReleaseContext(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("mcp token required"))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "releaseID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid releaseID"))
		return
	}

	rel, err := s.Releases.GetForService(r.Context(), identity, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	links, err := s.ReleasesStore.ListLinks(r.Context(), s.Pool, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	present, err := s.EvidenceStore.PresentTypes(r.Context(), s.Pool, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var evidenceTypes []string
	for t := range present {
		evidenceTypes = append(evidenceTypes, t)
	}
	latest, err := s.ApprovalsStore.LatestPerApprover(r.Context(), s.Pool, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	approvedCount := 0
	for _, a := range latest {
		if a.Decision == "approved" {
			approvedCount++
		}
	}
	eligibility, err := s.Gate.Evaluate(r.Context(), id)
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
	pendingStatus := mcpnotes.SubmissionPending
	pending, err := s.MCPNotesStore.ListSubmissionsByRelease(r.Context(), s.Pool, id, &pendingStatus)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, MCPReleaseContext{
		Release: rel, Links: nonNilLinks(links), EvidenceTypesSubmitted: evidenceTypes, ApprovedCount: approvedCount,
		Eligibility: map[string]any{"eligible": eligibility.Eligible, "blockers": eligibility.Blockers, "stage": eligibility.Stage},
		ChangeNotes: nonNilChangeNotes(changeNotes), DesignDecisions: nonNilDesignDecisions(designDecisions),
		PendingSubmissions: len(pending),
	})
}
