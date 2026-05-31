package fines

import (
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
)

// DefaultDailyRateCents is the per-day fine rate the source TS ships with.
// The spec locks 25 cents as the Go-port default.
const DefaultDailyRateCents AmountCents = 25

// DefaultSuspensionThresholdCents is the unpaid-fines total at which
// maybeAutoSuspend pushes a member to SUSPENDED. The spec locks 1000 cents
// as the Go-port default (the source TS uses 500; the Go port diverges per
// the Phase-4 spec's locked policy).
const DefaultSuspensionThresholdCents AmountCents = 1000

// Overrides is the test-substitution extension point for the fines Facade.
// Every field is optional — a zero value means "use the default." Tests
// construct it with only the fields they need to swap.
type Overrides struct {
	Lending    *lending.Facade
	Membership *membership.Facade
	Repository FineRepository
	Bus        events.EventBus
	Config     *FinesConfig
	NewID      func() string
	Clock      func() time.Time
	Logger     *slog.Logger
}

// NewFacadeWithOverrides constructs a Facade applying the supplied Overrides
// on top of the in-memory defaults. The defaults wire fresh in-memory
// lending + membership facades, a fresh in-memory fine repository, a fresh
// in-memory event bus, the locked default FinesConfig, UUID-string id
// generation, wall-clock time and a silent slog logger.
//
// In production the composition root passes the WIRED lending + membership
// facades so this module reads from the same in-memory state the rest of
// the app writes to.
func NewFacadeWithOverrides(o Overrides) *Facade {
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
		membershipFacade = membership.NewFacadeWithOverrides(membership.Overrides{})
	}
	repository := o.Repository
	if repository == nil {
		repository = NewInMemoryFineRepository()
	}
	bus := o.Bus
	if bus == nil {
		bus = events.NewInMemoryEventBus(logger)
	}
	config := defaultConfig()
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
	return NewFacade(lendingFacade, membershipFacade, repository, bus, config, newID, clock, logger)
}

// defaultConfig returns the locked Phase-4 defaults as a fresh value.
func defaultConfig() FinesConfig {
	return FinesConfig{
		DailyRateCents:           DefaultDailyRateCents,
		SuspensionThresholdCents: DefaultSuspensionThresholdCents,
	}
}
