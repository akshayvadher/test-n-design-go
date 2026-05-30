// in_memory_reservation_repository_test.go covers the
// InMemoryReservationRepository directly: every method's happy path + the
// "not found returns nil" path, plus the tx-staging contract.
// PendingReservationCountForBook gets dedicated coverage because Phase 4
// will rely on it for the auto-loan saga.
package lending

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// -----------------------------------------------------------------------------
// SaveReservation + commit — stages the write inside the tx; commit applies
// it; abort discards.
// -----------------------------------------------------------------------------

func TestInMemoryReservationRepository_SaveReservation_CommitAppliesWrite(t *testing.T) {
	repo := NewInMemoryReservationRepository()
	txc := newTxContext()
	ctx := context.Background()

	reservation := sampleStoredReservation("r-1", "m-1", "b-1")
	if err := txc.Run(ctx, func(ctx context.Context) error {
		return repo.SaveReservation(ctx, reservation, txc)
	}); err != nil {
		t.Fatalf("Run: got error %v, want nil", err)
	}

	got, err := repo.FindReservationById(ctx, reservation.ReservationId)
	if err != nil {
		t.Fatalf("FindReservationById: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("FindReservationById: got nil, want reservation %+v", reservation)
	}
	if got.ReservationId != reservation.ReservationId || got.MemberId != reservation.MemberId || got.BookId != reservation.BookId {
		t.Errorf("FindReservationById: got %+v, want %+v", *got, reservation)
	}
}

func TestInMemoryReservationRepository_SaveReservation_RolledBackOnWorkError(t *testing.T) {
	repo := NewInMemoryReservationRepository()
	txc := newTxContext()
	ctx := context.Background()
	reservation := sampleStoredReservation("r-1", "m-1", "b-1")

	workErr := errors.New("boom")
	err := txc.Run(ctx, func(ctx context.Context) error {
		if err := repo.SaveReservation(ctx, reservation, txc); err != nil {
			return err
		}
		return workErr
	})
	if err == nil || !errors.Is(err, workErr) {
		t.Fatalf("Run: got %v, want error wrapping %v", err, workErr)
	}

	got, err := repo.FindReservationById(ctx, reservation.ReservationId)
	if err != nil {
		t.Fatalf("FindReservationById: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("FindReservationById: got %+v, want nil (rolled back)", *got)
	}
}

// -----------------------------------------------------------------------------
// FindReservationById — (nil, nil) on miss.
// -----------------------------------------------------------------------------

func TestInMemoryReservationRepository_FindReservationById_ReturnsNilOnMiss(t *testing.T) {
	repo := NewInMemoryReservationRepository()
	got, err := repo.FindReservationById(context.Background(), ReservationId("unknown"))
	if err != nil {
		t.Fatalf("FindReservationById: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("FindReservationById: got %+v, want nil (miss)", *got)
	}
}

// -----------------------------------------------------------------------------
// ListReservationsForBook / ListReservationsForMember — filter by the named
// field; ascending ReservationId order.
// -----------------------------------------------------------------------------

func TestInMemoryReservationRepository_ListReservationsForBook_FiltersByBook(t *testing.T) {
	repo := NewInMemoryReservationRepository()
	seedReservation(t, repo, sampleStoredReservation("r-1", "m-1", "b-1"))
	seedReservation(t, repo, sampleStoredReservation("r-2", "m-2", "b-1"))
	seedReservation(t, repo, sampleStoredReservation("r-3", "m-3", "b-2"))

	got, err := repo.ListReservationsForBook(context.Background(), catalog.BookId("b-1"))
	if err != nil {
		t.Fatalf("ListReservationsForBook: got error %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListReservationsForBook(b-1): got %d, want 2", len(got))
	}
	if got[0].ReservationId != "r-1" || got[1].ReservationId != "r-2" {
		t.Errorf("ListReservationsForBook(b-1) order: got [%q, %q], want [r-1, r-2]", got[0].ReservationId, got[1].ReservationId)
	}
}

func TestInMemoryReservationRepository_ListReservationsForMember_FiltersByMember(t *testing.T) {
	repo := NewInMemoryReservationRepository()
	seedReservation(t, repo, sampleStoredReservation("r-1", "m-1", "b-1"))
	seedReservation(t, repo, sampleStoredReservation("r-2", "m-1", "b-2"))
	seedReservation(t, repo, sampleStoredReservation("r-3", "m-2", "b-3"))

	got, err := repo.ListReservationsForMember(context.Background(), membership.MemberId("m-1"))
	if err != nil {
		t.Fatalf("ListReservationsForMember: got error %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListReservationsForMember(m-1): got %d, want 2", len(got))
	}
	for _, reservation := range got {
		if reservation.MemberId != membership.MemberId("m-1") {
			t.Errorf("ListReservationsForMember(m-1): got reservation with MemberId %q", reservation.MemberId)
		}
	}
}

// -----------------------------------------------------------------------------
// PendingReservationCountForBook — counts entries with FulfilledAt == nil
// AND BookId matching. Fulfilled reservations are excluded.
// -----------------------------------------------------------------------------

func TestInMemoryReservationRepository_PendingReservationCountForBook_CountsPendingOnly(t *testing.T) {
	repo := NewInMemoryReservationRepository()
	seedReservation(t, repo, sampleStoredReservation("r-1", "m-1", "b-1"))
	seedReservation(t, repo, sampleStoredReservation("r-2", "m-2", "b-1"))
	fulfilled := sampleStoredReservation("r-3", "m-3", "b-1")
	fulfilledAt := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	fulfilled.FulfilledAt = &fulfilledAt
	seedReservation(t, repo, fulfilled)
	seedReservation(t, repo, sampleStoredReservation("r-4", "m-4", "b-2"))

	count, err := repo.PendingReservationCountForBook(context.Background(), catalog.BookId("b-1"))
	if err != nil {
		t.Fatalf("PendingReservationCountForBook: got error %v, want nil", err)
	}
	if count != 2 {
		t.Errorf("PendingReservationCountForBook(b-1): got %d, want 2 (r-3 fulfilled; r-4 belongs to b-2)", count)
	}
}

func TestInMemoryReservationRepository_PendingReservationCountForBook_ZeroForUnknownBook(t *testing.T) {
	repo := NewInMemoryReservationRepository()
	seedReservation(t, repo, sampleStoredReservation("r-1", "m-1", "b-1"))

	count, err := repo.PendingReservationCountForBook(context.Background(), catalog.BookId("unknown"))
	if err != nil {
		t.Fatalf("PendingReservationCountForBook: got error %v, want nil", err)
	}
	if count != 0 {
		t.Errorf("PendingReservationCountForBook(unknown): got %d, want 0", count)
	}
}

// -----------------------------------------------------------------------------
// Defensive copies — mutating the FulfilledAt pointer of a returned dto must
// NOT alter stored state.
// -----------------------------------------------------------------------------

func TestInMemoryReservationRepository_FindReservationById_DefensiveFulfilledAtCopy(t *testing.T) {
	repo := NewInMemoryReservationRepository()
	fulfilledAt := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)
	stored := sampleStoredReservation("r-1", "m-1", "b-1")
	stored.FulfilledAt = &fulfilledAt
	seedReservation(t, repo, stored)

	first, err := repo.FindReservationById(context.Background(), ReservationId("r-1"))
	if err != nil {
		t.Fatalf("FindReservationById: got error %v, want nil", err)
	}
	if first == nil || first.FulfilledAt == nil {
		t.Fatalf("FindReservationById: missing FulfilledAt on first read")
	}
	*first.FulfilledAt = time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC)

	second, err := repo.FindReservationById(context.Background(), ReservationId("r-1"))
	if err != nil {
		t.Fatalf("FindReservationById (second): got error %v, want nil", err)
	}
	if second == nil || second.FulfilledAt == nil {
		t.Fatalf("FindReservationById (second): missing FulfilledAt")
	}
	if !second.FulfilledAt.Equal(fulfilledAt) {
		t.Errorf("FindReservationById (second).FulfilledAt: got %v, want %v (caller mutation leaked)", *second.FulfilledAt, fulfilledAt)
	}
}

// -----------------------------------------------------------------------------
// Helpers — same-file, stdlib only.
// -----------------------------------------------------------------------------

func sampleStoredReservation(reservationId ReservationId, memberId membership.MemberId, bookId catalog.BookId) ReservationDto {
	return ReservationDto{
		ReservationId: reservationId,
		MemberId:      memberId,
		BookId:        bookId,
		ReservedAt:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		FulfilledAt:   nil,
	}
}

func seedReservation(t *testing.T, repo *InMemoryReservationRepository, reservation ReservationDto) {
	t.Helper()
	txc := newTxContext()
	ctx := context.Background()
	if err := txc.Run(ctx, func(ctx context.Context) error {
		return repo.SaveReservation(ctx, reservation, txc)
	}); err != nil {
		t.Fatalf("seedReservation(%q): got error %v, want nil", reservation.ReservationId, err)
	}
}
