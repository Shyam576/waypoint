package mcpnotes

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/httpx"
)

type Handlers struct {
	Svc *Service
}

func NewHandlers(svc *Service) *Handlers { return &Handlers{Svc: svc} }

func mustIdentity(w http.ResponseWriter, r *http.Request) (authn.Identity, bool) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return authn.Identity{}, false
	}
	return identity, true
}

func releaseIDParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "releaseID"))
	if err != nil {
		return uuid.UUID{}, httpx.BadRequest("invalid releaseID")
	}
	return id, nil
}

// --- Web: direct authorship ---

func (h *Handlers) CreateChangeNoteWeb(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	releaseID, err := releaseIDParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var p ChangeNotePayload
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Error(w, r, err)
		return
	}
	c, err := h.Svc.CreateChangeNoteWeb(r.Context(), identity, releaseID, p)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, c)
}

func (h *Handlers) ListChangeNotes(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	releaseID, err := releaseIDParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	list, err := h.Svc.ListChangeNotes(r.Context(), identity, releaseID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

func (h *Handlers) CreateDesignDecisionWeb(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	releaseID, err := releaseIDParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var p DesignDecisionPayload
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Error(w, r, err)
		return
	}
	d, err := h.Svc.CreateDesignDecisionWeb(r.Context(), identity, releaseID, p)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, d)
}

func (h *Handlers) ListDesignDecisions(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	releaseID, err := releaseIDParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	list, err := h.Svc.ListDesignDecisions(r.Context(), identity, releaseID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

// --- Web: reviewing pending MCP submissions ---

func (h *Handlers) ListPending(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	releaseID, err := releaseIDParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var status *SubmissionStatus
	if v := r.URL.Query().Get("status"); v != "" {
		s := SubmissionStatus(v)
		status = &s
	}
	list, err := h.Svc.ListPending(r.Context(), identity, releaseID, status)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

type reviewRequest struct {
	ReviewNote string `json:"review_note"`
}

func (h *Handlers) Accept(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "submissionID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid submissionID"))
		return
	}
	var req reviewRequest
	_ = httpx.Decode(r, &req)
	sub, err := h.Svc.Accept(r.Context(), identity, id, req.ReviewNote)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, sub)
}

func (h *Handlers) Reject(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "submissionID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid submissionID"))
		return
	}
	var req reviewRequest
	_ = httpx.Decode(r, &req)
	sub, err := h.Svc.Reject(r.Context(), identity, id, req.ReviewNote)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, sub)
}

// --- MCP token-authenticated submission endpoints, used by cmd/mcp ---

func (h *Handlers) SubmitChangeNoteMCP(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	releaseID, err := releaseIDParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var p ChangeNotePayload
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Error(w, r, err)
		return
	}
	sub, err := h.Svc.SubmitChangeNote(r.Context(), identity, releaseID, p)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, sub)
}

func (h *Handlers) SubmitDesignDecisionMCP(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	releaseID, err := releaseIDParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var p DesignDecisionPayload
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Error(w, r, err)
		return
	}
	sub, err := h.Svc.SubmitDesignDecision(r.Context(), identity, releaseID, p)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, sub)
}

func (h *Handlers) SubmitSupportNoteMCP(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	releaseID, err := releaseIDParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var p SupportNotePayload
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Error(w, r, err)
		return
	}
	sub, err := h.Svc.SubmitSupportNote(r.Context(), identity, releaseID, p)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, sub)
}
