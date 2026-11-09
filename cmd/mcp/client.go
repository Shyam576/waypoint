package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// waypointClient is a thin HTTP client for the subset of Waypoint's API
// under /api/v1/mcp/*. It never talks to the database directly: every
// business rule (visibility, "submissions start pending", attribution to
// the authenticated developer) is enforced once, by the API, and this
// client just carries the developer's MCP token.
type waypointClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newWaypointClient(baseURL, token string) *waypointClient {
	return &waypointClient{baseURL: baseURL, token: token, http: &http.Client{Timeout: 15 * time.Second}}
}

type apiError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) Error() string {
	return fmt.Sprintf("waypoint api error (%d %s): %s", e.Status, e.Code, e.Message)
}

func (c *waypointClient) do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("calling waypoint api: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		var wrapped struct {
			Error apiError `json:"error"`
		}
		_ = json.Unmarshal(respBody, &wrapped)
		wrapped.Error.Status = resp.StatusCode
		if wrapped.Error.Message == "" {
			wrapped.Error.Message = string(respBody)
		}
		return &wrapped.Error
	}

	if out != nil && len(respBody) > 0 {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

func (c *waypointClient) getReleaseContext(ctx context.Context, releaseID string) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, "/api/v1/mcp/releases/"+releaseID+"/context", nil, &out)
	return out, err
}

func (c *waypointClient) submitChangeNote(ctx context.Context, releaseID string, payload any) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodPost, "/api/v1/mcp/releases/"+releaseID+"/change-notes", payload, &out)
	return out, err
}

func (c *waypointClient) submitDesignDecision(ctx context.Context, releaseID string, payload any) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodPost, "/api/v1/mcp/releases/"+releaseID+"/design-decisions", payload, &out)
	return out, err
}

func (c *waypointClient) submitSupportNote(ctx context.Context, releaseID string, payload any) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodPost, "/api/v1/mcp/releases/"+releaseID+"/support-notes", payload, &out)
	return out, err
}
