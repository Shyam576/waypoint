CREATE TABLE evidence (
	id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	release_id  UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
	type        TEXT NOT NULL CHECK (type IN
		('change_summary', 'test_results', 'uat_signoff', 'rollback_plan', 'security_review', 'other')),
	title       TEXT NOT NULL,
	notes       TEXT NOT NULL DEFAULT '',
	attachment_url TEXT,
	submitted_by UUID NOT NULL REFERENCES users(id),
	submitted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_evidence_release_id ON evidence(release_id);

-- Every decision a named approver makes is kept (not overwritten), so the
-- history shows who changed their mind and when. The gate (internal/gate)
-- only looks at each approver's most recent row.
CREATE TABLE approvals (
	id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	release_id  UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
	approver_id UUID NOT NULL REFERENCES users(id),
	decision    TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
	comment     TEXT NOT NULL DEFAULT '',
	decided_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_approvals_release_id ON approvals(release_id);
