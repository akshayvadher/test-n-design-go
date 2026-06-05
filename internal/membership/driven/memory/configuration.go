package memory

import (
	"io"
	"log/slog"

	"github.com/google/uuid"

	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// Overrides is the test-substitution extension point for the membership
// Facade. Every field is optional — a zero value means "use the default."
// Tests construct it with only the fields they need to swap (typically
// NewID for deterministic ids).
type Overrides struct {
	Repository membership.Repository
	NewID      func() string
	Logger     *slog.Logger
}

// NewFacadeWithOverrides constructs a membership.Facade applying the
// supplied Overrides on top of the in-memory defaults. The defaults
// wire a fresh in-memory Repository, a UUID-string id generator, and a
// silent slog logger. Tests reuse this constructor so each test gets a
// clean substrate without restating the wiring.
func NewFacadeWithOverrides(o Overrides) *membership.Facade {
	repository := o.Repository
	if repository == nil {
		repository = NewRepository()
	}
	newID := o.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return membership.NewFacade(repository, newID, logger)
}
