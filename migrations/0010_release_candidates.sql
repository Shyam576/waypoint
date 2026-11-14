-- A release candidate is the immutable build identity (commit, image
-- digest, config/migration checksums, build provenance) a release is
-- currently pointing at. Approvals record which candidate was current
-- when the decision was made (candidate_id below); projects that set
-- require_candidate_for_approval only count an approval toward
-- eligibility while it matches the release's current candidate, so
-- registering a new candidate (the artifact changed) silently drops
-- stale approvals from the count instead of silently continuing to
-- trust them. See docs/architecture-audit.md Finding 2.
--
-- There is no "current candidate" column on releases: the current
-- candidate is simply the most recently registered row for that
-- release (release_candidates.release_id, ORDER BY created_at DESC
-- LIMIT 1), avoiding a circular update between releases and this table.
CREATE TABLE release_candidates (
	id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	release_id             UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
	git_repo               TEXT,
	git_branch             TEXT,
	commit_sha             TEXT,
	build_id               TEXT,
	image_digest           TEXT NOT NULL,
	app_version            TEXT,
	config_checksum        TEXT,
	migration_checksum     TEXT,
	build_provenance_url   TEXT,
	created_via            TEXT NOT NULL CHECK (created_via IN ('web', 'ci')),
	created_by             UUID REFERENCES users(id),
	created_by_ci_token_id UUID REFERENCES ci_tokens(id),
	created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_release_candidates_release_id ON release_candidates(release_id, created_at DESC);

ALTER TABLE approvals ADD COLUMN candidate_id UUID REFERENCES release_candidates(id);

-- Off by default: existing projects and the existing approval/gate
-- behavior are unaffected until a project opts in.
ALTER TABLE project_policies ADD COLUMN require_candidate_for_approval BOOLEAN NOT NULL DEFAULT false;
