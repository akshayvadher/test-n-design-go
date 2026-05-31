package fines

import (
	"context"
	"sort"
	"sync"

	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// InMemoryFineRepository is the in-memory FineRepository implementation.
// It is safe for concurrent use. Fines are stored by FineId; ListFinesForMember
// returns the values sorted by FineId ascending (matches the established
// in-memory ordering convention across the project).
type InMemoryFineRepository struct {
	mu        sync.RWMutex
	finesById map[FineId]FineDto
}

// NewInMemoryFineRepository constructs an empty InMemoryFineRepository.
func NewInMemoryFineRepository() *InMemoryFineRepository {
	return &InMemoryFineRepository{
		finesById: map[FineId]FineDto{},
	}
}

// SaveFine upserts the fine keyed by FineId. The adapter takes a defensive
// snapshot of the dto so callers cannot mutate the stored value after the
// call returns. PayFine relies on the upsert semantics to overwrite the
// existing row with a non-nil PaidAt.
func (r *InMemoryFineRepository) SaveFine(_ context.Context, fine FineDto) error {
	snapshot := cloneFineDto(fine)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finesById[snapshot.FineId] = snapshot
	return nil
}

// FindFineById returns a defensive copy of the stored fine, or (nil, nil)
// on miss.
func (r *InMemoryFineRepository) FindFineById(_ context.Context, fineId FineId) (*FineDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fine, ok := r.finesById[fineId]
	if !ok {
		return nil, nil
	}
	copied := cloneFineDto(fine)
	return &copied, nil
}

// FindFineByLoanId scans the map for the first fine whose LoanId matches.
// Returns (nil, nil) on miss. The "at most one fine per loan" invariant is
// enforced by the facade's already-fined short-circuit, so a linear scan
// is safe for the in-memory size we exercise in tests.
func (r *InMemoryFineRepository) FindFineByLoanId(_ context.Context, loanId lending.LoanId) (*FineDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, fine := range r.finesById {
		if fine.LoanId == loanId {
			copied := cloneFineDto(fine)
			return &copied, nil
		}
	}
	return nil, nil
}

// ListFinesForMember returns a snapshot of the fines whose MemberId matches,
// sorted by FineId ascending. The returned slice is freshly allocated;
// callers can mutate it without affecting the repository.
func (r *InMemoryFineRepository) ListFinesForMember(_ context.Context, memberId membership.MemberId) ([]FineDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]FineId, 0, len(r.finesById))
	for id, fine := range r.finesById {
		if fine.MemberId == memberId {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	fines := make([]FineDto, 0, len(ids))
	for _, id := range ids {
		fines = append(fines, cloneFineDto(r.finesById[id]))
	}
	return fines, nil
}

// cloneFineDto returns a defensive copy of fine so internal state and
// returned values do not share the PaidAt pointer.
func cloneFineDto(fine FineDto) FineDto {
	clone := fine
	if fine.PaidAt != nil {
		paidAt := *fine.PaidAt
		clone.PaidAt = &paidAt
	}
	return clone
}
