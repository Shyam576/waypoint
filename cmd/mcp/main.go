// Command mcp runs Waypoint's MCP server for Claude Code. It exposes
// four narrowly-scoped tools (get_release_context, submit_change_note,
// submit_design_decision, submit_support_note) that call Waypoint's HTTP
// API using a per-user MCP token read from the environment. It never
// talks to Waypoint's database directly, never ingests or stores full
// conversation transcripts, and never lets a submission become accepted
// release evidence on its own: every submit_* tool lands as a pending
// mcp_submissions row that a developer must review and confirm in the
// web app. See README.md for setup instructions and .mcp.json.example
// for a configuration template.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	baseURL := os.Getenv("WAYPOINT_API_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	token := os.Getenv("WAYPOINT_MCP_TOKEN")
	if token == "" {
		log.Fatal("WAYPOINT_MCP_TOKEN is required: mint one via POST /api/v1/users/{userID}/mcp-tokens and set it in the environment running this server")
	}

	client := newWaypointClient(baseURL, token)

	server := mcp.NewServer(&mcp.Implementation{Name: "waypoint", Version: "0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_release_context",
		Description: "Read the current status, evidence checklist, approvals, linked work, and recent change/design " +
			"notes for a Waypoint release. Use this before submitting a change note or design decision, so the note " +
			"reflects the release's actual current state.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getReleaseContextInput) (*mcp.CallToolResult, any, error) {
		raw, err := client.getReleaseContext(ctx, in.ReleaseID)
		return textResult(raw, err)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "submit_change_note",
		Description: "Submit a structured note describing what changed in a release, why, which service/files were " +
			"affected, and what tests were run (or not run), with an optional linked PR/commit. This is always " +
			"recorded as PENDING in Waypoint: a developer must review and confirm it in the web app before it " +
			"becomes accepted release evidence.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in submitChangeNoteInput) (*mcp.CallToolResult, any, error) {
		raw, err := client.submitChangeNote(ctx, in.ReleaseID, in.toPayload())
		return pendingResult(raw, err)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "submit_design_decision",
		Description: "Submit a structured design decision for a release: the decision itself, its rationale, " +
			"alternatives considered, impact, and an optional linked PR/commit. This is always recorded as PENDING " +
			"in Waypoint: a developer must review and confirm it in the web app before it becomes accepted release " +
			"evidence.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in submitDesignDecisionInput) (*mcp.CallToolResult, any, error) {
		raw, err := client.submitDesignDecision(ctx, in.ReleaseID, in.toPayload())
		return pendingResult(raw, err)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "submit_support_note",
		Description: "Submit a post-go-live issue, investigation summary, or fix note for a release that is in (or " +
			"has been through) production. This is always recorded as PENDING in Waypoint: a developer must review " +
			"and confirm it in the web app before it becomes an accepted support item.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in submitSupportNoteInput) (*mcp.CallToolResult, any, error) {
		raw, err := client.submitSupportNote(ctx, in.ReleaseID, in.toPayload())
		return pendingResult(raw, err)
	})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("mcp server exited: %v", err)
	}
}

type getReleaseContextInput struct {
	ReleaseID string `json:"release_id" jsonschema:"the Waypoint release's UUID"`
}

type submitChangeNoteInput struct {
	ReleaseID       string `json:"release_id" jsonschema:"the Waypoint release's UUID"`
	Summary         string `json:"summary" jsonschema:"what changed, in one or two sentences"`
	Rationale       string `json:"rationale,omitempty" jsonschema:"why this change was made"`
	AffectedService string `json:"affected_service,omitempty" jsonschema:"the service, package, or files this change touches"`
	TestsRun        string `json:"tests_run,omitempty" jsonschema:"what tests were run, if any"`
	TestsStatus     string `json:"tests_status,omitempty" jsonschema:"one of: passed, failed, not_run"`
	LinkedPRURL     string `json:"linked_pr_url,omitempty" jsonschema:"a linked pull request URL, if any"`
	LinkedCommitSHA string `json:"linked_commit_sha,omitempty" jsonschema:"a linked commit SHA, if any"`
}

func (in submitChangeNoteInput) toPayload() map[string]any {
	return map[string]any{
		"summary": in.Summary, "rationale": in.Rationale, "affected_service": in.AffectedService,
		"tests_run": in.TestsRun, "tests_status": orDefault(in.TestsStatus, "not_run"),
		"linked_pr_url": nilIfEmpty(in.LinkedPRURL), "linked_commit_sha": nilIfEmpty(in.LinkedCommitSHA),
	}
}

type submitDesignDecisionInput struct {
	ReleaseID              string `json:"release_id" jsonschema:"the Waypoint release's UUID"`
	Decision               string `json:"decision" jsonschema:"the decision that was made"`
	Rationale              string `json:"rationale,omitempty" jsonschema:"why this decision was made"`
	AlternativesConsidered string `json:"alternatives_considered,omitempty" jsonschema:"other options that were considered and why they were not chosen"`
	Impact                 string `json:"impact,omitempty" jsonschema:"the expected impact of this decision"`
	LinkedPRURL            string `json:"linked_pr_url,omitempty" jsonschema:"a linked pull request URL, if any"`
	LinkedCommitSHA        string `json:"linked_commit_sha,omitempty" jsonschema:"a linked commit SHA, if any"`
}

func (in submitDesignDecisionInput) toPayload() map[string]any {
	return map[string]any{
		"decision": in.Decision, "rationale": in.Rationale, "alternatives_considered": in.AlternativesConsidered,
		"impact": in.Impact, "linked_pr_url": nilIfEmpty(in.LinkedPRURL), "linked_commit_sha": nilIfEmpty(in.LinkedCommitSHA),
	}
}

type submitSupportNoteInput struct {
	ReleaseID       string `json:"release_id" jsonschema:"the Waypoint release's UUID"`
	Summary         string `json:"summary" jsonschema:"a summary of the post-go-live issue, investigation, or fix"`
	Severity        string `json:"severity,omitempty" jsonschema:"one of: low, medium, high, critical"`
	LinkedTicketURL string `json:"linked_ticket_url,omitempty" jsonschema:"a linked incident/ticket URL, if any"`
	LinkedPRURL     string `json:"linked_pr_url,omitempty" jsonschema:"a linked pull request URL, if any"`
	ResolutionNote  string `json:"resolution_note,omitempty" jsonschema:"what was found or fixed, if known yet"`
}

func (in submitSupportNoteInput) toPayload() map[string]any {
	return map[string]any{
		"summary": in.Summary, "severity": orDefault(in.Severity, "medium"),
		"linked_ticket_url": nilIfEmpty(in.LinkedTicketURL), "linked_pr_url": nilIfEmpty(in.LinkedPRURL),
		"resolution_note": in.ResolutionNote,
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func textResult(raw json.RawMessage, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
	}
	pretty, _ := json.MarshalIndent(json.RawMessage(raw), "", "  ")
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(pretty)}}}, nil, nil
}

func pendingResult(raw json.RawMessage, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
	}
	pretty, _ := json.MarshalIndent(json.RawMessage(raw), "", "  ")
	text := fmt.Sprintf("Recorded as a PENDING submission in Waypoint. It will not appear as accepted release "+
		"evidence until a developer reviews and confirms it in the web app.\n\n%s", string(pretty))
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}
