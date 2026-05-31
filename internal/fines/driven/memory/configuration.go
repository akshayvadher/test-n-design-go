package memory

import (
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/akshayvadher/test-n-design-go/internal/fines"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	membershipmemory "github.com/akshayvadher/test-n-design-go/internal/membership/driven/memory"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
)

// Overrides is the test-substitution extension point for the fines
// Facade. Every field is optional — a zero value means "use the
// default." Tests construct it with only the fields they need to swap.
type Overrides struct {
	Lending    *lending.Facade
	Membership *membership.Facade
	Repository fines.FineRepository
	Bus        events.EventBus
	Config     *fines.FinesConfig
	NewID      func() string
	Clock      func() time.Time
	Logger     *slog.Logger
}

// NewFacadeWithOverrides constructs a fines.Facade applying the supplied
// Overrides on top of the in-memory defaults. The defaults wire fresh
// in-memory lending + membership facades, a fresh in-memory fine
// repository, a fresh in-memory event bus, the locked default
// FinesConfig, UUID-string id generation, wall-clock time and a silent
// slog logger.
//
// In production the composition root passes the WIRED lending +
// membership facades so this module reads from the same in-memory state
// the rest of the app writes to.
func NewFacadeWithOverrides(o Overrides) *fines.Facade {
	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	lendingFacade := o.Lending
	if lendingFacade == nil {
		lendingFacade = lending.NewFacadeWithOverrides(lending.Overrides{})
	}
	membershipFacade := o.Membership
	if membershipFacade == nil {
		membershipFacade = membershipmemory.NewFacadeWithOverrides(membershipmemory.Overrides{})
	}
	repository := o.Repository
	if repository == nil {
		repository = NewRepository()
	}
	bus := o.Bus
	if bus == nil {
		bus = events.NewInMemoryEventBus(logger)
	}
	config := fines.DefaultConfig()
	if o.Config != nil {
		config = *o.Config
	}
	newID := o.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	clock := o.Clock
	if clock == nil {
		clock = time.Now
	}
	return fines.NewFacade(lendingFacade, membershipFacade, repository, bus, config, newID, clock, logger)
}
