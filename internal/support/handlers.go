package support

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

type createRequest struct {
	Summary         string     `json:"summary"`
	Severity        Severity   `json:"severity"`
	OwnerID         *uuid.UUID `json:"owner_id"`
	LinkedTicketURL *string    `json:"linked_ticket_url"`
	LinkedPRURL     *string    `json:"linked_pr_url"`
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	releaseID, err := uuid.Parse(chi.URLParam(r, "releaseID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid releaseID"))
		return
	}
	var req createRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	it, err := h.Svc.Create(r.Context(), identity, releaseID, CreateInput{
		Summary: req.Summary, Severity: req.Severity, OwnerID: req.OwnerID,
		LinkedTicketURL: req.LinkedTicketURL, LinkedPRURL: req.LinkedPRURL,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, it)
}

func (h *Handlers) ListByRelease(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	releaseID, err := uuid.Parse(chi.URLParam(r, "releaseID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid releaseID"))
		return
	}
	list, err := h.Svc.ListByRelease(r.Context(), identity, releaseID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

type updateRequest struct {
	Severity        Severity   `json:"severity"`
	Status          ItemStatus `json:"status"`
	OwnerID         *uuid.UUID `json:"owner_id"`
	LinkedTicketURL *string    `json:"linked_ticket_url"`
	LinkedPRURL     *string    `json:"linked_pr_url"`
	ResolutionNote  string     `json:"resolution_note"`
}

func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("sign in required"))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "supportItemID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid supportItemID"))
		return
	}
	var req updateRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	it, err := h.Svc.Update(r.Context(), identity, id, UpdateInput{
		Severity: req.Severity, Status: req.Status, OwnerID: req.OwnerID,
		LinkedTicketURL: req.LinkedTicketURL, LinkedPRURL: req.LinkedPRURL, ResolutionNote: req.ResolutionNote,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}
