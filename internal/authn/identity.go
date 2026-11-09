package authn

import (
	"context"

	"github.com/google/uuid"

	"github.com/Shyam576/waypoint/internal/audit"
	"github.com/Shyam576/waypoint/internal/users"
)

// Via identifies which channel authenticated the current request. Several
// business rules (e.g. "MCP submissions always start pending") branch on
// this, and every audit entry records it so the timeline shows whether a
// change came from the web app, Claude via MCP, or a CI pipeline.
type Via string

const (
	ViaWeb Via = "web"
	ViaMCP Via = "mcp"
	ViaCI  Via = "ci"
)

type Identity struct {
	User        users.User
	Via         Via
	MCPTokenID  *uuid.UUID
	CITokenID   *uuid.UUID
	CIProjectID *uuid.UUID
}

func (id Identity) AuditActor() audit.Actor {
	if id.Via == ViaCI {
		return audit.Actor{Label: "ci-pipeline"}
	}
	label := id.User.Email
	if id.Via == ViaMCP {
		label += " (via mcp)"
	}
	return audit.Actor{ID: &id.User.ID, Label: label}
}

type ctxKey int

const identityCtxKey ctxKey = iota

func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey, id)
}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityCtxKey).(Identity)
	return id, ok
}
