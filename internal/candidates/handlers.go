package candidates

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

type registerRequest struct {
	GitRepo            string `json:"git_repo"`
	GitBranch          string `json:"git_branch"`
	CommitSHA          string `json:"commit_sha"`
	BuildID            string `json:"build_id"`
	ImageDigest        string `json:"image_digest"`
	AppVersion         string `json:"app_version"`
	ConfigChecksum     string `json:"config_checksum"`
	MigrationChecksum  string `json:"migration_checksum"`
	BuildProvenanceURL string `json:"build_provenance_url"`
}

func (req registerRequest) toInput() RegisterInput {
	return RegisterInput{
		GitRepo: req.GitRepo, GitBranch: req.GitBranch, CommitSHA: req.CommitSHA, BuildID: req.BuildID,
		ImageDigest: req.ImageDigest, AppVersion: req.AppVersion, ConfigChecksum: req.ConfigChecksum,
		MigrationChecksum: req.MigrationChecksum, BuildProvenanceURL: req.BuildProvenanceURL,
	}
}

// RegisterFromWeb handles POST /api/v1/releases/{releaseID}/candidates.
func (h *Handlers) RegisterFromWeb(w http.ResponseWriter, r *http.Request) {
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
	var req registerRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	c, err := h.Svc.RegisterFromWeb(r.Context(), identity, releaseID, req.toInput())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, c)
}

// RegisterFromCI handles
// POST /api/v1/ci/releases/{releaseID}/candidates.
func (h *Handlers) RegisterFromCI(w http.ResponseWriter, r *http.Request) {
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
	var req registerRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	c, err := h.Svc.RegisterFromCI(r.Context(), identity, releaseID, req.toInput())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, c)
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
