package fines

import (
	"context"

	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// FineRepository is the port for fines persistence. Implementations live in
// in_memory_repository.go (tests + composition-root in-memory mode) and
// bun_repository.go (production Postgres).
//
// Per Open Question 2 the fines module does NOT integrate with
// TransactionalContext — its writes are single-aggregate per fine; no
// event-with-write atomicity is required. SaveFine therefore takes no tx
// parameter; the publish-after-save sequence happens at the facade layer.
//
// Find* methods return (nil, nil) on miss, matching every other module's
// port in the project.
type FineRepository interface {
	SaveFine(ctx context.Context, fine FineDto) error
	FindFineById(ctx context.Context, fineId FineId) (*FineDto, error)
	FindFineByLoanId(ctx context.Context, loanId lending.LoanId) (*FineDto, error)
	ListFinesForMember(ctx context.Context, memberId membership.MemberId) ([]FineDto, error)
}
