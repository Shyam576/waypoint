package projects

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

func parseUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	raw := chi.URLParam(r, name)
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.UUID{}, httpx.BadRequest("invalid " + name)
	}
	return id, nil
}

type createProjectRequest struct {
	Name       string  `json:"name"`
	OwningTeam string  `json:"owning_team"`
	RepoURL    *string `json:"repo_url"`
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	var req createProjectRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	p, err := h.Svc.Create(r.Context(), identity, CreateInput{Name: req.Name, OwningTeam: req.OwningTeam, RepoURL: req.RepoURL})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, p)
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	list, err := h.Svc.List(r.Context(), identity)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "projectID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	p, err := h.Svc.Get(r.Context(), identity, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

type updateProjectRequest struct {
	Name       string  `json:"name"`
	OwningTeam string  `json:"owning_team"`
	RepoURL    *string `json:"repo_url"`
	Status     Status  `json:"status"`
}

func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "projectID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req updateProjectRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	p, err := h.Svc.Update(r.Context(), identity, id, UpdateInput{Name: req.Name, OwningTeam: req.OwningTeam, RepoURL: req.RepoURL, Status: req.Status})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

func (h *Handlers) GetPolicy(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "projectID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	p, err := h.Svc.GetPolicy(r.Context(), identity, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

type updatePolicyRequest struct {
	RequiredEvidenceTypes  []string `json:"required_evidence_types"`
	RequiredApprovalsCount int      `json:"required_approvals_count"`
	BlockOnOpenRejection   bool     `json:"block_on_open_rejection"`
}

func (h *Handlers) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "projectID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req updatePolicyRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	p, err := h.Svc.UpdatePolicy(r.Context(), identity, id, UpdatePolicyInput{
		RequiredEvidenceTypes: req.RequiredEvidenceTypes, RequiredApprovalsCount: req.RequiredApprovalsCount, BlockOnOpenRejection: req.BlockOnOpenRejection,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

type addMemberRequest struct {
	UserID uuid.UUID `json:"user_id"`
}

func (h *Handlers) AddMember(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "projectID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req addMemberRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.Svc.AddMember(r.Context(), identity, id, req.UserID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"status": "added"})
}

func (h *Handlers) ListMembers(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "projectID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	list, err := h.Svc.ListMembers(r.Context(), identity, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}
