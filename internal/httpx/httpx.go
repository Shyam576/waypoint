// Package httpx holds small, shared HTTP helpers: a consistent JSON
// envelope, a typed API error that handlers can return, and logging for
// unexpected failures. Handlers should stay thin and delegate to services;
// this package is what lets them do that without repeating boilerplate.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// APIError is a deliberate, user-facing error with an HTTP status attached.
// Services return these (or wrap them) instead of generic errors so
// handlers never have to guess what status code to send.
type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *APIError) Error() string { return e.Message }

func NewAPIError(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

func BadRequest(message string) *APIError {
	return NewAPIError(http.StatusBadRequest, "bad_request", message)
}
func Unauthorized(message string) *APIError {
	return NewAPIError(http.StatusUnauthorized, "unauthorized", message)
}
func Forbidden(message string) *APIError {
	return NewAPIError(http.StatusForbidden, "forbidden", message)
}
func NotFound(message string) *APIError {
	return NewAPIError(http.StatusNotFound, "not_found", message)
}
func Conflict(message string) *APIError { return NewAPIError(http.StatusConflict, "conflict", message) }
func Unprocessable(message string) *APIError {
	return NewAPIError(http.StatusUnprocessableEntity, "unprocessable", message)
}

// WithDetails attaches structured extra data (e.g. a list of validation
// problems or gate blockers) to an existing APIError.
func (e *APIError) WithDetails(details any) *APIError {
	e.Details = details
	return e
}

// JSON writes v as a JSON response body with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode json response", "error", err)
	}
}

// Error writes err as a JSON error envelope. Known *APIError values are
// sent to the client as-is; anything else is logged with full detail and
// reduced to a generic 500 so internals never leak to the client.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		JSON(w, apiErr.Status, map[string]any{"error": apiErr})
		return
	}

	slog.Error("unhandled error", "method", r.Method, "path", r.URL.Path, "error", err)
	JSON(w, http.StatusInternalServerError, map[string]any{
		"error": APIError{Code: "internal_error", Message: "An unexpected error occurred."},
	})
}

// Decode reads a JSON request body into v, rejecting unknown fields so
// typos in client payloads surface immediately instead of being ignored.
func Decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return BadRequest("request body is not valid JSON: " + err.Error())
	}
	return nil
}
