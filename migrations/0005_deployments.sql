-- artifact_digest is the deployment's identity (e.g. a container image
-- digest "sha256:..."), never just a reusable tag, so a deployment record
-- always points at one immutable build.
CREATE TABLE deployments (
	id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	release_id       UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
	environment      TEXT NOT NULL CHECK (environment IN ('development', 'uat', 'staging', 'production')),
	artifact_digest  TEXT NOT NULL,
	artifact_ref     TEXT,
	commit_sha       TEXT,
	workflow_run_url TEXT NOT NULL,
	result           TEXT NOT NULL CHECK (result IN ('success', 'failure')),
	recorded_via     TEXT NOT NULL CHECK (recorded_via IN ('ci', 'web')),
	recorded_by      UUID REFERENCES users(id),
	recorded_by_ci_token_id UUID REFERENCES ci_tokens(id),
	occurred_at      TIMESTAMPTZ NOT NULL,
	created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_deployments_release_id ON deployments(release_id);
CREATE INDEX idx_deployments_environment ON deployments(environment);
