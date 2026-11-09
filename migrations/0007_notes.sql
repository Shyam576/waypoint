CREATE TABLE change_notes (
	id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	release_id       UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
	author_id        UUID NOT NULL REFERENCES users(id),
	summary          TEXT NOT NULL,
	rationale        TEXT NOT NULL DEFAULT '',
	affected_service TEXT NOT NULL DEFAULT '',
	tests_run        TEXT NOT NULL DEFAULT '',
	tests_status     TEXT NOT NULL DEFAULT 'not_run' CHECK (tests_status IN ('passed', 'failed', 'not_run')),
	linked_pr_url    TEXT,
	linked_commit_sha TEXT,
	source           TEXT NOT NULL DEFAULT 'web' CHECK (source IN ('web', 'mcp')),
	mcp_submission_id UUID,
	created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_change_notes_release_id ON change_notes(release_id);

CREATE TABLE design_decisions (
	id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	release_id             UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
	author_id              UUID NOT NULL REFERENCES users(id),
	decision               TEXT NOT NULL,
	rationale              TEXT NOT NULL DEFAULT '',
	alternatives_considered TEXT NOT NULL DEFAULT '',
	impact                 TEXT NOT NULL DEFAULT '',
	linked_pr_url          TEXT,
	linked_commit_sha      TEXT,
	source                 TEXT NOT NULL DEFAULT 'web' CHECK (source IN ('web', 'mcp')),
	mcp_submission_id      UUID,
	created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_design_decisions_release_id ON design_decisions(release_id);

-- Staging area for everything Claude Code submits through MCP. A row here
-- is never shown as accepted release evidence by itself; a developer must
-- accept it in the web app, which materializes it into change_notes /
-- design_decisions / support_items (see internal/mcpnotes). Rejected rows
-- are kept for traceability but never materialize.
CREATE TABLE mcp_submissions (
	id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	release_id   UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
	type         TEXT NOT NULL CHECK (type IN ('change_note', 'design_decision', 'support_note')),
	payload      JSONB NOT NULL,
	submitted_by UUID NOT NULL REFERENCES users(id),
	status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected')),
	reviewed_by  UUID REFERENCES users(id),
	reviewed_at  TIMESTAMPTZ,
	review_note  TEXT NOT NULL DEFAULT '',
	created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_mcp_submissions_release_id ON mcp_submissions(release_id);
CREATE INDEX idx_mcp_submissions_status ON mcp_submissions(status);

ALTER TABLE change_notes ADD CONSTRAINT fk_change_notes_submission FOREIGN KEY (mcp_submission_id) REFERENCES mcp_submissions(id);
ALTER TABLE design_decisions ADD CONSTRAINT fk_design_decisions_submission FOREIGN KEY (mcp_submission_id) REFERENCES mcp_submissions(id);
ALTER TABLE support_items ADD CONSTRAINT fk_support_items_submission FOREIGN KEY (mcp_submission_id) REFERENCES mcp_submissions(id);
