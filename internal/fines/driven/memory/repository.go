package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/akshayvadher/test-n-design-go/internal/fines"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// Repository is the in-memory fines.FineRepository implementation. It is
// safe for concurrent use. Fines are stored by FineId; ListFinesForMember
// returns the values sorted by FineId ascending (matches the established
// in-memory ordering convention across the project).
type Repository struct {
	mu        sync.RWMutex
	finesById map[fines.FineId]fines.FineDto
}

// Compile-time assertion that *Repository satisfies the fines driven port.
var _ fines.FineRepository = (*Repository)(nil)

// NewRepository constructs an empty in-memory Repository.
func NewRepository() *Repository {
	return &Repository{
		finesById: map[fines.FineId]fines.FineDto{},
	}
}

// SaveFine upserts the fine keyed by FineId. The adapter takes a defensive
// snapshot of the dto so callers cannot mutate the stored value after the
// call returns. PayFine relies on the upsert semantics to overwrite the
// existing row with a non-nil PaidAt.
func (r *Repository) SaveFine(_ context.Context, fine fines.FineDto) error {
	snapshot := cloneFineDto(fine)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finesById[snapshot.FineId] = snapshot
	return nil
}

// FindFineById returns a defensive copy of the stored fine, or (nil, nil)
// on miss.
func (r *Repository) FindFineById(_ context.Context, fineId fines.FineId) (*fines.FineDto, error) {
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
func (r *Repository) FindFineByLoanId(_ context.Context, loanId lending.LoanId) (*fines.FineDto, error) {
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
func (r *Repository) ListFinesForMember(_ context.Context, memberId membership.MemberId) ([]fines.FineDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]fines.FineId, 0, len(r.finesById))
	for id, fine := range r.finesById {
		if fine.MemberId == memberId {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]fines.FineDto, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneFineDto(r.finesById[id]))
	}
	return out, nil
}

// cloneFineDto returns a defensive copy of fine so internal state and
// returned values do not share the PaidAt pointer.
func cloneFineDto(fine fines.FineDto) fines.FineDto {
	clone := fine
	if fine.PaidAt != nil {
		paidAt := *fine.PaidAt
		clone.PaidAt = &paidAt
	}
	return clone
}
