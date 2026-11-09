CREATE TABLE releases (
	id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	project_id         UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	owner_id           UUID NOT NULL REFERENCES users(id),
	name               TEXT NOT NULL,
	target_date        DATE,
	risk_level         TEXT NOT NULL CHECK (risk_level IN ('low', 'medium', 'high')),
	summary            TEXT NOT NULL DEFAULT '',
	rollback_plan      TEXT NOT NULL DEFAULT '',
	support_period_days INT NOT NULL DEFAULT 14 CHECK (support_period_days >= 0),
	stage              TEXT NOT NULL DEFAULT 'draft' CHECK (stage IN
		('draft', 'development', 'uat', 'ready_for_production', 'production', 'post_release_support', 'closed')),
	support_ends_at    TIMESTAMPTZ,
	created_by         UUID NOT NULL REFERENCES users(id),
	created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_releases_project_id ON releases(project_id);
CREATE INDEX idx_releases_stage ON releases(stage);
CREATE INDEX idx_releases_owner_id ON releases(owner_id);

CREATE TABLE release_links (
	id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	release_id  UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
	link_type   TEXT NOT NULL CHECK (link_type IN ('work_item', 'pull_request', 'commit', 'ci_run')),
	url         TEXT NOT NULL,
	label       TEXT,
	created_by  UUID NOT NULL REFERENCES users(id),
	created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_release_links_release_id ON release_links(release_id);
