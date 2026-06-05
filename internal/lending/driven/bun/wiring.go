package bun

import (
	"log/slog"
	"time"

	"github.com/google/uuid"
	upstreambun "github.com/uptrace/bun"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
	txbun "github.com/akshayvadher/test-n-design-go/internal/shared/tx/bun"
)

// Wiring bundles the production-grade lending Facade together with the
// shared deps Phase-4's saga consumer needs to plug into the SAME tx +
// reservation substrate the facade was wired with.
//
// Reservations and TxFactory are exposed so AutoLoanOnReturnConsumer can
// be constructed in internal/app/wiring.go without re-allocating its own
// repository or building a second tx factory — guaranteeing claim +
// un-fulfil txes go through the same path the facade's own writes use.
type Wiring struct {
	Facade       *lending.Facade
	Reservations lending.ReservationRepository
	TxFactory    tx.TransactionalContextFactory
}

// WireFacade builds a production-grade lending Facade backed by the
// supplied *bun.DB and returns the facade alongside the shared deps the
// saga consumer reuses. The bus + tx factory close over the same
// events.EventBus so staged events publish through the same channel the
// facade's direct bus.Publish calls reach.
//
// This helper lives in the bun adapter package so the composition root
// (internal/app/wiring.go) imports it as the single source of production
// lending wiring; the parent lending package depends on nothing outside
// its domain.
func WireFacade(
	db *upstreambun.DB,
	bus events.EventBus,
	catalogFacade *catalog.Facade,
	membershipFacade *membership.Facade,
	accessControlFacade *accesscontrol.Facade,
	logger *slog.Logger,
) Wiring {
	reservations := NewReservationRepository(db)
	// The factory closes over the same bus instance so staged events
	// publish through the same channel the facade's direct bus.Publish
	// calls reach. Per BunTransactionalContext's contract, callers
	// construct a fresh context per business operation.
	txFactory := func() tx.TransactionalContext {
		return txbun.NewTransactionalContext(db, bus, logger)
	}
	facade := lending.NewFacade(
		catalogFacade,
		membershipFacade,
		accessControlFacade,
		NewLoanRepository(db),
		reservations,
		bus,
		txFactory,
		uuid.NewString,
		time.Now,
		logger,
	)
	return Wiring{
		Facade:       facade,
		Reservations: reservations,
		TxFactory:    txFactory,
	}
}
