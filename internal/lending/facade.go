package lending

import (
	"context"
	"log/slog"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// LoanDurationDays is the loan window in days. Matches the source TS
// constant LOAN_DURATION_DAYS = 14. Borrow uses it for due-date
// computation; declared at the top of facade.go because it is a property
// of the facade's domain policy, not of any single method.
const LoanDurationDays = 14

// Facade is the only public surface of the lending module. Unexported
// fields keep collaborators encapsulated; the composition root wires them
// via NewFacade and tests substitute them via NewFacadeWithOverrides.
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

// Borrow opens a loan against an AVAILABLE copy for an eligible MEMBER.
//
// Architectural rules embodied here, in order:
//
//  1. Authorize first — accessControl.Authorize gates the call before any
//     state is touched.
//  2. Cross-module reads happen BEFORE the tx opens (membership eligibility,
//     catalog copy lookup); failures bubble unchanged.
//  3. The own-tx wraps only own-module writes (loan save) + the staged
//     LoanOpened event. No cross-module writes inside the tx.
//  4. The post-commit cross-module mutation (catalog.MarkCopyUnavailable)
//     runs AFTER the tx returns — never via tx.Stage.
//  5. LoanOpened publishes BEFORE MarkCopyUnavailable runs: staged events
//     publish during commit, and the catalog mutation runs after Run
//     returns. This ordering is what subscribers can rely on.
//
// Known post-commit-failure gap: if MarkCopyUnavailable fails after commit,
// the loan is already persisted and LoanOpened is already on the bus. The
// teaching repo surfaces the catalog error to the caller and accepts the
// inconsistency rather than introducing a saga (Phase 4's auto-loan saga
// covers a different scenario; reconciliation is left to a future phase).
func (f *Facade) Borrow(ctx context.Context, authUser accesscontrol.AuthUser, copyId catalog.CopyId) (LoanDto, error) {
	if err := f.accessControl.Authorize(authUser, "lending", "borrow"); err != nil {
		return LoanDto{}, err
	}
	if err := f.requireEligible(ctx, membership.MemberId(authUser.MemberID)); err != nil {
		return LoanDto{}, err
	}
	copy, err := f.catalog.FindCopy(ctx, copyId)
	if err != nil {
		return LoanDto{}, err
	}
	if copy.Status != catalog.CopyStatusAvailable {
		return LoanDto{}, &CopyUnavailableError{CopyId: copyId}
	}

	loan := f.buildLoan(membership.MemberId(authUser.MemberID), copy.CopyId, copy.BookId)

	txc := f.txFactory()
	if err := txc.Run(ctx, func(ctx context.Context) error {
		if err := f.loans.SaveLoan(ctx, loan, txc); err != nil {
			return err
		}
		txc.StageEvent(loanOpenedEvent(loan))
		return nil
	}); err != nil {
		return LoanDto{}, err
	}

	if _, err := f.catalog.MarkCopyUnavailable(ctx, copyId); err != nil {
		return LoanDto{}, err
	}
	return loan, nil
}

// Reserve queues a reservation for a book on behalf of an eligible member.
//
// Pure staged-event flow — no post-commit cross-module side effect.
// Compare with Borrow which calls catalog.MarkCopyUnavailable after commit.
//
// Reserve does NOT validate the BookId against catalog; the source TS
// delegates that responsibility to the auto-loan consumer (Phase 4),
// which fails-soft on unknown books. Matching that 1:1.
func (f *Facade) Reserve(ctx context.Context, memberId membership.MemberId, bookId catalog.BookId) (ReservationDto, error) {
	if err := f.requireEligible(ctx, memberId); err != nil {
		return ReservationDto{}, err
	}

	reservation := ReservationDto{
		ReservationId: ReservationId(f.newID()),
		MemberId:      memberId,
		BookId:        bookId,
		ReservedAt:    f.clock(),
		FulfilledAt:   nil,
	}

	txc := f.txFactory()
	if err := txc.Run(ctx, func(ctx context.Context) error {
		if err := f.reservations.SaveReservation(ctx, reservation, txc); err != nil {
			return err
		}
		txc.StageEvent(reservationQueuedEvent(reservation))
		return nil
	}); err != nil {
		return ReservationDto{}, err
	}
	return reservation, nil
}

// ReturnLoan closes a loan and flips the copy back to AVAILABLE.
//
// LoanReturned publishes via bus.Publish AFTER the catalog mark-available.
// NOT via tx.StageEvent (which would publish during commit, BEFORE the
// catalog mutation). Phase 4's auto-loan saga consumer relies on this
// ordering: by the time it receives LoanReturned, the copy is AVAILABLE
// and a new lending.Borrow will succeed. Diverges from the Borrow pattern
// (where LoanOpened IS staged) precisely because Borrow's consumers don't
// need to observe the post-commit catalog state.
//
// Known gaps:
//   - Not idempotent: calling twice writes a second returnedAt and
//     publishes a second LoanReturned. Matches the TS source's behaviour.
//   - Post-commit catalog failure leaves the loan flagged returned but
//     the copy UNAVAILABLE. Caller receives the catalog error.
//   - Bus publish failures are logged (error level) but do NOT surface;
//     the durable state has already settled.
func (f *Facade) ReturnLoan(ctx context.Context, loanId LoanId) (LoanDto, error) {
	loan, err := f.loans.FindLoanById(ctx, loanId)
	if err != nil {
		return LoanDto{}, err
	}
	if loan == nil {
		return LoanDto{}, &LoanNotFoundError{LoanId: loanId}
	}

	returnedAt := f.clock()
	returned := *loan
	returned.ReturnedAt = &returnedAt

	txc := f.txFactory()
	if err := txc.Run(ctx, func(ctx context.Context) error {
		return f.loans.SaveLoan(ctx, returned, txc)
	}); err != nil {
		return LoanDto{}, err
	}

	if _, err := f.catalog.MarkCopyAvailable(ctx, returned.CopyId); err != nil {
		return LoanDto{}, err
	}

	if err := f.bus.Publish(ctx, loanReturnedEvent(returned)); err != nil {
		f.logger.Error("LoanReturned publish failed",
			slog.String("loan_id", string(returned.LoanId)),
			slog.String("error", err.Error()),
		)
	}
	return returned, nil
}

// requireEligible loads the member's eligibility and translates an
// ineligible result into a *MemberIneligibleError, defaulting Reason to
// "INELIGIBLE" if the membership facade returned an empty reason.
func (f *Facade) requireEligible(ctx context.Context, memberId membership.MemberId) error {
	eligibility, err := f.membership.CheckEligibility(ctx, memberId)
	if err != nil {
		return err
	}
	if eligibility.Eligible {
		return nil
	}
	reason := eligibility.Reason
	if reason == "" {
		reason = "INELIGIBLE"
	}
	return &MemberIneligibleError{MemberId: memberId, Reason: reason}
}

// buildLoan composes the LoanDto from the cross-module reads. Kept as a
// helper so Borrow stays at one level of abstraction.
func (f *Facade) buildLoan(memberId membership.MemberId, copyId catalog.CopyId, bookId catalog.BookId) LoanDto {
	borrowedAt := f.clock()
	return LoanDto{
		LoanId:     LoanId(f.newID()),
		MemberId:   memberId,
		CopyId:     copyId,
		BookId:     bookId,
		BorrowedAt: borrowedAt,
		DueDate:    addDays(borrowedAt, LoanDurationDays),
		ReturnedAt: nil,
	}
}

// loanOpenedEvent projects a LoanDto into the LoanOpened event payload.
func loanOpenedEvent(loan LoanDto) LoanOpened {
	return LoanOpened{
		LoanId:     loan.LoanId,
		MemberId:   loan.MemberId,
		CopyId:     loan.CopyId,
		BookId:     loan.BookId,
		BorrowedAt: loan.BorrowedAt,
		DueDate:    loan.DueDate,
	}
}

// loanReturnedEvent projects a returned LoanDto into the LoanReturned
// event payload. The caller guarantees loan.ReturnedAt is non-nil.
func loanReturnedEvent(loan LoanDto) LoanReturned {
	return LoanReturned{
		LoanId:     loan.LoanId,
		MemberId:   loan.MemberId,
		CopyId:     loan.CopyId,
		BookId:     loan.BookId,
		ReturnedAt: *loan.ReturnedAt,
	}
}

// reservationQueuedEvent projects a ReservationDto into the
// ReservationQueued event payload.
func reservationQueuedEvent(reservation ReservationDto) ReservationQueued {
	return ReservationQueued{
		ReservationId: reservation.ReservationId,
		MemberId:      reservation.MemberId,
		BookId:        reservation.BookId,
		ReservedAt:    reservation.ReservedAt,
	}
}

// addDays returns t shifted forward by days calendar days. Kept as an
// unexported helper so Borrow and ReturnLoan compute due dates the same
// way.
func addDays(t time.Time, days int) time.Time {
	return t.AddDate(0, 0, days)
}
