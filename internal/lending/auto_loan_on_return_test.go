// auto_loan_on_return_test.go ports the saga consumer scenarios from
// apps/library/src/lending/auto-loan-on-return.consumer.spec.ts. The file
// combines Slice 1 (behavioural scenarios) and Slice 2 (atomicity
// invariants — claim-tx rollback, un-fulfil-tx rollback, lending.AutoLoanFailed
// outside any tx, per-book serialisation) in a single file. Stdlib testing
// only — no testify, no mock library. Spec-local decorators
// (throwingOnceSaveLoanRepository, throwingOnceSaveReservationRepository)
// live at the bottom of the file. They are unexported and never imported
// by any other package.
//
// Borrow-failure injection strategy: a throwingOnceSaveLoanRepository
// (Nth-call variant) is shared between the scene's lending facade and the
// saga's lending facade. The saga's auto-Borrow is the Nth SaveLoan call
// after seed-time: arming the failure on that call makes the consumer's
// Borrow return an error — exactly the way the source TS catch-block
// observes it.
package lending_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"testing"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	catalogmemory "github.com/akshayvadher/test-n-design-go/internal/catalog/driven/memory"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	lendingmemory "github.com/akshayvadher/test-n-design-go/internal/lending/driven/memory"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	membershipmemory "github.com/akshayvadher/test-n-design-go/internal/membership/driven/memory"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// -----------------------------------------------------------------------------
// Scene helper — builds the full saga scene with shared substrates so
// cross-module flows are observable end-to-end.
// -----------------------------------------------------------------------------

// consumerScene aggregates the lending facade, the saga consumer (sharing
// the same facade), the shared cross-module facades, the captured-events
// sink, and the shared repositories tests assert against.
type consumerScene struct {
	t            *testing.T
	ctx          context.Context
	facade       *lending.Facade
	consumer     *lending.AutoLoanOnReturnConsumer
	catalog      *catalog.Facade
	membership   *membership.Facade
	bus          *events.InMemoryEventBus
	loans        lending.LoanRepository
	loansMem     *lendingmemory.LoanRepository
	reservations lending.ReservationRepository
	collected    *collectedEvents
	logBuf       *bytes.Buffer
}

// consumerSceneOpts lets a test inject decorators / overrides before the
// scene is built.
type consumerSceneOpts struct {
	// Loans swaps the shared loan repo. nil means a fresh
	// InMemoryLoanRepository. Tests that need to arm a borrow failure pass
	// a throwingOnceLoanRepository here.
	Loans lending.LoanRepository
	// Reservations swaps the shared reservation repo. nil means a fresh
	// InMemoryReservationRepository.
	Reservations lending.ReservationRepository
}

// buildConsumerScene wires the full saga scene. The lending facade and the
// consumer SHARE every substrate so claims, events and the borrow flow all
// interact end-to-end. Returns the scene with the consumer already started.
func buildConsumerScene(t *testing.T, opts consumerSceneOpts) *consumerScene {
	t.Helper()

	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelError}))

	catalogFacade := catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{
		NewID:  sequentialIds("cat"),
		Logger: logger,
	})
	membershipFacade := membershipmemory.NewFacadeWithOverrides(membershipmemory.Overrides{
		NewID:  sequentialIds("mem"),
		Logger: logger,
	})

	var loansRepo lending.LoanRepository = opts.Loans
	var loansMem *lendingmemory.LoanRepository
	if loansRepo == nil {
		loansMem = lendingmemory.NewLoanRepository()
		loansRepo = loansMem
	}
	var reservationsRepo lending.ReservationRepository = opts.Reservations
	if reservationsRepo == nil {
		reservationsRepo = lendingmemory.NewReservationRepository()
	}

	bus := events.NewInMemoryEventBus(logger)
	txFactory := func() tx.TransactionalContext {
		return tx.NewInMemoryTransactionalContext(bus, logger)
	}

	facade := lendingmemory.NewFacadeWithOverrides(lendingmemory.Overrides{
		Catalog:      catalogFacade,
		Membership:   membershipFacade,
		Loans:        loansRepo,
		Reservations: reservationsRepo,
		Bus:          bus,
		TxFactory:    txFactory,
		NewID:        sequentialIds("loan"),
		Clock:        fixedClock,
		Logger:       logger,
	})

	consumer := lending.NewAutoLoanOnReturnConsumer(lending.AutoLoanOnReturnConsumerDeps{
		Bus:          bus,
		Membership:   membershipFacade,
		Reservations: reservationsRepo,
		Lending:      facade,
		TxFactory:    txFactory,
		Clock:        fixedClock,
		Logger:       logger,
	})

	collected := &collectedEvents{}
	subscribeAllSagaEvents(bus, collected)

	if err := consumer.Start(context.Background()); err != nil {
		t.Fatalf("consumer.Start: %v", err)
	}
	t.Cleanup(func() {
		_ = consumer.Stop(context.Background())
	})

	return &consumerScene{
		t:            t,
		ctx:          context.Background(),
		facade:       facade,
		consumer:     consumer,
		catalog:      catalogFacade,
		membership:   membershipFacade,
		bus:          bus,
		loans:        loansRepo,
		loansMem:     loansMem,
		reservations: reservationsRepo,
		collected:    collected,
		logBuf:       logBuf,
	}
}

// subscribeAllSagaEvents registers the captured-events sink against every
// event type the saga touches.
func subscribeAllSagaEvents(bus events.EventBus, collected *collectedEvents) {
	for _, eventType := range []string{
		"LoanOpened",
		"LoanReturned",
		"ReservationQueued",
		"ReservationFulfilled",
		"ReservationUnfulfilled",
		"AutoLoanOpened",
		"AutoLoanFailed",
	} {
		bus.Subscribe(eventType, func(_ context.Context, evt events.DomainEvent) error {
			collected.append(evt)
			return nil
		})
	}
}

// seedAvailableCopyForConsumer mirrors seedAvailableCopy from facade_test.go.
func seedAvailableCopyForConsumer(t *testing.T, s *consumerScene, seq int) (catalog.BookDto, catalog.CopyDto) {
	t.Helper()
	isbn := catalog.Isbn("978-" + padLeft(strconv.Itoa(seq), 10, '0'))
	book, err := s.catalog.AddBook(s.ctx, catalog.SampleNewBook(catalog.WithIsbn(isbn)))
	if err != nil {
		t.Fatalf("seedAvailableCopy: AddBook: %v", err)
	}
	copyDto, err := s.catalog.RegisterCopy(s.ctx, book.BookId, catalog.SampleNewCopy(catalog.WithBookId(book.BookId)))
	if err != nil {
		t.Fatalf("seedAvailableCopy: RegisterCopy: %v", err)
	}
	return book, copyDto
}

func seedExtraCopy(t *testing.T, s *consumerScene, bookId catalog.BookId) catalog.CopyDto {
	t.Helper()
	copyDto, err := s.catalog.RegisterCopy(s.ctx, bookId, catalog.SampleNewCopy(catalog.WithBookId(bookId)))
	if err != nil {
		t.Fatalf("seedExtraCopy: %v", err)
	}
	return copyDto
}

func registerMemberForConsumer(t *testing.T, s *consumerScene, seq int, name string) membership.MemberDto {
	t.Helper()
	email := "consumer-member-" + strconv.Itoa(seq) + "@lib.test"
	member, err := s.membership.RegisterMember(s.ctx, membership.SampleNewMember(
		membership.WithName(name),
		membership.WithEmail(email),
	))
	if err != nil {
		t.Fatalf("registerMember: %v", err)
	}
	return member
}

func requireEventTypes(t *testing.T, collected *collectedEvents, want []string) {
	t.Helper()
	got := collected.types()
	if !equalStrings(got, want) {
		t.Errorf("event types: got %v, want %v", got, want)
	}
}

func findReservation(t *testing.T, repo lending.ReservationRepository, id lending.ReservationId) lending.ReservationDto {
	t.Helper()
	res, err := repo.FindReservationById(context.Background(), id)
	if err != nil {
		t.Fatalf("FindReservationById: %v", err)
	}
	if res == nil {
		t.Fatalf("reservation %q: not found", id)
	}
	return *res
}

// loansFor returns the loans for memberId. Works regardless of whether the
// scene's loan repo is the throwing decorator or the bare InMemoryLoanRepository.
func loansFor(t *testing.T, s *consumerScene, memberId membership.MemberId) []lending.LoanDto {
	t.Helper()
	loans, err := s.loans.ListLoansForMember(s.ctx, memberId)
	if err != nil {
		t.Fatalf("ListLoansForMember: %v", err)
	}
	return loans
}

// -----------------------------------------------------------------------------
// Slice 1 — happy paths
// -----------------------------------------------------------------------------

func TestAutoLoanConsumer_HeadOfQueueReserverGetsAutoLoan(t *testing.T) {
	s := buildConsumerScene(t, consumerSceneOpts{})
	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	bob := registerMemberForConsumer(t, s, 2, "Bob")

	aliceLoan, err := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	bobReservation, err := s.facade.Reserve(s.ctx, bob.MemberId, book.BookId)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	s.collected.reset()

	if _, err := s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId); err != nil {
		t.Fatalf("ReturnLoan: %v", err)
	}

	bobLoans := loansFor(t, s, bob.MemberId)
	if len(bobLoans) != 1 {
		t.Fatalf("bob loans: got %d, want 1", len(bobLoans))
	}
	if bobLoans[0].CopyId != copyDto.CopyId {
		t.Errorf("bob loan CopyId: got %q, want %q", bobLoans[0].CopyId, copyDto.CopyId)
	}
	if bobLoans[0].ReturnedAt != nil {
		t.Errorf("bob loan ReturnedAt: got %v, want nil", *bobLoans[0].ReturnedAt)
	}

	requireEventTypes(t, s.collected, []string{
		"LoanReturned",
		"ReservationFulfilled",
		"LoanOpened",
		"AutoLoanOpened",
	})

	bobResAfter := findReservation(t, s.reservations, bobReservation.ReservationId)
	if bobResAfter.FulfilledAt == nil || !bobResAfter.FulfilledAt.Equal(fixedNow) {
		t.Errorf("bob reservation FulfilledAt: got %v, want %v", bobResAfter.FulfilledAt, fixedNow)
	}
}

func TestAutoLoanConsumer_AutoLoanLeavesCopyUnavailable(t *testing.T) {
	s := buildConsumerScene(t, consumerSceneOpts{})
	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	bob := registerMemberForConsumer(t, s, 2, "Bob")

	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	_, _ = s.facade.Reserve(s.ctx, bob.MemberId, book.BookId)
	_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)

	got, _ := s.catalog.FindCopy(s.ctx, copyDto.CopyId)
	if got.Status != catalog.CopyStatusUnavailable {
		t.Errorf("copy.Status: got %q, want UNAVAILABLE", got.Status)
	}
}

func TestAutoLoanConsumer_EmptyQueueNoOp(t *testing.T) {
	s := buildConsumerScene(t, consumerSceneOpts{})
	_, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")

	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	s.collected.reset()

	if _, err := s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId); err != nil {
		t.Fatalf("ReturnLoan: %v", err)
	}

	requireEventTypes(t, s.collected, []string{"LoanReturned"})

	got, _ := s.catalog.FindCopy(s.ctx, copyDto.CopyId)
	if got.Status != catalog.CopyStatusAvailable {
		t.Errorf("copy.Status: got %q, want AVAILABLE", got.Status)
	}
}

func TestAutoLoanConsumer_FifoFirstReserverWins(t *testing.T) {
	s := buildConsumerScene(t, consumerSceneOpts{})
	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	bob := registerMemberForConsumer(t, s, 2, "Bob")
	carol := registerMemberForConsumer(t, s, 3, "Carol")

	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	bobReservation, _ := s.facade.Reserve(s.ctx, bob.MemberId, book.BookId)
	carolReservation, _ := s.facade.Reserve(s.ctx, carol.MemberId, book.BookId)
	s.collected.reset()

	_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)

	bobLoans := loansFor(t, s, bob.MemberId)
	carolLoans := loansFor(t, s, carol.MemberId)
	if len(bobLoans) != 1 {
		t.Errorf("bob loans: got %d, want 1", len(bobLoans))
	}
	if len(carolLoans) != 0 {
		t.Errorf("carol loans: got %d, want 0", len(carolLoans))
	}

	bobRes := findReservation(t, s.reservations, bobReservation.ReservationId)
	carolRes := findReservation(t, s.reservations, carolReservation.ReservationId)
	if bobRes.FulfilledAt == nil {
		t.Errorf("bob reservation: FulfilledAt nil")
	}
	if carolRes.FulfilledAt != nil {
		t.Errorf("carol reservation FulfilledAt: got %v, want nil", *carolRes.FulfilledAt)
	}
}

func TestAutoLoanConsumer_SkipsSuspendedReservers(t *testing.T) {
	s := buildConsumerScene(t, consumerSceneOpts{})
	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	suspended := registerMemberForConsumer(t, s, 2, "Suspended")
	eligible := registerMemberForConsumer(t, s, 3, "Eligible")

	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	suspendedRes, _ := s.facade.Reserve(s.ctx, suspended.MemberId, book.BookId)
	eligibleRes, _ := s.facade.Reserve(s.ctx, eligible.MemberId, book.BookId)
	if _, err := s.membership.Suspend(s.ctx, suspended.MemberId); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	s.collected.reset()

	_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)

	if got := loansFor(t, s, eligible.MemberId); len(got) != 1 {
		t.Errorf("eligible loans: got %d, want 1", len(got))
	}
	if got := loansFor(t, s, suspended.MemberId); len(got) != 0 {
		t.Errorf("suspended loans: got %d, want 0", len(got))
	}

	suspendedResAfter := findReservation(t, s.reservations, suspendedRes.ReservationId)
	eligibleResAfter := findReservation(t, s.reservations, eligibleRes.ReservationId)
	if suspendedResAfter.FulfilledAt != nil {
		t.Errorf("suspended res FulfilledAt: got %v, want nil", *suspendedResAfter.FulfilledAt)
	}
	if eligibleResAfter.FulfilledAt == nil {
		t.Errorf("eligible res FulfilledAt: got nil, want %v", fixedNow)
	}

	requireEventTypes(t, s.collected, []string{
		"LoanReturned",
		"ReservationFulfilled",
		"LoanOpened",
		"AutoLoanOpened",
	})
}

func TestAutoLoanConsumer_AllIneligibleNoOp(t *testing.T) {
	s := buildConsumerScene(t, consumerSceneOpts{})
	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	susOne := registerMemberForConsumer(t, s, 2, "SuspendedOne")
	susTwo := registerMemberForConsumer(t, s, 3, "SuspendedTwo")

	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	resOne, _ := s.facade.Reserve(s.ctx, susOne.MemberId, book.BookId)
	resTwo, _ := s.facade.Reserve(s.ctx, susTwo.MemberId, book.BookId)
	_, _ = s.membership.Suspend(s.ctx, susOne.MemberId)
	_, _ = s.membership.Suspend(s.ctx, susTwo.MemberId)
	s.collected.reset()

	_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)

	for _, m := range []membership.MemberId{susOne.MemberId, susTwo.MemberId} {
		if got := loansFor(t, s, m); len(got) != 0 {
			t.Errorf("member %q loans: got %d, want 0", m, len(got))
		}
	}
	for _, id := range []lending.ReservationId{resOne.ReservationId, resTwo.ReservationId} {
		res := findReservation(t, s.reservations, id)
		if res.FulfilledAt != nil {
			t.Errorf("res %q FulfilledAt: got %v, want nil", id, *res.FulfilledAt)
		}
	}
	requireEventTypes(t, s.collected, []string{"LoanReturned"})

	got, _ := s.catalog.FindCopy(s.ctx, copyDto.CopyId)
	if got.Status != catalog.CopyStatusAvailable {
		t.Errorf("copy.Status: got %q, want AVAILABLE", got.Status)
	}
}

// -----------------------------------------------------------------------------
// Slice 1 — lifecycle
// -----------------------------------------------------------------------------

func TestAutoLoanConsumer_StartTwiceIsIdempotent(t *testing.T) {
	s := buildConsumerScene(t, consumerSceneOpts{})
	if err := s.consumer.Start(s.ctx); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	bob := registerMemberForConsumer(t, s, 2, "Bob")
	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	_, _ = s.facade.Reserve(s.ctx, bob.MemberId, book.BookId)
	s.collected.reset()

	_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)

	if got := countType(s.collected.types(), "AutoLoanOpened"); got != 1 {
		t.Errorf("lending.AutoLoanOpened count: got %d, want 1 (Start is idempotent)", got)
	}
}

func TestAutoLoanConsumer_StopDetachesHandler(t *testing.T) {
	s := buildConsumerScene(t, consumerSceneOpts{})
	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	bob := registerMemberForConsumer(t, s, 2, "Bob")
	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	_, _ = s.facade.Reserve(s.ctx, bob.MemberId, book.BookId)

	if err := s.consumer.Stop(s.ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	s.collected.reset()

	_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)

	requireEventTypes(t, s.collected, []string{"LoanReturned"})

	if got := loansFor(t, s, bob.MemberId); len(got) != 0 {
		t.Errorf("bob loans after Stop: got %d, want 0", len(got))
	}
}

func TestAutoLoanConsumer_StartAfterStopRebinds(t *testing.T) {
	s := buildConsumerScene(t, consumerSceneOpts{})
	if err := s.consumer.Stop(s.ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := s.consumer.Start(s.ctx); err != nil {
		t.Fatalf("Start after Stop: %v", err)
	}

	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	bob := registerMemberForConsumer(t, s, 2, "Bob")
	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	_, _ = s.facade.Reserve(s.ctx, bob.MemberId, book.BookId)
	s.collected.reset()

	_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)

	if got := countType(s.collected.types(), "AutoLoanOpened"); got != 1 {
		t.Errorf("lending.AutoLoanOpened count after re-Start: got %d, want 1", got)
	}
}

func TestAutoLoanConsumer_AutoLoanOpenedPayload(t *testing.T) {
	s := buildConsumerScene(t, consumerSceneOpts{})
	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	bob := registerMemberForConsumer(t, s, 2, "Bob")
	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	bobReservation, _ := s.facade.Reserve(s.ctx, bob.MemberId, book.BookId)
	s.collected.reset()

	_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)

	bobLoans := loansFor(t, s, bob.MemberId)
	if len(bobLoans) != 1 {
		t.Fatalf("bob loans: got %d, want 1", len(bobLoans))
	}
	opened, ok := firstByType[lending.AutoLoanOpened](s.collected.snapshot())
	if !ok {
		t.Fatalf("lending.AutoLoanOpened not published")
	}
	if opened.BookId != copyDto.BookId {
		t.Errorf("lending.AutoLoanOpened.BookId: got %q, want %q", opened.BookId, copyDto.BookId)
	}
	if opened.LoanId != bobLoans[0].LoanId {
		t.Errorf("lending.AutoLoanOpened.LoanId: got %q, want %q", opened.LoanId, bobLoans[0].LoanId)
	}
	if opened.MemberId != bob.MemberId {
		t.Errorf("lending.AutoLoanOpened.MemberId: got %q, want %q", opened.MemberId, bob.MemberId)
	}
	if opened.ReservationId != bobReservation.ReservationId {
		t.Errorf("lending.AutoLoanOpened.ReservationId: got %q, want %q", opened.ReservationId, bobReservation.ReservationId)
	}
	if !opened.OpenedAt.Equal(fixedNow) {
		t.Errorf("lending.AutoLoanOpened.OpenedAt: got %v, want %v", opened.OpenedAt, fixedNow)
	}
}

// -----------------------------------------------------------------------------
// Slice 1 — failure policy (borrow rejects)
// -----------------------------------------------------------------------------

// TestAutoLoanConsumer_BorrowFailureTriggersUnfulfilAndAutoLoanFailed verifies
// the consumer's failure path: when Borrow throws, the claim is rolled back
// via the un-fulfil tx and lending.AutoLoanFailed publishes with the borrow error
// message.
//
// Failure injection: arm the SHARED loans repo to fail on the NEXT SaveLoan.
// The seed-time Borrow already saved (call #1); after that we arm the next
// call. The auto-loan saga's Borrow attempt is the next SaveLoan, which fails.
func TestAutoLoanConsumer_BorrowFailureTriggersUnfulfilAndAutoLoanFailed(t *testing.T) {
	// SaveLoan call sequence: #1 Alice's borrow; #2 Alice's return; #3 the
	// consumer's auto-Borrow for Bob — this is the one we want to fail.
	armed := errors.New("simulated borrow failure")
	loans := newThrowingOnceSaveLoanRepository(3, armed)
	s := buildConsumerScene(t, consumerSceneOpts{Loans: loans})
	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	bob := registerMemberForConsumer(t, s, 2, "Bob")

	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	bobReservation, _ := s.facade.Reserve(s.ctx, bob.MemberId, book.BookId)
	s.collected.reset()

	_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)

	if got := loansFor(t, s, bob.MemberId); len(got) != 0 {
		t.Errorf("bob loans: got %d, want 0 (borrow rejected)", len(got))
	}
	bobResAfter := findReservation(t, s.reservations, bobReservation.ReservationId)
	if bobResAfter.FulfilledAt != nil {
		t.Errorf("bob reservation FulfilledAt: got %v, want nil (un-fulfil ran)", *bobResAfter.FulfilledAt)
	}

	requireEventTypes(t, s.collected, []string{
		"LoanReturned",
		"ReservationFulfilled",
		"ReservationUnfulfilled",
		"AutoLoanFailed",
	})

	failed, _ := firstByType[lending.AutoLoanFailed](s.collected.snapshot())
	// The borrow failure wraps the armed error via tx.Run ("tx work: ...").
	// We assert the lending.AutoLoanFailed.Reason contains the armed error message.
	if !bytes.Contains([]byte(failed.Reason), []byte(armed.Error())) {
		t.Errorf("lending.AutoLoanFailed.Reason: got %q, want substring %q", failed.Reason, armed.Error())
	}
}

// -----------------------------------------------------------------------------
// Slice 2 — atomicity invariants (the four canonical claims)
// -----------------------------------------------------------------------------

// TestAutoLoanConsumer_ClaimTxRollbackSuppressesReservationFulfilled is the
// FIRST canonical atomicity AC: when the claim-tx errors, the staged
// lending.ReservationFulfilled event is suppressed (because tx rollback discards
// staged events).
//
// Bob's Reserve = save call #1; the consumer's claim = save call #2.
func TestAutoLoanConsumer_ClaimTxRollbackSuppressesReservationFulfilled(t *testing.T) {
	armed := errors.New("armed failure")
	reservations := newThrowingOnceSaveReservationRepository(2, armed)

	s := buildConsumerScene(t, consumerSceneOpts{Reservations: reservations})
	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	bob := registerMemberForConsumer(t, s, 2, "Bob")

	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	bobReservation, _ := s.facade.Reserve(s.ctx, bob.MemberId, book.BookId)
	s.collected.reset()
	s.logBuf.Reset()

	_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)

	bobResAfter := findReservation(t, s.reservations, bobReservation.ReservationId)
	if bobResAfter.FulfilledAt != nil {
		t.Errorf("bob reservation FulfilledAt: got %v, want nil (claim rolled back)", *bobResAfter.FulfilledAt)
	}

	if got := loansFor(t, s, bob.MemberId); len(got) != 0 {
		t.Errorf("bob loans: got %d, want 0", len(got))
	}

	types := s.collected.types()
	if !containsString(types, "LoanReturned") {
		t.Errorf("events: want lending.LoanReturned in %v", types)
	}
	if containsString(types, "ReservationFulfilled") {
		t.Errorf("events: did NOT want lending.ReservationFulfilled in %v (claim tx rolled back)", types)
	}
	if containsString(types, "LoanOpened") {
		t.Errorf("events: did NOT want lending.LoanOpened in %v (no borrow attempted)", types)
	}
	if containsString(types, "AutoLoanOpened") {
		t.Errorf("events: did NOT want lending.AutoLoanOpened in %v", types)
	}

	if !bytes.Contains(s.logBuf.Bytes(), []byte("claim reservation failed")) {
		t.Errorf("log: want a 'claim reservation failed' record, got %q", s.logBuf.String())
	}
}

// TestAutoLoanConsumer_UnfulfilTxRollbackSuppressesReservationUnfulfilled is
// the SECOND canonical atomicity AC: un-fulfil tx errors, lending.ReservationUnfulfilled
// suppressed, but the prior claim stays committed.
//
// Bob's Reserve = #1; claim = #2; un-fulfil = #3.
func TestAutoLoanConsumer_UnfulfilTxRollbackSuppressesReservationUnfulfilled(t *testing.T) {
	borrowErr := errors.New("simulated borrow failure")
	// SaveLoan #1 Alice borrow; #2 Alice return; #3 consumer auto-Borrow (fail).
	loans := newThrowingOnceSaveLoanRepository(3, borrowErr)
	unfulfilArmed := errors.New("armed un-fulfil failure")
	// SaveReservation #1 Bob's Reserve; #2 consumer claim; #3 consumer un-fulfil (fail).
	reservations := newThrowingOnceSaveReservationRepository(3, unfulfilArmed)

	s := buildConsumerScene(t, consumerSceneOpts{
		Loans:        loans,
		Reservations: reservations,
	})
	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	bob := registerMemberForConsumer(t, s, 2, "Bob")

	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	bobReservation, _ := s.facade.Reserve(s.ctx, bob.MemberId, book.BookId)
	s.collected.reset()

	_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)

	bobResAfter := findReservation(t, s.reservations, bobReservation.ReservationId)
	if bobResAfter.FulfilledAt == nil || !bobResAfter.FulfilledAt.Equal(fixedNow) {
		t.Errorf("bob reservation FulfilledAt: got %v, want %v (claim committed)", bobResAfter.FulfilledAt, fixedNow)
	}

	types := s.collected.types()
	if !containsString(types, "ReservationFulfilled") {
		t.Errorf("events: want lending.ReservationFulfilled in %v", types)
	}
	if containsString(types, "ReservationUnfulfilled") {
		t.Errorf("events: did NOT want lending.ReservationUnfulfilled in %v (un-fulfil tx rolled back)", types)
	}
}

// TestAutoLoanConsumer_AutoLoanFailedFiresOutsideUnfulfilTx is the THIRD
// canonical atomicity AC: lending.AutoLoanFailed publishes OUTSIDE the un-fulfil
// tx, so it lands on the bus even when the un-fulfil tx rolled back. The
// Reason payload reflects the ORIGINAL borrow error, not the un-fulfil error.
func TestAutoLoanConsumer_AutoLoanFailedFiresOutsideUnfulfilTx(t *testing.T) {
	borrowErr := errors.New("simulated borrow failure")
	loans := newThrowingOnceSaveLoanRepository(3, borrowErr)
	unfulfilArmed := errors.New("armed un-fulfil failure")
	reservations := newThrowingOnceSaveReservationRepository(3, unfulfilArmed)

	s := buildConsumerScene(t, consumerSceneOpts{
		Loans:        loans,
		Reservations: reservations,
	})
	book, copyDto := seedAvailableCopyForConsumer(t, s, 1)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	bob := registerMemberForConsumer(t, s, 2, "Bob")

	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	_, _ = s.facade.Reserve(s.ctx, bob.MemberId, book.BookId)
	s.collected.reset()

	_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)

	requireEventTypes(t, s.collected, []string{
		"LoanReturned",
		"ReservationFulfilled",
		"AutoLoanFailed",
	})

	failed, _ := firstByType[lending.AutoLoanFailed](s.collected.snapshot())
	if !bytes.Contains([]byte(failed.Reason), []byte(borrowErr.Error())) {
		t.Errorf("lending.AutoLoanFailed.Reason: got %q, want substring %q (NOT the un-fulfil error %q)",
			failed.Reason, borrowErr.Error(), unfulfilArmed.Error())
	}
}

// TestAutoLoanConsumer_PerBookMutexPreventsDoubleFulfilment is the FOURTH
// canonical atomicity AC: two concurrent returns of different copies of the
// SAME book result in EXACTLY ONE auto-loan. Run under `go test -race`.
func TestAutoLoanConsumer_PerBookMutexPreventsDoubleFulfilment(t *testing.T) {
	s := buildConsumerScene(t, consumerSceneOpts{})
	book, copyA := seedAvailableCopyForConsumer(t, s, 1)
	copyB := seedExtraCopy(t, s, book.BookId)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	carol := registerMemberForConsumer(t, s, 2, "Carol")
	bob := registerMemberForConsumer(t, s, 3, "Bob")

	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyA.CopyId)
	carolLoan, _ := s.facade.Borrow(s.ctx, memberAuth(carol.MemberId), copyB.CopyId)
	_, _ = s.facade.Reserve(s.ctx, bob.MemberId, book.BookId)
	s.collected.reset()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)
	}()
	go func() {
		defer wg.Done()
		_, _ = s.facade.ReturnLoan(s.ctx, carolLoan.LoanId)
	}()
	wg.Wait()

	bobLoans := loansFor(t, s, bob.MemberId)
	if len(bobLoans) != 1 {
		t.Fatalf("bob loans: got %d, want EXACTLY 1", len(bobLoans))
	}

	unavailableCount := 0
	for _, c := range []catalog.CopyDto{copyA, copyB} {
		got, _ := s.catalog.FindCopy(s.ctx, c.CopyId)
		if got.Status == catalog.CopyStatusUnavailable {
			unavailableCount++
		}
	}
	if unavailableCount != 1 {
		t.Errorf("unavailable copies: got %d, want EXACTLY 1", unavailableCount)
	}

	types := s.collected.types()
	if countType(types, "LoanReturned") != 2 {
		t.Errorf("lending.LoanReturned count: got %d, want 2", countType(types, "LoanReturned"))
	}
	if countType(types, "ReservationFulfilled") != 1 {
		t.Errorf("lending.ReservationFulfilled count: got %d, want 1 (per-book mutex)", countType(types, "ReservationFulfilled"))
	}
	if countType(types, "LoanOpened") != 1 {
		t.Errorf("lending.LoanOpened count: got %d, want 1", countType(types, "LoanOpened"))
	}
	if countType(types, "AutoLoanOpened") != 1 {
		t.Errorf("lending.AutoLoanOpened count: got %d, want 1", countType(types, "AutoLoanOpened"))
	}
}

func TestAutoLoanConsumer_DifferentBooksRunInParallel(t *testing.T) {
	s := buildConsumerScene(t, consumerSceneOpts{})
	bookOne, copyOne := seedAvailableCopyForConsumer(t, s, 1)
	bookTwo, copyTwo := seedAvailableCopyForConsumer(t, s, 2)
	alice := registerMemberForConsumer(t, s, 1, "Alice")
	carol := registerMemberForConsumer(t, s, 2, "Carol")
	bob := registerMemberForConsumer(t, s, 3, "Bob")
	dan := registerMemberForConsumer(t, s, 4, "Dan")

	aliceLoan, _ := s.facade.Borrow(s.ctx, memberAuth(alice.MemberId), copyOne.CopyId)
	carolLoan, _ := s.facade.Borrow(s.ctx, memberAuth(carol.MemberId), copyTwo.CopyId)
	_, _ = s.facade.Reserve(s.ctx, bob.MemberId, bookOne.BookId)
	_, _ = s.facade.Reserve(s.ctx, dan.MemberId, bookTwo.BookId)
	s.collected.reset()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = s.facade.ReturnLoan(s.ctx, aliceLoan.LoanId)
	}()
	go func() {
		defer wg.Done()
		_, _ = s.facade.ReturnLoan(s.ctx, carolLoan.LoanId)
	}()
	wg.Wait()

	for _, m := range []membership.MemberId{bob.MemberId, dan.MemberId} {
		if got := loansFor(t, s, m); len(got) != 1 {
			t.Errorf("member %q loans: got %d, want 1", m, len(got))
		}
	}

	if got := countType(s.collected.types(), "AutoLoanOpened"); got != 2 {
		t.Errorf("lending.AutoLoanOpened count: got %d, want 2", got)
	}
}

// -----------------------------------------------------------------------------
// Local helpers
// -----------------------------------------------------------------------------

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func countType(haystack []string, needle string) int {
	count := 0
	for _, s := range haystack {
		if s == needle {
			count++
		}
	}
	return count
}

// firstByType returns the first event of type T in the captured slice.
func firstByType[T events.DomainEvent](snap []events.DomainEvent) (T, bool) {
	var zero T
	for _, evt := range snap {
		if cast, ok := evt.(T); ok {
			return cast, true
		}
	}
	return zero, false
}

// -----------------------------------------------------------------------------
// Spec-local test doubles — unexported.
// -----------------------------------------------------------------------------

// throwingOnceSaveLoanRepository wraps an InMemoryLoanRepository and arms a
// single-shot error to fire on the Nth SaveLoan call (1-indexed). Differs
// from facade_test.go's throwingOnceLoanRepository which fires on the NEXT
// call regardless of position. The Nth-call variant lets the saga tests
// arm a failure that fires precisely on the consumer's auto-Borrow without
// the test having to manage timing between Alice's return-tx and the bus
// fanout.
type throwingOnceSaveLoanRepository struct {
	delegate     *lendingmemory.LoanRepository
	mu           sync.Mutex
	saveCount    int
	failOnCallNo int
	armedErr     error
	fired        bool
}

func newThrowingOnceSaveLoanRepository(failOnCallNo int, err error) *throwingOnceSaveLoanRepository {
	return &throwingOnceSaveLoanRepository{
		delegate:     lendingmemory.NewLoanRepository(),
		failOnCallNo: failOnCallNo,
		armedErr:     err,
	}
}

func (r *throwingOnceSaveLoanRepository) SaveLoan(ctx context.Context, loan lending.LoanDto, txc tx.TransactionalContext) error {
	r.mu.Lock()
	r.saveCount++
	shouldFire := !r.fired && r.saveCount == r.failOnCallNo
	if shouldFire {
		r.fired = true
	}
	r.mu.Unlock()
	if shouldFire {
		return r.armedErr
	}
	return r.delegate.SaveLoan(ctx, loan, txc)
}

func (r *throwingOnceSaveLoanRepository) FindLoanById(ctx context.Context, loanId lending.LoanId) (*lending.LoanDto, error) {
	return r.delegate.FindLoanById(ctx, loanId)
}

func (r *throwingOnceSaveLoanRepository) ListLoansForMember(ctx context.Context, memberId membership.MemberId) ([]lending.LoanDto, error) {
	return r.delegate.ListLoansForMember(ctx, memberId)
}

func (r *throwingOnceSaveLoanRepository) ListLoansForBook(ctx context.Context, bookId catalog.BookId) ([]lending.LoanDto, error) {
	return r.delegate.ListLoansForBook(ctx, bookId)
}

func (r *throwingOnceSaveLoanRepository) ListLoans(ctx context.Context) ([]lending.LoanDto, error) {
	return r.delegate.ListLoans(ctx)
}

// throwingOnceSaveReservationRepository wraps an InMemoryReservationRepository
// and arms a single-shot error to fire on the Nth SaveReservation call
// (1-indexed). After the armed call, the wrapper reverts to delegating cleanly.
//
// Sequenced fault injection is what lets atomicity tests trigger failure on
// a specific saga step: the consumer's claim is the 2nd save (after Bob's
// Reserve = #1); the consumer's un-fulfil is the 3rd save.
type throwingOnceSaveReservationRepository struct {
	delegate     *lendingmemory.ReservationRepository
	mu           sync.Mutex
	saveCount    int
	failOnCallNo int
	armedErr     error
	fired        bool
}

func newThrowingOnceSaveReservationRepository(failOnCallNo int, err error) *throwingOnceSaveReservationRepository {
	return &throwingOnceSaveReservationRepository{
		delegate:     lendingmemory.NewReservationRepository(),
		failOnCallNo: failOnCallNo,
		armedErr:     err,
	}
}

func (r *throwingOnceSaveReservationRepository) SaveReservation(ctx context.Context, reservation lending.ReservationDto, txc tx.TransactionalContext) error {
	r.mu.Lock()
	r.saveCount++
	shouldFire := !r.fired && r.saveCount == r.failOnCallNo
	if shouldFire {
		r.fired = true
	}
	r.mu.Unlock()
	if shouldFire {
		return r.armedErr
	}
	return r.delegate.SaveReservation(ctx, reservation, txc)
}

func (r *throwingOnceSaveReservationRepository) FindReservationById(ctx context.Context, reservationId lending.ReservationId) (*lending.ReservationDto, error) {
	return r.delegate.FindReservationById(ctx, reservationId)
}

func (r *throwingOnceSaveReservationRepository) ListReservationsForBook(ctx context.Context, bookId catalog.BookId) ([]lending.ReservationDto, error) {
	return r.delegate.ListReservationsForBook(ctx, bookId)
}

func (r *throwingOnceSaveReservationRepository) ListReservationsForMember(ctx context.Context, memberId membership.MemberId) ([]lending.ReservationDto, error) {
	return r.delegate.ListReservationsForMember(ctx, memberId)
}

func (r *throwingOnceSaveReservationRepository) PendingReservationCountForBook(ctx context.Context, bookId catalog.BookId) (int, error) {
	return r.delegate.PendingReservationCountForBook(ctx, bookId)
}

func (r *throwingOnceSaveReservationRepository) ListPendingReservationsForBook(ctx context.Context, bookId catalog.BookId) ([]lending.ReservationDto, error) {
	return r.delegate.ListPendingReservationsForBook(ctx, bookId)
}
