// Package http provides shared HTTP infrastructure used by every business
// module: a JSON response helper, the canonical error-response envelope, and
// the middleware stack (request id, real ip, slog adapter, recoverer, and the
// domain-error translator).
//
// Phase 1 wires the middleware stack from cmd/library/main.go. The
// DomainErrorRegistry is constructed there as well — Phase 1 starts with an
// empty registry; Phase 2+ modules extend the registration block when their
// domain errors are introduced.
package http

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse is the canonical JSON body the DomainErrorMiddleware emits
// when a handler returns an error. Optional fields are tagged omitempty so
// callers that don't populate them don't leak null values into the body.
//
// The Error field carries the snake_case error code registered with the
// DomainErrorRegistry (e.g. "unauthorized_role"). The Message field carries
// the human-readable detail — for registered domain errors it is the wrapped
// error's Error() string; for unregistered errors it is the canonical
// "internal server error" sentinel (the raw error text never reaches the
// client).
type ErrorResponse struct {
	Error     string         `json:"error"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// WriteJSON serialises body as JSON, sets the Content-Type header, writes
// the given status code, and returns any encoder error so callers can decide
// whether to log it.
//
// The Content-Type and status must be written before the body — once the
// encoder writes the first byte, the response head is locked.
func WriteJSON(w http.ResponseWriter, status int, body any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(body)
}
