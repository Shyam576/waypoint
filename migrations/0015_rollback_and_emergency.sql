-- Rollback tracking and emergency release workflows (brief Phase Seven).

-- Emergency release policy per project. required_approvals_count has a
-- floor of 1 enforced at the database level, not just in Go: an
-- emergency changes the bar, it never removes it (see
-- docs/implementation-roadmap.md's "Open questions" recommendation).
CREATE TABLE project_emergency_policies (
	project_id               UUID PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
	required_approvals_count INT NOT NULL DEFAULT 1 CHECK (required_approvals_count >= 1),
	require_retrospective    BOOLEAN NOT NULL DEFAULT true,
	updated_by               UUID REFERENCES users(id),
	updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Emergency classification on a release: explicit, audited, and read
-- live by the gate (never frozen into the policy snapshot) so declaring
-- an emergency takes effect immediately even for a release already
-- sitting in ready_for_production — that is exactly the situation
-- emergency declaration exists for.
ALTER TABLE releases ADD COLUMN is_emergency BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE releases ADD COLUMN emergency_justification TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN emergency_authorized_by UUID REFERENCES users(id);
ALTER TABLE releases ADD COLUMN emergency_declared_at TIMESTAMPTZ;
ALTER TABLE releases ADD COLUMN emergency_retrospective_completed_by UUID REFERENCES users(id);
ALTER TABLE releases ADD COLUMN emergency_retrospective_completed_at TIMESTAMPTZ;
ALTER TABLE releases ADD COLUMN emergency_retrospective_notes TEXT NOT NULL DEFAULT '';

-- A rollback links the deployment being undone (original_deployment_id,
-- must belong to this release) to the prior deployment being reverted
-- to (target_deployment_id, which may belong to a different release —
-- rolling back often means "go back to what the previous release had
-- running"). Preserves full history: nothing here is ever deleted, and
-- every status change is additionally captured in audit_logs.
CREATE TABLE rollbacks (
	id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	release_id              UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
	original_deployment_id  UUID NOT NULL REFERENCES deployments(id),
	target_deployment_id    UUID NOT NULL REFERENCES deployments(id),
	reason                  TEXT NOT NULL,
	initiated_by            UUID NOT NULL REFERENCES users(id),
	status                  TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'failed')),
	resulting_deployment_id UUID REFERENCES deployments(id),
	verified_by             UUID REFERENCES users(id),
	verified_at             TIMESTAMPTZ,
	verification_note       TEXT NOT NULL DEFAULT '',
	created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_rollbacks_release_id ON rollbacks(release_id);

-- Links a deployment_authorizations row to the rollback it was minted
-- for, so RecordFromCI's normal authorization-consumption path can
-- update the rollback's status as a side effect, with no new
-- CI-facing endpoint needed.
ALTER TABLE deployment_authorizations ADD COLUMN rollback_id UUID REFERENCES rollbacks(id);

-- A rollback authorization is minted by a human admin/coordinator from
-- the web app (InitiateRollback), not by a CI token, so
-- issued_by_ci_token_id cannot be required for it. The CHECK below keeps
-- every authorization traceable to exactly one issuing path: either a
-- CI token (the normal IssueAuthorization flow) or a rollback (this
-- one) — never neither.
ALTER TABLE deployment_authorizations ALTER COLUMN issued_by_ci_token_id DROP NOT NULL;
ALTER TABLE deployment_authorizations ADD CONSTRAINT deployment_authorizations_issuer_chk
	CHECK (issued_by_ci_token_id IS NOT NULL OR rollback_id IS NOT NULL);
