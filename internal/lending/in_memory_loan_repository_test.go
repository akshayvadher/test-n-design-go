// in_memory_loan_repository_test.go covers the InMemoryLoanRepository
// directly: every method's happy path + the "not found returns nil" path,
// plus the tx-staging contract (SaveLoan stages; commit applies; abort
// discards). Uses a real InMemoryTransactionalContext from internal/shared/tx
// — no mocks. Stdlib testing only; same-package so the test can construct
// LoanDto values directly.
package lending

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// -----------------------------------------------------------------------------
// SaveLoan + commit — stages the write inside the tx; commit applies it; the
// row is then visible to FindLoanById and List* methods.
// -----------------------------------------------------------------------------

func TestInMemoryLoanRepository_SaveLoan_CommitAppliesWrite(t *testing.T) {
	repo := NewInMemoryLoanRepository()
	txc := newTxContext()
	ctx := context.Background()

	loan := sampleStoredLoan("l-1", "m-1", "c-1", "b-1")
	if err := txc.Run(ctx, func(ctx context.Context) error {
		return repo.SaveLoan(ctx, loan, txc)
	}); err != nil {
		t.Fatalf("Run: got error %v, want nil", err)
	}

	got, err := repo.FindLoanById(ctx, loan.LoanId)
	if err != nil {
		t.Fatalf("FindLoanById: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("FindLoanById: got nil, want loan %+v after commit", loan)
	}
	if got.LoanId != loan.LoanId || got.MemberId != loan.MemberId || got.CopyId != loan.CopyId {
		t.Errorf("FindLoanById: got %+v, want %+v", *got, loan)
	}
}

func TestInMemoryLoanRepository_SaveLoan_RolledBackOnWorkError(t *testing.T) {
	repo := NewInMemoryLoanRepository()
	txc := newTxContext()
	ctx := context.Background()
	loan := sampleStoredLoan("l-1", "m-1", "c-1", "b-1")

	workErr := errors.New("boom")
	err := txc.Run(ctx, func(ctx context.Context) error {
		if err := repo.SaveLoan(ctx, loan, txc); err != nil {
			return err
		}
		return workErr
	})
	if err == nil || !errors.Is(err, workErr) {
		t.Fatalf("Run: got %v, want error wrapping %v", err, workErr)
	}

	got, err := repo.FindLoanById(ctx, loan.LoanId)
	if err != nil {
		t.Fatalf("FindLoanById: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("FindLoanById: got %+v, want nil (rolled back)", *got)
	}
}

// -----------------------------------------------------------------------------
// FindLoanById — (nil, nil) on miss.
// -----------------------------------------------------------------------------

func TestInMemoryLoanRepository_FindLoanById_ReturnsNilOnMiss(t *testing.T) {
	repo := NewInMemoryLoanRepository()
	got, err := repo.FindLoanById(context.Background(), LoanId("unknown"))
	if err != nil {
		t.Fatalf("FindLoanById: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("FindLoanById: got %+v, want nil (miss)", *got)
	}
}

// -----------------------------------------------------------------------------
// ListLoans — ascending LoanId order, defensive slice copy on read.
// -----------------------------------------------------------------------------

func TestInMemoryLoanRepository_ListLoans_ReturnsAllSortedById(t *testing.T) {
	repo := NewInMemoryLoanRepository()
	ctx := context.Background()
	seedLoan(t, repo, sampleStoredLoan("l-3", "m-1", "c-3", "b-1"))
	seedLoan(t, repo, sampleStoredLoan("l-1", "m-1", "c-1", "b-1"))
	seedLoan(t, repo, sampleStoredLoan("l-2", "m-2", "c-2", "b-2"))

	got, err := repo.ListLoans(ctx)
	if err != nil {
		t.Fatalf("ListLoans: got error %v, want nil", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListLoans: got %d loans, want 3", len(got))
	}
	wantOrder := []LoanId{"l-1", "l-2", "l-3"}
	for index, want := range wantOrder {
		if got[index].LoanId != want {
			t.Errorf("ListLoans[%d].LoanId: got %q, want %q", index, got[index].LoanId, want)
		}
	}
}

func TestInMemoryLoanRepository_ListLoans_EmptyRepoReturnsEmptySlice(t *testing.T) {
	repo := NewInMemoryLoanRepository()
	got, err := repo.ListLoans(context.Background())
	if err != nil {
		t.Fatalf("ListLoans: got error %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("ListLoans: got %d loans, want 0", len(got))
	}
}

// -----------------------------------------------------------------------------
// ListLoansForMember / ListLoansForBook — filter by the named field; loans
// from other members/books are not included.
// -----------------------------------------------------------------------------

func TestInMemoryLoanRepository_ListLoansForMember_FiltersByMember(t *testing.T) {
	repo := NewInMemoryLoanRepository()
	seedLoan(t, repo, sampleStoredLoan("l-1", "m-1", "c-1", "b-1"))
	seedLoan(t, repo, sampleStoredLoan("l-2", "m-1", "c-2", "b-2"))
	seedLoan(t, repo, sampleStoredLoan("l-3", "m-2", "c-3", "b-1"))

	got, err := repo.ListLoansForMember(context.Background(), membership.MemberId("m-1"))
	if err != nil {
		t.Fatalf("ListLoansForMember: got error %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListLoansForMember(m-1): got %d, want 2", len(got))
	}
	for _, loan := range got {
		if loan.MemberId != membership.MemberId("m-1") {
			t.Errorf("ListLoansForMember(m-1): got loan with MemberId %q", loan.MemberId)
		}
	}
}

func TestInMemoryLoanRepository_ListLoansForBook_FiltersByBook(t *testing.T) {
	repo := NewInMemoryLoanRepository()
	seedLoan(t, repo, sampleStoredLoan("l-1", "m-1", "c-1", "b-1"))
	seedLoan(t, repo, sampleStoredLoan("l-2", "m-2", "c-2", "b-2"))
	seedLoan(t, repo, sampleStoredLoan("l-3", "m-3", "c-3", "b-1"))

	got, err := repo.ListLoansForBook(context.Background(), catalog.BookId("b-1"))
	if err != nil {
		t.Fatalf("ListLoansForBook: got error %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListLoansForBook(b-1): got %d, want 2", len(got))
	}
	for _, loan := range got {
		if loan.BookId != catalog.BookId("b-1") {
			t.Errorf("ListLoansForBook(b-1): got loan with BookId %q", loan.BookId)
		}
	}
}

// -----------------------------------------------------------------------------
// Defensive copies — mutating the ReturnedAt pointer of a returned dto must
// NOT alter stored state.
// -----------------------------------------------------------------------------

func TestInMemoryLoanRepository_FindLoanById_DefensiveReturnedAtCopy(t *testing.T) {
	repo := NewInMemoryLoanRepository()
	returnedAt := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	stored := sampleStoredLoan("l-1", "m-1", "c-1", "b-1")
	stored.ReturnedAt = &returnedAt
	seedLoan(t, repo, stored)

	first, err := repo.FindLoanById(context.Background(), LoanId("l-1"))
	if err != nil {
		t.Fatalf("FindLoanById: got error %v, want nil", err)
	}
	if first == nil || first.ReturnedAt == nil {
		t.Fatalf("FindLoanById: missing ReturnedAt on first read")
	}
	// Mutate the returned pointer's value — must not leak.
	*first.ReturnedAt = time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC)

	second, err := repo.FindLoanById(context.Background(), LoanId("l-1"))
	if err != nil {
		t.Fatalf("FindLoanById (second): got error %v, want nil", err)
	}
	if second == nil || second.ReturnedAt == nil {
		t.Fatalf("FindLoanById (second): missing ReturnedAt")
	}
	if !second.ReturnedAt.Equal(returnedAt) {
		t.Errorf("FindLoanById (second).ReturnedAt: got %v, want %v (caller mutation leaked)", *second.ReturnedAt, returnedAt)
	}
}

// -----------------------------------------------------------------------------
// Helpers — tiny, same-file, stdlib only. No mocks.
// -----------------------------------------------------------------------------

func sampleStoredLoan(loanId LoanId, memberId membership.MemberId, copyId catalog.CopyId, bookId catalog.BookId) LoanDto {
	return LoanDto{
		LoanId:     loanId,
		MemberId:   memberId,
		CopyId:     copyId,
		BookId:     bookId,
		BorrowedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		DueDate:    time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		ReturnedAt: nil,
	}
}

func seedLoan(t *testing.T, repo *InMemoryLoanRepository, loan LoanDto) {
	t.Helper()
	txc := newTxContext()
	ctx := context.Background()
	if err := txc.Run(ctx, func(ctx context.Context) error {
		return repo.SaveLoan(ctx, loan, txc)
	}); err != nil {
		t.Fatalf("seedLoan(%q): got error %v, want nil", loan.LoanId, err)
	}
}

func newTxContext() tx.TransactionalContext {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := events.NewInMemoryEventBus(logger)
	return tx.NewInMemoryTransactionalContext(bus, logger)
}
