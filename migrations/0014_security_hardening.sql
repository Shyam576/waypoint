-- Security hardening pass (docs/architecture-audit.md Findings 6, 9).

-- Finding 6: MCP and CI tokens never expired. Existing tokens keep
-- working forever (NULL = no expiry, preserving current behavior for
-- anything already issued); every newly issued token from here on gets
-- an expires_at based on WAYPOINT_MCP_TOKEN_TTL_DAYS /
-- WAYPOINT_CI_TOKEN_TTL_DAYS (see internal/config).
ALTER TABLE mcp_tokens ADD COLUMN expires_at TIMESTAMPTZ;
ALTER TABLE ci_tokens ADD COLUMN expires_at TIMESTAMPTZ;

-- Finding 9: separation of duties was a single hardcoded rule ("not the
-- release's own owner"). This adds one configurable extra rule per
-- project, off by default: an approver who submitted evidence for a
-- release cannot also approve it. See internal/approvals.
ALTER TABLE project_policies ADD COLUMN require_approver_not_submitter BOOLEAN NOT NULL DEFAULT false;
