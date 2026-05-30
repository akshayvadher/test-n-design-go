package lending

import (
	"context"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// LoanRepository is the persistence port for the loan aggregate. The
// in-memory adapter ships in this slice; the bun-backed Postgres adapter
// lands in Slice 7 alongside the migration.
//
// SaveLoan takes an explicit *tx.TransactionalContext so the write
// participates in the active transaction — the adapter calls
// txc.Stage(...) so the actual mutation happens at commit time. Reads
// (Find*, List*) bypass the tx substrate: they are not tx-scoped because
// Phase 3 only requires read-your-writes inside the facade, and the
// facade's reads happen BEFORE the tx opens.
//
// Find* methods return (nil, nil) on "no rows" — the facade is responsible
// for translating that into LoanNotFoundError. A non-nil error indicates
// infrastructure failure and is propagated unchanged.
type LoanRepository interface {
	SaveLoan(ctx context.Context, loan LoanDto, txc tx.TransactionalContext) error
	FindLoanById(ctx context.Context, loanId LoanId) (*LoanDto, error)
	ListLoansForMember(ctx context.Context, memberId membership.MemberId) ([]LoanDto, error)
	ListLoansForBook(ctx context.Context, bookId catalog.BookId) ([]LoanDto, error)
	ListLoans(ctx context.Context) ([]LoanDto, error)
}
