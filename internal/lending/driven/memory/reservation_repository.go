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

// ReservationRepository is the in-memory lending.ReservationRepository
// implementation. It is safe for concurrent use. Reservations are stored
// by ReservationId; List* methods return the values sorted by
// ReservationId ascending (matches Phase 2's established in-memory
// ordering convention).
type ReservationRepository struct {
	mu               sync.RWMutex
	reservationsById map[lending.ReservationId]lending.ReservationDto
}

// Compile-time assertion that *ReservationRepository satisfies the
// lending ReservationRepository driven port.
var _ lending.ReservationRepository = (*ReservationRepository)(nil)

// NewReservationRepository constructs an empty in-memory
// ReservationRepository.
func NewReservationRepository() *ReservationRepository {
	return &ReservationRepository{
		reservationsById: map[lending.ReservationId]lending.ReservationDto{},
	}
}

// SaveReservation stages the write inside the supplied
// TransactionalContext. The adapter takes a defensive snapshot of the dto
// so callers cannot mutate the staged value before commit.
func (r *ReservationRepository) SaveReservation(_ context.Context, reservation lending.ReservationDto, txc tx.TransactionalContext) error {
	snapshot := cloneReservationDto(reservation)
	txc.Stage(func(_ context.Context) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.reservationsById[snapshot.ReservationId] = snapshot
		return nil
	})
	return nil
}

// FindReservationById returns a defensive copy of the stored reservation,
// or (nil, nil) on miss.
func (r *ReservationRepository) FindReservationById(_ context.Context, reservationId lending.ReservationId) (*lending.ReservationDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reservation, ok := r.reservationsById[reservationId]
	if !ok {
		return nil, nil
	}
	copied := cloneReservationDto(reservation)
	return &copied, nil
}

// ListReservationsForBook returns the reservations whose BookId matches.
func (r *ReservationRepository) ListReservationsForBook(_ context.Context, bookId catalog.BookId) ([]lending.ReservationDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotReservations(func(res lending.ReservationDto) bool { return res.BookId == bookId }), nil
}

// ListReservationsForMember returns the reservations whose MemberId
// matches.
func (r *ReservationRepository) ListReservationsForMember(_ context.Context, memberId membership.MemberId) ([]lending.ReservationDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotReservations(func(res lending.ReservationDto) bool { return res.MemberId == memberId }), nil
}

// PendingReservationCountForBook returns the number of reservations whose
// BookId matches AND whose FulfilledAt is nil.
func (r *ReservationRepository) PendingReservationCountForBook(_ context.Context, bookId catalog.BookId) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, reservation := range r.reservationsById {
		if reservation.BookId == bookId && reservation.FulfilledAt == nil {
			count++
		}
	}
	return count, nil
}

// ListPendingReservationsForBook returns reservations for bookId whose
// FulfilledAt is nil, defensively copied and ordered by ReservedAt ASC
// (FIFO queue order). Ties on ReservedAt fall back to ReservationId ASC for
// determinism.
func (r *ReservationRepository) ListPendingReservationsForBook(_ context.Context, bookId catalog.BookId) ([]lending.ReservationDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pending := make([]lending.ReservationDto, 0)
	for _, reservation := range r.reservationsById {
		if reservation.BookId != bookId || reservation.FulfilledAt != nil {
			continue
		}
		pending = append(pending, cloneReservationDto(reservation))
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].ReservedAt.Equal(pending[j].ReservedAt) {
			return pending[i].ReservationId < pending[j].ReservationId
		}
		return pending[i].ReservedAt.Before(pending[j].ReservedAt)
	})
	return pending, nil
}

// snapshotReservations returns the reservations matching keep, sorted by
// ReservationId ascending, defensively copied. Caller MUST hold r.mu.
func (r *ReservationRepository) snapshotReservations(keep func(lending.ReservationDto) bool) []lending.ReservationDto {
	ids := make([]lending.ReservationId, 0, len(r.reservationsById))
	for id, reservation := range r.reservationsById {
		if keep(reservation) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	reservations := make([]lending.ReservationDto, 0, len(ids))
	for _, id := range ids {
		reservations = append(reservations, cloneReservationDto(r.reservationsById[id]))
	}
	return reservations
}

// cloneReservationDto returns a defensive copy of reservation so internal
// state and returned values do not share the FulfilledAt pointer.
func cloneReservationDto(reservation lending.ReservationDto) lending.ReservationDto {
	clone := reservation
	if reservation.FulfilledAt != nil {
		fulfilledAt := *reservation.FulfilledAt
		clone.FulfilledAt = &fulfilledAt
	}
	return clone
}
