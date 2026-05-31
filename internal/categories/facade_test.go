// facade_test.go is the facade-level spec for the categories module —
// a port of apps/library/src/categories/categories.facade.spec.ts from
// the source TypeScript repository.
//
// Stdlib testing only — t.Run for nested describe blocks, errors.As
// for typed-error assertions, no testify, no mock library.
package categories

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// fixedNow is the deterministic clock reading every test starts from
// unless it advances the clock itself.
var fixedNow = time.Date(2030, 1, 15, 12, 0, 0, 0, time.UTC)

// sequentialIds returns a deterministic id generator over a closed
// counter so minted CategoryId values are predictable in assertions.
// Default prefix is "category".
func sequentialIds(prefix string) func() string {
	if prefix == "" {
		prefix = "category"
	}
	counter := 0
	return func() string {
		counter++
		return prefix + "-" + strconv.Itoa(counter)
	}
}

// buildFacadeWithClock constructs a Facade with deterministic ids and
// the supplied clock. Used by tests that need to advance the clock.
func buildFacadeWithClock(clock func() time.Time) *Facade {
	return NewFacadeWithOverrides(Overrides{
		NewID: sequentialIds("category"),
		Clock: clock,
	})
}

// buildFacade constructs a Facade with deterministic ids and a frozen
// clock at fixedNow.
func buildFacade() *Facade {
	return buildFacadeWithClock(func() time.Time { return fixedNow })
}

func mustCreateCategory(t *testing.T, facade *Facade, name string) CategoryDto {
	t.Helper()
	category, err := facade.CreateCategory(context.Background(), name)
	if err != nil {
		t.Fatalf("CreateCategory(%q) returned unexpected error: %v", name, err)
	}
	return category
}

func assertInvalidCategory(t *testing.T, err error) {
	t.Helper()
	var target *InvalidCategoryError
	if !errors.As(err, &target) {
		t.Fatalf("expected *InvalidCategoryError, got %T (%v)", err, err)
	}
}

func assertCategoryNotFound(t *testing.T, err error) {
	t.Helper()
	var target *CategoryNotFoundError
	if !errors.As(err, &target) {
		t.Fatalf("expected *CategoryNotFoundError, got %T (%v)", err, err)
	}
}

func assertDuplicateCategory(t *testing.T, err error) {
	t.Helper()
	var target *DuplicateCategoryError
	if !errors.As(err, &target) {
		t.Fatalf("expected *DuplicateCategoryError, got %T (%v)", err, err)
	}
}

func assertInvalidCategoriesQuery(t *testing.T, err error) {
	t.Helper()
	var target *InvalidCategoriesQueryError
	if !errors.As(err, &target) {
		t.Fatalf("expected *InvalidCategoriesQueryError, got %T (%v)", err, err)
	}
}

// -----------------------------------------------------------------------------
// CategoriesFacade — full spec port
// -----------------------------------------------------------------------------

func TestCategoriesFacade(t *testing.T) {
	ctx := context.Background()

	t.Run("CreateCategory returns the saved category with id, trimmed name, and stamped createdAt", func(t *testing.T) {
		facade := buildFacade()

		category, err := facade.CreateCategory(ctx, "  Fiction  ")
		if err != nil {
			t.Fatalf("CreateCategory: %v", err)
		}
		if category.CategoryId != CategoryId("category-1") {
			t.Errorf("CategoryId: got %q, want %q", category.CategoryId, "category-1")
		}
		if category.Name != "Fiction" {
			t.Errorf("Name: got %q, want %q", category.Name, "Fiction")
		}
		if !category.CreatedAt.Equal(fixedNow) {
			t.Errorf("CreatedAt: got %v, want %v", category.CreatedAt, fixedNow)
		}
	})

	t.Run("CreateCategory persists the category so FindCategoryById returns it", func(t *testing.T) {
		facade := buildFacade()
		created := mustCreateCategory(t, facade, "Fiction")

		found, err := facade.FindCategoryById(ctx, created.CategoryId)
		if err != nil {
			t.Fatalf("FindCategoryById: %v", err)
		}
		if !reflect.DeepEqual(found, created) {
			t.Errorf("FindCategoryById: got %+v, want %+v", found, created)
		}
	})

	t.Run("CreateCategory rejects a blank name with *InvalidCategoryError", func(t *testing.T) {
		facade := buildFacade()

		_, err := facade.CreateCategory(ctx, "")
		assertInvalidCategory(t, err)

		_, err = facade.CreateCategory(ctx, "   ")
		assertInvalidCategory(t, err)
	})

	t.Run("CreateCategory rejects a too-long name with reason 'name too long'", func(t *testing.T) {
		facade := buildFacade()

		_, err := facade.CreateCategory(ctx, strings.Repeat("a", 101))
		var invalid *InvalidCategoryError
		if !errors.As(err, &invalid) {
			t.Fatalf("got %T (%v), want *InvalidCategoryError", err, err)
		}
		if invalid.Reason != "name too long" {
			t.Errorf("Reason: got %q, want %q", invalid.Reason, "name too long")
		}
	})

	t.Run("CreateCategory rejects a case-insensitive duplicate with *DuplicateCategoryError", func(t *testing.T) {
		facade := buildFacade()
		mustCreateCategory(t, facade, "Fiction")

		_, err := facade.CreateCategory(ctx, "FICTION")
		assertDuplicateCategory(t, err)
	})

	t.Run("CreateCategory mints a fresh id per call so ids are deterministic", func(t *testing.T) {
		facade := buildFacade()

		first := mustCreateCategory(t, facade, "Fiction")
		second := mustCreateCategory(t, facade, "History")
		third := mustCreateCategory(t, facade, "Science")

		if first.CategoryId != "category-1" || second.CategoryId != "category-2" || third.CategoryId != "category-3" {
			t.Errorf("ids: got %q,%q,%q want category-1,category-2,category-3",
				first.CategoryId, second.CategoryId, third.CategoryId)
		}
	})

	t.Run("CreateCategory reads the clock per call so createdAt advances", func(t *testing.T) {
		timestamps := []time.Time{
			time.Date(2030, 1, 15, 0, 0, 0, 0, time.UTC),
			time.Date(2030, 1, 16, 0, 0, 0, 0, time.UTC),
		}
		tick := 0
		facade := buildFacadeWithClock(func() time.Time {
			t := timestamps[tick]
			tick++
			return t
		})

		first := mustCreateCategory(t, facade, "Fiction")
		second := mustCreateCategory(t, facade, "History")

		if !first.CreatedAt.Equal(timestamps[0]) {
			t.Errorf("first.CreatedAt: got %v, want %v", first.CreatedAt, timestamps[0])
		}
		if !second.CreatedAt.Equal(timestamps[1]) {
			t.Errorf("second.CreatedAt: got %v, want %v", second.CreatedAt, timestamps[1])
		}
	})

	t.Run("FindCategoryById returns the stored category for a known id", func(t *testing.T) {
		facade := buildFacade()
		created := mustCreateCategory(t, facade, "Fiction")

		found, err := facade.FindCategoryById(ctx, created.CategoryId)
		if err != nil {
			t.Fatalf("FindCategoryById: %v", err)
		}
		if !reflect.DeepEqual(found, created) {
			t.Errorf("FindCategoryById: got %+v, want %+v", found, created)
		}
	})

	t.Run("FindCategoryById returns *CategoryNotFoundError for an unknown id", func(t *testing.T) {
		facade := buildFacade()

		_, err := facade.FindCategoryById(ctx, CategoryId("unknown-id"))
		assertCategoryNotFound(t, err)
	})

	t.Run("ListByPrefix returns matches sorted by name ASC", func(t *testing.T) {
		facade := buildFacade()
		mustCreateCategory(t, facade, "Apple")
		mustCreateCategory(t, facade, "art")
		mustCreateCategory(t, facade, "Banana")
		mustCreateCategory(t, facade, "blueberry")

		matches, err := facade.ListByPrefix(ctx, "a")
		if err != nil {
			t.Fatalf("ListByPrefix: %v", err)
		}
		if len(matches) != 2 {
			t.Fatalf("len: got %d, want 2", len(matches))
		}
		if matches[0].Name != "Apple" || matches[1].Name != "art" {
			t.Errorf("names: got [%q,%q], want [Apple, art]", matches[0].Name, matches[1].Name)
		}
	})

	t.Run("ListByPrefix returns [] when no name matches", func(t *testing.T) {
		facade := buildFacade()
		mustCreateCategory(t, facade, "Fiction")
		mustCreateCategory(t, facade, "History")

		matches, err := facade.ListByPrefix(ctx, "zzz")
		if err != nil {
			t.Fatalf("ListByPrefix: %v", err)
		}
		if len(matches) != 0 {
			t.Errorf("len: got %d, want 0", len(matches))
		}
	})

	t.Run("ListByPrefix rejects a blank prefix with *InvalidCategoriesQueryError", func(t *testing.T) {
		facade := buildFacade()

		_, err := facade.ListByPrefix(ctx, "")
		assertInvalidCategoriesQuery(t, err)

		_, err = facade.ListByPrefix(ctx, "   ")
		assertInvalidCategoriesQuery(t, err)
	})
}
