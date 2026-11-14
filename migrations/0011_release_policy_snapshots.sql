-- A release's policy snapshot: the project policy as it existed at the
-- moment the release most recently entered ready_for_production.
-- internal/gate.Evaluate prefers this over the live project policy
-- whenever one exists, so editing a project's policy afterward can no
-- longer silently change the requirements an in-progress release is
-- held to. Re-entering ready_for_production (e.g. after being sent back
-- to uat for fixes) refreshes the snapshot deliberately, as part of that
-- audited transition — not silently. See docs/architecture-audit.md
-- Finding 3.
CREATE TABLE release_policy_snapshots (
	release_id                     UUID PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,
	required_evidence_types        TEXT[] NOT NULL,
	required_approvals_count       INT NOT NULL,
	block_on_open_rejection        BOOLEAN NOT NULL,
	require_candidate_for_approval BOOLEAN NOT NULL,
	snapshotted_at                 TIMESTAMPTZ NOT NULL DEFAULT now()
);
