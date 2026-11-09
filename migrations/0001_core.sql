-- Core identity: users, browser sessions, and the two scoped service
-- tokens (per-user MCP tokens, per-project CI tokens). All secrets are
-- stored as SHA-256 hashes; the raw token is shown to the caller exactly
-- once, at creation time, and never persisted.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
	id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	email         TEXT NOT NULL UNIQUE,
	name          TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	role          TEXT NOT NULL CHECK (role IN ('admin', 'developer', 'approver', 'release_coordinator', 'support')),
	is_active     BOOLEAN NOT NULL DEFAULT true,
	created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
	id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	token_hash TEXT NOT NULL UNIQUE,
	user_agent TEXT,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_sessions_user_id ON sessions(user_id);

CREATE TABLE mcp_tokens (
	id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	label         TEXT NOT NULL,
	token_hash    TEXT NOT NULL UNIQUE,
	created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
	last_used_at  TIMESTAMPTZ,
	revoked_at    TIMESTAMPTZ
);

CREATE INDEX idx_mcp_tokens_user_id ON mcp_tokens(user_id);

CREATE TABLE ci_tokens (
	id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	project_id UUID NOT NULL,
	label      TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	created_by UUID NOT NULL REFERENCES users(id),
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	revoked_at TIMESTAMPTZ
);

CREATE INDEX idx_ci_tokens_project_id ON ci_tokens(project_id);
