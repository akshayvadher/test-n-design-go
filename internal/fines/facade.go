package fines

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
)

// Facade is the only public surface of the fines module. Unexported fields
// keep collaborators encapsulated; the composition root wires them via
// NewFacade and tests substitute them via NewFacadeWithOverrides.
//
// The facade reads from lending + membership and writes only its own
// fines table. Auto-suspend is the single cross-module WRITE — it calls
// membership.Suspend AFTER the per-fine save completes, matching the
// post-commit cross-module-write rule.
type Facade struct {
	lending    *lending.Facade
	membership *membership.Facade
	repository FineRepository
	bus        events.EventBus
	config     FinesConfig
	newID      func() string
	clock      func() time.Time
	logger     *slog.Logger
}

// NewFacade wires the Facade with explicit dependencies. The composition
// root passes the concrete implementations; tests use NewFacadeWithOverrides.
func NewFacade(
	lendingFacade *lending.Facade,
	membershipFacade *membership.Facade,
	repository FineRepository,
	bus events.EventBus,
	config FinesConfig,
	newID func() string,
	clock func() time.Time,
	logger *slog.Logger,
) *Facade {
	return &Facade{
		lending:    lendingFacade,
		membership: membershipFacade,
		repository: repository,
		bus:        bus,
		config:     config,
		newID:      newID,
		clock:      clock,
		logger:     logger,
	}
}

// AssessFinesFor scans the member's loans, fines the overdue ones that
// have not already been fined, and returns the freshly assessed slice.
// Re-running for the same memberId at the same `now` returns an empty
// slice (idempotent by way of the already-fined short-circuit).
//
// Cross-module reads happen BEFORE the local writes: membership.FindMember
// surfaces *MemberNotFoundError for unknown members; lending.ListLoansFor
// returns the named member's loans.
func (f *Facade) AssessFinesFor(ctx context.Context, memberId membership.MemberId, now time.Time) ([]FineDto, error) {
	if _, err := f.membership.FindMember(ctx, memberId); err != nil {
		return nil, err
	}
	loans, err := f.lending.ListLoansFor(ctx, memberId)
	if err != nil {
		return nil, err
	}
	assessed := make([]FineDto, 0)
	for _, loan := range loans {
		if !isOverdue(loan, now) {
			continue
		}
		existing, err := f.repository.FindFineByLoanId(ctx, loan.LoanId)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			continue
		}
		fine := f.buildFine(loan, now)
		if err := f.repository.SaveFine(ctx, fine); err != nil {
			return nil, err
		}
		if err := f.bus.Publish(ctx, fineAssessedEvent(fine)); err != nil {
			f.logger.Error("fines: FineAssessed publish failed",
				slog.String("fine_id", string(fine.FineId)),
				slog.String("error", err.Error()),
			)
		}
		assessed = append(assessed, fine)
	}
	return assessed, nil
}

// ProcessOverdueLoans iterates every distinct memberId with at least one
// overdue loan at `now`, assesses fines for each, and then runs the
// auto-suspend check. Returns on the first error from either call (matches
// the source TS loop semantics).
func (f *Facade) ProcessOverdueLoans(ctx context.Context, now time.Time) error {
	overdue, err := f.lending.ListOverdueLoans(ctx, now)
	if err != nil {
		return err
	}
	for _, memberId := range distinctMemberIds(overdue) {
		if _, err := f.AssessFinesFor(ctx, memberId, now); err != nil {
			return err
		}
		if err := f.maybeAutoSuspend(ctx, memberId, now); err != nil {
			return err
		}
	}
	return nil
}

// ListFinesFor delegates to the repository's per-member listing. Pure
// read; no events.
func (f *Facade) ListFinesFor(ctx context.Context, memberId membership.MemberId) ([]FineDto, error) {
	return f.repository.ListFinesForMember(ctx, memberId)
}

// FindFine loads the fine by id. Unknown id returns *FineNotFoundError.
func (f *Facade) FindFine(ctx context.Context, fineId FineId) (FineDto, error) {
	fine, err := f.repository.FindFineById(ctx, fineId)
	if err != nil {
		return FineDto{}, err
	}
	if fine == nil {
		return FineDto{}, &FineNotFoundError{FineId: fineId}
	}
	return *fine, nil
}

// PayFine flips PaidAt to the current clock. Unknown id returns
// *FineNotFoundError; an already-paid fine returns *FineAlreadyPaidError.
// NO event is published on pay (matches the source TS — payFine returns
// the updated DTO and that is the entire effect).
func (f *Facade) PayFine(ctx context.Context, fineId FineId) (FineDto, error) {
	fine, err := f.repository.FindFineById(ctx, fineId)
	if err != nil {
		return FineDto{}, err
	}
	if fine == nil {
		return FineDto{}, &FineNotFoundError{FineId: fineId}
	}
	if fine.PaidAt != nil {
		return FineDto{}, &FineAlreadyPaidError{FineId: fineId}
	}
	paidAt := f.clock()
	paid := *fine
	paid.PaidAt = &paidAt
	if err := f.repository.SaveFine(ctx, paid); err != nil {
		return FineDto{}, err
	}
	return paid, nil
}

// maybeAutoSuspend computes the member's unpaid-fines total and, when it
// crosses the configured threshold, flips the membership status to
// SUSPENDED + publishes MemberAutoSuspended. Idempotent: a member who is
// already SUSPENDED is skipped (no double-suspend, no second event).
func (f *Facade) maybeAutoSuspend(ctx context.Context, memberId membership.MemberId, now time.Time) error {
	total, err := f.unpaidTotal(ctx, memberId)
	if err != nil {
		return err
	}
	if total < f.config.SuspensionThresholdCents {
		return nil
	}
	member, err := f.membership.FindMember(ctx, memberId)
	if err != nil {
		return err
	}
	if member.Status == membership.MembershipStatusSuspended {
		return nil
	}
	if _, err := f.membership.Suspend(ctx, memberId); err != nil {
		return err
	}
	evt := MemberAutoSuspended{
		MemberId:         memberId,
		TotalUnpaidCents: total,
		ThresholdCents:   f.config.SuspensionThresholdCents,
		SuspendedAt:      now,
	}
	if err := f.bus.Publish(ctx, evt); err != nil {
		f.logger.Error("fines: MemberAutoSuspended publish failed",
			slog.String("member_id", string(memberId)),
			slog.String("error", err.Error()),
		)
	}
	return nil
}

// unpaidTotal sums AmountCents of the member's fines whose PaidAt is nil.
func (f *Facade) unpaidTotal(ctx context.Context, memberId membership.MemberId) (AmountCents, error) {
	fines, err := f.repository.ListFinesForMember(ctx, memberId)
	if err != nil {
		return 0, err
	}
	var total AmountCents
	for _, fine := range fines {
		if fine.PaidAt != nil {
			continue
		}
		total += fine.AmountCents
	}
	return total, nil
}

// buildFine projects an overdue loan into a fresh FineDto. The
// daysOverdue computation uses ceil-of-days-between to match the source
// TS Math.ceil((later - earlier) / MS_PER_DAY).
func (f *Facade) buildFine(loan lending.LoanDto, now time.Time) FineDto {
	days := daysBetween(loan.DueDate, now)
	return FineDto{
		FineId:      FineId(f.newID()),
		MemberId:    loan.MemberId,
		LoanId:      loan.LoanId,
		AmountCents: AmountCents(days) * f.config.DailyRateCents,
		AssessedAt:  now,
		PaidAt:      nil,
	}
}

// fineAssessedEvent projects a FineDto into the FineAssessed event payload.
func fineAssessedEvent(fine FineDto) FineAssessed {
	return FineAssessed{
		FineId:      fine.FineId,
		MemberId:    fine.MemberId,
		LoanId:      fine.LoanId,
		AmountCents: fine.AmountCents,
		AssessedAt:  fine.AssessedAt,
	}
}

// isOverdue returns true when the loan is still open and its DueDate is
// strictly before now. Matches the source TS predicate verbatim.
func isOverdue(loan lending.LoanDto, now time.Time) bool {
	return loan.ReturnedAt == nil && loan.DueDate.Before(now)
}

// distinctMemberIds collects the unique MemberId values from loans in
// first-seen order. Matches the source TS Array.from(new Set(...))
// semantics.
func distinctMemberIds(loans []lending.LoanDto) []membership.MemberId {
	seen := make(map[membership.MemberId]struct{}, len(loans))
	ids := make([]membership.MemberId, 0, len(loans))
	for _, loan := range loans {
		if _, ok := seen[loan.MemberId]; ok {
			continue
		}
		seen[loan.MemberId] = struct{}{}
		ids = append(ids, loan.MemberId)
	}
	return ids
}

// daysBetween returns ceil((later - earlier) / 24h) as an int64 day count.
// Matches the source TS Math.ceil((later.getTime() - earlier.getTime()) /
// MS_PER_DAY).
func daysBetween(earlier, later time.Time) int64 {
	hours := later.Sub(earlier).Hours()
	return int64(math.Ceil(hours / 24))
}
