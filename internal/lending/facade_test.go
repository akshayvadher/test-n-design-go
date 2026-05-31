// facade_test.go is the facade-level spec for the lending module — a
// scenario port of apps/library/src/lending/lending.facade.spec.ts from
// the source TypeScript repository, scoped to the methods Phase 3 ships
// (Borrow, Reserve, ReturnLoan). The list-and-report flows
// (listOverdueLoans, listLoansFor, listActiveLoansWithQueuedReservations,
// listOverdueLoansWithTitles) are deferred to Phase 4.
//
// Stdlib testing only — errors.As for typed-error assertions, no testify,
// no mock library. Spec-local decorators
// (throwingOnceLoanRepository, throwingOnceReservationRepository,
// throwingOnceCopyMutationsRepository, recordingCopyMutationsRepository,
// flakyBus) live at the bottom of the file. They are unexported and
// never imported by any other package — the proof that fault-injection
// state never leaks into production.
package lending_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	catalogmemory "github.com/akshayvadher/test-n-design-go/internal/catalog/driven/memory"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	lendingmemory "github.com/akshayvadher/test-n-design-go/internal/lending/driven/memory"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	membershipmemory "github.com/akshayvadher/test-n-design-go/internal/membership/driven/memory"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	eventsmemory "github.com/akshayvadher/test-n-design-go/internal/shared/events/memory"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
	txmemory "github.com/akshayvadher/test-n-design-go/internal/shared/tx/memory"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// fixedNow is the deterministic timestamp every test reads through the
// scene's clock. Mirrors the TS spec's FIXED_NOW.
var fixedNow = time.Date(2030, 1, 15, 0, 0, 0, 0, time.UTC)

// fixedClock returns the fixed timestamp on every call.
func fixedClock() time.Time {
	return fixedNow
}

// sequentialIds returns a deterministic id generator producing
// "<prefix>-<n>" strings. Mirrors the TS sequentialIds.
func sequentialIds(prefix string) func() string {
	counter := 0
	return func() string {
		counter++
		return prefix + "-" + strconv.Itoa(counter)
	}
}

// scene aggregates the facade + the underlying in-memory cross-module
// facades + the bus + the captured-events slice. Every test in the file
// builds a fresh scene so substrates do not leak across cases.
type scene struct {
	facade     *lending.Facade
	catalog    *catalog.Facade
	membership *membership.Facade
	bus        *eventsmemory.Bus
	loans      *lendingmemory.LoanRepository
	collected  *collectedEvents
}

// collectedEvents is the goroutine-safe append target for the bus
// subscribers Slice 4-6 tests use to assert event publishing.
type collectedEvents struct {
	mu     sync.Mutex
	events []events.DomainEvent
}

func (c *collectedEvents) append(evt events.DomainEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, evt)
}

func (c *collectedEvents) snapshot() []events.DomainEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]events.DomainEvent(nil), c.events...)
}

func (c *collectedEvents) types() []string {
	snap := c.snapshot()
	types := make([]string, 0, len(snap))
	for _, evt := range snap {
		types = append(types, evt.Type())
	}
	return types
}

func (c *collectedEvents) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = nil
}

// buildScene constructs a fresh scene with deterministic ids, the fixed
// clock, fresh cross-module facades, a fresh in-memory bus, and a
// captured-events subscription for the three Phase-3 event types.
func buildScene(t *testing.T) *scene {
	t.Helper()
	return buildSceneWith(t, lendingmemory.Overrides{})
}

// buildSceneWith mirrors buildScene but lets the caller pre-seed
// lending.Overrides fields (a custom loan/reservation repository, a wrapped
// catalog facade, a custom bus). Fields the caller leaves zero are
// filled with the test defaults.
func buildSceneWith(t *testing.T, extra lendingmemory.Overrides) *scene {
	t.Helper()

	logger := extra.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	catalogFacade := extra.Catalog
	if catalogFacade == nil {
		catalogFacade = catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{
			NewID:  sequentialIds("cat"),
			Logger: logger,
		})
	}
	membershipFacade := extra.Membership
	if membershipFacade == nil {
		membershipFacade = membershipmemory.NewFacadeWithOverrides(membershipmemory.Overrides{
			NewID:  sequentialIds("mem"),
			Logger: logger,
		})
	}

	loansRepo := extra.Loans
	var inMemoryLoans *lendingmemory.LoanRepository
	if loansRepo == nil {
		inMemoryLoans = lendingmemory.NewLoanRepository()
		loansRepo = inMemoryLoans
	}
	reservationsRepo := extra.Reservations
	if reservationsRepo == nil {
		reservationsRepo = lendingmemory.NewReservationRepository()
	}

	bus := extra.Bus
	var inMemoryBus *eventsmemory.Bus
	if bus == nil {
		inMemoryBus = eventsmemory.NewBus(logger)
		bus = inMemoryBus
	} else if asInMem, ok := bus.(*eventsmemory.Bus); ok {
		inMemoryBus = asInMem
	}

	overrides := lendingmemory.Overrides{
		Catalog:       catalogFacade,
		Membership:    membershipFacade,
		AccessControl: extra.AccessControl,
		Loans:         loansRepo,
		Reservations:  reservationsRepo,
		Bus:           bus,
		TxFactory:     extra.TxFactory,
		NewID:         extra.NewID,
		Clock:         extra.Clock,
		Logger:        logger,
	}
	if overrides.NewID == nil {
		overrides.NewID = sequentialIds("loan")
	}
	if overrides.Clock == nil {
		overrides.Clock = fixedClock
	}

	facade := lendingmemory.NewFacadeWithOverrides(overrides)

	collected := &collectedEvents{}
	subscribe := func(eventType string) {
		bus.Subscribe(eventType, func(_ context.Context, evt events.DomainEvent) error {
			collected.append(evt)
			return nil
		})
	}
	subscribe("LoanOpened")
	subscribe("LoanReturned")
	subscribe("ReservationQueued")

	return &scene{
		facade:     facade,
		catalog:    catalogFacade,
		membership: membershipFacade,
		bus:        inMemoryBus,
		loans:      inMemoryLoans,
		collected:  collected,
	}
}

// seedAvailableCopy adds a fresh book + an AVAILABLE copy to the scene's
// catalog and returns both. Each invocation uses a unique ISBN so the
// real duplicate-ISBN rule does not interfere.
func seedAvailableCopy(t *testing.T, s *scene, seq int) (catalog.BookDto, catalog.CopyDto) {
	t.Helper()
	isbn := catalog.Isbn("978-" + padLeft(strconv.Itoa(seq), 10, '0'))
	book, err := s.catalog.AddBook(context.Background(), catalog.SampleNewBook(catalog.WithIsbn(isbn)))
	if err != nil {
		t.Fatalf("seedAvailableCopy: AddBook: %v", err)
	}
	copyDto, err := s.catalog.RegisterCopy(context.Background(), book.BookId, catalog.SampleNewCopy(catalog.WithBookId(book.BookId)))
	if err != nil {
		t.Fatalf("seedAvailableCopy: RegisterCopy: %v", err)
	}
	return book, copyDto
}

// padLeft left-pads s with pad up to length n. Used to mint unique ISBNs.
func padLeft(s string, n int, pad byte) string {
	if len(s) >= n {
		return s
	}
	buf := make([]byte, n)
	for i := 0; i < n-len(s); i++ {
		buf[i] = pad
	}
	copy(buf[n-len(s):], s)
	return string(buf)
}

// registerMember registers a fresh member via the underlying membership
// facade. Each invocation uses a unique email so the duplicate-email
// rule does not interfere.
func registerMember(t *testing.T, s *scene, seq int, name string) membership.MemberDto {
	t.Helper()
	email := "member-" + strconv.Itoa(seq) + "@lib.test"
	member, err := s.membership.RegisterMember(context.Background(), membership.SampleNewMember(
		membership.WithName(name),
		membership.WithEmail(email),
	))
	if err != nil {
		t.Fatalf("registerMember: %v", err)
	}
	return member
}

// memberAuth builds the AuthUser with role MEMBER for the given memberId.
func memberAuth(memberId membership.MemberId) accesscontrol.AuthUser {
	return accesscontrol.AuthUser{MemberID: string(memberId), Role: accesscontrol.RoleMember}
}

// accountAuth builds an AuthUser with role ACCOUNT (used to exercise the
// authorization-rejection paths).
func accountAuth(memberId membership.MemberId) accesscontrol.AuthUser {
	return accesscontrol.AuthUser{MemberID: string(memberId), Role: accesscontrol.RoleAccount}
}

// newRecordingCatalog builds a *catalog.Facade backed by a recording
// repository that journals every SaveCopy that flips a status. Returns
// the facade and the underlying repository so the test can read other
// state without rewrapping.
func newRecordingCatalog(t *testing.T, journal *journalT) (*catalog.Facade, *recordingCopyMutationsRepository) {
	t.Helper()
	repo := &recordingCopyMutationsRepository{
		delegate: catalogmemory.NewRepository(),
		journal:  journal,
	}
	facade := catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{
		Repository: repo,
		NewID:      sequentialIds("cat"),
	})
	return facade, repo
}

// newThrowingCatalog builds a *catalog.Facade backed by a throwing-once
// repository whose SaveCopy honours an armed error for either the
// next mark-unavailable or the next mark-available transition.
func newThrowingCatalog(t *testing.T) (*catalog.Facade, *throwingOnceCopyMutationsRepository) {
	t.Helper()
	repo := &throwingOnceCopyMutationsRepository{
		delegate: catalogmemory.NewRepository(),
	}
	facade := catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{
		Repository: repo,
		NewID:      sequentialIds("cat"),
	})
	return facade, repo
}

// -----------------------------------------------------------------------------
// lending.Facade.Borrow — happy path
// -----------------------------------------------------------------------------

func TestLendingFacade_Borrow_OpensLoanAndEmitsEvent(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")

	loan, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}

	if loan.LoanId == "" {
		t.Errorf("loan.LoanId: got empty, want non-empty")
	}
	if loan.MemberId != alice.MemberId {
		t.Errorf("loan.MemberId: got %q, want %q", loan.MemberId, alice.MemberId)
	}
	if loan.CopyId != copyDto.CopyId {
		t.Errorf("loan.CopyId: got %q, want %q", loan.CopyId, copyDto.CopyId)
	}
	if loan.BookId != copyDto.BookId {
		t.Errorf("loan.BookId: got %q, want %q", loan.BookId, copyDto.BookId)
	}
	if !loan.BorrowedAt.Equal(fixedNow) {
		t.Errorf("loan.BorrowedAt: got %v, want %v", loan.BorrowedAt, fixedNow)
	}
	wantDue := fixedNow.AddDate(0, 0, lending.LoanDurationDays)
	if !loan.DueDate.Equal(wantDue) {
		t.Errorf("loan.DueDate: got %v, want %v", loan.DueDate, wantDue)
	}
	if loan.ReturnedAt != nil {
		t.Errorf("loan.ReturnedAt: got %v, want nil", *loan.ReturnedAt)
	}

	stored, err := s.loans.FindLoanById(ctx, loan.LoanId)
	if err != nil {
		t.Fatalf("FindLoanById: %v", err)
	}
	if stored == nil || stored.LoanId != loan.LoanId {
		t.Errorf("loan repo: stored=%+v, want loan with id %q", stored, loan.LoanId)
	}

	if got := s.collected.types(); !equalStrings(got, []string{"LoanOpened"}) {
		t.Errorf("event types: got %v, want [lending.LoanOpened]", got)
	}
	publishedLoan := s.collected.snapshot()[0].(lending.LoanOpened)
	if publishedLoan.LoanId != loan.LoanId || publishedLoan.MemberId != loan.MemberId || publishedLoan.CopyId != loan.CopyId || publishedLoan.BookId != loan.BookId {
		t.Errorf("lending.LoanOpened payload: got %+v, want fields matching %+v", publishedLoan, loan)
	}
}

func TestLendingFacade_Borrow_MarksCopyUnavailable(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")

	if _, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId); err != nil {
		t.Fatalf("Borrow: %v", err)
	}

	got, err := s.catalog.FindCopy(ctx, copyDto.CopyId)
	if err != nil {
		t.Fatalf("FindCopy: %v", err)
	}
	if got.Status != catalog.CopyStatusUnavailable {
		t.Errorf("copy.Status: got %q, want %q", got.Status, catalog.CopyStatusUnavailable)
	}
}

// TestLendingFacade_Borrow_PostCommitOrdering is the canonical AC for the
// post-commit rule: the staged lending.LoanOpened event publishes BEFORE the
// catalog mark-unavailable runs, because staged events fire during commit
// and the catalog call happens after Run returns.
func TestLendingFacade_Borrow_PostCommitOrdering(t *testing.T) {
	ctx := context.Background()
	journal := &journalT{}

	recordingCatalog, _ := newRecordingCatalog(t, journal)
	s := buildSceneWith(t, lendingmemory.Overrides{Catalog: recordingCatalog})

	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	// Reset journal so seed-time SaveCopy calls don't pollute the assertion.
	journal.reset()

	s.bus.Subscribe("LoanOpened", func(_ context.Context, _ events.DomainEvent) error {
		journal.append("event:LoanOpened")
		return nil
	})

	if _, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId); err != nil {
		t.Fatalf("Borrow: %v", err)
	}

	if got := journal.snapshot(); !equalStrings(got, []string{"event:LoanOpened", "catalog:MarkCopyUnavailable"}) {
		t.Errorf("journal: got %v, want [event:LoanOpened, catalog:MarkCopyUnavailable]", got)
	}
}

// -----------------------------------------------------------------------------
// lending.Facade.Borrow — authorization + eligibility
// -----------------------------------------------------------------------------

func TestLendingFacade_Borrow_RejectsAccountRole(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")

	_, err := s.facade.Borrow(ctx, accountAuth(alice.MemberId), copyDto.CopyId)
	var target *accesscontrol.UnauthorizedRoleError
	if !errors.As(err, &target) {
		t.Fatalf("Borrow: got %v, want *UnauthorizedRoleError", err)
	}

	stored, _ := s.loans.ListLoans(ctx)
	if len(stored) != 0 {
		t.Errorf("loan repo: got %d loans, want 0", len(stored))
	}
	if got := s.collected.types(); len(got) != 0 {
		t.Errorf("events: got %v, want none", got)
	}
	copyDto2, _ := s.catalog.FindCopy(ctx, copyDto.CopyId)
	if copyDto2.Status != catalog.CopyStatusAvailable {
		t.Errorf("copy.Status: got %q, want AVAILABLE", copyDto2.Status)
	}
}

func TestLendingFacade_Borrow_AuthorizationBeforeEligibility(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	if _, err := s.membership.Suspend(ctx, alice.MemberId); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	_, err := s.facade.Borrow(ctx, accountAuth(alice.MemberId), copyDto.CopyId)
	var target *accesscontrol.UnauthorizedRoleError
	if !errors.As(err, &target) {
		t.Fatalf("Borrow: got %v, want *UnauthorizedRoleError (authorization wins over eligibility)", err)
	}
}

func TestLendingFacade_Borrow_RejectsSuspendedMember(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	if _, err := s.membership.Suspend(ctx, alice.MemberId); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	_, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	var target *lending.MemberIneligibleError
	if !errors.As(err, &target) {
		t.Fatalf("Borrow: got %v, want *lending.MemberIneligibleError", err)
	}
	if target.Reason != "SUSPENDED" {
		t.Errorf("Reason: got %q, want SUSPENDED", target.Reason)
	}

	stored, _ := s.loans.ListLoans(ctx)
	if len(stored) != 0 {
		t.Errorf("loan repo: got %d loans, want 0", len(stored))
	}
	if got := s.collected.types(); len(got) != 0 {
		t.Errorf("events: got %v, want none", got)
	}
	got, _ := s.catalog.FindCopy(ctx, copyDto.CopyId)
	if got.Status != catalog.CopyStatusAvailable {
		t.Errorf("copy.Status: got %q, want AVAILABLE", got.Status)
	}
}

func TestLendingFacade_Borrow_RejectsUnavailableCopy(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	bob := registerMember(t, s, 2, "Bob")

	if _, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId); err != nil {
		t.Fatalf("Borrow (alice): %v", err)
	}
	s.collected.reset()

	_, err := s.facade.Borrow(ctx, memberAuth(bob.MemberId), copyDto.CopyId)
	var target *lending.CopyUnavailableError
	if !errors.As(err, &target) {
		t.Fatalf("Borrow (bob): got %v, want *lending.CopyUnavailableError", err)
	}
	if target.CopyId != copyDto.CopyId {
		t.Errorf("lending.CopyUnavailableError.CopyId: got %q, want %q", target.CopyId, copyDto.CopyId)
	}

	bobLoans, _ := s.loans.ListLoansForMember(ctx, bob.MemberId)
	if len(bobLoans) != 0 {
		t.Errorf("bob loans: got %d, want 0", len(bobLoans))
	}
	if got := s.collected.types(); len(got) != 0 {
		t.Errorf("events: got %v, want none", got)
	}
}

func TestLendingFacade_Borrow_RejectsUnknownCopy(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	alice := registerMember(t, s, 1, "Alice")

	_, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), "nonexistent-copy")
	var target *catalog.CopyNotFoundError
	if !errors.As(err, &target) {
		t.Fatalf("Borrow: got %v, want *catalog.CopyNotFoundError", err)
	}

	stored, _ := s.loans.ListLoans(ctx)
	if len(stored) != 0 {
		t.Errorf("loan repo: got %d loans, want 0", len(stored))
	}
	if got := s.collected.types(); len(got) != 0 {
		t.Errorf("events: got %v, want none", got)
	}
}

// -----------------------------------------------------------------------------
// lending.Facade.Borrow — tx atomicity
// -----------------------------------------------------------------------------

func TestLendingFacade_Borrow_RollsBackOnLoanSaveFailure(t *testing.T) {
	ctx := context.Background()
	loans := newThrowingOnceLoanRepository()
	s := buildSceneWith(t, lendingmemory.Overrides{Loans: loans})
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")

	armed := errors.New("loan store is down")
	loans.armFailureOnNextSave(armed)

	_, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	if err == nil || !errors.Is(err, armed) {
		t.Fatalf("Borrow: got %v, want error wrapping %v", err, armed)
	}

	stored, _ := loans.ListLoans(ctx)
	if len(stored) != 0 {
		t.Errorf("loan repo: got %d loans, want 0 (rolled back)", len(stored))
	}
	if got := s.collected.types(); len(got) != 0 {
		t.Errorf("events: got %v, want none (staged event discarded)", got)
	}
	got, _ := s.catalog.FindCopy(ctx, copyDto.CopyId)
	if got.Status != catalog.CopyStatusAvailable {
		t.Errorf("copy.Status: got %q, want AVAILABLE (catalog mutation skipped)", got.Status)
	}
}

func TestLendingFacade_Borrow_CatalogFailureAfterCommitLeavesLoanPersisted(t *testing.T) {
	ctx := context.Background()
	throwingCatalog, throwingRepo := newThrowingCatalog(t)
	s := buildSceneWith(t, lendingmemory.Overrides{Catalog: throwingCatalog})
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")

	armed := errors.New("catalog is down")
	throwingRepo.armFailureOnNextMarkCopyUnavailable(armed)

	_, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	if !errors.Is(err, armed) {
		t.Fatalf("Borrow: got %v, want error wrapping %v", err, armed)
	}

	stored, _ := s.loans.ListLoans(ctx)
	if len(stored) != 1 {
		t.Errorf("loan repo: got %d loans, want 1 (tx committed before failure)", len(stored))
	}
	if got := s.collected.types(); !equalStrings(got, []string{"LoanOpened"}) {
		t.Errorf("events: got %v, want [lending.LoanOpened] (published during commit)", got)
	}
	got, _ := throwingCatalog.FindCopy(ctx, copyDto.CopyId)
	if got.Status != catalog.CopyStatusAvailable {
		t.Errorf("copy.Status: got %q, want AVAILABLE (catalog mutation failed — known gap)", got.Status)
	}
}

// -----------------------------------------------------------------------------
// lending.Facade.Reserve
// -----------------------------------------------------------------------------

func TestLendingFacade_Reserve_PersistsAndEmitsEvent(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	book, _ := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")

	reservation, err := s.facade.Reserve(ctx, alice.MemberId, book.BookId)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	if reservation.ReservationId == "" {
		t.Errorf("reservation.ReservationId: got empty, want non-empty")
	}
	if reservation.MemberId != alice.MemberId {
		t.Errorf("reservation.MemberId: got %q, want %q", reservation.MemberId, alice.MemberId)
	}
	if reservation.BookId != book.BookId {
		t.Errorf("reservation.BookId: got %q, want %q", reservation.BookId, book.BookId)
	}
	if !reservation.ReservedAt.Equal(fixedNow) {
		t.Errorf("reservation.ReservedAt: got %v, want %v", reservation.ReservedAt, fixedNow)
	}
	if reservation.FulfilledAt != nil {
		t.Errorf("reservation.FulfilledAt: got %v, want nil", *reservation.FulfilledAt)
	}

	if got := s.collected.types(); !equalStrings(got, []string{"ReservationQueued"}) {
		t.Errorf("event types: got %v, want [lending.ReservationQueued]", got)
	}
	published := s.collected.snapshot()[0].(lending.ReservationQueued)
	if published.ReservationId != reservation.ReservationId || published.MemberId != reservation.MemberId || published.BookId != reservation.BookId {
		t.Errorf("lending.ReservationQueued payload: got %+v, want fields matching %+v", published, reservation)
	}
}

func TestLendingFacade_Reserve_RejectsSuspendedMember(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	book, _ := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	if _, err := s.membership.Suspend(ctx, alice.MemberId); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	_, err := s.facade.Reserve(ctx, alice.MemberId, book.BookId)
	var target *lending.MemberIneligibleError
	if !errors.As(err, &target) {
		t.Fatalf("Reserve: got %v, want *lending.MemberIneligibleError", err)
	}
	if target.Reason != "SUSPENDED" {
		t.Errorf("Reason: got %q, want SUSPENDED", target.Reason)
	}
	if got := s.collected.types(); len(got) != 0 {
		t.Errorf("events: got %v, want none", got)
	}
}

func TestLendingFacade_Reserve_RejectsUnknownMember(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	book, _ := seedAvailableCopy(t, s, 1)

	_, err := s.facade.Reserve(ctx, "ghost-member", book.BookId)
	var target *membership.MemberNotFoundError
	if !errors.As(err, &target) {
		t.Fatalf("Reserve: got %v, want *membership.MemberNotFoundError", err)
	}
	if got := s.collected.types(); len(got) != 0 {
		t.Errorf("events: got %v, want none", got)
	}
}

func TestLendingFacade_Reserve_RollsBackOnRepoFailure(t *testing.T) {
	ctx := context.Background()
	reservations := newThrowingOnceReservationRepository()
	s := buildSceneWith(t, lendingmemory.Overrides{Reservations: reservations})
	book, _ := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")

	armed := errors.New("reservation store is down")
	reservations.armFailureOnNextSave(armed)

	_, err := s.facade.Reserve(ctx, alice.MemberId, book.BookId)
	if !errors.Is(err, armed) {
		t.Fatalf("Reserve: got %v, want error wrapping %v", err, armed)
	}

	stored, _ := reservations.ListReservationsForMember(ctx, alice.MemberId)
	if len(stored) != 0 {
		t.Errorf("reservation repo: got %d, want 0", len(stored))
	}
	if got := s.collected.types(); len(got) != 0 {
		t.Errorf("events: got %v, want none", got)
	}
}

// TestLendingFacade_Reserve_DoesNotTouchCatalog verifies the pure
// staged-event variant: Reserve never mutates catalog state. Uses a
// recording wrapper around the catalog repository.
func TestLendingFacade_Reserve_DoesNotTouchCatalog(t *testing.T) {
	ctx := context.Background()
	journal := &journalT{}
	recordingCatalog, _ := newRecordingCatalog(t, journal)
	s := buildSceneWith(t, lendingmemory.Overrides{Catalog: recordingCatalog})

	book, _ := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	journal.reset()

	if _, err := s.facade.Reserve(ctx, alice.MemberId, book.BookId); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	if got := journal.snapshot(); len(got) != 0 {
		t.Errorf("recorded copy mutations during Reserve: got %v, want none", got)
	}
}

// -----------------------------------------------------------------------------
// lending.Facade.ReturnLoan
// -----------------------------------------------------------------------------

func TestLendingFacade_ReturnLoan_ClosesLoanAndEmitsEvent(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	loan, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	s.collected.reset()

	returned, err := s.facade.ReturnLoan(ctx, loan.LoanId)
	if err != nil {
		t.Fatalf("ReturnLoan: %v", err)
	}

	if returned.ReturnedAt == nil {
		t.Fatalf("returned.ReturnedAt: got nil, want non-nil")
	}
	if !returned.ReturnedAt.Equal(fixedNow) {
		t.Errorf("returned.ReturnedAt: got %v, want %v", *returned.ReturnedAt, fixedNow)
	}

	stored, _ := s.loans.FindLoanById(ctx, loan.LoanId)
	if stored == nil || stored.ReturnedAt == nil || !stored.ReturnedAt.Equal(fixedNow) {
		t.Errorf("loan repo: got %+v, want loan with ReturnedAt=%v", stored, fixedNow)
	}

	if got := s.collected.types(); !equalStrings(got, []string{"LoanReturned"}) {
		t.Errorf("event types: got %v, want [lending.LoanReturned]", got)
	}
	published := s.collected.snapshot()[0].(lending.LoanReturned)
	if !published.ReturnedAt.Equal(fixedNow) {
		t.Errorf("lending.LoanReturned.ReturnedAt: got %v, want %v", published.ReturnedAt, fixedNow)
	}
}

func TestLendingFacade_ReturnLoan_MarksCopyAvailable(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	loan, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}

	if _, err := s.facade.ReturnLoan(ctx, loan.LoanId); err != nil {
		t.Fatalf("ReturnLoan: %v", err)
	}

	got, _ := s.catalog.FindCopy(ctx, copyDto.CopyId)
	if got.Status != catalog.CopyStatusAvailable {
		t.Errorf("copy.Status: got %q, want AVAILABLE", got.Status)
	}
}

// TestLendingFacade_ReturnLoan_PostCommitOrdering is the canonical AC for
// the ReturnLoan ordering rule: the catalog mark-available runs FIRST,
// then lending.LoanReturned publishes. Subscribers can rely on the fully-
// consistent state.
func TestLendingFacade_ReturnLoan_PostCommitOrdering(t *testing.T) {
	ctx := context.Background()
	journal := &journalT{}
	recordingCatalog, _ := newRecordingCatalog(t, journal)
	s := buildSceneWith(t, lendingmemory.Overrides{Catalog: recordingCatalog})

	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	loan, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	journal.reset()
	s.collected.reset()

	s.bus.Subscribe("LoanReturned", func(_ context.Context, _ events.DomainEvent) error {
		journal.append("event:LoanReturned")
		return nil
	})

	if _, err := s.facade.ReturnLoan(ctx, loan.LoanId); err != nil {
		t.Fatalf("ReturnLoan: %v", err)
	}

	if got := journal.snapshot(); !equalStrings(got, []string{"catalog:MarkCopyAvailable", "event:LoanReturned"}) {
		t.Errorf("journal: got %v, want [catalog:MarkCopyAvailable, event:LoanReturned]", got)
	}
}

func TestLendingFacade_ReturnLoan_UnknownLoanReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)

	_, err := s.facade.ReturnLoan(ctx, "never-issued")
	var target *lending.LoanNotFoundError
	if !errors.As(err, &target) {
		t.Fatalf("ReturnLoan: got %v, want *lending.LoanNotFoundError", err)
	}
	if target.LoanId != "never-issued" {
		t.Errorf("lending.LoanNotFoundError.LoanId: got %q, want never-issued", target.LoanId)
	}
	if got := s.collected.types(); len(got) != 0 {
		t.Errorf("events: got %v, want none", got)
	}
}

func TestLendingFacade_ReturnLoan_RollsBackOnLoanSaveFailure(t *testing.T) {
	ctx := context.Background()
	loans := newThrowingOnceLoanRepository()
	s := buildSceneWith(t, lendingmemory.Overrides{Loans: loans})
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	loan, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	s.collected.reset()

	armed := errors.New("loan store is down")
	loans.armFailureOnNextSave(armed)

	_, err = s.facade.ReturnLoan(ctx, loan.LoanId)
	if !errors.Is(err, armed) {
		t.Fatalf("ReturnLoan: got %v, want error wrapping %v", err, armed)
	}

	stored, _ := loans.FindLoanById(ctx, loan.LoanId)
	if stored == nil {
		t.Fatalf("loan repo: missing loan after rollback")
	}
	if stored.ReturnedAt != nil {
		t.Errorf("loan.ReturnedAt: got %v, want nil (tx rolled back)", *stored.ReturnedAt)
	}
	got, _ := s.catalog.FindCopy(ctx, copyDto.CopyId)
	if got.Status != catalog.CopyStatusUnavailable {
		t.Errorf("copy.Status: got %q, want UNAVAILABLE (catalog mutation skipped)", got.Status)
	}
	if got := s.collected.types(); len(got) != 0 {
		t.Errorf("events: got %v, want none", got)
	}
}

func TestLendingFacade_ReturnLoan_CatalogFailureBlocksEventPublish(t *testing.T) {
	ctx := context.Background()
	throwingCatalog, throwingRepo := newThrowingCatalog(t)
	s := buildSceneWith(t, lendingmemory.Overrides{Catalog: throwingCatalog})
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	loan, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	s.collected.reset()

	armed := errors.New("catalog is down")
	throwingRepo.armFailureOnNextMarkCopyAvailable(armed)

	_, err = s.facade.ReturnLoan(ctx, loan.LoanId)
	if !errors.Is(err, armed) {
		t.Fatalf("ReturnLoan: got %v, want error wrapping %v", err, armed)
	}

	stored, _ := s.loans.FindLoanById(ctx, loan.LoanId)
	if stored == nil || stored.ReturnedAt == nil || !stored.ReturnedAt.Equal(fixedNow) {
		t.Errorf("loan repo: got %+v, want ReturnedAt=%v (tx committed)", stored, fixedNow)
	}
	got, _ := throwingCatalog.FindCopy(ctx, copyDto.CopyId)
	if got.Status != catalog.CopyStatusUnavailable {
		t.Errorf("copy.Status: got %q, want UNAVAILABLE (catalog mutation failed)", got.Status)
	}
	if eventTypes := s.collected.types(); len(eventTypes) != 0 {
		t.Errorf("events: got %v, want none (publish happens AFTER catalog call)", eventTypes)
	}
}

func TestLendingFacade_ReturnLoan_BusFailureIsLoggedNotSurfaced(t *testing.T) {
	ctx := context.Background()
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelError}))

	flaky := newFlakyBus(logger)
	// Use a TxFactory that publishes through the same flaky bus so staged
	// lending.LoanOpened events route through the failure-aware path. We only arm
	// failure for lending.LoanReturned after Borrow completes — so Borrow's staged
	// lending.LoanOpened still publishes normally.
	txFactory := func() tx.TransactionalContext {
		return txmemory.NewTransactionalContext(flaky, logger)
	}
	s := buildSceneWith(t, lendingmemory.Overrides{Bus: flaky, TxFactory: txFactory, Logger: logger})
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	loan, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	logBuf.Reset()
	flaky.failNext("LoanReturned", errors.New("bus unreachable"))

	returned, err := s.facade.ReturnLoan(ctx, loan.LoanId)
	if err != nil {
		t.Fatalf("ReturnLoan: got error %v, want nil (bus errors are non-fatal)", err)
	}
	if returned.ReturnedAt == nil {
		t.Errorf("returned.ReturnedAt: got nil, want non-nil")
	}
	got, _ := s.catalog.FindCopy(ctx, copyDto.CopyId)
	if got.Status != catalog.CopyStatusAvailable {
		t.Errorf("copy.Status: got %q, want AVAILABLE", got.Status)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("LoanReturned publish failed")) {
		t.Errorf("log: got %q, want a LoanReturned publish-failure error record", logBuf.String())
	}
}

func TestLendingFacade_ReturnLoan_IsNotIdempotent(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	loan, err := s.facade.Borrow(ctx, memberAuth(alice.MemberId), copyDto.CopyId)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	s.collected.reset()

	if _, err := s.facade.ReturnLoan(ctx, loan.LoanId); err != nil {
		t.Fatalf("ReturnLoan #1: %v", err)
	}
	if _, err := s.facade.ReturnLoan(ctx, loan.LoanId); err != nil {
		t.Fatalf("ReturnLoan #2: %v", err)
	}

	if got := s.collected.types(); !equalStrings(got, []string{"LoanReturned", "LoanReturned"}) {
		t.Errorf("events: got %v, want two lending.LoanReturned (no idempotency check)", got)
	}
}

// -----------------------------------------------------------------------------
// Shared assertion helpers
// -----------------------------------------------------------------------------

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// -----------------------------------------------------------------------------
// Spec-local test doubles — unexported, kept in this file only.
// -----------------------------------------------------------------------------

// journalT is the goroutine-safe append-only journal the post-commit
// ordering tests use to compare the relative ordering of catalog calls vs.
// bus publishes. Mirrors the TS "journal" array.
type journalT struct {
	mu    sync.Mutex
	lines []string
}

func (j *journalT) append(line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.lines = append(j.lines, line)
}

func (j *journalT) snapshot() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.lines...)
}

func (j *journalT) reset() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.lines = nil
}

// recordingCopyMutationsRepository wraps an in-memory catalog repository
// and journals every SaveCopy that flips a copy's Status. The journal
// line is "catalog:MarkCopyUnavailable" or "catalog:MarkCopyAvailable"
// depending on the new status. Other repository calls pass through. The
// lending lending.Facade holds *catalog.Facade (concrete type), so we cannot
// wrap the facade itself — substituting the repository inside the facade
// is the established Phase-2 pattern.
type recordingCopyMutationsRepository struct {
	delegate catalog.Repository
	journal  *journalT
}

func (r *recordingCopyMutationsRepository) SaveCopy(ctx context.Context, c catalog.CopyDto) error {
	switch c.Status {
	case catalog.CopyStatusUnavailable:
		r.journal.append("catalog:MarkCopyUnavailable")
	case catalog.CopyStatusAvailable:
		// First save (registration) lands as AVAILABLE too. Tests reset
		// the journal after seeding to drop the noise.
		r.journal.append("catalog:MarkCopyAvailable")
	}
	return r.delegate.SaveCopy(ctx, c)
}

func (r *recordingCopyMutationsRepository) SaveBook(ctx context.Context, b catalog.BookDto) error {
	return r.delegate.SaveBook(ctx, b)
}
func (r *recordingCopyMutationsRepository) FindBookById(ctx context.Context, id catalog.BookId) (*catalog.BookDto, error) {
	return r.delegate.FindBookById(ctx, id)
}
func (r *recordingCopyMutationsRepository) FindBookByIsbn(ctx context.Context, isbn catalog.Isbn) (*catalog.BookDto, error) {
	return r.delegate.FindBookByIsbn(ctx, isbn)
}
func (r *recordingCopyMutationsRepository) ListBooks(ctx context.Context) ([]catalog.BookDto, error) {
	return r.delegate.ListBooks(ctx)
}
func (r *recordingCopyMutationsRepository) ListBooksByIds(ctx context.Context, ids []catalog.BookId) ([]catalog.BookDto, error) {
	return r.delegate.ListBooksByIds(ctx, ids)
}
func (r *recordingCopyMutationsRepository) DeleteBook(ctx context.Context, id catalog.BookId) error {
	return r.delegate.DeleteBook(ctx, id)
}
func (r *recordingCopyMutationsRepository) FindCopyById(ctx context.Context, id catalog.CopyId) (*catalog.CopyDto, error) {
	return r.delegate.FindCopyById(ctx, id)
}

// throwingOnceCopyMutationsRepository wraps an in-memory catalog
// repository and arms a single-shot error for the next SaveCopy whose
// Status matches the armed transition. Implements catalog.Repository
// so a real catalog.Facade can be built on top of it.
type throwingOnceCopyMutationsRepository struct {
	delegate               catalog.Repository
	mu                     sync.Mutex
	nextMarkUnavailableErr error
	nextMarkAvailableErr   error
}

func (t *throwingOnceCopyMutationsRepository) armFailureOnNextMarkCopyUnavailable(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextMarkUnavailableErr = err
}

func (t *throwingOnceCopyMutationsRepository) armFailureOnNextMarkCopyAvailable(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextMarkAvailableErr = err
}

func (t *throwingOnceCopyMutationsRepository) SaveCopy(ctx context.Context, c catalog.CopyDto) error {
	t.mu.Lock()
	var armed error
	switch c.Status {
	case catalog.CopyStatusUnavailable:
		armed = t.nextMarkUnavailableErr
		t.nextMarkUnavailableErr = nil
	case catalog.CopyStatusAvailable:
		armed = t.nextMarkAvailableErr
		t.nextMarkAvailableErr = nil
	}
	t.mu.Unlock()
	if armed != nil {
		return armed
	}
	return t.delegate.SaveCopy(ctx, c)
}

func (t *throwingOnceCopyMutationsRepository) SaveBook(ctx context.Context, b catalog.BookDto) error {
	return t.delegate.SaveBook(ctx, b)
}
func (t *throwingOnceCopyMutationsRepository) FindBookById(ctx context.Context, id catalog.BookId) (*catalog.BookDto, error) {
	return t.delegate.FindBookById(ctx, id)
}
func (t *throwingOnceCopyMutationsRepository) FindBookByIsbn(ctx context.Context, isbn catalog.Isbn) (*catalog.BookDto, error) {
	return t.delegate.FindBookByIsbn(ctx, isbn)
}
func (t *throwingOnceCopyMutationsRepository) ListBooks(ctx context.Context) ([]catalog.BookDto, error) {
	return t.delegate.ListBooks(ctx)
}
func (t *throwingOnceCopyMutationsRepository) ListBooksByIds(ctx context.Context, ids []catalog.BookId) ([]catalog.BookDto, error) {
	return t.delegate.ListBooksByIds(ctx, ids)
}
func (t *throwingOnceCopyMutationsRepository) DeleteBook(ctx context.Context, id catalog.BookId) error {
	return t.delegate.DeleteBook(ctx, id)
}
func (t *throwingOnceCopyMutationsRepository) FindCopyById(ctx context.Context, id catalog.CopyId) (*catalog.CopyDto, error) {
	return t.delegate.FindCopyById(ctx, id)
}

// throwingOnceLoanRepository wraps a real InMemoryLoanRepository and arms
// a single-shot error for the next SaveLoan call. After the armed error
// fires, the wrapper reverts to delegating cleanly.
type throwingOnceLoanRepository struct {
	delegate *lendingmemory.LoanRepository
	mu       sync.Mutex
	nextErr  error
}

func newThrowingOnceLoanRepository() *throwingOnceLoanRepository {
	return &throwingOnceLoanRepository{delegate: lendingmemory.NewLoanRepository()}
}

func (t *throwingOnceLoanRepository) armFailureOnNextSave(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextErr = err
}

func (t *throwingOnceLoanRepository) SaveLoan(ctx context.Context, loan lending.LoanDto, txc tx.TransactionalContext) error {
	t.mu.Lock()
	armed := t.nextErr
	t.nextErr = nil
	t.mu.Unlock()
	if armed != nil {
		return armed
	}
	return t.delegate.SaveLoan(ctx, loan, txc)
}

func (t *throwingOnceLoanRepository) FindLoanById(ctx context.Context, loanId lending.LoanId) (*lending.LoanDto, error) {
	return t.delegate.FindLoanById(ctx, loanId)
}

func (t *throwingOnceLoanRepository) ListLoansForMember(ctx context.Context, memberId membership.MemberId) ([]lending.LoanDto, error) {
	return t.delegate.ListLoansForMember(ctx, memberId)
}

func (t *throwingOnceLoanRepository) ListLoansForBook(ctx context.Context, bookId catalog.BookId) ([]lending.LoanDto, error) {
	return t.delegate.ListLoansForBook(ctx, bookId)
}

func (t *throwingOnceLoanRepository) ListLoans(ctx context.Context) ([]lending.LoanDto, error) {
	return t.delegate.ListLoans(ctx)
}

// throwingOnceReservationRepository mirrors throwingOnceLoanRepository
// for the Reserve atomicity test.
type throwingOnceReservationRepository struct {
	delegate *lendingmemory.ReservationRepository
	mu       sync.Mutex
	nextErr  error
}

func newThrowingOnceReservationRepository() *throwingOnceReservationRepository {
	return &throwingOnceReservationRepository{delegate: lendingmemory.NewReservationRepository()}
}

func (t *throwingOnceReservationRepository) armFailureOnNextSave(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextErr = err
}

func (t *throwingOnceReservationRepository) SaveReservation(ctx context.Context, reservation lending.ReservationDto, txc tx.TransactionalContext) error {
	t.mu.Lock()
	armed := t.nextErr
	t.nextErr = nil
	t.mu.Unlock()
	if armed != nil {
		return armed
	}
	return t.delegate.SaveReservation(ctx, reservation, txc)
}

func (t *throwingOnceReservationRepository) FindReservationById(ctx context.Context, reservationId lending.ReservationId) (*lending.ReservationDto, error) {
	return t.delegate.FindReservationById(ctx, reservationId)
}

func (t *throwingOnceReservationRepository) ListReservationsForBook(ctx context.Context, bookId catalog.BookId) ([]lending.ReservationDto, error) {
	return t.delegate.ListReservationsForBook(ctx, bookId)
}

func (t *throwingOnceReservationRepository) ListReservationsForMember(ctx context.Context, memberId membership.MemberId) ([]lending.ReservationDto, error) {
	return t.delegate.ListReservationsForMember(ctx, memberId)
}

func (t *throwingOnceReservationRepository) PendingReservationCountForBook(ctx context.Context, bookId catalog.BookId) (int, error) {
	return t.delegate.PendingReservationCountForBook(ctx, bookId)
}

func (t *throwingOnceReservationRepository) ListPendingReservationsForBook(ctx context.Context, bookId catalog.BookId) ([]lending.ReservationDto, error) {
	return t.delegate.ListPendingReservationsForBook(ctx, bookId)
}

// flakyBus wraps a real InMemoryEventBus, optionally short-circuiting
// Publish for a single armed event type. Subscribe always delegates so
// non-armed events fan out normally.
type flakyBus struct {
	mu        sync.Mutex
	failTypes map[string]error
	delegate  *eventsmemory.Bus
}

func newFlakyBus(logger *slog.Logger) *flakyBus {
	return &flakyBus{
		failTypes: map[string]error{},
		delegate:  eventsmemory.NewBus(logger),
	}
}

func (f *flakyBus) failNext(eventType string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failTypes[eventType] = err
}

func (f *flakyBus) Publish(ctx context.Context, evt events.DomainEvent) error {
	f.mu.Lock()
	armed, ok := f.failTypes[evt.Type()]
	if ok {
		delete(f.failTypes, evt.Type())
	}
	f.mu.Unlock()
	if armed != nil {
		return armed
	}
	return f.delegate.Publish(ctx, evt)
}

func (f *flakyBus) Subscribe(eventType string, handler func(ctx context.Context, evt events.DomainEvent) error) events.Unsubscribe {
	return f.delegate.Subscribe(eventType, handler)
}
