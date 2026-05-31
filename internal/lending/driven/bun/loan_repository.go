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
	txbun "github.com/akshayvadher/test-n-design-go/internal/shared/tx/bun"
)

// LoanRow is the bun-mapped persistent shape of a loan. JSON tags are
// intentionally absent — this struct never crosses the HTTP boundary;
// the HTTP DTOs in internal/lending/driving/http own that.
//
// Column names match migrations/0003_lending.sql verbatim. ReturnedAt is a
// pointer for the "not yet returned" sentinel (matches LoanDto).
type LoanRow struct {
	upstreambun.BaseModel `bun:"table:loans"`

	LoanId     lending.LoanId      `bun:"loan_id,pk"`
	MemberId   membership.MemberId `bun:"member_id,notnull"`
	CopyId     catalog.CopyId      `bun:"copy_id,notnull"`
	BookId     catalog.BookId      `bun:"book_id,notnull"`
	BorrowedAt time.Time           `bun:"borrowed_at,notnull"`
	DueDate    time.Time           `bun:"due_date,notnull"`
	ReturnedAt *time.Time          `bun:"returned_at"`
}

// LoanRepository is the Postgres-backed lending.LoanRepository
// implementation. SaveLoan stages the write inside the supplied
// TransactionalContext so the INSERT runs against the live tx handle
// resolved via txbun.TxFromContext. Reads (FindLoanById, List*) bypass the
// tx substrate and go directly through the base *bun.DB.
type LoanRepository struct {
	db *upstreambun.DB
}

// Compile-time assertion that *LoanRepository satisfies the lending
// LoanRepository driven port.
var _ lending.LoanRepository = (*LoanRepository)(nil)

// NewLoanRepository constructs a *LoanRepository bound to db. The caller
// owns the *bun.DB lifecycle (open + close); LoanRepository does not
// close it.
func NewLoanRepository(db *upstreambun.DB) *LoanRepository {
	return &LoanRepository{db: db}
}

// SaveLoan stages an upsert keyed by loan_id. The stage closure runs
// immediately inside the bun tx callback (per BunTransactionalContext's
// Stage contract); the upsert matches catalog.SaveBook's
// `onConflictDoUpdate` shape so re-saving a loan (e.g. flipping ReturnedAt
// from nil to a timestamp during ReturnLoan) overwrites in place.
func (r *LoanRepository) SaveLoan(_ context.Context, loan lending.LoanDto, txc tx.TransactionalContext) error {
	row := toLoanRow(loan)
	txc.Stage(func(ctx context.Context) error {
		handle := resolveBunHandle(ctx, r.db)
		_, err := handle.NewInsert().
			Model(&row).
			On("CONFLICT (loan_id) DO UPDATE").
			Set("member_id = EXCLUDED.member_id").
			Set("copy_id = EXCLUDED.copy_id").
			Set("book_id = EXCLUDED.book_id").
			Set("borrowed_at = EXCLUDED.borrowed_at").
			Set("due_date = EXCLUDED.due_date").
			Set("returned_at = EXCLUDED.returned_at").
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("save loan %q: %w", loan.LoanId, err)
		}
		return nil
	})
	return nil
}

// FindLoanById returns the loan by primary key, or (nil, nil) on miss.
func (r *LoanRepository) FindLoanById(ctx context.Context, loanId lending.LoanId) (*lending.LoanDto, error) {
	var row LoanRow
	err := r.db.NewSelect().Model(&row).Where("loan_id = ?", loanId).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find loan by id %q: %w", loanId, err)
	}
	loan := toLoanDto(row)
	return &loan, nil
}

// ListLoans returns every loan ordered by loan_id ASC.
func (r *LoanRepository) ListLoans(ctx context.Context) ([]lending.LoanDto, error) {
	var rows []LoanRow
	err := r.db.NewSelect().Model(&rows).OrderExpr("loan_id ASC").Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list loans: %w", err)
	}
	return toLoanDtos(rows), nil
}

// ListLoansForMember returns the loans whose member_id matches, ordered by
// loan_id ASC for deterministic output.
func (r *LoanRepository) ListLoansForMember(ctx context.Context, memberId membership.MemberId) ([]lending.LoanDto, error) {
	var rows []LoanRow
	err := r.db.NewSelect().
		Model(&rows).
		Where("member_id = ?", memberId).
		OrderExpr("loan_id ASC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list loans for member %q: %w", memberId, err)
	}
	return toLoanDtos(rows), nil
}

// ListLoansForBook returns the loans whose book_id matches, ordered by
// loan_id ASC for deterministic output.
func (r *LoanRepository) ListLoansForBook(ctx context.Context, bookId catalog.BookId) ([]lending.LoanDto, error) {
	var rows []LoanRow
	err := r.db.NewSelect().
		Model(&rows).
		Where("book_id = ?", bookId).
		OrderExpr("loan_id ASC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list loans for book %q: %w", bookId, err)
	}
	return toLoanDtos(rows), nil
}

// resolveBunHandle returns the active bun handle from ctx (the live tx when
// inside a TransactionalContext.Run) or the base *bun.DB as fallback. The
// repo MUST honour the live tx so staged writes participate in the rollback
// when the work function or another stage closure fails.
func resolveBunHandle(ctx context.Context, db *upstreambun.DB) upstreambun.IDB {
	if handle, ok := txbun.TxFromContext(ctx); ok && handle != nil {
		return handle
	}
	return db
}

// toLoanRow converts a domain LoanDto into the bun row. The ReturnedAt
// pointer is propagated as-is; nil maps to NULL in Postgres via bun's
// nullable column handling.
func toLoanRow(loan lending.LoanDto) LoanRow {
	return LoanRow{
		LoanId:     loan.LoanId,
		MemberId:   loan.MemberId,
		CopyId:     loan.CopyId,
		BookId:     loan.BookId,
		BorrowedAt: loan.BorrowedAt,
		DueDate:    loan.DueDate,
		ReturnedAt: loan.ReturnedAt,
	}
}

// toLoanDto converts a bun row back into a domain LoanDto.
func toLoanDto(row LoanRow) lending.LoanDto {
	return lending.LoanDto{
		LoanId:     row.LoanId,
		MemberId:   row.MemberId,
		CopyId:     row.CopyId,
		BookId:     row.BookId,
		BorrowedAt: row.BorrowedAt,
		DueDate:    row.DueDate,
		ReturnedAt: row.ReturnedAt,
	}
}

// toLoanDtos converts a slice of bun rows into a fresh slice of domain
// LoanDtos, preserving order.
func toLoanDtos(rows []LoanRow) []lending.LoanDto {
	loans := make([]lending.LoanDto, 0, len(rows))
	for _, row := range rows {
		loans = append(loans, toLoanDto(row))
	}
	return loans
}
