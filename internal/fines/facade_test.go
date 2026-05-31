// facade_test.go is the facade-level spec for the fines module — a
// scenario port of apps/library/src/fines/fines.facade.spec.ts from the
// source TypeScript repository.
//
// Stdlib testing only — errors.As for typed-error assertions, no testify,
// no mock library. The scene helper wires real *lending.Facade +
// *membership.Facade + *catalog.Facade so the fines facade reads the
// in-memory state the test seeds via real cross-module calls.
package fines

import (
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
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// fixedNow is the deterministic timestamp the scene's clocks start from.
var fixedNow = time.Date(2030, 1, 15, 0, 0, 0, 0, time.UTC)

// sequentialIds returns a deterministic id generator producing
// "<prefix>-<n>" strings.
func sequentialIds(prefix string) func() string {
	counter := 0
	return func() string {
		counter++
		return prefix + "-" + strconv.Itoa(counter)
	}
}

// silentLogger returns a slog.Logger that discards all output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mutableClock holds a time.Time the test can advance. Both the lending
// facade and the fines facade read through clock() so calling advance()
// shifts every subsequent time read for both modules.
type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMutableClock(start time.Time) *mutableClock {
	return &mutableClock{now: start}
}

func (c *mutableClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// collectedEvents is the goroutine-safe append target for the bus
// subscribers.
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
	out := make([]string, 0, len(snap))
	for _, evt := range snap {
		out = append(out, evt.Type())
	}
	return out
}

// scene aggregates every collaborator a fines facade test reads through:
// the fines facade itself, the underlying lending + membership + catalog
// facades (for seeding loans/members/copies), the in-memory bus and the
// captured-events slice.
type scene struct {
	fines      *Facade
	lending    *lending.Facade
	membership *membership.Facade
	catalog    *catalog.Facade
	repository *InMemoryFineRepository
	bus        *events.InMemoryEventBus
	collected  *collectedEvents
	clock      *mutableClock
	config     FinesConfig
}

// sceneOpts lets a test override the FinesConfig used to build the scene
// (auto-suspend tests need a low threshold).
type sceneOpts struct {
	Config *FinesConfig
}

// buildScene constructs a fresh scene. Both the lending facade and the
// fines facade read through the same mutableClock; the bus captures every
// fines + lending event in append order.
func buildScene(t *testing.T, opts ...func(*sceneOpts)) *scene {
	t.Helper()
	cfg := &sceneOpts{}
	for _, opt := range opts {
		opt(cfg)
	}

	logger := silentLogger()
	clock := newMutableClock(fixedNow)
	bus := events.NewInMemoryEventBus(logger)

	catalogFacade := catalog.NewFacadeWithOverrides(catalog.Overrides{
		NewID:  sequentialIds("cat"),
		Logger: logger,
	})
	membershipFacade := membership.NewFacadeWithOverrides(membership.Overrides{
		NewID:  sequentialIds("mem"),
		Logger: logger,
	})
	lendingFacade := lending.NewFacadeWithOverrides(lending.Overrides{
		Catalog:    catalogFacade,
		Membership: membershipFacade,
		Bus:        bus,
		NewID:      sequentialIds("loan"),
		Clock:      clock.read,
		Logger:     logger,
	})

	repo := NewInMemoryFineRepository()
	finesConfig := defaultConfig()
	if cfg.Config != nil {
		finesConfig = *cfg.Config
	}
	finesFacade := NewFacadeWithOverrides(Overrides{
		Lending:    lendingFacade,
		Membership: membershipFacade,
		Repository: repo,
		Bus:        bus,
		Config:     &finesConfig,
		NewID:      sequentialIds("fine"),
		Clock:      clock.read,
		Logger:     logger,
	})

	collected := &collectedEvents{}
	for _, eventType := range []string{
		"LoanOpened", "LoanReturned", "ReservationQueued",
		"FineAssessed", "MemberAutoSuspended",
	} {
		bus.Subscribe(eventType, func(_ context.Context, evt events.DomainEvent) error {
			collected.append(evt)
			return nil
		})
	}

	return &scene{
		fines:      finesFacade,
		lending:    lendingFacade,
		membership: membershipFacade,
		catalog:    catalogFacade,
		repository: repo,
		bus:        bus,
		collected:  collected,
		clock:      clock,
		config:     finesConfig,
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
	email := "fines-member-" + strconv.Itoa(seq) + "@lib.test"
	member, err := s.membership.RegisterMember(context.Background(), membership.NewMemberDto{
		Name:  name,
		Email: email,
	})
	if err != nil {
		t.Fatalf("registerMember: %v", err)
	}
	return member
}

// memberAuth builds the AuthUser with role MEMBER for the given memberId.
func memberAuth(memberId membership.MemberId) accesscontrol.AuthUser {
	return accesscontrol.AuthUser{MemberID: string(memberId), Role: accesscontrol.RoleMember}
}

// borrowCopyAt advances the scene's clock to borrowAt, opens a loan for
// the given member against the copy, and returns the loan DTO. The clock
// is left at borrowAt; the caller advances it further to make the loan
// overdue.
func borrowCopyAt(t *testing.T, s *scene, member membership.MemberDto, copyId catalog.CopyId, borrowAt time.Time) lending.LoanDto {
	t.Helper()
	s.clock.mu.Lock()
	s.clock.now = borrowAt
	s.clock.mu.Unlock()
	loan, err := s.lending.Borrow(context.Background(), memberAuth(member.MemberId), copyId)
	if err != nil {
		t.Fatalf("borrowCopyAt: %v", err)
	}
	return loan
}

// -----------------------------------------------------------------------------
// AssessFinesFor
// -----------------------------------------------------------------------------

func TestFinesFacade_AssessFinesFor_OneOverdueLoan(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")

	borrowedAt := fixedNow
	borrowCopyAt(t, s, alice, copyDto.CopyId, borrowedAt)

	now := borrowedAt.Add(30 * 24 * time.Hour)
	assessed, err := s.fines.AssessFinesFor(ctx, alice.MemberId, now)
	if err != nil {
		t.Fatalf("AssessFinesFor: %v", err)
	}
	if len(assessed) != 1 {
		t.Fatalf("assessed length: got %d, want 1", len(assessed))
	}
	fine := assessed[0]
	// Loan dueDate is borrowedAt + 14 days; now is borrowedAt + 30 days.
	// ceil((30 - 14) days) == 16 days; 16 * 25 == 400.
	if fine.AmountCents != 400 {
		t.Errorf("fine.AmountCents: got %d, want 400 (16 days * 25 cents)", fine.AmountCents)
	}
	if fine.MemberId != alice.MemberId {
		t.Errorf("fine.MemberId: got %q, want %q", fine.MemberId, alice.MemberId)
	}
	if !fine.AssessedAt.Equal(now) {
		t.Errorf("fine.AssessedAt: got %v, want %v", fine.AssessedAt, now)
	}
	if fine.PaidAt != nil {
		t.Errorf("fine.PaidAt: got %v, want nil", *fine.PaidAt)
	}

	stored, err := s.repository.FindFineById(ctx, fine.FineId)
	if err != nil {
		t.Fatalf("FindFineById: %v", err)
	}
	if stored == nil || stored.FineId != fine.FineId {
		t.Errorf("repository: got %+v, want fine with id %q", stored, fine.FineId)
	}

	if got := s.collected.types(); !containsType(got, "FineAssessed") {
		t.Errorf("event types: got %v, want to contain FineAssessed", got)
	}
}

func TestFinesFacade_AssessFinesFor_SkipsAlreadyFinedLoans(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	borrowCopyAt(t, s, alice, copyDto.CopyId, fixedNow)
	now := fixedNow.Add(30 * 24 * time.Hour)

	first, err := s.fines.AssessFinesFor(ctx, alice.MemberId, now)
	if err != nil {
		t.Fatalf("AssessFinesFor (first): %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first AssessFinesFor length: got %d, want 1", len(first))
	}

	second, err := s.fines.AssessFinesFor(ctx, alice.MemberId, now)
	if err != nil {
		t.Fatalf("AssessFinesFor (second): %v", err)
	}
	if len(second) != 0 {
		t.Errorf("second AssessFinesFor length: got %d, want 0 (already fined)", len(second))
	}

	stored, err := s.repository.ListFinesForMember(ctx, alice.MemberId)
	if err != nil {
		t.Fatalf("ListFinesForMember: %v", err)
	}
	if len(stored) != 1 {
		t.Errorf("stored fines: got %d, want 1", len(stored))
	}
	if got := countAssessedEvents(s.collected); got != 1 {
		t.Errorf("FineAssessed count: got %d, want 1", got)
	}
}

func TestFinesFacade_AssessFinesFor_SkipsNotYetDueLoans(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	borrowCopyAt(t, s, alice, copyDto.CopyId, fixedNow)

	now := fixedNow.Add(7 * 24 * time.Hour) // before dueDate (14 days)
	assessed, err := s.fines.AssessFinesFor(ctx, alice.MemberId, now)
	if err != nil {
		t.Fatalf("AssessFinesFor: %v", err)
	}
	if len(assessed) != 0 {
		t.Errorf("assessed length: got %d, want 0 (not yet due)", len(assessed))
	}
}

func TestFinesFacade_AssessFinesFor_SkipsReturnedLoans(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	loan := borrowCopyAt(t, s, alice, copyDto.CopyId, fixedNow)

	// Return inside the lending facade BEFORE assessing fines.
	s.clock.mu.Lock()
	s.clock.now = fixedNow.Add(10 * 24 * time.Hour)
	s.clock.mu.Unlock()
	if _, err := s.lending.ReturnLoan(ctx, loan.LoanId); err != nil {
		t.Fatalf("ReturnLoan: %v", err)
	}

	now := fixedNow.Add(30 * 24 * time.Hour)
	assessed, err := s.fines.AssessFinesFor(ctx, alice.MemberId, now)
	if err != nil {
		t.Fatalf("AssessFinesFor: %v", err)
	}
	if len(assessed) != 0 {
		t.Errorf("assessed length: got %d, want 0 (returned)", len(assessed))
	}
}

func TestFinesFacade_AssessFinesFor_UnknownMember(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)

	_, err := s.fines.AssessFinesFor(ctx, membership.MemberId("does-not-exist"), fixedNow)
	if err == nil {
		t.Fatal("AssessFinesFor: got nil, want *membership.MemberNotFoundError")
	}
	var notFound *membership.MemberNotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("AssessFinesFor error: got %T(%v), want *membership.MemberNotFoundError", err, err)
	}
	if got := countAssessedEvents(s.collected); got != 0 {
		t.Errorf("FineAssessed count: got %d, want 0", got)
	}
}

// -----------------------------------------------------------------------------
// ProcessOverdueLoans
// -----------------------------------------------------------------------------

func TestFinesFacade_ProcessOverdueLoans_AssessesDistinctMembers(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)

	_, copyA := seedAvailableCopy(t, s, 1)
	_, copyB1 := seedAvailableCopy(t, s, 2)
	_, copyB2 := seedAvailableCopy(t, s, 3)
	_, copyC1 := seedAvailableCopy(t, s, 4)
	_, copyC2 := seedAvailableCopy(t, s, 5)

	alice := registerMember(t, s, 1, "Alice")
	bob := registerMember(t, s, 2, "Bob")
	carol := registerMember(t, s, 3, "Carol")

	// Alice: one overdue loan.
	borrowCopyAt(t, s, alice, copyA.CopyId, fixedNow)
	// Bob: one overdue + one not-yet-due (borrowed near `now`).
	borrowCopyAt(t, s, bob, copyB1.CopyId, fixedNow)
	borrowCopyAt(t, s, bob, copyB2.CopyId, fixedNow.Add(29*24*time.Hour))
	// Carol: one returned + one overdue.
	carolReturned := borrowCopyAt(t, s, carol, copyC1.CopyId, fixedNow)
	s.clock.mu.Lock()
	s.clock.now = fixedNow.Add(5 * 24 * time.Hour)
	s.clock.mu.Unlock()
	if _, err := s.lending.ReturnLoan(ctx, carolReturned.LoanId); err != nil {
		t.Fatalf("Carol ReturnLoan: %v", err)
	}
	borrowCopyAt(t, s, carol, copyC2.CopyId, fixedNow)

	now := fixedNow.Add(30 * 24 * time.Hour)
	if err := s.fines.ProcessOverdueLoans(ctx, now); err != nil {
		t.Fatalf("ProcessOverdueLoans: %v", err)
	}

	for _, member := range []membership.MemberDto{alice, bob, carol} {
		fines, err := s.repository.ListFinesForMember(ctx, member.MemberId)
		if err != nil {
			t.Fatalf("ListFinesForMember(%q): %v", member.MemberId, err)
		}
		if len(fines) != 1 {
			t.Errorf("fines for %s: got %d, want 1", member.Name, len(fines))
		}
	}
	if got := countAssessedEvents(s.collected); got != 3 {
		t.Errorf("FineAssessed count: got %d, want 3", got)
	}
}

// -----------------------------------------------------------------------------
// Auto-suspend
// -----------------------------------------------------------------------------

func TestFinesFacade_AutoSuspend_AtThreshold(t *testing.T) {
	ctx := context.Background()
	low := FinesConfig{DailyRateCents: 25, SuspensionThresholdCents: 100}
	s := buildScene(t, func(opts *sceneOpts) { opts.Config = &low })

	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	borrowCopyAt(t, s, alice, copyDto.CopyId, fixedNow)

	// 30 days overdue against 14-day window = 16 days * 25 = 400 cents — well past 100.
	now := fixedNow.Add(30 * 24 * time.Hour)
	if err := s.fines.ProcessOverdueLoans(ctx, now); err != nil {
		t.Fatalf("ProcessOverdueLoans: %v", err)
	}

	member, err := s.membership.FindMember(ctx, alice.MemberId)
	if err != nil {
		t.Fatalf("FindMember: %v", err)
	}
	if member.Status != membership.MembershipStatusSuspended {
		t.Errorf("member.Status: got %q, want %q", member.Status, membership.MembershipStatusSuspended)
	}

	suspendedEvents := collectByType(s.collected, "MemberAutoSuspended")
	if len(suspendedEvents) != 1 {
		t.Fatalf("MemberAutoSuspended count: got %d, want 1", len(suspendedEvents))
	}
	evt := suspendedEvents[0].(MemberAutoSuspended)
	if evt.MemberId != alice.MemberId {
		t.Errorf("MemberAutoSuspended.MemberId: got %q, want %q", evt.MemberId, alice.MemberId)
	}
	if evt.TotalUnpaidCents != 400 {
		t.Errorf("MemberAutoSuspended.TotalUnpaidCents: got %d, want 400", evt.TotalUnpaidCents)
	}
	if evt.ThresholdCents != 100 {
		t.Errorf("MemberAutoSuspended.ThresholdCents: got %d, want 100", evt.ThresholdCents)
	}
	if !evt.SuspendedAt.Equal(now) {
		t.Errorf("MemberAutoSuspended.SuspendedAt: got %v, want %v", evt.SuspendedAt, now)
	}
}

func TestFinesFacade_AutoSuspend_SkippedUnderThreshold(t *testing.T) {
	ctx := context.Background()
	high := FinesConfig{DailyRateCents: 25, SuspensionThresholdCents: 10_000}
	s := buildScene(t, func(opts *sceneOpts) { opts.Config = &high })

	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	borrowCopyAt(t, s, alice, copyDto.CopyId, fixedNow)

	now := fixedNow.Add(30 * 24 * time.Hour)
	if err := s.fines.ProcessOverdueLoans(ctx, now); err != nil {
		t.Fatalf("ProcessOverdueLoans: %v", err)
	}

	member, err := s.membership.FindMember(ctx, alice.MemberId)
	if err != nil {
		t.Fatalf("FindMember: %v", err)
	}
	if member.Status == membership.MembershipStatusSuspended {
		t.Errorf("member.Status: got SUSPENDED, want non-suspended (under threshold)")
	}
	if got := len(collectByType(s.collected, "MemberAutoSuspended")); got != 0 {
		t.Errorf("MemberAutoSuspended count: got %d, want 0", got)
	}
}

func TestFinesFacade_AutoSuspend_SkippedWhenAlreadySuspended(t *testing.T) {
	ctx := context.Background()
	low := FinesConfig{DailyRateCents: 25, SuspensionThresholdCents: 100}
	s := buildScene(t, func(opts *sceneOpts) { opts.Config = &low })

	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	borrowCopyAt(t, s, alice, copyDto.CopyId, fixedNow)
	// Pre-suspend before assessing.
	if _, err := s.membership.Suspend(ctx, alice.MemberId); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	now := fixedNow.Add(30 * 24 * time.Hour)
	if err := s.fines.ProcessOverdueLoans(ctx, now); err != nil {
		t.Fatalf("ProcessOverdueLoans: %v", err)
	}

	if got := len(collectByType(s.collected, "MemberAutoSuspended")); got != 0 {
		t.Errorf("MemberAutoSuspended count: got %d, want 0 (already suspended)", got)
	}
}

// -----------------------------------------------------------------------------
// FindFine / PayFine / ListFinesFor
// -----------------------------------------------------------------------------

func TestFinesFacade_FindFine_Happy(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	borrowCopyAt(t, s, alice, copyDto.CopyId, fixedNow)

	now := fixedNow.Add(30 * 24 * time.Hour)
	assessed, err := s.fines.AssessFinesFor(ctx, alice.MemberId, now)
	if err != nil {
		t.Fatalf("AssessFinesFor: %v", err)
	}
	got, err := s.fines.FindFine(ctx, assessed[0].FineId)
	if err != nil {
		t.Fatalf("FindFine: %v", err)
	}
	if got.FineId != assessed[0].FineId {
		t.Errorf("FindFine: got %q, want %q", got.FineId, assessed[0].FineId)
	}
}

func TestFinesFacade_FindFine_NotFound(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, err := s.fines.FindFine(ctx, FineId("missing"))
	if err == nil {
		t.Fatal("FindFine: got nil, want *FineNotFoundError")
	}
	var notFound *FineNotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("FindFine error: got %T(%v), want *FineNotFoundError", err, err)
	}
}

func TestFinesFacade_PayFine_Happy(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	borrowCopyAt(t, s, alice, copyDto.CopyId, fixedNow)
	now := fixedNow.Add(30 * 24 * time.Hour)
	assessed, err := s.fines.AssessFinesFor(ctx, alice.MemberId, now)
	if err != nil {
		t.Fatalf("AssessFinesFor: %v", err)
	}

	payAt := now.Add(2 * 24 * time.Hour)
	s.clock.mu.Lock()
	s.clock.now = payAt
	s.clock.mu.Unlock()

	paid, err := s.fines.PayFine(ctx, assessed[0].FineId)
	if err != nil {
		t.Fatalf("PayFine: %v", err)
	}
	if paid.PaidAt == nil || !paid.PaidAt.Equal(payAt) {
		t.Errorf("paid.PaidAt: got %v, want %v", paid.PaidAt, payAt)
	}
	again, err := s.fines.FindFine(ctx, assessed[0].FineId)
	if err != nil {
		t.Fatalf("FindFine (after pay): %v", err)
	}
	if again.PaidAt == nil || !again.PaidAt.Equal(payAt) {
		t.Errorf("FindFine.PaidAt: got %v, want %v", again.PaidAt, payAt)
	}
}

func TestFinesFacade_PayFine_AlreadyPaid(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyDto := seedAvailableCopy(t, s, 1)
	alice := registerMember(t, s, 1, "Alice")
	borrowCopyAt(t, s, alice, copyDto.CopyId, fixedNow)
	now := fixedNow.Add(30 * 24 * time.Hour)
	assessed, err := s.fines.AssessFinesFor(ctx, alice.MemberId, now)
	if err != nil {
		t.Fatalf("AssessFinesFor: %v", err)
	}
	if _, err := s.fines.PayFine(ctx, assessed[0].FineId); err != nil {
		t.Fatalf("PayFine (first): %v", err)
	}
	_, err = s.fines.PayFine(ctx, assessed[0].FineId)
	if err == nil {
		t.Fatal("PayFine (second): got nil, want *FineAlreadyPaidError")
	}
	var alreadyPaid *FineAlreadyPaidError
	if !errors.As(err, &alreadyPaid) {
		t.Errorf("PayFine error: got %T(%v), want *FineAlreadyPaidError", err, err)
	}
}

func TestFinesFacade_PayFine_NotFound(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, err := s.fines.PayFine(ctx, FineId("missing"))
	if err == nil {
		t.Fatal("PayFine: got nil, want *FineNotFoundError")
	}
	var notFound *FineNotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("PayFine error: got %T(%v), want *FineNotFoundError", err, err)
	}
}

func TestFinesFacade_ListFinesFor_OnlyNamedMember(t *testing.T) {
	ctx := context.Background()
	s := buildScene(t)
	_, copyA := seedAvailableCopy(t, s, 1)
	_, copyB := seedAvailableCopy(t, s, 2)
	alice := registerMember(t, s, 1, "Alice")
	bob := registerMember(t, s, 2, "Bob")
	borrowCopyAt(t, s, alice, copyA.CopyId, fixedNow)
	borrowCopyAt(t, s, bob, copyB.CopyId, fixedNow)
	now := fixedNow.Add(30 * 24 * time.Hour)
	if _, err := s.fines.AssessFinesFor(ctx, alice.MemberId, now); err != nil {
		t.Fatalf("AssessFinesFor(alice): %v", err)
	}
	if _, err := s.fines.AssessFinesFor(ctx, bob.MemberId, now); err != nil {
		t.Fatalf("AssessFinesFor(bob): %v", err)
	}

	aliceFines, err := s.fines.ListFinesFor(ctx, alice.MemberId)
	if err != nil {
		t.Fatalf("ListFinesFor(alice): %v", err)
	}
	if len(aliceFines) != 1 {
		t.Errorf("aliceFines length: got %d, want 1", len(aliceFines))
	}
	if aliceFines[0].MemberId != alice.MemberId {
		t.Errorf("aliceFines[0].MemberId: got %q, want %q", aliceFines[0].MemberId, alice.MemberId)
	}
}

// -----------------------------------------------------------------------------
// Misc helpers
// -----------------------------------------------------------------------------

func containsType(types []string, want string) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}

func countAssessedEvents(collected *collectedEvents) int {
	count := 0
	for _, t := range collected.types() {
		if t == "FineAssessed" {
			count++
		}
	}
	return count
}

func collectByType(collected *collectedEvents, wantType string) []events.DomainEvent {
	snap := collected.snapshot()
	out := make([]events.DomainEvent, 0)
	for _, evt := range snap {
		if evt.Type() == wantType {
			out = append(out, evt)
		}
	}
	return out
}
