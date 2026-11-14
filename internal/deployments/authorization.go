package deployments

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/dbx"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/releases"
)

// Authorization is a short-lived, single-use credential scoped to one
// release and one exact artifact digest. A CI pipeline must obtain one
// (minting only succeeds when the gate currently says the release is
// eligible) and present it back to RecordFromCI for that same
// release+digest; it is consumed atomically on first use, so it can
// never authorize a second deployment, and it expires on its own if
// never used. See docs/architecture-audit.md Finding 1.
type Authorization struct {
	ID                uuid.UUID  `json:"id"`
	ReleaseID         uuid.UUID  `json:"release_id"`
	ArtifactDigest    string     `json:"artifact_digest"`
	IssuedByCITokenID uuid.UUID  `json:"-"`
	IssuedAt          time.Time  `json:"issued_at"`
	ExpiresAt         time.Time  `json:"expires_at"`
	ConsumedAt        *time.Time `json:"consumed_at,omitempty"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
}

func generateAuthToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashAuthToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) CreateAuthorization(ctx context.Context, q dbx.Querier, releaseID uuid.UUID, artifactDigest string, ciTokenID uuid.UUID, tokenHash string, expiresAt time.Time) (Authorization, error) {
	var a Authorization
	err := q.QueryRow(ctx, `
		INSERT INTO deployment_authorizations (release_id, artifact_digest, token_hash, issued_by_ci_token_id, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, release_id, artifact_digest, issued_by_ci_token_id, issued_at, expires_at, consumed_at, revoked_at`,
		releaseID, artifactDigest, tokenHash, ciTokenID, expiresAt,
	).Scan(&a.ID, &a.ReleaseID, &a.ArtifactDigest, &a.IssuedByCITokenID, &a.IssuedAt, &a.ExpiresAt, &a.ConsumedAt, &a.RevokedAt)
	return a, err
}

// ConsumeAuthorization atomically marks one authorization token as used,
// but only if it is unexpired, unrevoked, and not already consumed. The
// WHERE clause plus RETURNING make this a single atomic
// compare-and-swap: under concurrent requests presenting the same token,
// Postgres' row-level locking serializes the two UPDATEs, exactly one
// call gets a row back, and the other gets pgx.ErrNoRows — so two
// deployments can never be recorded from one authorization, regardless of
// timing. Run inside the same transaction as the deployment insert it
// authorizes, so a failed/rolled-back deployment attempt never burns the
// token.
func (s *Store) ConsumeAuthorization(ctx context.Context, q dbx.Querier, tokenHash string) (Authorization, error) {
	var a Authorization
	err := q.QueryRow(ctx, `
		UPDATE deployment_authorizations
		SET consumed_at = now()
		WHERE token_hash = $1 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > now()
		RETURNING id, release_id, artifact_digest, issued_by_ci_token_id, issued_at, expires_at, consumed_at, revoked_at`,
		tokenHash,
	).Scan(&a.ID, &a.ReleaseID, &a.ArtifactDigest, &a.IssuedByCITokenID, &a.IssuedAt, &a.ExpiresAt, &a.ConsumedAt, &a.RevokedAt)
	return a, err
}

// resolveCIRelease is the shared "is this CI identity allowed to act on
// this release" check used by RecordFromCI, EligibilityForCI, and
// IssueAuthorization: the release must exist and the presented CI
// token's project must match the release's project exactly.
func (s *Service) resolveCIRelease(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) (releases.Release, error) {
	if identity.Via != authn.ViaCI {
		return releases.Release{}, httpx.Unauthorized("ci token required")
	}
	rel, err := s.releases.GetByID(ctx, s.pool, releaseID)
	if err != nil {
		return releases.Release{}, httpx.NotFound("release not found")
	}
	if identity.CIProjectID == nil || *identity.CIProjectID != rel.ProjectID {
		return releases.Release{}, httpx.Forbidden("this ci token is not scoped to this release's project")
	}
	return rel, nil
}

// IssueAuthorization re-checks eligibility (the same gate.Evaluate every
// other eligibility path uses) and, only if the release is currently
// eligible, mints a single-use authorization for the exact artifact
// digest the caller names. RecordFromCI will later require and consume
// this exact token for a production deployment of this release.
func (s *Service) IssueAuthorization(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, artifactDigest string) (Authorization, string, error) {
	if artifactDigest == "" {
		return Authorization{}, "", httpx.BadRequest("artifact_digest is required and must be an immutable digest, not a reusable tag")
	}
	rel, err := s.resolveCIRelease(ctx, identity, releaseID)
	if err != nil {
		return Authorization{}, "", err
	}

	result, err := s.gate.Evaluate(ctx, rel.ID)
	if err != nil {
		return Authorization{}, "", err
	}
	if !result.Eligible {
		return Authorization{}, "", httpx.Conflict("release is not eligible for production deployment").WithDetails(result)
	}

	// If a release candidate has been registered, the digest being
	// authorized must be the one on record — this is what stops an
	// authorization (and therefore a production deployment) from ever
	// covering a different artifact than the one tracked as this
	// release's current build, regardless of whether the project policy
	// also requires candidate-bound approvals. See
	// docs/architecture-audit.md Finding 2.
	current, err := s.candidates.GetCurrentByRelease(ctx, s.pool, rel.ID)
	if err != nil {
		return Authorization{}, "", err
	}
	if current != nil && current.ImageDigest != artifactDigest {
		return Authorization{}, "", httpx.Conflict("artifact_digest does not match this release's current candidate").WithDetails(map[string]string{
			"requested_digest": artifactDigest, "candidate_digest": current.ImageDigest,
		})
	}

	token, err := generateAuthToken()
	if err != nil {
		return Authorization{}, "", err
	}
	expiresAt := time.Now().Add(s.authTTL)
	a, err := s.store.CreateAuthorization(ctx, s.pool, rel.ID, artifactDigest, *identity.CITokenID, hashAuthToken(token), expiresAt)
	if err != nil {
		return Authorization{}, "", err
	}
	_ = s.audit.Record(ctx, s.pool, "release", rel.ID, "deployment_authorization_issued", identity.AuditActor(), nil, a, "artifact_digest="+artifactDigest)
	return a, token, nil
}
