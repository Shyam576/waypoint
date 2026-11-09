package approvals

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

type decideRequest struct {
	Decision Decision `json:"decision"`
	Comment  string   `json:"comment"`
}

func (h *Handlers) Decide(w http.ResponseWriter, r *http.Request) {
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
	var req decideRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, err := h.Svc.Decide(r.Context(), identity, releaseID, req.Decision, req.Comment)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, a)
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
