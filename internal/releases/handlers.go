package releases

import (
	"net/http"
	"time"

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

func parseUUID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.UUID{}, httpx.BadRequest("invalid " + name)
	}
	return id, nil
}

type createReleaseRequest struct {
	OwnerID           *uuid.UUID `json:"owner_id"`
	Name              string     `json:"name"`
	TargetDate        *string    `json:"target_date"`
	RiskLevel         RiskLevel  `json:"risk_level"`
	RiskCategories    []string   `json:"risk_categories"`
	Summary           string     `json:"summary"`
	RollbackPlan      string     `json:"rollback_plan"`
	SupportPeriodDays int        `json:"support_period_days"`
}

func parseDate(s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return nil, httpx.BadRequest("target_date must be YYYY-MM-DD")
	}
	return &t, nil
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	projectID, err := parseUUID(r, "projectID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req createReleaseRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	targetDate, err := parseDate(req.TargetDate)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	ownerID := identity.User.ID
	if req.OwnerID != nil {
		ownerID = *req.OwnerID
	}
	rel, err := h.Svc.Create(r.Context(), identity, projectID, CreateInput{
		OwnerID: ownerID, Name: req.Name, TargetDate: targetDate, RiskLevel: req.RiskLevel, RiskCategories: req.RiskCategories,
		Summary: req.Summary, RollbackPlan: req.RollbackPlan, SupportPeriodDays: req.SupportPeriodDays,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, rel)
}

func (h *Handlers) ListByProject(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	projectID, err := parseUUID(r, "projectID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var stage *Stage
	if v := r.URL.Query().Get("stage"); v != "" {
		s := Stage(v)
		stage = &s
	}
	list, err := h.Svc.ListByProject(r.Context(), identity, projectID, stage)
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
	id, err := parseUUID(r, "releaseID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	rel, err := h.Svc.Get(r.Context(), identity, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rel)
}

type updateReleaseRequest struct {
	Name              string    `json:"name"`
	TargetDate        *string   `json:"target_date"`
	RiskLevel         RiskLevel `json:"risk_level"`
	RiskCategories    []string  `json:"risk_categories"`
	Summary           string    `json:"summary"`
	RollbackPlan      string    `json:"rollback_plan"`
	SupportPeriodDays int       `json:"support_period_days"`
}

func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUID(r, "releaseID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req updateReleaseRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	targetDate, err := parseDate(req.TargetDate)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	rel, err := h.Svc.UpdateMeta(r.Context(), identity, id, UpdateParams{
		Name: req.Name, TargetDate: targetDate, RiskLevel: req.RiskLevel, RiskCategories: req.RiskCategories,
		Summary: req.Summary, RollbackPlan: req.RollbackPlan, SupportPeriodDays: req.SupportPeriodDays,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rel)
}

type transitionRequest struct {
	ToStage Stage  `json:"to_stage"`
	Reason  string `json:"reason"`
}

func (h *Handlers) Transition(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUID(r, "releaseID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req transitionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	rel, err := h.Svc.Transition(r.Context(), identity, id, req.ToStage, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rel)
}

type closeRequest struct {
	Reason string `json:"reason"`
}

func (h *Handlers) Close(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUID(r, "releaseID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req closeRequest
	_ = httpx.Decode(r, &req)
	rel, err := h.Svc.Close(r.Context(), identity, id, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rel)
}

type createLinkRequest struct {
	LinkType LinkType `json:"link_type"`
	URL      string   `json:"url"`
	Label    *string  `json:"label"`
}

func (h *Handlers) CreateLink(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUID(r, "releaseID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req createLinkRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	l, err := h.Svc.CreateLink(r.Context(), identity, id, req.LinkType, req.URL, req.Label)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, l)
}

func (h *Handlers) ListLinks(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUID(r, "releaseID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	list, err := h.Svc.ListLinks(r.Context(), identity, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

type acceptRiskRequest struct {
	Note string `json:"note"`
}

func (h *Handlers) AcceptRisk(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUID(r, "releaseID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req acceptRiskRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, err := h.Svc.AcceptRisk(r.Context(), identity, id, req.Note)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, a)
}

func (h *Handlers) GetRiskAcceptance(w http.ResponseWriter, r *http.Request) {
	identity, ok := mustIdentity(w, r)
	if !ok {
		return
	}
	id, err := parseUUID(r, "releaseID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, err := h.Svc.GetRiskAcceptance(r.Context(), identity, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if a == nil {
		httpx.Error(w, r, httpx.NotFound("this release's risk has not been accepted"))
		return
	}
	httpx.JSON(w, http.StatusOK, a)
}
