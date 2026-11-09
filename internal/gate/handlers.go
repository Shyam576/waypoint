package gate

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/releases"
)

type ReleaseAccess interface {
	GetForService(ctx context.Context, identity authn.Identity, id uuid.UUID) (releases.Release, error)
}

type Handlers struct {
	Svc      *Service
	Releases ReleaseAccess
}

func NewHandlers(svc *Service, releaseAccess ReleaseAccess) *Handlers {
	return &Handlers{Svc: svc, Releases: releaseAccess}
}

// Eligibility serves GET /api/v1/releases/{releaseID}/eligibility for
// signed-in web users; visibility is enforced via the normal project
// membership check. The CI-scoped equivalent lives in internal/deployments,
// which checks the release's project against the CI token's project
// instead of a user's project membership.
func (h *Handlers) Eligibility(w http.ResponseWriter, r *http.Request) {
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
	if _, err := h.Releases.GetForService(r.Context(), identity, releaseID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	result, err := h.Svc.Evaluate(r.Context(), releaseID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
