-- Post-deployment verification state (brief's Phase Eight, closes
-- docs/architecture-audit.md Finding 12): a deployment's `result` only
-- records whether the deploy step itself exited 0. It says nothing
-- about whether the application is actually healthy afterward. These
-- columns are a separate, explicit state for that, defaulting to
-- 'pending' so existing deployments are unaffected and nothing is
-- silently assumed healthy or unhealthy.
ALTER TABLE deployments ADD COLUMN verification_status TEXT NOT NULL DEFAULT 'pending'
	CHECK (verification_status IN ('pending', 'passed', 'failed'));
ALTER TABLE deployments ADD COLUMN verified_by UUID REFERENCES users(id);
ALTER TABLE deployments ADD COLUMN verified_via TEXT CHECK (verified_via IN ('ci', 'web'));
ALTER TABLE deployments ADD COLUMN verified_at TIMESTAMPTZ;
ALTER TABLE deployments ADD COLUMN verification_note TEXT NOT NULL DEFAULT '';
