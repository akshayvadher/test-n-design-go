// in_memory_repository_test.go covers the InMemoryRepository directly:
// (nil, nil) on every Find miss, round-trip via SaveMember +
// FindMemberById / FindMemberByEmail, and the upsert behaviour
// FindMember-based facade flows depend on.
//
// Stdlib testing only; same-package so the test can construct MemberDto
// values directly without going through the facade.
package membership

import (
	"context"
	"testing"
)

func TestInMemoryRepository_FindMemberById_ReturnsNilOnMiss(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	got, err := repo.FindMemberById(ctx, MemberId("unknown"))
	if err != nil {
		t.Fatalf("FindMemberById: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("FindMemberById: got %v, want nil (miss)", got)
	}
}

func TestInMemoryRepository_FindMemberByEmail_ReturnsNilOnMiss(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	got, err := repo.FindMemberByEmail(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("FindMemberByEmail: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("FindMemberByEmail: got %v, want nil (miss)", got)
	}
}

func TestInMemoryRepository_SaveMember_RoundTripsViaFindMemberById(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()
	seed := MemberDto{
		MemberId: MemberId("m-1"),
		Name:     "Ada Lovelace",
		Email:    "ada@example.com",
		Tier:     MembershipTierStandard,
		Status:   MembershipStatusActive,
	}

	if err := repo.SaveMember(ctx, seed); err != nil {
		t.Fatalf("SaveMember: got error %v, want nil", err)
	}

	got, err := repo.FindMemberById(ctx, seed.MemberId)
	if err != nil {
		t.Fatalf("FindMemberById: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("FindMemberById: got nil, want %+v", seed)
	}
	if *got != seed {
		t.Errorf("FindMemberById: got %+v, want %+v", *got, seed)
	}
}

func TestInMemoryRepository_SaveMember_RoundTripsViaFindMemberByEmail(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()
	seed := MemberDto{
		MemberId: MemberId("m-1"),
		Name:     "Ada Lovelace",
		Email:    "ada@example.com",
		Tier:     MembershipTierStandard,
		Status:   MembershipStatusActive,
	}
	if err := repo.SaveMember(ctx, seed); err != nil {
		t.Fatalf("SaveMember: got error %v, want nil", err)
	}

	got, err := repo.FindMemberByEmail(ctx, "ada@example.com")
	if err != nil {
		t.Fatalf("FindMemberByEmail: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("FindMemberByEmail: got nil, want %+v", seed)
	}
	if *got != seed {
		t.Errorf("FindMemberByEmail: got %+v, want %+v", *got, seed)
	}
}

func TestInMemoryRepository_SaveMember_UpsertOverwrites(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	first := MemberDto{
		MemberId: MemberId("m-1"),
		Name:     "Ada Lovelace",
		Email:    "ada@example.com",
		Tier:     MembershipTierStandard,
		Status:   MembershipStatusActive,
	}
	if err := repo.SaveMember(ctx, first); err != nil {
		t.Fatalf("SaveMember (first): got error %v, want nil", err)
	}

	updated := first
	updated.Status = MembershipStatusSuspended
	updated.Tier = MembershipTierPremium
	if err := repo.SaveMember(ctx, updated); err != nil {
		t.Fatalf("SaveMember (update): got error %v, want nil", err)
	}

	got, err := repo.FindMemberById(ctx, first.MemberId)
	if err != nil {
		t.Fatalf("FindMemberById: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("FindMemberById: got nil, want %+v", updated)
	}
	if got.Status != MembershipStatusSuspended {
		t.Errorf("Status: got %q, want %q (upsert did not overwrite)", got.Status, MembershipStatusSuspended)
	}
	if got.Tier != MembershipTierPremium {
		t.Errorf("Tier: got %q, want %q (upsert did not overwrite)", got.Tier, MembershipTierPremium)
	}
}
