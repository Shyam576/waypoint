// Package mcpnotes implements the two things Claude Code can do against a
// release through MCP: submit a change note, a design decision, or a
// support note (all of which land as a pending mcp_submissions row), and
// the developer-facing accept/reject review of those submissions. A
// submission only becomes real release evidence/activity once a
// developer accepts it in the web app; nothing it submits is visible as
// accepted evidence before that. Change notes and design decisions can
// also be authored directly from the web app, bypassing the staging step
// entirely, which is why their stores live here rather than requiring an
// MCP round trip.
package mcpnotes

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Source string

const (
	SourceWeb Source = "web"
	SourceMCP Source = "mcp"
)

type TestsStatus string

const (
	TestsPassed TestsStatus = "passed"
	TestsFailed TestsStatus = "failed"
	TestsNotRun TestsStatus = "not_run"
)

func (t TestsStatus) Valid() bool {
	switch t {
	case TestsPassed, TestsFailed, TestsNotRun:
		return true
	}
	return false
}

type ChangeNote struct {
	ID              uuid.UUID   `json:"id"`
	ReleaseID       uuid.UUID   `json:"release_id"`
	AuthorID        uuid.UUID   `json:"author_id"`
	Summary         string      `json:"summary"`
	Rationale       string      `json:"rationale"`
	AffectedService string      `json:"affected_service"`
	TestsRun        string      `json:"tests_run"`
	TestsStatus     TestsStatus `json:"tests_status"`
	LinkedPRURL     *string     `json:"linked_pr_url,omitempty"`
	LinkedCommitSHA *string     `json:"linked_commit_sha,omitempty"`
	Source          Source      `json:"source"`
	MCPSubmissionID *uuid.UUID  `json:"mcp_submission_id,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
}

type DesignDecision struct {
	ID                     uuid.UUID  `json:"id"`
	ReleaseID              uuid.UUID  `json:"release_id"`
	AuthorID               uuid.UUID  `json:"author_id"`
	Decision               string     `json:"decision"`
	Rationale              string     `json:"rationale"`
	AlternativesConsidered string     `json:"alternatives_considered"`
	Impact                 string     `json:"impact"`
	LinkedPRURL            *string    `json:"linked_pr_url,omitempty"`
	LinkedCommitSHA        *string    `json:"linked_commit_sha,omitempty"`
	Source                 Source     `json:"source"`
	MCPSubmissionID        *uuid.UUID `json:"mcp_submission_id,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
}

type SubmissionType string

const (
	SubmissionChangeNote     SubmissionType = "change_note"
	SubmissionDesignDecision SubmissionType = "design_decision"
	SubmissionSupportNote    SubmissionType = "support_note"
)

func (t SubmissionType) Valid() bool {
	switch t {
	case SubmissionChangeNote, SubmissionDesignDecision, SubmissionSupportNote:
		return true
	}
	return false
}

type SubmissionStatus string

const (
	SubmissionPending  SubmissionStatus = "pending"
	SubmissionAccepted SubmissionStatus = "accepted"
	SubmissionRejected SubmissionStatus = "rejected"
)

type Submission struct {
	ID          uuid.UUID        `json:"id"`
	ReleaseID   uuid.UUID        `json:"release_id"`
	Type        SubmissionType   `json:"type"`
	Payload     json.RawMessage  `json:"payload"`
	SubmittedBy uuid.UUID        `json:"submitted_by"`
	Status      SubmissionStatus `json:"status"`
	ReviewedBy  *uuid.UUID       `json:"reviewed_by,omitempty"`
	ReviewedAt  *time.Time       `json:"reviewed_at,omitempty"`
	ReviewNote  string           `json:"review_note"`
	CreatedAt   time.Time        `json:"created_at"`
}

// ChangeNotePayload is the structured body of a change_note submission,
// whether it came from the web form or from Claude via MCP.
type ChangeNotePayload struct {
	Summary         string      `json:"summary"`
	Rationale       string      `json:"rationale"`
	AffectedService string      `json:"affected_service"`
	TestsRun        string      `json:"tests_run"`
	TestsStatus     TestsStatus `json:"tests_status"`
	LinkedPRURL     *string     `json:"linked_pr_url"`
	LinkedCommitSHA *string     `json:"linked_commit_sha"`
}

type DesignDecisionPayload struct {
	Decision               string  `json:"decision"`
	Rationale              string  `json:"rationale"`
	AlternativesConsidered string  `json:"alternatives_considered"`
	Impact                 string  `json:"impact"`
	LinkedPRURL            *string `json:"linked_pr_url"`
	LinkedCommitSHA        *string `json:"linked_commit_sha"`
}

type SupportNotePayload struct {
	Summary         string  `json:"summary"`
	Severity        string  `json:"severity"`
	LinkedTicketURL *string `json:"linked_ticket_url"`
	LinkedPRURL     *string `json:"linked_pr_url"`
	ResolutionNote  string  `json:"resolution_note"`
}
