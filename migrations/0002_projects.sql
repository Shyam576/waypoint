CREATE TABLE projects (
	id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	name         TEXT NOT NULL,
	slug         TEXT NOT NULL UNIQUE,
	owning_team  TEXT NOT NULL,
	repo_url     TEXT,
	status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
	created_by   UUID NOT NULL REFERENCES users(id),
	created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE ci_tokens ADD CONSTRAINT fk_ci_tokens_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE;

-- One policy row per project. required_evidence_types is a small fixed
-- vocabulary enforced in application code (see internal/evidence);
-- storing it as text[] keeps it editable without a schema change per type.
CREATE TABLE project_policies (
	id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	project_id               UUID NOT NULL UNIQUE REFERENCES projects(id) ON DELETE CASCADE,
	required_evidence_types  TEXT[] NOT NULL DEFAULT ARRAY['change_summary','test_results','uat_signoff','rollback_plan'],
	required_approvals_count INT NOT NULL DEFAULT 1 CHECK (required_approvals_count >= 0),
	block_on_open_rejection  BOOLEAN NOT NULL DEFAULT true,
	updated_by               UUID REFERENCES users(id),
	updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE project_members (
	project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	added_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY (project_id, user_id)
);
