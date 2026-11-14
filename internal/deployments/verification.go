package deployments

import (
	"context"

	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/dbx"
	"github.com/Shyam576/waypoint/internal/httpx"
)

// SubmitVerification records a post-deployment health-check result
// (pending is the implicit default on every row and is never submitted
// explicitly). It only ever matches a row whose own Result is success
// — there is nothing to confirm healthy about a deployment that never
// went out — which the service layer checks up front for a clear error
// rather than relying on this returning zero rows.
func (s *Store) SubmitVerification(ctx context.Context, q dbx.Querier, id uuid.UUID, status VerificationStatus, verifiedBy *uuid.UUID, via VerifiedVia, note string) (Deployment, error) {
	row := q.QueryRow(ctx, `
		UPDATE deployments
		SET verification_status = $2, verified_by = $3, verified_via = $4, verified_at = now(), verification_note = $5
		WHERE id = $1 AND result = $6
		RETURNING `+deploymentColumns,
		id, status, verifiedBy, via, note, ResultSuccess,
	)
	return scanDeployment(row)
}

type SubmitVerificationInput struct {
	Status VerificationStatus
	Note   string
}

func (in SubmitVerificationInput) validate() error {
	if in.Status != VerificationPassed && in.Status != VerificationFailed {
		return httpx.BadRequest("status must be passed or failed")
	}
	if in.Note == "" {
		return httpx.BadRequest("a verification note is required")
	}
	return nil
}

// SubmitVerificationFromCI lets a CI pipeline report the result of its
// own automated post-deployment check (e.g. a smoke-test suite run
// after the deploy step exits 0) — this is what actually confirms the
// application is healthy, not just that the deploy command succeeded.
func (s *Service) SubmitVerificationFromCI(ctx context.Context, identity authn.Identity, releaseID, deploymentID uuid.UUID, in SubmitVerificationInput) (Deployment, error) {
	rel, err := s.resolveCIRelease(ctx, identity, releaseID)
	if err != nil {
		return Deployment{}, err
	}
	return s.submitVerification(ctx, identity, rel.ID, deploymentID, in, VerifiedViaCI, nil)
}

// SubmitVerificationFromWeb lets a human manually record that they
// checked and the deployment is, or isn't, actually healthy. Held to
// the same permission bar as confirming a checklist item
// (developer/coordinator/admin), not the stricter bar for recording a
// deployment in the first place.
func (s *Service) SubmitVerificationFromWeb(ctx context.Context, identity authn.Identity, releaseID, deploymentID uuid.UUID, in SubmitVerificationInput) (Deployment, error) {
	rel, err := s.releaseAcc.GetForService(ctx, identity, releaseID)
	if err != nil {
		return Deployment{}, err
	}
	ok, err := s.releaseAcc.CanEdit(ctx, identity, rel.ProjectID)
	if err != nil {
		return Deployment{}, err
	}
	if !ok {
		return Deployment{}, httpx.Forbidden("you do not have permission to verify deployments for this release")
	}
	return s.submitVerification(ctx, identity, rel.ID, deploymentID, in, VerifiedViaWeb, &identity.User.ID)
}

func (s *Service) submitVerification(ctx context.Context, identity authn.Identity, releaseID, deploymentID uuid.UUID, in SubmitVerificationInput, via VerifiedVia, verifiedBy *uuid.UUID) (Deployment, error) {
	if err := in.validate(); err != nil {
		return Deployment{}, err
	}
	before, err := s.store.GetByID(ctx, s.pool, deploymentID)
	if err != nil {
		return Deployment{}, httpx.NotFound("deployment not found")
	}
	if before.ReleaseID != releaseID {
		return Deployment{}, httpx.NotFound("deployment not found")
	}
	if before.Result != ResultSuccess {
		return Deployment{}, httpx.BadRequest("cannot verify a deployment that did not succeed")
	}
	after, err := s.store.SubmitVerification(ctx, s.pool, deploymentID, in.Status, verifiedBy, via, in.Note)
	if err != nil {
		return Deployment{}, err
	}
	_ = s.audit.Record(ctx, s.pool, "release", releaseID, "deployment_verification_recorded", identity.AuditActor(), before, after, string(in.Status))
	return after, nil
}
