package categories

import (
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Overrides is the test-substitution extension point for the categories
// Facade. Every field is optional — a zero value means "use the
// default." Tests construct it with only the fields they need to swap
// (typically NewID and Clock for deterministic ids + timestamps).
type Overrides struct {
	Repository CategoryRepository
	NewID      func() string
	Clock      func() time.Time
	Logger     *slog.Logger
}

// NewFacadeWithOverrides constructs a Facade applying the supplied
// Overrides on top of the in-memory defaults. The defaults wire a
// fresh in-memory repository, a UUID-string id generator, time.Now as
// the clock, and a silent slog logger. Tests reuse this constructor so
// each test gets a clean substrate without restating the wiring.
func NewFacadeWithOverrides(o Overrides) *Facade {
	repository := o.Repository
	if repository == nil {
		repository = NewInMemoryCategoryRepository()
	}
	newID := o.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	clock := o.Clock
	if clock == nil {
		clock = time.Now
	}
	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return NewFacade(repository, newID, clock, logger)
}
