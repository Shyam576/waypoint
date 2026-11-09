CREATE TABLE support_items (
	id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	release_id       UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
	summary          TEXT NOT NULL,
	severity         TEXT NOT NULL CHECK (severity IN ('low', 'medium', 'high', 'critical')),
	status           TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'investigating', 'resolved', 'closed')),
	owner_id         UUID REFERENCES users(id),
	linked_ticket_url TEXT,
	linked_pr_url    TEXT,
	resolution_note  TEXT NOT NULL DEFAULT '',
	source           TEXT NOT NULL DEFAULT 'web' CHECK (source IN ('web', 'mcp')),
	mcp_submission_id UUID,
	created_by       UUID NOT NULL REFERENCES users(id),
	created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
	closed_at        TIMESTAMPTZ
);

CREATE INDEX idx_support_items_release_id ON support_items(release_id);
