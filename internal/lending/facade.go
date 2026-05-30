package lending

import (
	"log/slog"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// LoanDurationDays is the loan window in days. Matches the source TS
// constant LOAN_DURATION_DAYS = 14. Slice 4's Borrow uses it for due-date
// computation; declared at the top of facade.go because it is a property
// of the facade's domain policy, not of any single method.
const LoanDurationDays = 14

// Facade is the only public surface of the lending module. Slice 3 ships
// the struct + constructor + the two-package-level helpers (LoanDurationDays
// + addDays) other code already depends on. The exported business methods
// land in subsequent slices:
//
//   - Slice 4 — Borrow
//   - Slice 5 — Reserve
//   - Slice 6 — ReturnLoan
//
// Unexported fields keep collaborators encapsulated; the composition root
// wires them via NewFacade and tests substitute them via
// NewFacadeWithOverrides.
type Facade struct {
	catalog       *catalog.Facade
	membership    *membership.Facade
	accessControl *accesscontrol.Facade
	loans         LoanRepository
	reservations  ReservationRepository
	bus           events.EventBus
	txFactory     tx.TransactionalContextFactory
	newID         func() string
	clock         func() time.Time
	logger        *slog.Logger
}

// NewFacade wires the Facade with explicit dependencies. The composition
// root passes the concrete implementations; tests use
// NewFacadeWithOverrides which fills the same arguments from an Overrides
// struct with in-memory defaults.
//
// Dependency order: cross-module facades first (catalog, membership,
// accessControl), then own-module repos (loans, reservations), then the
// shared substrate (bus, txFactory), then the cross-cutting helpers
// (newID, clock, logger).
func NewFacade(
	catalog *catalog.Facade,
	membership *membership.Facade,
	accessControl *accesscontrol.Facade,
	loans LoanRepository,
	reservations ReservationRepository,
	bus events.EventBus,
	txFactory tx.TransactionalContextFactory,
	newID func() string,
	clock func() time.Time,
	logger *slog.Logger,
) *Facade {
	return &Facade{
		catalog:       catalog,
		membership:    membership,
		accessControl: accessControl,
		loans:         loans,
		reservations:  reservations,
		bus:           bus,
		txFactory:     txFactory,
		newID:         newID,
		clock:         clock,
		logger:        logger,
	}
}

// addDays returns t shifted forward by days calendar days. Kept as an
// unexported helper so Borrow (Slice 4) and ReturnLoan (Slice 6) compute
// due dates the same way.
func addDays(t time.Time, days int) time.Time {
	return t.AddDate(0, 0, days)
}
