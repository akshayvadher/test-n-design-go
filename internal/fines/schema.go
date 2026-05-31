package fines

import (
	"fmt"
	"strings"
)

// InvalidFineError is returned by ParseFineId when the input fails
// validation. Reason is the validator's first failure message.
type InvalidFineError struct {
	Reason string
}

// Error implements error on a pointer receiver so errors.As resolves
// *InvalidFineError targets through wrapping layers.
func (e *InvalidFineError) Error() string {
	return fmt.Sprintf("Invalid fine request: %s", e.Reason)
}

// ParseFineId trims the raw URL parameter and rejects blank. Returns the
// typed FineId on success.
func ParseFineId(raw string) (FineId, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", &InvalidFineError{Reason: "fineId is required"}
	}
	return FineId(trimmed), nil
}
