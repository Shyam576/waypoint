-- Short-lived, single-use deployment authorizations. A CI pipeline must
-- obtain one of these, scoped to an exact release and artifact digest,
-- before Waypoint will accept a production deployment record for that
-- artifact. Minting one re-runs the same gate.Evaluate() every other
-- eligibility path uses, so it can never be issued for an ineligible
-- release. Consuming one is a single atomic UPDATE guarded by
-- "consumed_at IS NULL AND revoked_at IS NULL AND expires_at > now()",
-- so it can never authorize two deployments and never be used past its
-- expiry. This closes the gap described in docs/architecture-audit.md
-- Finding 1: previously, eligibility was only checked at the moment a
-- deployment was *recorded*, with nothing binding that check to one
-- specific, consumable, expiring credential.
CREATE TABLE deployment_authorizations (
	id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	release_id            UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
	artifact_digest       TEXT NOT NULL,
	token_hash            TEXT NOT NULL UNIQUE,
	issued_by_ci_token_id UUID NOT NULL REFERENCES ci_tokens(id),
	issued_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
	expires_at            TIMESTAMPTZ NOT NULL,
	consumed_at           TIMESTAMPTZ,
	revoked_at            TIMESTAMPTZ
);

CREATE INDEX idx_deployment_authorizations_release_id ON deployment_authorizations(release_id);

-- Records which authorization a production deployment consumed, so the
-- link is visible from the deployment side without a circular FK.
ALTER TABLE deployments ADD COLUMN authorization_id UUID REFERENCES deployment_authorizations(id);
