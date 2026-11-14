package checklists

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

type templateItemRequest struct {
	Category           Category           `json:"category"`
	Title              string             `json:"title"`
	Description        string             `json:"description"`
	IsRequired         bool               `json:"is_required"`
	VerificationMethod VerificationMethod `json:"verification_method"`
	SortOrder          int                `json:"sort_order"`
}

type createTemplateRequest struct {
	Items []templateItemRequest `json:"items"`
}

func (h *Handlers) CreateTemplateVersion(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid projectID"))
		return
	}
	var req createTemplateRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	items := make([]TemplateItemInput, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, TemplateItemInput{
			Category: it.Category, Title: it.Title, Description: it.Description,
			IsRequired: it.IsRequired, VerificationMethod: it.VerificationMethod, SortOrder: it.SortOrder,
		})
	}
	t, err := h.Svc.CreateTemplateVersion(r.Context(), identity, projectID, items)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, t)
}

func (h *Handlers) GetActiveTemplate(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid projectID"))
		return
	}
	t, err := h.Svc.GetActiveTemplate(r.Context(), identity, projectID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if t == nil {
		httpx.Error(w, r, httpx.NotFound("this project has no checklist template"))
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

func (h *Handlers) ListTemplateVersions(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid projectID"))
		return
	}
	list, err := h.Svc.ListTemplateVersions(r.Context(), identity, projectID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

func (h *Handlers) ListByRelease(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
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

type assignOwnerRequest struct {
	OwnerID uuid.UUID `json:"owner_id"`
}

func (h *Handlers) AssignOwner(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "itemID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid itemID"))
		return
	}
	var req assignOwnerRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	it, err := h.Svc.AssignOwner(r.Context(), identity, itemID, req.OwnerID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

type verifyRequest struct {
	EvidenceRef string `json:"evidence_ref"`
}

func (h *Handlers) Verify(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "itemID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid itemID"))
		return
	}
	var req verifyRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	it, err := h.Svc.Verify(r.Context(), identity, itemID, req.EvidenceRef)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

type reasonRequest struct {
	Reason string `json:"reason"`
}

func (h *Handlers) Reject(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "itemID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid itemID"))
		return
	}
	var req reasonRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	it, err := h.Svc.Reject(r.Context(), identity, itemID, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}

func (h *Handlers) Waive(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "itemID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid itemID"))
		return
	}
	var req reasonRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	it, err := h.Svc.Waive(r.Context(), identity, itemID, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, it)
}
