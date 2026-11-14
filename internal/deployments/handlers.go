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
	Environment                  Environment `json:"environment"`
	ArtifactDigest               string      `json:"artifact_digest"`
	ArtifactRef                  *string     `json:"artifact_ref"`
	CommitSHA                    *string     `json:"commit_sha"`
	WorkflowRunURL               string      `json:"workflow_run_url"`
	Result                       Result      `json:"result"`
	OccurredAt                   *time.Time  `json:"occurred_at"`
	DeploymentAuthorizationToken string      `json:"deployment_authorization_token"`
}

func (req recordRequest) toInput() RecordInput {
	occurredAt := time.Now()
	if req.OccurredAt != nil {
		occurredAt = *req.OccurredAt
	}
	return RecordInput{
		Environment: req.Environment, ArtifactDigest: req.ArtifactDigest, ArtifactRef: req.ArtifactRef,
		CommitSHA: req.CommitSHA, WorkflowRunURL: req.WorkflowRunURL, Result: req.Result, OccurredAt: occurredAt,
		DeploymentAuthorization: req.DeploymentAuthorizationToken,
	}
}

type issueAuthorizationRequest struct {
	ArtifactDigest string `json:"artifact_digest"`
}

// IssueAuthorization handles
// POST /api/v1/ci/releases/{releaseID}/deployment-authorizations. A CI
// pipeline calls this once it knows the exact artifact digest it intends
// to deploy; it only succeeds when the gate currently says the release
// is eligible, and the returned token must be presented back to
// RecordFromCI for the same release and digest.
func (h *Handlers) IssueAuthorization(w http.ResponseWriter, r *http.Request) {
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
	var req issueAuthorizationRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	auth, token, err := h.Svc.IssueAuthorization(r.Context(), identity, releaseID, req.ArtifactDigest)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"authorization": auth, "token": token})
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

type initiateRollbackRequest struct {
	OriginalDeploymentID uuid.UUID `json:"original_deployment_id"`
	TargetDeploymentID   uuid.UUID `json:"target_deployment_id"`
	Reason               string    `json:"reason"`
}

// InitiateRollback handles
// POST /api/v1/releases/{releaseID}/rollbacks for admins/release
// coordinators. It mints a single-use deployment authorization token
// (returned in the response) that CI must present to the normal
// POST .../deployments endpoint to actually execute the rollback.
func (h *Handlers) InitiateRollback(w http.ResponseWriter, r *http.Request) {
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
	var req initiateRollbackRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.Svc.InitiateRollback(r.Context(), identity, releaseID, InitiateRollbackInput{
		OriginalDeploymentID: req.OriginalDeploymentID, TargetDeploymentID: req.TargetDeploymentID, Reason: req.Reason,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

type verifyRollbackRequest struct {
	Note string `json:"note"`
}

// VerifyRollback handles
// POST /api/v1/releases/{releaseID}/rollbacks/{rollbackID}/verify.
func (h *Handlers) VerifyRollback(w http.ResponseWriter, r *http.Request) {
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
	rollbackID, err := uuid.Parse(chi.URLParam(r, "rollbackID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid rollbackID"))
		return
	}
	var req verifyRollbackRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	rb, err := h.Svc.VerifyRollback(r.Context(), identity, releaseID, rollbackID, req.Note)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rb)
}

type submitVerificationRequest struct {
	Status VerificationStatus `json:"status"`
	Note   string             `json:"note"`
}

// SubmitVerificationFromCI handles
// POST /api/v1/ci/releases/{releaseID}/deployments/{deploymentID}/verification.
func (h *Handlers) SubmitVerificationFromCI(w http.ResponseWriter, r *http.Request) {
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
	deploymentID, err := uuid.Parse(chi.URLParam(r, "deploymentID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid deploymentID"))
		return
	}
	var req submitVerificationRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	d, err := h.Svc.SubmitVerificationFromCI(r.Context(), identity, releaseID, deploymentID, SubmitVerificationInput{Status: req.Status, Note: req.Note})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}

// SubmitVerificationFromWeb handles
// POST /api/v1/releases/{releaseID}/deployments/{deploymentID}/verification.
func (h *Handlers) SubmitVerificationFromWeb(w http.ResponseWriter, r *http.Request) {
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
	deploymentID, err := uuid.Parse(chi.URLParam(r, "deploymentID"))
	if err != nil {
		httpx.Error(w, r, httpx.BadRequest("invalid deploymentID"))
		return
	}
	var req submitVerificationRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	d, err := h.Svc.SubmitVerificationFromWeb(r.Context(), identity, releaseID, deploymentID, SubmitVerificationInput{Status: req.Status, Note: req.Note})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}

// ListRollbacksByRelease handles GET /api/v1/releases/{releaseID}/rollbacks.
func (h *Handlers) ListRollbacksByRelease(w http.ResponseWriter, r *http.Request) {
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
	list, err := h.Svc.ListRollbacksByRelease(r.Context(), identity, releaseID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}
