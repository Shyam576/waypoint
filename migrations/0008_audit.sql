-- Audit records are append-only: the application never exposes an
-- edit/delete endpoint for them (see internal/audit), and this trigger
-- enforces the same rule at the database level in case a future change
-- forgets that rule.
CREATE TABLE audit_logs (
	id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	entity_type    TEXT NOT NULL,
	entity_id      UUID NOT NULL,
	action         TEXT NOT NULL,
	actor_id       UUID REFERENCES users(id),
	actor_label    TEXT NOT NULL,
	previous_state JSONB,
	new_state      JSONB,
	remarks        TEXT NOT NULL DEFAULT '',
	created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_logs_entity ON audit_logs(entity_type, entity_id);
CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at);

CREATE FUNCTION audit_logs_immutable() RETURNS TRIGGER AS $$
BEGIN
	RAISE EXCEPTION 'audit_logs is append-only: % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_audit_logs_no_update
	BEFORE UPDATE ON audit_logs
	FOR EACH ROW EXECUTE FUNCTION audit_logs_immutable();

CREATE TRIGGER trg_audit_logs_no_delete
	BEFORE DELETE ON audit_logs
	FOR EACH ROW EXECUTE FUNCTION audit_logs_immutable();
