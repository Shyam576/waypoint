// Package candidates implements release candidates: the immutable build
// identity (commit, image digest, config/migration checksums, build
// provenance) a release currently points at. A release's "current
// candidate" is simply the most recently registered row for that
// release — there is no separate pointer column to keep in sync.
//
// Registering a new candidate is how a project with
// policy.RequireCandidateForApproval set forces revalidation after the
// artifact changes: internal/approvals stamps each decision with the
// candidate that was current when it was made, and internal/gate only
// counts a decision toward eligibility while it still matches the
// current candidate. internal/deployments requires any minted
// deployment authorization's artifact_digest to match the current
// candidate's image_digest whenever one is registered, regardless of
// that policy flag. See docs/architecture-audit.md Finding 2.
package candidates

import (
	"time"

	"github.com/google/uuid"
)

type CreatedVia string

const (
	CreatedViaWeb CreatedVia = "web"
	CreatedViaCI  CreatedVia = "ci"
)

type Candidate struct {
	ID                 uuid.UUID  `json:"id"`
	ReleaseID          uuid.UUID  `json:"release_id"`
	GitRepo            string     `json:"git_repo,omitempty"`
	GitBranch          string     `json:"git_branch,omitempty"`
	CommitSHA          string     `json:"commit_sha,omitempty"`
	BuildID            string     `json:"build_id,omitempty"`
	ImageDigest        string     `json:"image_digest"`
	AppVersion         string     `json:"app_version,omitempty"`
	ConfigChecksum     string     `json:"config_checksum,omitempty"`
	MigrationChecksum  string     `json:"migration_checksum,omitempty"`
	BuildProvenanceURL string     `json:"build_provenance_url,omitempty"`
	CreatedVia         CreatedVia `json:"created_via"`
	CreatedBy          *uuid.UUID `json:"created_by,omitempty"`
	CreatedByCITokenID *uuid.UUID `json:"created_by_ci_token_id,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}
