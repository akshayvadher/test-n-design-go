// in_memory_repository_test.go covers the InMemoryCategoryRepository
// directly: (nil, nil) on every Find miss, round-trip via Save +
// FindById, the case-insensitive name uniqueness check, and the
// case-insensitive prefix match with ascending name ordering.
//
// Stdlib testing only; same-package so the test can construct
// CategoryDto values directly without going through the facade.
package categories

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInMemoryCategoryRepository_FindById_ReturnsNilOnMiss(t *testing.T) {
	repo := NewInMemoryCategoryRepository()

	got, err := repo.FindById(context.Background(), CategoryId("unknown"))
	if err != nil {
		t.Fatalf("FindById: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("FindById: got %v, want nil (miss)", got)
	}
}

func TestInMemoryCategoryRepository_Save_RoundTripsViaFindById(t *testing.T) {
	repo := NewInMemoryCategoryRepository()
	seed := CategoryDto{
		CategoryId: CategoryId("cat-1"),
		Name:       "Fiction",
		CreatedAt:  time.Date(2030, 1, 15, 0, 0, 0, 0, time.UTC),
	}

	if err := repo.Save(context.Background(), seed); err != nil {
		t.Fatalf("Save: got error %v, want nil", err)
	}
	got, err := repo.FindById(context.Background(), seed.CategoryId)
	if err != nil {
		t.Fatalf("FindById: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("FindById: got nil, want %+v", seed)
	}
	if *got != seed {
		t.Errorf("FindById: got %+v, want %+v", *got, seed)
	}
}

func TestInMemoryCategoryRepository_Save_RejectsDuplicateNameCaseInsensitive(t *testing.T) {
	repo := NewInMemoryCategoryRepository()
	ctx := context.Background()

	first := CategoryDto{CategoryId: "cat-1", Name: "Fiction", CreatedAt: time.Now()}
	second := CategoryDto{CategoryId: "cat-2", Name: "FICTION", CreatedAt: time.Now()}

	if err := repo.Save(ctx, first); err != nil {
		t.Fatalf("Save first: got error %v, want nil", err)
	}
	err := repo.Save(ctx, second)
	var dup *DuplicateCategoryError
	if !errors.As(err, &dup) {
		t.Fatalf("Save second (duplicate): got %v (%T), want *DuplicateCategoryError", err, err)
	}
	if dup.Name != "FICTION" {
		t.Errorf("DuplicateCategoryError.Name: got %q, want %q", dup.Name, "FICTION")
	}
}

func TestInMemoryCategoryRepository_Save_AllowsSameIdReSave(t *testing.T) {
	repo := NewInMemoryCategoryRepository()
	ctx := context.Background()

	first := CategoryDto{CategoryId: "cat-1", Name: "Fiction", CreatedAt: time.Now()}
	if err := repo.Save(ctx, first); err != nil {
		t.Fatalf("Save first: got error %v, want nil", err)
	}
	// re-save same id with same name must not raise (the linear-scan
	// duplicate check excludes the matching id).
	if err := repo.Save(ctx, first); err != nil {
		t.Errorf("Save re-save same id: got error %v, want nil", err)
	}
}

func TestInMemoryCategoryRepository_FindByNamePrefix_CaseInsensitiveSortedAscending(t *testing.T) {
	repo := NewInMemoryCategoryRepository()
	ctx := context.Background()

	seeds := []CategoryDto{
		{CategoryId: "cat-1", Name: "Apple", CreatedAt: time.Now()},
		{CategoryId: "cat-2", Name: "art", CreatedAt: time.Now()},
		{CategoryId: "cat-3", Name: "Banana", CreatedAt: time.Now()},
		{CategoryId: "cat-4", Name: "blueberry", CreatedAt: time.Now()},
	}
	for _, seed := range seeds {
		if err := repo.Save(ctx, seed); err != nil {
			t.Fatalf("Save %q: %v", seed.Name, err)
		}
	}

	got, err := repo.FindByNamePrefix(ctx, "a")
	if err != nil {
		t.Fatalf("FindByNamePrefix: got error %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: got %d, want 2", len(got))
	}
	names := []string{got[0].Name, got[1].Name}
	want := []string{"Apple", "art"}
	if names[0] != want[0] || names[1] != want[1] {
		t.Errorf("names: got %v, want %v", names, want)
	}
}

func TestInMemoryCategoryRepository_FindByNamePrefix_ReturnsEmptyOnNoMatch(t *testing.T) {
	repo := NewInMemoryCategoryRepository()
	ctx := context.Background()
	if err := repo.Save(ctx, CategoryDto{CategoryId: "cat-1", Name: "Fiction", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := repo.FindByNamePrefix(ctx, "zzz")
	if err != nil {
		t.Fatalf("FindByNamePrefix: got error %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("len: got %d, want 0", len(got))
	}
}

func TestInMemoryCategoryRepository_FindByNamePrefix_CapsAt100(t *testing.T) {
	repo := NewInMemoryCategoryRepository()
	ctx := context.Background()
	for i := 0; i < 105; i++ {
		seed := CategoryDto{
			CategoryId: CategoryId("cat-" + threeDigit(i)),
			Name:       "cat" + threeDigit(i),
			CreatedAt:  time.Now(),
		}
		if err := repo.Save(ctx, seed); err != nil {
			t.Fatalf("Save %q: %v", seed.Name, err)
		}
	}
	got, err := repo.FindByNamePrefix(ctx, "cat")
	if err != nil {
		t.Fatalf("FindByNamePrefix: %v", err)
	}
	if len(got) != MaxPrefixResults {
		t.Errorf("len: got %d, want %d", len(got), MaxPrefixResults)
	}
}

// threeDigit zero-pads n to width 3 so the seeded names sort
// identically under lexical and numeric order.
func threeDigit(n int) string {
	digits := []byte("000")
	for i := 2; i >= 0 && n > 0; i-- {
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits)
}
