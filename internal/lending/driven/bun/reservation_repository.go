package bun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	upstreambun "github.com/uptrace/bun"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// ReservationRow is the bun-mapped persistent shape of a reservation.
// Column names match migrations/0003_lending.sql verbatim. FulfilledAt is
// a pointer for the "not yet fulfilled" sentinel (matches ReservationDto).
type ReservationRow struct {
	upstreambun.BaseModel `bun:"table:reservations"`

	ReservationId lending.ReservationId `bun:"reservation_id,pk"`
	MemberId      membership.MemberId   `bun:"member_id,notnull"`
	BookId        catalog.BookId        `bun:"book_id,notnull"`
	ReservedAt    time.Time             `bun:"reserved_at,notnull"`
	FulfilledAt   *time.Time            `bun:"fulfilled_at"`
}

// ReservationRepository is the Postgres-backed
// lending.ReservationRepository implementation. SaveReservation stages the
// write inside the supplied TransactionalContext so the INSERT runs
// against the live tx handle resolved via tx.TxFromContext. Reads bypass
// the tx substrate.
type ReservationRepository struct {
	db *upstreambun.DB
}

// Compile-time assertion that *ReservationRepository satisfies the
// lending ReservationRepository driven port.
var _ lending.ReservationRepository = (*ReservationRepository)(nil)

// NewReservationRepository constructs a *ReservationRepository bound to
// db. The caller owns the *bun.DB lifecycle.
func NewReservationRepository(db *upstreambun.DB) *ReservationRepository {
	return &ReservationRepository{db: db}
}

// SaveReservation stages an upsert keyed by reservation_id. The stage
// closure runs immediately inside the bun tx callback.
func (r *ReservationRepository) SaveReservation(_ context.Context, reservation lending.ReservationDto, txc tx.TransactionalContext) error {
	row := toReservationRow(reservation)
	txc.Stage(func(ctx context.Context) error {
		handle := resolveBunHandle(ctx, r.db)
		_, err := handle.NewInsert().
			Model(&row).
			On("CONFLICT (reservation_id) DO UPDATE").
			Set("member_id = EXCLUDED.member_id").
			Set("book_id = EXCLUDED.book_id").
			Set("reserved_at = EXCLUDED.reserved_at").
			Set("fulfilled_at = EXCLUDED.fulfilled_at").
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("save reservation %q: %w", reservation.ReservationId, err)
		}
		return nil
	})
	return nil
}

// FindReservationById returns the reservation by primary key, or
// (nil, nil) on miss.
func (r *ReservationRepository) FindReservationById(ctx context.Context, reservationId lending.ReservationId) (*lending.ReservationDto, error) {
	var row ReservationRow
	err := r.db.NewSelect().Model(&row).Where("reservation_id = ?", reservationId).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find reservation by id %q: %w", reservationId, err)
	}
	reservation := toReservationDto(row)
	return &reservation, nil
}

// ListReservationsForBook returns the reservations whose book_id matches,
// ordered by reservation_id ASC for deterministic output.
func (r *ReservationRepository) ListReservationsForBook(ctx context.Context, bookId catalog.BookId) ([]lending.ReservationDto, error) {
	var rows []ReservationRow
	err := r.db.NewSelect().
		Model(&rows).
		Where("book_id = ?", bookId).
		OrderExpr("reservation_id ASC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list reservations for book %q: %w", bookId, err)
	}
	return toReservationDtos(rows), nil
}

// ListReservationsForMember returns the reservations whose member_id
// matches, ordered by reservation_id ASC.
func (r *ReservationRepository) ListReservationsForMember(ctx context.Context, memberId membership.MemberId) ([]lending.ReservationDto, error) {
	var rows []ReservationRow
	err := r.db.NewSelect().
		Model(&rows).
		Where("member_id = ?", memberId).
		OrderExpr("reservation_id ASC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list reservations for member %q: %w", memberId, err)
	}
	return toReservationDtos(rows), nil
}

// PendingReservationCountForBook counts reservations for bookId whose
// FulfilledAt IS NULL — declared in Phase 3 for the contract, consumed in
// Phase 4 by the auto-loan saga.
func (r *ReservationRepository) PendingReservationCountForBook(ctx context.Context, bookId catalog.BookId) (int, error) {
	count, err := r.db.NewSelect().
		Model((*ReservationRow)(nil)).
		Where("book_id = ? AND fulfilled_at IS NULL", bookId).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("count pending reservations for book %q: %w", bookId, err)
	}
	return count, nil
}

// ListPendingReservationsForBook returns reservations for bookId whose
// fulfilled_at IS NULL, ordered by reserved_at ASC (FIFO queue order).
// Reads bypass the tx substrate per the Phase 3 convention.
func (r *ReservationRepository) ListPendingReservationsForBook(ctx context.Context, bookId catalog.BookId) ([]lending.ReservationDto, error) {
	var rows []ReservationRow
	err := r.db.NewSelect().
		Model(&rows).
		Where("book_id = ? AND fulfilled_at IS NULL", bookId).
		OrderExpr("reserved_at ASC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending reservations for book %q: %w", bookId, err)
	}
	return toReservationDtos(rows), nil
}

// toReservationRow converts a domain ReservationDto into the bun row.
func toReservationRow(reservation lending.ReservationDto) ReservationRow {
	return ReservationRow{
		ReservationId: reservation.ReservationId,
		MemberId:      reservation.MemberId,
		BookId:        reservation.BookId,
		ReservedAt:    reservation.ReservedAt,
		FulfilledAt:   reservation.FulfilledAt,
	}
}

// toReservationDto converts a bun row back into a domain ReservationDto.
func toReservationDto(row ReservationRow) lending.ReservationDto {
	return lending.ReservationDto{
		ReservationId: row.ReservationId,
		MemberId:      row.MemberId,
		BookId:        row.BookId,
		ReservedAt:    row.ReservedAt,
		FulfilledAt:   row.FulfilledAt,
	}
}

// toReservationDtos converts a slice of bun rows into a fresh slice of
// domain ReservationDtos, preserving order.
func toReservationDtos(rows []ReservationRow) []lending.ReservationDto {
	reservations := make([]lending.ReservationDto, 0, len(rows))
	for _, row := range rows {
		reservations = append(reservations, toReservationDto(row))
	}
	return reservations
}
