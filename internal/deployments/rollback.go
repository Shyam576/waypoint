package deployments

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Shyam576/waypoint/internal/authn"
	"github.com/Shyam576/waypoint/internal/dbx"
	"github.com/Shyam576/waypoint/internal/httpx"
	"github.com/Shyam576/waypoint/internal/users"
)

type RollbackStatus string

const (
	RollbackPending   RollbackStatus = "pending"
	RollbackCompleted RollbackStatus = "completed"
	RollbackFailed    RollbackStatus = "failed"
)

// Rollback records an intent, authorized up front by an
// admin/coordinator, to revert a release's production deployment to a
// previously-deployed artifact. It deliberately reuses the existing
// deployment_authorizations single-use token mechanism (see
// authorization.go) rather than a separate execution path: initiating a
// rollback mints an authorization bound to this row via rollback_id,
// and CI consumes it through the same POST .../deployments endpoint it
// already uses, so no new CI-facing route exists. What makes this safe
// despite bypassing the normal gate re-check is that TargetDeploymentID
// must itself have been a prior *successful* production deployment —
// an admin can only authorize rolling back to something that was
// already proven safe in production, never to an arbitrary artifact.
type Rollback struct {
	ID                    uuid.UUID      `json:"id"`
	ReleaseID             uuid.UUID      `json:"release_id"`
	OriginalDeploymentID  uuid.UUID      `json:"original_deployment_id"`
	TargetDeploymentID    uuid.UUID      `json:"target_deployment_id"`
	Reason                string         `json:"reason"`
	InitiatedBy           uuid.UUID      `json:"initiated_by"`
	Status                RollbackStatus `json:"status"`
	ResultingDeploymentID *uuid.UUID     `json:"resulting_deployment_id,omitempty"`
	VerifiedBy            *uuid.UUID     `json:"verified_by,omitempty"`
	VerifiedAt            *time.Time     `json:"verified_at,omitempty"`
	VerificationNote      string         `json:"verification_note"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

const rollbackColumns = `id, release_id, original_deployment_id, target_deployment_id, reason, initiated_by, status, resulting_deployment_id, verified_by, verified_at, verification_note, created_at, updated_at`

func scanRollback(row pgx.Row) (Rollback, error) {
	var rb Rollback
	err := row.Scan(&rb.ID, &rb.ReleaseID, &rb.OriginalDeploymentID, &rb.TargetDeploymentID, &rb.Reason, &rb.InitiatedBy,
		&rb.Status, &rb.ResultingDeploymentID, &rb.VerifiedBy, &rb.VerifiedAt, &rb.VerificationNote, &rb.CreatedAt, &rb.UpdatedAt)
	return rb, err
}

type CreateRollbackParams struct {
	ReleaseID            uuid.UUID
	OriginalDeploymentID uuid.UUID
	TargetDeploymentID   uuid.UUID
	Reason               string
	InitiatedBy          uuid.UUID
}

func (s *Store) CreateRollback(ctx context.Context, q dbx.Querier, p CreateRollbackParams) (Rollback, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO rollbacks (release_id, original_deployment_id, target_deployment_id, reason, initiated_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+rollbackColumns,
		p.ReleaseID, p.OriginalDeploymentID, p.TargetDeploymentID, p.Reason, p.InitiatedBy,
	)
	return scanRollback(row)
}

func (s *Store) GetRollback(ctx context.Context, q dbx.Querier, id uuid.UUID) (Rollback, error) {
	row := q.QueryRow(ctx, `SELECT `+rollbackColumns+` FROM rollbacks WHERE id = $1`, id)
	return scanRollback(row)
}

func (s *Store) ListRollbacksByRelease(ctx context.Context, q dbx.Querier, releaseID uuid.UUID) ([]Rollback, error) {
	rows, err := q.Query(ctx, `SELECT `+rollbackColumns+` FROM rollbacks WHERE release_id = $1 ORDER BY created_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rollback{}
	for rows.Next() {
		rb, err := scanRollback(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rb)
	}
	return out, rows.Err()
}

// MarkRollbackResult is called by record() as a side effect of
// consuming a rollback-linked authorization: the rollback's status
// follows the outcome of the deployment it authorized.
func (s *Store) MarkRollbackResult(ctx context.Context, q dbx.Querier, id uuid.UUID, result Result, resultingDeploymentID uuid.UUID) (Rollback, error) {
	status := RollbackCompleted
	if result != ResultSuccess {
		status = RollbackFailed
	}
	row := q.QueryRow(ctx, `
		UPDATE rollbacks SET status = $2, resulting_deployment_id = $3, updated_at = now()
		WHERE id = $1
		RETURNING `+rollbackColumns,
		id, status, resultingDeploymentID,
	)
	return scanRollback(row)
}

func (s *Store) MarkRollbackVerified(ctx context.Context, q dbx.Querier, id uuid.UUID, verifiedBy uuid.UUID, note string) (Rollback, error) {
	row := q.QueryRow(ctx, `
		UPDATE rollbacks SET verified_by = $2, verified_at = now(), verification_note = $3, updated_at = now()
		WHERE id = $1
		RETURNING `+rollbackColumns,
		id, verifiedBy, note,
	)
	return scanRollback(row)
}

type InitiateRollbackInput struct {
	OriginalDeploymentID uuid.UUID
	TargetDeploymentID   uuid.UUID
	Reason               string
}

// RollbackAuthorization is what InitiateRollback hands back: the
// rollback record itself, plus the one-time plaintext token CI must
// present to POST .../deployments to actually execute it. Like
// IssueAuthorization's token, this plaintext value is never persisted
// or retrievable again.
type RollbackAuthorization struct {
	Rollback Rollback `json:"rollback"`
	Token    string   `json:"deployment_authorization_token"`
}

// InitiateRollback is a privileged, explicit bypass of the normal
// production gate: an admin/coordinator decides, right now, that this
// release's current production deployment must be reverted, without
// waiting for whatever approvals/checklist items the gate would
// otherwise require. What keeps this from being an unrestricted bypass
// is the provenance check below — the target must be a digest that was
// itself already deployed to production successfully, so a rollback
// can only ever move to a previously-proven-safe state, never to an
// arbitrary artifact. Every rollback is audited, and record() applies
// its own result (success/failure) back onto this row when CI consumes
// the minted authorization.
func (s *Service) InitiateRollback(ctx context.Context, identity authn.Identity, releaseID uuid.UUID, in InitiateRollbackInput) (RollbackAuthorization, error) {
	rel, err := s.releaseAcc.GetForService(ctx, identity, releaseID)
	if err != nil {
		return RollbackAuthorization{}, err
	}
	if identity.User.Role != users.RoleAdmin && identity.User.Role != users.RoleReleaseCoordinator {
		return RollbackAuthorization{}, httpx.Forbidden("only admins and release coordinators can initiate a rollback")
	}
	canManage, err := s.projects.CanManage(ctx, identity, rel.ProjectID)
	if err != nil {
		return RollbackAuthorization{}, err
	}
	if !canManage {
		return RollbackAuthorization{}, httpx.Forbidden("you do not have permission to manage this project")
	}
	if in.Reason == "" {
		return RollbackAuthorization{}, httpx.BadRequest("reason is required")
	}

	original, err := s.store.GetByID(ctx, s.pool, in.OriginalDeploymentID)
	if err != nil {
		return RollbackAuthorization{}, httpx.NotFound("original deployment not found")
	}
	if original.ReleaseID != rel.ID {
		return RollbackAuthorization{}, httpx.BadRequest("original_deployment_id does not belong to this release")
	}

	target, err := s.store.GetByID(ctx, s.pool, in.TargetDeploymentID)
	if err != nil {
		return RollbackAuthorization{}, httpx.NotFound("target deployment not found")
	}
	if target.Environment != EnvProduction || target.Result != ResultSuccess {
		return RollbackAuthorization{}, httpx.BadRequest("rollback target must be a digest that was itself a prior successful production deployment")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RollbackAuthorization{}, err
	}
	defer tx.Rollback(ctx)

	rb, err := s.store.CreateRollback(ctx, tx, CreateRollbackParams{
		ReleaseID: rel.ID, OriginalDeploymentID: original.ID, TargetDeploymentID: target.ID,
		Reason: in.Reason, InitiatedBy: identity.User.ID,
	})
	if err != nil {
		return RollbackAuthorization{}, err
	}

	token, err := generateAuthToken()
	if err != nil {
		return RollbackAuthorization{}, err
	}
	expiresAt := time.Now().Add(s.authTTL)
	if _, err := s.store.CreateRollbackAuthorization(ctx, tx, rel.ID, target.ArtifactDigest, hashAuthToken(token), expiresAt, rb.ID); err != nil {
		return RollbackAuthorization{}, err
	}

	if err := s.audit.Record(ctx, tx, "release", rel.ID, "rollback_initiated", identity.AuditActor(), nil, rb, "reason="+in.Reason); err != nil {
		return RollbackAuthorization{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return RollbackAuthorization{}, err
	}
	return RollbackAuthorization{Rollback: rb, Token: token}, nil
}

// VerifyRollback records a human confirmation that a completed rollback
// actually fixed the problem it was initiated for. This is a
// documentation step, not a privileged override, so it only requires
// the same project-management permission as initiating the rollback.
func (s *Service) VerifyRollback(ctx context.Context, identity authn.Identity, releaseID, rollbackID uuid.UUID, note string) (Rollback, error) {
	rel, err := s.releaseAcc.GetForService(ctx, identity, releaseID)
	if err != nil {
		return Rollback{}, err
	}
	canManage, err := s.projects.CanManage(ctx, identity, rel.ProjectID)
	if err != nil {
		return Rollback{}, err
	}
	if !canManage {
		return Rollback{}, httpx.Forbidden("you do not have permission to manage this project")
	}
	rb, err := s.store.GetRollback(ctx, s.pool, rollbackID)
	if err != nil {
		return Rollback{}, httpx.NotFound("rollback not found")
	}
	if rb.ReleaseID != rel.ID {
		return Rollback{}, httpx.NotFound("rollback not found")
	}
	if rb.Status == RollbackPending {
		return Rollback{}, httpx.Conflict("this rollback has not been executed yet; there is nothing to verify")
	}
	if note == "" {
		return Rollback{}, httpx.BadRequest("verification note is required")
	}
	updated, err := s.store.MarkRollbackVerified(ctx, s.pool, rollbackID, identity.User.ID, note)
	if err != nil {
		return Rollback{}, err
	}
	_ = s.audit.Record(ctx, s.pool, "release", rel.ID, "rollback_verified", identity.AuditActor(), rb, updated, "")
	return updated, nil
}

func (s *Service) ListRollbacksByRelease(ctx context.Context, identity authn.Identity, releaseID uuid.UUID) ([]Rollback, error) {
	if _, err := s.releaseAcc.GetForService(ctx, identity, releaseID); err != nil {
		return nil, err
	}
	return s.store.ListRollbacksByRelease(ctx, s.pool, releaseID)
}
