package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// LoanRepository is the in-memory lending.LoanRepository implementation.
// It is safe for concurrent use. Loans are stored by LoanId; List* methods
// return the values sorted by LoanId ascending (matches Phase 2's
// established in-memory ordering convention).
type LoanRepository struct {
	mu        sync.RWMutex
	loansById map[lending.LoanId]lending.LoanDto
}

// Compile-time assertion that *LoanRepository satisfies the lending
// LoanRepository driven port.
var _ lending.LoanRepository = (*LoanRepository)(nil)

// NewLoanRepository constructs an empty in-memory LoanRepository.
func NewLoanRepository() *LoanRepository {
	return &LoanRepository{
		loansById: map[lending.LoanId]lending.LoanDto{},
	}
}

// SaveLoan stages the write inside the supplied TransactionalContext. The
// adapter takes a defensive snapshot of the dto so callers cannot mutate
// the staged value before commit.
func (r *LoanRepository) SaveLoan(_ context.Context, loan lending.LoanDto, txc tx.TransactionalContext) error {
	snapshot := cloneLoanDto(loan)
	txc.Stage(func(_ context.Context) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.loansById[snapshot.LoanId] = snapshot
		return nil
	})
	return nil
}

// FindLoanById returns a defensive copy of the stored loan, or (nil, nil)
// on miss.
func (r *LoanRepository) FindLoanById(_ context.Context, loanId lending.LoanId) (*lending.LoanDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	loan, ok := r.loansById[loanId]
	if !ok {
		return nil, nil
	}
	copied := cloneLoanDto(loan)
	return &copied, nil
}

// ListLoans returns every stored loan in ascending LoanId order. The
// returned slice is freshly allocated; callers can mutate it without
// affecting the repository.
func (r *LoanRepository) ListLoans(_ context.Context) ([]lending.LoanDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotLoans(func(_ lending.LoanDto) bool { return true }), nil
}

// ListLoansForMember returns the loans whose MemberId matches.
func (r *LoanRepository) ListLoansForMember(_ context.Context, memberId membership.MemberId) ([]lending.LoanDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotLoans(func(l lending.LoanDto) bool { return l.MemberId == memberId }), nil
}

// ListLoansForBook returns the loans whose BookId matches.
func (r *LoanRepository) ListLoansForBook(_ context.Context, bookId catalog.BookId) ([]lending.LoanDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotLoans(func(l lending.LoanDto) bool { return l.BookId == bookId }), nil
}

// snapshotLoans returns the loans matching keep, sorted by LoanId
// ascending, defensively copied. Caller MUST hold r.mu (read or write).
func (r *LoanRepository) snapshotLoans(keep func(lending.LoanDto) bool) []lending.LoanDto {
	ids := make([]lending.LoanId, 0, len(r.loansById))
	for id, loan := range r.loansById {
		if keep(loan) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	loans := make([]lending.LoanDto, 0, len(ids))
	for _, id := range ids {
		loans = append(loans, cloneLoanDto(r.loansById[id]))
	}
	return loans
}

// cloneLoanDto returns a defensive copy of loan so internal state and
// returned values do not share the ReturnedAt pointer.
func cloneLoanDto(loan lending.LoanDto) lending.LoanDto {
	clone := loan
	if loan.ReturnedAt != nil {
		returnedAt := *loan.ReturnedAt
		clone.ReturnedAt = &returnedAt
	}
	return clone
}
