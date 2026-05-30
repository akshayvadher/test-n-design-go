package lending

import (
	"context"
	"sort"
	"sync"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// InMemoryReservationRepository is the in-memory ReservationRepository
// implementation. It is safe for concurrent use. Reservations are stored
// by ReservationId; List* methods return the values sorted by
// ReservationId ascending (matches Phase 2's established in-memory
// ordering convention).
type InMemoryReservationRepository struct {
	mu               sync.RWMutex
	reservationsById map[ReservationId]ReservationDto
}

// NewInMemoryReservationRepository constructs an empty
// InMemoryReservationRepository.
func NewInMemoryReservationRepository() *InMemoryReservationRepository {
	return &InMemoryReservationRepository{
		reservationsById: map[ReservationId]ReservationDto{},
	}
}

// SaveReservation stages the write inside the supplied
// TransactionalContext. The adapter takes a defensive snapshot of the dto
// so callers cannot mutate the staged value before commit.
func (r *InMemoryReservationRepository) SaveReservation(_ context.Context, reservation ReservationDto, txc tx.TransactionalContext) error {
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
func (r *InMemoryReservationRepository) FindReservationById(_ context.Context, reservationId ReservationId) (*ReservationDto, error) {
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
func (r *InMemoryReservationRepository) ListReservationsForBook(_ context.Context, bookId catalog.BookId) ([]ReservationDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotReservations(func(res ReservationDto) bool { return res.BookId == bookId }), nil
}

// ListReservationsForMember returns the reservations whose MemberId
// matches.
func (r *InMemoryReservationRepository) ListReservationsForMember(_ context.Context, memberId membership.MemberId) ([]ReservationDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotReservations(func(res ReservationDto) bool { return res.MemberId == memberId }), nil
}

// PendingReservationCountForBook returns the number of reservations whose
// BookId matches AND whose FulfilledAt is nil.
func (r *InMemoryReservationRepository) PendingReservationCountForBook(_ context.Context, bookId catalog.BookId) (int, error) {
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

// snapshotReservations returns the reservations matching keep, sorted by
// ReservationId ascending, defensively copied. Caller MUST hold r.mu.
func (r *InMemoryReservationRepository) snapshotReservations(keep func(ReservationDto) bool) []ReservationDto {
	ids := make([]ReservationId, 0, len(r.reservationsById))
	for id, reservation := range r.reservationsById {
		if keep(reservation) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	reservations := make([]ReservationDto, 0, len(ids))
	for _, id := range ids {
		reservations = append(reservations, cloneReservationDto(r.reservationsById[id]))
	}
	return reservations
}

// cloneReservationDto returns a defensive copy of reservation so internal
// state and returned values do not share the FulfilledAt pointer.
func cloneReservationDto(reservation ReservationDto) ReservationDto {
	clone := reservation
	if reservation.FulfilledAt != nil {
		fulfilledAt := *reservation.FulfilledAt
		clone.FulfilledAt = &fulfilledAt
	}
	return clone
}
