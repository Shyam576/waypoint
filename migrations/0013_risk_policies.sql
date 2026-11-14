-- High-risk / payment change controls (brief Phase Five,
-- docs/architecture-audit.md Finding 4). risk_level gains a 'critical'
-- tier and is now actually consulted by internal/gate (previously
-- stored but never read by anything). risk_categories lets a release
-- be tagged with which sensitive areas it touches, for classification
-- and audit visibility; it does not by itself change gate requirements
-- — risk_level is what drives extra policy via project_risk_policies
-- below.
ALTER TABLE releases DROP CONSTRAINT releases_risk_level_check;
ALTER TABLE releases ADD CONSTRAINT releases_risk_level_check CHECK (risk_level IN ('low', 'medium', 'high', 'critical'));
ALTER TABLE releases ADD COLUMN risk_categories TEXT[] NOT NULL DEFAULT '{}';

-- Three new evidence types to support the brief's specific high-risk
-- asks: a configuration diff, business-critical smoke tests, and a
-- machine-checkable payment-config verification. The last one is a
-- deliberately narrow, explicitly machine-oriented evidence type — see
-- internal/evidence's Type doc comment for why this is not, and cannot
-- be, a general "detect wrong business values" feature.
ALTER TABLE evidence DROP CONSTRAINT evidence_type_check;
ALTER TABLE evidence ADD CONSTRAINT evidence_type_check CHECK (type IN
	('change_summary', 'test_results', 'uat_signoff', 'rollback_plan', 'security_review',
	 'config_comparison', 'smoke_test_results', 'payment_config_check', 'other'));

-- Extra requirements a project can layer on top of its base policy for
-- releases at a given risk tier. A project typically only defines rows
-- for 'high' and 'critical' (new projects are seeded with sensible
-- defaults for both); 'low'/'medium' having no row means no extra
-- requirements, which is also what an existing project with no risk
-- policy rows at all gets — fully backward compatible.
CREATE TABLE project_risk_policies (
	id                             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	project_id                     UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	risk_level                     TEXT NOT NULL CHECK (risk_level IN ('low', 'medium', 'high', 'critical')),
	extra_required_approvals_count INT NOT NULL DEFAULT 0 CHECK (extra_required_approvals_count >= 0),
	extra_required_evidence_types  TEXT[] NOT NULL DEFAULT '{}',
	require_risk_acceptance        BOOLEAN NOT NULL DEFAULT false,
	updated_by                     UUID REFERENCES users(id),
	updated_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (project_id, risk_level)
);

-- An explicit, named acknowledgement that a high-risk release's risk was
-- considered and accepted — distinct from an ordinary approval, and
-- required before the gate considers the release eligible only when the
-- matching project_risk_policies row sets require_risk_acceptance. One
-- row per release; accepting again (e.g. after a material change)
-- overwrites it, so there is always exactly one "the risk was accepted,
-- most recently by X at T" record, not a growing history — the
-- surrounding audit_logs entry is what preserves history.
CREATE TABLE release_risk_acceptances (
	release_id  UUID PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,
	accepted_by UUID NOT NULL REFERENCES users(id),
	note        TEXT NOT NULL DEFAULT '',
	accepted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The policy snapshot (migration 0011) freezes the resolved risk-tier
-- extras too, the same way it freezes the base policy fields.
ALTER TABLE release_policy_snapshots ADD COLUMN extra_required_approvals_count INT NOT NULL DEFAULT 0;
ALTER TABLE release_policy_snapshots ADD COLUMN extra_required_evidence_types TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE release_policy_snapshots ADD COLUMN require_risk_acceptance BOOLEAN NOT NULL DEFAULT false;
