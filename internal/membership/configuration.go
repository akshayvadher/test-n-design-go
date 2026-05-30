package membership

import (
	"io"
	"log/slog"

	"github.com/google/uuid"
)

// Overrides is the test-substitution extension point for the membership
// Facade. Every field is optional — a zero value means "use the default."
// Tests construct it with only the fields they need to swap (typically
// NewID for deterministic ids).
type Overrides struct {
	Repository Repository
	NewID      func() string
	Logger     *slog.Logger
}

// NewFacadeWithOverrides constructs a Facade applying the supplied
// Overrides on top of the in-memory defaults. The defaults wire a fresh
// in-memory repository, a UUID-string id generator, and a silent slog
// logger. Tests reuse this constructor so each test gets a clean substrate
// without restating the wiring.
func NewFacadeWithOverrides(o Overrides) *Facade {
	repository := o.Repository
	if repository == nil {
		repository = NewInMemoryRepository()
	}
	newID := o.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return NewFacade(repository, newID, logger)
}
