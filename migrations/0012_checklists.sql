-- Structured go-live checklists (extends, rather than replaces, the
-- existing free-text evidence system in internal/evidence). A project
-- defines a versioned checklist template; a release is assigned the
-- project's currently-active template version at the moment it is
-- created, by copying each template item into its own
-- release_checklist_items row. That copy is what lets a release keep
-- the checklist version it was assigned even after the project
-- activates a newer template version later — there is no live join back
-- to the template for enforcement, only template_item_id kept for
-- traceability. See docs/architecture-audit.md Finding 5.
CREATE TABLE checklist_templates (
	id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	version    INT NOT NULL,
	is_active  BOOLEAN NOT NULL DEFAULT true,
	created_by UUID REFERENCES users(id),
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (project_id, version)
);

-- At most one active template per project at a time.
CREATE UNIQUE INDEX idx_checklist_templates_one_active_per_project ON checklist_templates(project_id) WHERE is_active;

CREATE TABLE checklist_template_items (
	id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	template_id         UUID NOT NULL REFERENCES checklist_templates(id) ON DELETE CASCADE,
	category            TEXT NOT NULL CHECK (category IN ('development', 'uat', 'production_readiness', 'post_deployment')),
	title               TEXT NOT NULL,
	description         TEXT NOT NULL DEFAULT '',
	is_required         BOOLEAN NOT NULL DEFAULT true,
	-- Documents whether an item is meant to be confirmed by a human or by
	-- an automated check; this release does not wire up a channel for
	-- automated systems to report results, so every item is still
	-- verified/rejected/waived by a human today regardless of this value
	-- (see docs/architecture-audit.md Finding 5 discussion). It exists so
	-- the distinction the brief asks for ("do not pretend Waypoint can
	-- automatically detect every incorrect business value") is at least
	-- recorded, not silently collapsed.
	verification_method TEXT NOT NULL DEFAULT 'human_attestation' CHECK (verification_method IN ('human_attestation', 'automated_check')),
	sort_order          INT NOT NULL DEFAULT 0
);

CREATE INDEX idx_checklist_template_items_template ON checklist_template_items(template_id);

-- One row per release: which template version it was assigned.
CREATE TABLE release_checklists (
	release_id  UUID PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,
	template_id UUID NOT NULL REFERENCES checklist_templates(id),
	created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The release's own copy of each template item, with its live
-- verification state. title/description/is_required/verification_method
-- are copied at creation time and never re-read from the template.
CREATE TABLE release_checklist_items (
	id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	release_id          UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
	template_item_id    UUID REFERENCES checklist_template_items(id),
	category            TEXT NOT NULL CHECK (category IN ('development', 'uat', 'production_readiness', 'post_deployment')),
	title               TEXT NOT NULL,
	description         TEXT NOT NULL DEFAULT '',
	is_required         BOOLEAN NOT NULL DEFAULT true,
	verification_method TEXT NOT NULL DEFAULT 'human_attestation' CHECK (verification_method IN ('human_attestation', 'automated_check')),
	owner_id            UUID REFERENCES users(id),
	status              TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'verified', 'rejected', 'waived')),
	evidence_ref        TEXT NOT NULL DEFAULT '',
	rejection_reason    TEXT NOT NULL DEFAULT '',
	waiver_reason       TEXT NOT NULL DEFAULT '',
	decided_by          UUID REFERENCES users(id),
	decided_at          TIMESTAMPTZ,
	created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_release_checklist_items_release ON release_checklist_items(release_id);

-- Off by default: existing projects and the existing gate behavior are
-- unaffected until a project opts in (same convention as
-- require_candidate_for_approval).
ALTER TABLE project_policies ADD COLUMN require_checklist_completion BOOLEAN NOT NULL DEFAULT false;

-- The policy snapshot (migration 0011) freezes this flag too, the same
-- way it freezes required_approvals_count etc.
ALTER TABLE release_policy_snapshots ADD COLUMN require_checklist_completion BOOLEAN NOT NULL DEFAULT false;
