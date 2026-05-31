// in_memory_repository_test.go covers the InMemoryFineRepository
// directly: (nil, nil) on every Find miss, round-trip via SaveFine +
// FindFineById / FindFineByLoanId, the upsert behaviour PayFine depends
// on, and ListFinesForMember filtering + ordering.
package fines

import (
	"context"
	"testing"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

func TestInMemoryFineRepository_FindFineById_ReturnsNilOnMiss(t *testing.T) {
	repo := NewInMemoryFineRepository()
	got, err := repo.FindFineById(context.Background(), FineId("missing"))
	if err != nil {
		t.Fatalf("FindFineById: %v", err)
	}
	if got != nil {
		t.Errorf("FindFineById: got %v, want nil", got)
	}
}

func TestInMemoryFineRepository_FindFineByLoanId_ReturnsNilOnMiss(t *testing.T) {
	repo := NewInMemoryFineRepository()
	got, err := repo.FindFineByLoanId(context.Background(), lending.LoanId("missing"))
	if err != nil {
		t.Fatalf("FindFineByLoanId: %v", err)
	}
	if got != nil {
		t.Errorf("FindFineByLoanId: got %v, want nil", got)
	}
}

func TestInMemoryFineRepository_SaveFine_RoundTripsById(t *testing.T) {
	repo := NewInMemoryFineRepository()
	ctx := context.Background()
	seed := SampleNewFine(WithFineId(FineId("f-1")), WithFineLoanId(lending.LoanId("loan-1")))

	if err := repo.SaveFine(ctx, seed); err != nil {
		t.Fatalf("SaveFine: %v", err)
	}
	got, err := repo.FindFineById(ctx, seed.FineId)
	if err != nil {
		t.Fatalf("FindFineById: %v", err)
	}
	if got == nil {
		t.Fatalf("FindFineById: got nil, want %+v", seed)
	}
	if got.FineId != seed.FineId || got.AmountCents != seed.AmountCents {
		t.Errorf("FindFineById: got %+v, want %+v", *got, seed)
	}
}

func TestInMemoryFineRepository_SaveFine_RoundTripsByLoanId(t *testing.T) {
	repo := NewInMemoryFineRepository()
	ctx := context.Background()
	seed := SampleNewFine(WithFineId(FineId("f-1")), WithFineLoanId(lending.LoanId("loan-1")))

	if err := repo.SaveFine(ctx, seed); err != nil {
		t.Fatalf("SaveFine: %v", err)
	}
	got, err := repo.FindFineByLoanId(ctx, seed.LoanId)
	if err != nil {
		t.Fatalf("FindFineByLoanId: %v", err)
	}
	if got == nil || got.FineId != seed.FineId {
		t.Errorf("FindFineByLoanId: got %+v, want fine with id %q", got, seed.FineId)
	}
}

func TestInMemoryFineRepository_SaveFine_UpsertsExisting(t *testing.T) {
	repo := NewInMemoryFineRepository()
	ctx := context.Background()
	first := SampleNewFine(WithFineId(FineId("f-1")))
	if err := repo.SaveFine(ctx, first); err != nil {
		t.Fatalf("SaveFine (first): %v", err)
	}
	paidAt := first.AssessedAt.Add(48 * time.Hour)
	updated := first
	updated.PaidAt = &paidAt
	if err := repo.SaveFine(ctx, updated); err != nil {
		t.Fatalf("SaveFine (upsert): %v", err)
	}
	got, err := repo.FindFineById(ctx, first.FineId)
	if err != nil {
		t.Fatalf("FindFineById: %v", err)
	}
	if got.PaidAt == nil || !got.PaidAt.Equal(paidAt) {
		t.Errorf("upsert PaidAt: got %v, want %v", got.PaidAt, paidAt)
	}
}

func TestInMemoryFineRepository_ListFinesForMember_FiltersAndSorts(t *testing.T) {
	repo := NewInMemoryFineRepository()
	ctx := context.Background()
	alice := membership.MemberId("alice")
	bob := membership.MemberId("bob")

	for _, fine := range []FineDto{
		SampleNewFine(WithFineId(FineId("f-3")), WithFineMemberId(alice)),
		SampleNewFine(WithFineId(FineId("f-1")), WithFineMemberId(alice)),
		SampleNewFine(WithFineId(FineId("f-2")), WithFineMemberId(bob)),
	} {
		if err := repo.SaveFine(ctx, fine); err != nil {
			t.Fatalf("SaveFine: %v", err)
		}
	}

	got, err := repo.ListFinesForMember(ctx, alice)
	if err != nil {
		t.Fatalf("ListFinesForMember: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("aliceFines length: got %d, want 2", len(got))
	}
	if got[0].FineId != FineId("f-1") || got[1].FineId != FineId("f-3") {
		t.Errorf("aliceFines order: got [%s, %s], want [f-1, f-3]", got[0].FineId, got[1].FineId)
	}
}
