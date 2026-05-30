package lending

import (
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// WireBunFacade builds a production-grade lending Facade backed by the
// supplied *bun.DB. The bus + tx factory close over the same events.EventBus
// so staged events publish through the same channel the facade's direct
// bus.Publish calls reach (the consistency rule documented on
// Overrides.TxFactory).
//
// This helper lives in package lending so the composition root
// (internal/app/wiring.go) can stay free of internal/shared/tx imports —
// avoiding an import cycle through test/support, which imports
// internal/app, which would otherwise import internal/shared/tx, which
// the integration tx tests import test/support back from.
func WireBunFacade(
	db *bun.DB,
	bus events.EventBus,
	catalogFacade *catalog.Facade,
	membershipFacade *membership.Facade,
	accessControlFacade *accesscontrol.Facade,
	logger *slog.Logger,
) *Facade {
	return NewFacadeWithOverrides(Overrides{
		Catalog:       catalogFacade,
		Membership:    membershipFacade,
		AccessControl: accessControlFacade,
		Loans:         NewBunLoanRepository(db),
		Reservations:  NewBunReservationRepository(db),
		Bus:           bus,
		// The factory closes over the same bus instance so staged events
		// publish through the same channel the facade's direct bus.Publish
		// calls reach. Per BunTransactionalContext's contract, callers
		// construct a fresh context per business operation.
		TxFactory: func() tx.TransactionalContext {
			return tx.NewBunTransactionalContext(db, bus, logger)
		},
		NewID:  uuid.NewString,
		Clock:  time.Now,
		Logger: logger,
	})
}
