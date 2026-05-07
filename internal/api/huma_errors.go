package api

import (
	"net/http"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"
)

// apiError is the §10 error envelope as a huma.StatusError. The JSON
// shape is `{"error": {"code": "...", "message": "...", "details": {...}}}` —
// the same wire format the non-huma `writeError` helper produces, so a
// client never has to branch on response source. The `Status` field is
// excluded from JSON; huma reads it via GetStatus.
type apiError struct {
	Status   int          `json:"-"`
	Envelope apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string         `json:"code" example:"not_found" doc:"Stable, machine-readable error code (CONTRACT.md §10)."`
	Message string         `json:"message" example:"resource not found" doc:"Human-readable error message; not used for client branching."`
	Details map[string]any `json:"details,omitempty" doc:"Optional structured detail; shape varies by error code."`
}

// Error satisfies the standard error interface.
func (e *apiError) Error() string { return e.Envelope.Message }

// GetStatus satisfies huma.StatusError so huma writes the correct
// HTTP status code.
func (e *apiError) GetStatus() int { return e.Status }

// newAPIError builds an envelope ready to return from a handler.
func newAPIError(status int, code, msg string, details map[string]any) *apiError {
	return &apiError{
		Status: status,
		Envelope: apiErrorBody{
			Code:    code,
			Message: msg,
			Details: details,
		},
	}
}

var installOnce sync.Once

// installEnvelopeErrors overrides huma.NewError so framework-emitted
// errors (validation, content negotiation, 404 from huma) use the §10
// envelope shape. Idempotent — safe to call from every api.New().
func installEnvelopeErrors() {
	installOnce.Do(func() {
		huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
			return newAPIError(status, codeForStatus(status), msg, collectDetails(errs))
		}
	})
}

// codeForStatus picks a stable snake_case code for each common HTTP
// status. Domain-specific codes (e.g. `collector_not_found`) are built
// directly by handlers via newAPIError.
func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusUnprocessableEntity:
		return "unprocessable"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case http.StatusInternalServerError:
		return "internal_error"
	}
	if status >= 500 {
		return "internal_error"
	}
	return strings.ToLower(strings.ReplaceAll(http.StatusText(status), " ", "_"))
}

// collectDetails folds huma error detail values into the envelope's
// `details.errors` slot when present. The shape is intentionally
// minimal (a list of strings) so internals never leak through.
func collectDetails(errs []error) map[string]any {
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		if e == nil {
			continue
		}
		msgs = append(msgs, e.Error())
	}
	if len(msgs) == 0 {
		return nil
	}
	return map[string]any{"errors": msgs}
}
