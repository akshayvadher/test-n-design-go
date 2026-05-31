package memory

import (
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	catalogmemory "github.com/akshayvadher/test-n-design-go/internal/catalog/driven/memory"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	membershipmemory "github.com/akshayvadher/test-n-design-go/internal/membership/driven/memory"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	eventsmemory "github.com/akshayvadher/test-n-design-go/internal/shared/events/memory"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
	txmemory "github.com/akshayvadher/test-n-design-go/internal/shared/tx/memory"
)

// Overrides is the test-substitution extension point for the lending
// Facade. Every field is optional — a zero value means "use the default."
// Tests construct it with only the fields they need to swap (typically
// NewID for deterministic ids and Clock for deterministic timestamps).
//
// Bus + TxFactory consistency rule: if you override Bus you MUST override
// TxFactory to construct a TransactionalContext against the same bus
// instance, otherwise staged events publish to a different bus than the
// facade's direct bus.Publish calls. The default wiring closes over a
// single resolved bus instance to guarantee consistency out of the box.
type Overrides struct {
	Catalog       *catalog.Facade
	Membership    *membership.Facade
	AccessControl *accesscontrol.Facade
	Loans         lending.LoanRepository
	Reservations  lending.ReservationRepository
	Bus           events.EventBus
	// TxFactory must construct a TransactionalContext against the same
	// EventBus the facade uses. See the type doc for the consistency rule
	// callers overriding both fields must follow.
	TxFactory tx.TransactionalContextFactory
	NewID     func() string
	Clock     func() time.Time
	Logger    *slog.Logger
}

// NewFacadeWithOverrides constructs a lending.Facade applying the
// supplied Overrides on top of the in-memory defaults. The defaults wire
// fresh cross-module facades, fresh in-memory loan + reservation repos, a
// fresh in-memory event bus, an in-memory TransactionalContext factory
// closing over that same bus, UUID-string id generation, wall-clock time
// and a silent slog logger.
func NewFacadeWithOverrides(o Overrides) *lending.Facade {
	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	catalogFacade := o.Catalog
	if catalogFacade == nil {
		catalogFacade = catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{})
	}
	membershipFacade := o.Membership
	if membershipFacade == nil {
		membershipFacade = membershipmemory.NewFacadeWithOverrides(membershipmemory.Overrides{})
	}
	accessControlFacade := o.AccessControl
	if accessControlFacade == nil {
		accessControlFacade = accesscontrol.NewFacade()
	}
	loans := o.Loans
	if loans == nil {
		loans = NewLoanRepository()
	}
	reservations := o.Reservations
	if reservations == nil {
		reservations = NewReservationRepository()
	}
	bus := o.Bus
	if bus == nil {
		bus = eventsmemory.NewBus(logger)
	}
	txFactory := o.TxFactory
	if txFactory == nil {
		// Close over the resolved bus so staged events publish through the
		// same instance the facade's direct bus.Publish calls reach.
		txFactory = func() tx.TransactionalContext {
			return txmemory.NewTransactionalContext(bus, logger)
		}
	}
	newID := o.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	clock := o.Clock
	if clock == nil {
		clock = time.Now
	}
	return lending.NewFacade(
		catalogFacade,
		membershipFacade,
		accessControlFacade,
		loans,
		reservations,
		bus,
		txFactory,
		newID,
		clock,
		logger,
	)
}
