package deployments

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

type recordRequest struct {
	Environment    Environment `json:"environment"`
	ArtifactDigest string      `json:"artifact_digest"`
	ArtifactRef    *string     `json:"artifact_ref"`
	CommitSHA      *string     `json:"commit_sha"`
	WorkflowRunURL string      `json:"workflow_run_url"`
	Result         Result      `json:"result"`
	OccurredAt     *time.Time  `json:"occurred_at"`
}

func (req recordRequest) toInput() RecordInput {
	occurredAt := time.Now()
	if req.OccurredAt != nil {
		occurredAt = *req.OccurredAt
	}
	return RecordInput{
		Environment: req.Environment, ArtifactDigest: req.ArtifactDigest, ArtifactRef: req.ArtifactRef,
		CommitSHA: req.CommitSHA, WorkflowRunURL: req.WorkflowRunURL, Result: req.Result, OccurredAt: occurredAt,
	}
}

// RecordFromCI handles POST /api/v1/ci/releases/{releaseID}/deployments,
// authenticated with a project-scoped CI token.
func (h *Handlers) RecordFromCI(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("ci token required"))
		return
	}
	releaseID, err := uuid.Parse(chi.URLParam(r, "releaseID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid releaseID"))
		return
	}
	var req recordRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	d, err := h.Svc.RecordFromCI(r.Context(), identity, releaseID, req.toInput())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, d)
}

// EligibilityForCI handles GET /api/v1/ci/releases/{releaseID}/eligibility,
// the check a pipeline must call before attempting a production deploy.
func (h *Handlers) EligibilityForCI(w http.ResponseWriter, r *http.Request) {
	identity, ok := authn.IdentityFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized("ci token required"))
		return
	}
	releaseID, err := uuid.Parse(chi.URLParam(r, "releaseID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid releaseID"))
		return
	}
	result, err := h.Svc.EligibilityForCI(r.Context(), identity, releaseID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

// RecordFromWeb handles POST /api/v1/releases/{releaseID}/deployments for
// admins/release coordinators recording or backfilling a deployment from
// the web app.
func (h *Handlers) RecordFromWeb(w http.ResponseWriter, r *http.Request) {
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
	var req recordRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	d, err := h.Svc.RecordFromWeb(r.Context(), identity, releaseID, req.toInput())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, d)
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
