package lending

import (
	"context"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// ReservationRepository is the persistence port for the reservation
// aggregate. The in-memory adapter ships in this slice; the bun-backed
// Postgres adapter lands in Slice 7.
//
// SaveReservation takes an explicit *tx.TransactionalContext so the write
// participates in the active transaction — staged via txc.Stage so the
// actual mutation happens at commit time.
//
// PendingReservationCountForBook counts entries where FulfilledAt == nil
// and BookId matches. Declared in Phase 3 even though Phase 3 itself does
// not call it; Phase 4's saga consumes it.
type ReservationRepository interface {
	SaveReservation(ctx context.Context, reservation ReservationDto, txc tx.TransactionalContext) error
	FindReservationById(ctx context.Context, reservationId ReservationId) (*ReservationDto, error)
	ListReservationsForBook(ctx context.Context, bookId catalog.BookId) ([]ReservationDto, error)
	ListReservationsForMember(ctx context.Context, memberId membership.MemberId) ([]ReservationDto, error)
	PendingReservationCountForBook(ctx context.Context, bookId catalog.BookId) (int, error)
}
