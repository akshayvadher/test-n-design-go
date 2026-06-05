package bun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	upstreambun "github.com/uptrace/bun"

	"github.com/akshayvadher/test-n-design-go/internal/fines"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// isInvalidUUIDSyntax detects the Postgres "invalid input syntax for type
// uuid" error so caller-supplied garbage IDs collapse to (nil, nil) — same
// shape as a real not-found. Without this, GET /fines/does-not-exist 500s
// instead of the spec-mandated 404.
func isInvalidUUIDSyntax(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE=22P02") || strings.Contains(msg, "invalid input syntax for type uuid")
}

// FineRow is the bun-mapped persistent shape of a fine. JSON tags are
// intentionally absent — this struct never crosses the HTTP boundary;
// the HTTP DTOs in internal/fines/driving/http own that.
//
// Column names match migrations/0004_fines.sql verbatim. PaidAt is a
// pointer for the "unpaid" sentinel (matches FineDto).
type FineRow struct {
	upstreambun.BaseModel `bun:"table:fines"`

	FineId      fines.FineId        `bun:"fine_id,pk"`
	MemberId    membership.MemberId `bun:"member_id,notnull"`
	LoanId      lending.LoanId      `bun:"loan_id,notnull"`
	AmountCents fines.AmountCents   `bun:"amount_cents,notnull"`
	AssessedAt  time.Time           `bun:"assessed_at,notnull"`
	PaidAt      *time.Time          `bun:"paid_at"`
}

// Repository is the Postgres-backed fines.FineRepository implementation.
// Writes run directly against the base *bun.DB — fines does NOT integrate
// with TransactionalContext (Open Question 2: single-aggregate per fine,
// no event-with-write atomicity required).
type Repository struct {
	db *upstreambun.DB
}

// Compile-time assertion that *Repository satisfies the fines driven port.
var _ fines.FineRepository = (*Repository)(nil)

// NewRepository constructs a *Repository bound to db. The caller owns the
// *bun.DB lifecycle.
func NewRepository(db *upstreambun.DB) *Repository {
	return &Repository{db: db}
}

// SaveFine upserts the fine by fine_id. The upsert lets PayFine overwrite
// PaidAt without re-routing through a separate UPDATE statement.
func (r *Repository) SaveFine(ctx context.Context, fine fines.FineDto) error {
	row := toFineRow(fine)
	_, err := r.db.NewInsert().
		Model(&row).
		On("CONFLICT (fine_id) DO UPDATE").
		Set("member_id = EXCLUDED.member_id").
		Set("loan_id = EXCLUDED.loan_id").
		Set("amount_cents = EXCLUDED.amount_cents").
		Set("assessed_at = EXCLUDED.assessed_at").
		Set("paid_at = EXCLUDED.paid_at").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("save fine %q: %w", fine.FineId, err)
	}
	return nil
}

// FindFineById returns the fine by primary key, or (nil, nil) on miss.
func (r *Repository) FindFineById(ctx context.Context, fineId fines.FineId) (*fines.FineDto, error) {
	var row FineRow
	err := r.db.NewSelect().Model(&row).Where("fine_id = ?", fineId).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) || isInvalidUUIDSyntax(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find fine by id %q: %w", fineId, err)
	}
	fine := toFineDto(row)
	return &fine, nil
}

// FindFineByLoanId returns the first fine for the given loan, or
// (nil, nil) on miss. The "at most one fine per loan" invariant is
// enforced at the facade layer; this query LIMITs to 1 defensively.
func (r *Repository) FindFineByLoanId(ctx context.Context, loanId lending.LoanId) (*fines.FineDto, error) {
	var row FineRow
	err := r.db.NewSelect().Model(&row).Where("loan_id = ?", loanId).Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find fine by loan id %q: %w", loanId, err)
	}
	fine := toFineDto(row)
	return &fine, nil
}

// ListFinesForMember returns the fines whose member_id matches, ordered
// by fine_id ASC for in-memory/bun parity.
func (r *Repository) ListFinesForMember(ctx context.Context, memberId membership.MemberId) ([]fines.FineDto, error) {
	var rows []FineRow
	err := r.db.NewSelect().
		Model(&rows).
		Where("member_id = ?", memberId).
		OrderExpr("fine_id ASC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list fines for member %q: %w", memberId, err)
	}
	out := make([]fines.FineDto, 0, len(rows))
	for _, row := range rows {
		out = append(out, toFineDto(row))
	}
	return out, nil
}

// toFineRow projects a domain FineDto into the bun row. The PaidAt pointer
// propagates as-is; nil maps to NULL in Postgres via bun's nullable column
// handling.
func toFineRow(fine fines.FineDto) FineRow {
	return FineRow{
		FineId:      fine.FineId,
		MemberId:    fine.MemberId,
		LoanId:      fine.LoanId,
		AmountCents: fine.AmountCents,
		AssessedAt:  fine.AssessedAt,
		PaidAt:      fine.PaidAt,
	}
}

// toFineDto projects a bun row back into a domain FineDto.
func toFineDto(row FineRow) fines.FineDto {
	return fines.FineDto{
		FineId:      row.FineId,
		MemberId:    row.MemberId,
		LoanId:      row.LoanId,
		AmountCents: row.AmountCents,
		AssessedAt:  row.AssessedAt,
		PaidAt:      row.PaidAt,
	}
}
