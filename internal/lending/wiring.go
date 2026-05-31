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

// BunWiring bundles the production-grade lending Facade together with the
// shared deps Phase-4's saga consumer needs to plug into the SAME tx +
// reservation substrate the facade was wired with.
//
// Reservations and TxFactory are exposed so AutoLoanOnReturnConsumer can be
// constructed in internal/app/wiring.go without re-allocating its own
// repository or building a second tx factory — guaranteeing claim + un-fulfil
// txes go through the same path the facade's own writes use.
type BunWiring struct {
	Facade       *Facade
	Reservations ReservationRepository
	TxFactory    tx.TransactionalContextFactory
}

// WireBunFacade builds a production-grade lending Facade backed by the
// supplied *bun.DB and returns the facade alongside the shared deps the saga
// consumer reuses. The bus + tx factory close over the same events.EventBus
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
) BunWiring {
	reservations := NewBunReservationRepository(db)
	// The factory closes over the same bus instance so staged events
	// publish through the same channel the facade's direct bus.Publish
	// calls reach. Per BunTransactionalContext's contract, callers
	// construct a fresh context per business operation.
	txFactory := func() tx.TransactionalContext {
		return tx.NewBunTransactionalContext(db, bus, logger)
	}
	facade := NewFacadeWithOverrides(Overrides{
		Catalog:       catalogFacade,
		Membership:    membershipFacade,
		AccessControl: accessControlFacade,
		Loans:         NewBunLoanRepository(db),
		Reservations:  reservations,
		Bus:           bus,
		TxFactory:     txFactory,
		NewID:         uuid.NewString,
		Clock:         time.Now,
		Logger:        logger,
	})
	return BunWiring{
		Facade:       facade,
		Reservations: reservations,
		TxFactory:    txFactory,
	}
}
