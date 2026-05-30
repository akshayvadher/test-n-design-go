package lending

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// AutoLoanOnReturnConsumer subscribes to LoanReturned events and walks the
// pending reservation queue for the returned book. On a LoanReturned, the
// consumer:
//
//  1. Acquires a per-book mutex (lazy-allocated in bookLocks) so concurrent
//     returns of the SAME book serialise.
//  2. Lists pending reservations for the returned bookId (FIFO by ReservedAt).
//  3. Walks the queue skipping ineligible reservers; on the first eligible
//     reservation, attempts the auto-loan flow.
//  4. attemptAutoLoan claims the reservation inside a fresh tx (own-tx
//     bundles the FulfilledAt write with the staged ReservationFulfilled
//     event), calls lending.Borrow OUTSIDE the tx, then either publishes
//     AutoLoanOpened (success) or un-fulfils inside a SECOND tx + publishes
//     AutoLoanFailed (failure). AutoLoanFailed publishes OUTSIDE the un-fulfil
//     tx so it lands on the bus even when the un-fulfil rolls back.
//
// Errors are SWALLOWED — the bus handler returns nil unconditionally. The
// consumer logs at error level with structured fields (book_id, reservation_id,
// member_id, step, error) for the operator.
//
// Per-book serialisation prevents two concurrent returns of copies of the
// SAME book from both claim-writing the same head-of-queue reservation in
// the goroutine-equivalent race the TS source documents. The real fix is a
// DB unique constraint on (book_id, member_id) WHERE fulfilled_at IS NULL —
// deliberately deferred to a later phase.
//
// Lifecycle: Start subscribes; Stop unsubscribes; both are idempotent and
// driven explicitly by the composition root. No init(); no goroutine started
// from the constructor.
type AutoLoanOnReturnConsumer struct {
	bus          events.EventBus
	membership   *membership.Facade
	reservations ReservationRepository
	lending      *Facade
	txFactory    tx.TransactionalContextFactory
	clock        func() time.Time
	logger       *slog.Logger

	bookLocksMu sync.Mutex
	bookLocks   map[catalog.BookId]*sync.Mutex

	lifecycleMu sync.Mutex
	unsubscribe events.Unsubscribe
	started     bool
}

// AutoLoanOnReturnConsumerDeps is the constructor argument struct. Bus,
// Membership, Reservations, Lending and TxFactory are required (a nil here
// is a programming error and will panic on first use). Clock and Logger are
// optional — nil substitutes time.Now and a silent slog logger respectively.
type AutoLoanOnReturnConsumerDeps struct {
	Bus          events.EventBus
	Membership   *membership.Facade
	Reservations ReservationRepository
	Lending      *Facade
	TxFactory    tx.TransactionalContextFactory
	Clock        func() time.Time
	Logger       *slog.Logger
}

// NewAutoLoanOnReturnConsumer wires the consumer with the supplied deps,
// substituting defaults for the optional fields. Does NOT subscribe — call
// Start to attach the handler.
func NewAutoLoanOnReturnConsumer(deps AutoLoanOnReturnConsumerDeps) *AutoLoanOnReturnConsumer {
	clock := deps.Clock
	if clock == nil {
		clock = time.Now
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &AutoLoanOnReturnConsumer{
		bus:          deps.Bus,
		membership:   deps.Membership,
		reservations: deps.Reservations,
		lending:      deps.Lending,
		txFactory:    deps.TxFactory,
		clock:        clock,
		logger:       logger,
		bookLocks:    map[catalog.BookId]*sync.Mutex{},
	}
}

// Start attaches the LoanReturned handler to the bus. Idempotent — a second
// Start without an intervening Stop is a no-op.
func (c *AutoLoanOnReturnConsumer) Start(_ context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.started {
		return nil
	}
	c.unsubscribe = c.bus.Subscribe(LoanReturned{}.Type(), c.handleLoanReturned)
	c.started = true
	return nil
}

// Stop detaches the LoanReturned handler. Idempotent — calling Stop without
// a prior Start, or twice in a row, is a no-op.
func (c *AutoLoanOnReturnConsumer) Stop(_ context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if !c.started {
		return nil
	}
	if c.unsubscribe != nil {
		c.unsubscribe()
		c.unsubscribe = nil
	}
	c.started = false
	return nil
}

// handleLoanReturned is the bus subscription target. Returns nil
// unconditionally — saga consumers swallow their own errors so the bus
// fanout continues to other subscribers.
func (c *AutoLoanOnReturnConsumer) handleLoanReturned(ctx context.Context, evt events.DomainEvent) error {
	returned, ok := evt.(LoanReturned)
	if !ok {
		c.logger.Warn("auto-loan consumer received non-LoanReturned event",
			slog.String("event_type", evt.Type()),
		)
		return nil
	}
	lock := c.acquireBookLock(returned.BookId)
	lock.Lock()
	defer lock.Unlock()
	c.processReturn(ctx, returned)
	return nil
}

// acquireBookLock returns the *sync.Mutex for bookId, lazy-allocating a
// fresh mutex on the first request. Subsequent requests for the same bookId
// receive the SAME mutex so concurrent handlers serialise.
func (c *AutoLoanOnReturnConsumer) acquireBookLock(bookId catalog.BookId) *sync.Mutex {
	c.bookLocksMu.Lock()
	defer c.bookLocksMu.Unlock()
	if existing, ok := c.bookLocks[bookId]; ok {
		return existing
	}
	fresh := &sync.Mutex{}
	c.bookLocks[bookId] = fresh
	return fresh
}

// processReturn lists pending reservations for the returned bookId, skips
// ineligible reservers, and attempts the auto-loan flow on the first
// eligible reservation. Stops after one attempt (matching the TS source's
// `return` after the first attempt).
func (c *AutoLoanOnReturnConsumer) processReturn(ctx context.Context, returned LoanReturned) {
	pending, err := c.reservations.ListPendingReservationsForBook(ctx, returned.BookId)
	if err != nil {
		c.logger.Error("auto-loan: list pending reservations failed",
			slog.String("book_id", string(returned.BookId)),
			slog.String("step", "list_pending"),
			slog.String("error", err.Error()),
		)
		return
	}
	for _, reservation := range pending {
		eligibility, err := c.membership.CheckEligibility(ctx, reservation.MemberId)
		if err != nil {
			c.logger.Error("auto-loan: eligibility check failed",
				slog.String("book_id", string(returned.BookId)),
				slog.String("reservation_id", string(reservation.ReservationId)),
				slog.String("member_id", string(reservation.MemberId)),
				slog.String("step", "check_eligibility"),
				slog.String("error", err.Error()),
			)
			continue
		}
		if !eligibility.Eligible {
			continue
		}
		c.attemptAutoLoan(ctx, reservation, returned.CopyId)
		return
	}
}

// attemptAutoLoan runs the claim → borrow → publish sequence for one
// eligible reservation. Failure paths route through tryUnfulfilClaim +
// publishAutoLoanFailed.
func (c *AutoLoanOnReturnConsumer) attemptAutoLoan(ctx context.Context, reservation ReservationDto, copyId catalog.CopyId) {
	claimed, err := c.claimReservation(ctx, reservation)
	if err != nil {
		c.logger.Error("auto-loan: claim reservation failed",
			slog.String("book_id", string(reservation.BookId)),
			slog.String("reservation_id", string(reservation.ReservationId)),
			slog.String("member_id", string(reservation.MemberId)),
			slog.String("step", "claim"),
			slog.String("error", err.Error()),
		)
		return
	}
	loan, borrowErr := c.lending.Borrow(ctx, c.borrowAuth(claimed.MemberId), copyId)
	if borrowErr != nil {
		c.tryUnfulfilClaim(ctx, claimed)
		c.publishAutoLoanFailed(ctx, claimed, borrowErr.Error())
		return
	}
	c.publishAutoLoanOpened(ctx, loan, claimed)
}

// borrowAuth builds the AuthUser the saga uses to call lending.Borrow on
// behalf of the reserver. Role is MEMBER — matches the TS source.
func (c *AutoLoanOnReturnConsumer) borrowAuth(memberId membership.MemberId) accesscontrol.AuthUser {
	return accesscontrol.AuthUser{
		MemberID: string(memberId),
		Role:     accesscontrol.RoleMember,
	}
}

// claimReservation writes FulfilledAt onto the reservation inside a fresh
// tx, bundled with the staged ReservationFulfilled event. If the tx returns
// an error, the staged event was suppressed by the rollback — no
// ReservationFulfilled lands on the bus.
func (c *AutoLoanOnReturnConsumer) claimReservation(ctx context.Context, reservation ReservationDto) (ReservationDto, error) {
	fulfilledAt := c.clock()
	claimed := cloneReservationDto(reservation)
	claimed.FulfilledAt = &fulfilledAt

	txc := c.txFactory()
	err := txc.Run(ctx, func(ctx context.Context) error {
		if err := c.reservations.SaveReservation(ctx, claimed, txc); err != nil {
			return err
		}
		txc.StageEvent(ReservationFulfilled{
			ReservationId: claimed.ReservationId,
			MemberId:      claimed.MemberId,
			BookId:        claimed.BookId,
			FulfilledAt:   fulfilledAt,
		})
		return nil
	})
	if err != nil {
		return ReservationDto{}, err
	}
	return claimed, nil
}

// tryUnfulfilClaim writes FulfilledAt=nil back onto the reservation inside
// a fresh tx, bundled with the staged ReservationUnfulfilled event. A tx
// failure is logged + swallowed — AutoLoanFailed still fires regardless
// (it is published OUTSIDE this tx). The reservation may be left in the
// inconsistent fulfilled-but-no-loan state; the operator investigates via
// the error log.
func (c *AutoLoanOnReturnConsumer) tryUnfulfilClaim(ctx context.Context, claimed ReservationDto) {
	unfulfilled := cloneReservationDto(claimed)
	unfulfilled.FulfilledAt = nil

	txc := c.txFactory()
	err := txc.Run(ctx, func(ctx context.Context) error {
		if err := c.reservations.SaveReservation(ctx, unfulfilled, txc); err != nil {
			return err
		}
		txc.StageEvent(ReservationUnfulfilled{
			ReservationId: unfulfilled.ReservationId,
			MemberId:      unfulfilled.MemberId,
			BookId:        unfulfilled.BookId,
			UnfulfilledAt: c.clock(),
		})
		return nil
	})
	if err != nil {
		c.logger.Error("auto-loan: un-fulfil claim failed",
			slog.String("book_id", string(claimed.BookId)),
			slog.String("reservation_id", string(claimed.ReservationId)),
			slog.String("member_id", string(claimed.MemberId)),
			slog.String("step", "unfulfil"),
			slog.String("error", err.Error()),
		)
	}
}

// publishAutoLoanOpened publishes the AutoLoanOpened event via direct
// bus.Publish (OUTSIDE any tx, because Borrow already ran outside the tx).
// Bus errors are logged but not surfaced.
func (c *AutoLoanOnReturnConsumer) publishAutoLoanOpened(ctx context.Context, loan LoanDto, reservation ReservationDto) {
	evt := AutoLoanOpened{
		BookId:        loan.BookId,
		LoanId:        loan.LoanId,
		MemberId:      loan.MemberId,
		ReservationId: reservation.ReservationId,
		OpenedAt:      c.clock(),
	}
	if err := c.bus.Publish(ctx, evt); err != nil {
		c.logger.Error("auto-loan: AutoLoanOpened publish failed",
			slog.String("book_id", string(loan.BookId)),
			slog.String("loan_id", string(loan.LoanId)),
			slog.String("reservation_id", string(reservation.ReservationId)),
			slog.String("step", "publish_opened"),
			slog.String("error", err.Error()),
		)
	}
}

// publishAutoLoanFailed publishes the AutoLoanFailed event via direct
// bus.Publish (OUTSIDE the un-fulfil tx). This is the canonical invariant:
// even when the un-fulfil tx rolls back, AutoLoanFailed remains observable
// on the bus.
func (c *AutoLoanOnReturnConsumer) publishAutoLoanFailed(ctx context.Context, reservation ReservationDto, reason string) {
	evt := AutoLoanFailed{
		BookId:        reservation.BookId,
		ReservationId: reservation.ReservationId,
		MemberId:      reservation.MemberId,
		Reason:        reason,
		FailedAt:      c.clock(),
	}
	if err := c.bus.Publish(ctx, evt); err != nil {
		c.logger.Error("auto-loan: AutoLoanFailed publish failed",
			slog.String("book_id", string(reservation.BookId)),
			slog.String("reservation_id", string(reservation.ReservationId)),
			slog.String("step", "publish_failed"),
			slog.String("error", err.Error()),
		)
	}
}
