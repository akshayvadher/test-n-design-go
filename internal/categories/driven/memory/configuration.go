package memory

import (
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/akshayvadher/test-n-design-go/internal/categories"
)

// Overrides is the test-substitution extension point for the categories
// Facade. Every field is optional — a zero value means "use the
// default." Tests construct it with only the fields they need to swap
// (typically NewID and Clock for deterministic ids + timestamps).
type Overrides struct {
	Repository categories.CategoryRepository
	NewID      func() string
	Clock      func() time.Time
	Logger     *slog.Logger
}

// NewFacadeWithOverrides constructs a categories.Facade applying the
// supplied Overrides on top of the in-memory defaults. The defaults
// wire a fresh in-memory Repository, a UUID-string id generator,
// time.Now as the clock, and a silent slog logger. Tests reuse this
// constructor so each test gets a clean substrate without restating
// the wiring.
func NewFacadeWithOverrides(o Overrides) *categories.Facade {
	repository := o.Repository
	if repository == nil {
		repository = NewRepository()
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
	return categories.NewFacade(repository, newID, clock, logger)
}
