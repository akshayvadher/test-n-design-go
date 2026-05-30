// in_memory_repository_test.go covers Slice 1's InMemoryRepository directly.
// The facade-level spec lands in Slice 2 — this file proves the repository
// substrate the facade depends on actually honours the contracts the facade
// will rely on: insertion-order on ListBooks, dedup + drop-misses on
// ListBooksByIds, (nil, nil) on every Find miss, and defensive slice copies
// on read so caller mutations cannot reach back into stored state.
//
// Stdlib testing only; same-package so the test can construct BookDto and
// CopyDto values directly without going through the facade.
package catalog

import (
	"context"
	"testing"
)

// -----------------------------------------------------------------------------
// SaveBook + ListBooks — insertion order preserved across repeated saves; an
// upsert of an existing BookId keeps the original position.
// -----------------------------------------------------------------------------

func TestInMemoryRepository_ListBooks_PreservesInsertionOrder(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	books := []BookDto{
		{BookId: "b-1", Title: "First", Authors: []string{"A"}, Isbn: Isbn("111")},
		{BookId: "b-2", Title: "Second", Authors: []string{"B"}, Isbn: Isbn("222")},
		{BookId: "b-3", Title: "Third", Authors: []string{"C"}, Isbn: Isbn("333")},
	}
	for _, b := range books {
		if err := repo.SaveBook(ctx, b); err != nil {
			t.Fatalf("SaveBook(%q): got error %v, want nil", b.BookId, err)
		}
	}

	got, err := repo.ListBooks(ctx)
	if err != nil {
		t.Fatalf("ListBooks: got error %v, want nil", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListBooks: got %d books, want 3", len(got))
	}
	for index, want := range []BookId{"b-1", "b-2", "b-3"} {
		if got[index].BookId != want {
			t.Errorf("ListBooks[%d].BookId: got %q, want %q", index, got[index].BookId, want)
		}
	}
}

func TestInMemoryRepository_SaveBook_UpsertPreservesOrder(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	mustSave(t, repo, BookDto{BookId: "b-1", Title: "First", Authors: []string{"A"}, Isbn: Isbn("111")})
	mustSave(t, repo, BookDto{BookId: "b-2", Title: "Second", Authors: []string{"B"}, Isbn: Isbn("222")})
	// Upsert b-1 — must NOT move it to the back of the order.
	mustSave(t, repo, BookDto{BookId: "b-1", Title: "First (updated)", Authors: []string{"A"}, Isbn: Isbn("111")})

	got, err := repo.ListBooks(ctx)
	if err != nil {
		t.Fatalf("ListBooks: got error %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListBooks: got %d books, want 2 (upsert must not append a duplicate)", len(got))
	}
	if got[0].BookId != "b-1" {
		t.Errorf("ListBooks[0].BookId: got %q, want %q (upsert moved position)", got[0].BookId, "b-1")
	}
	if got[0].Title != "First (updated)" {
		t.Errorf("ListBooks[0].Title: got %q, want %q (upsert did not update value)", got[0].Title, "First (updated)")
	}
}

// -----------------------------------------------------------------------------
// Find* — (nil, nil) on miss; happy paths return values that round-trip the
// stored fields.
// -----------------------------------------------------------------------------

func TestInMemoryRepository_FindBookById_ReturnsNilOnMiss(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	got, err := repo.FindBookById(ctx, BookId("unknown"))
	if err != nil {
		t.Fatalf("FindBookById: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("FindBookById: got %v, want nil (miss)", got)
	}
}

func TestInMemoryRepository_FindBookById_HappyPath(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()
	seed := BookDto{BookId: "b-1", Title: "T", Authors: []string{"A"}, Isbn: Isbn("111")}
	mustSave(t, repo, seed)

	got, err := repo.FindBookById(ctx, BookId("b-1"))
	if err != nil {
		t.Fatalf("FindBookById: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("FindBookById: got nil, want %+v", seed)
	}
	if got.BookId != seed.BookId || got.Title != seed.Title || got.Isbn != seed.Isbn {
		t.Errorf("FindBookById: got %+v, want %+v", *got, seed)
	}
}

func TestInMemoryRepository_FindBookByIsbn_ReturnsNilOnMiss(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	got, err := repo.FindBookByIsbn(ctx, Isbn("nope"))
	if err != nil {
		t.Fatalf("FindBookByIsbn: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("FindBookByIsbn: got %v, want nil (miss)", got)
	}
}

func TestInMemoryRepository_FindBookByIsbn_HappyPath(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()
	seed := BookDto{BookId: "b-1", Title: "T", Authors: []string{"A"}, Isbn: Isbn("isbn-1")}
	mustSave(t, repo, seed)

	got, err := repo.FindBookByIsbn(ctx, Isbn("isbn-1"))
	if err != nil {
		t.Fatalf("FindBookByIsbn: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("FindBookByIsbn: got nil, want %+v", seed)
	}
	if got.BookId != seed.BookId {
		t.Errorf("FindBookByIsbn.BookId: got %q, want %q", got.BookId, seed.BookId)
	}
}

// -----------------------------------------------------------------------------
// ListBooksByIds — empty input returns non-nil empty slice; duplicate ids
// dedup to one row per match; unknown ids silently dropped.
// -----------------------------------------------------------------------------

func TestInMemoryRepository_ListBooksByIds_EmptyInputReturnsNonNilEmptySlice(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	got, err := repo.ListBooksByIds(ctx, []BookId{})
	if err != nil {
		t.Fatalf("ListBooksByIds: got error %v, want nil", err)
	}
	if got == nil {
		t.Errorf("ListBooksByIds: got nil slice, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("ListBooksByIds: got %d entries, want 0", len(got))
	}
}

func TestInMemoryRepository_ListBooksByIds_DedupsAndDropsMisses(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	mustSave(t, repo, BookDto{BookId: "b-1", Title: "T1", Authors: []string{"A"}, Isbn: Isbn("111")})
	mustSave(t, repo, BookDto{BookId: "b-2", Title: "T2", Authors: []string{"B"}, Isbn: Isbn("222")})

	// Duplicate b-1 in the input, plus an unknown id that must be silently
	// dropped. The repo returns one row per matching book in insertion order.
	got, err := repo.ListBooksByIds(ctx, []BookId{"b-1", "b-1", "unknown", "b-2"})
	if err != nil {
		t.Fatalf("ListBooksByIds: got error %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListBooksByIds: got %d rows, want 2 (dedup + drop-miss)", len(got))
	}
	if got[0].BookId != "b-1" || got[1].BookId != "b-2" {
		t.Errorf("ListBooksByIds order: got [%q, %q], want [b-1, b-2]", got[0].BookId, got[1].BookId)
	}
}

func TestInMemoryRepository_ListBooksByIds_AllUnknownReturnsEmpty(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()
	mustSave(t, repo, BookDto{BookId: "b-1", Title: "T1", Authors: []string{"A"}, Isbn: Isbn("111")})

	got, err := repo.ListBooksByIds(ctx, []BookId{"x", "y"})
	if err != nil {
		t.Fatalf("ListBooksByIds: got error %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("ListBooksByIds: got %d rows, want 0", len(got))
	}
}

// -----------------------------------------------------------------------------
// DeleteBook — removes the book and its slot in the order slice; subsequent
// Finds miss; deleting a missing book is a no-op (no error).
// -----------------------------------------------------------------------------

func TestInMemoryRepository_DeleteBook_RemovesAndPreservesRestOrder(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	mustSave(t, repo, BookDto{BookId: "b-1", Title: "T1", Authors: []string{"A"}, Isbn: Isbn("111")})
	mustSave(t, repo, BookDto{BookId: "b-2", Title: "T2", Authors: []string{"B"}, Isbn: Isbn("222")})
	mustSave(t, repo, BookDto{BookId: "b-3", Title: "T3", Authors: []string{"C"}, Isbn: Isbn("333")})

	if err := repo.DeleteBook(ctx, BookId("b-2")); err != nil {
		t.Fatalf("DeleteBook: got error %v, want nil", err)
	}

	got, err := repo.ListBooks(ctx)
	if err != nil {
		t.Fatalf("ListBooks: got error %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListBooks: got %d, want 2 after delete", len(got))
	}
	if got[0].BookId != "b-1" || got[1].BookId != "b-3" {
		t.Errorf("ListBooks order: got [%q, %q], want [b-1, b-3]", got[0].BookId, got[1].BookId)
	}

	missing, err := repo.FindBookById(ctx, BookId("b-2"))
	if err != nil {
		t.Fatalf("FindBookById: got error %v, want nil", err)
	}
	if missing != nil {
		t.Errorf("FindBookById: got %v, want nil after delete", missing)
	}
}

func TestInMemoryRepository_DeleteBook_MissingIsNoOp(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	if err := repo.DeleteBook(ctx, BookId("unknown")); err != nil {
		t.Errorf("DeleteBook(unknown): got error %v, want nil (no-op)", err)
	}
}

// -----------------------------------------------------------------------------
// SaveCopy + FindCopyById — happy path and miss.
// -----------------------------------------------------------------------------

func TestInMemoryRepository_FindCopyById_ReturnsNilOnMiss(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	got, err := repo.FindCopyById(ctx, CopyId("unknown"))
	if err != nil {
		t.Fatalf("FindCopyById: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("FindCopyById: got %v, want nil (miss)", got)
	}
}

func TestInMemoryRepository_SaveCopy_RoundTripsViaFindCopyById(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()

	seed := CopyDto{
		CopyId:    CopyId("c-1"),
		BookId:    BookId("b-1"),
		Condition: CopyConditionGood,
		Status:    CopyStatusAvailable,
	}
	if err := repo.SaveCopy(ctx, seed); err != nil {
		t.Fatalf("SaveCopy: got error %v, want nil", err)
	}

	got, err := repo.FindCopyById(ctx, seed.CopyId)
	if err != nil {
		t.Fatalf("FindCopyById: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("FindCopyById: got nil, want %+v", seed)
	}
	if *got != seed {
		t.Errorf("FindCopyById: got %+v, want %+v", *got, seed)
	}
}

// -----------------------------------------------------------------------------
// Defensive copies — mutating a slice returned by FindBookById /
// FindBookByIsbn / ListBooks must NOT alter the stored value. This is the
// invariant the facade write-through cache and the lending facade will
// depend on once they ship in later slices.
// -----------------------------------------------------------------------------

func TestInMemoryRepository_FindBookById_ReturnsDefensiveSliceCopy(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()
	mustSave(t, repo, BookDto{
		BookId:  "b-1",
		Title:   "T",
		Authors: []string{"Andrew Hunt", "David Thomas"},
		Isbn:    Isbn("111"),
	})

	first, err := repo.FindBookById(ctx, BookId("b-1"))
	if err != nil {
		t.Fatalf("FindBookById: got error %v, want nil", err)
	}
	if first == nil || len(first.Authors) != 2 {
		t.Fatalf("FindBookById: unexpected initial value %+v", first)
	}
	// Mutate the returned slice — must not affect future reads.
	first.Authors[0] = "MUTATED"

	second, err := repo.FindBookById(ctx, BookId("b-1"))
	if err != nil {
		t.Fatalf("FindBookById (second): got error %v, want nil", err)
	}
	if second == nil || second.Authors[0] != "Andrew Hunt" {
		t.Errorf("FindBookById (second): authors mutation leaked into stored state: %+v", second)
	}
}

func TestInMemoryRepository_ListBooks_ReturnsDefensiveSliceCopy(t *testing.T) {
	repo := NewInMemoryRepository()
	ctx := context.Background()
	mustSave(t, repo, BookDto{
		BookId:  "b-1",
		Title:   "T",
		Authors: []string{"Andrew Hunt", "David Thomas"},
		Isbn:    Isbn("111"),
	})

	first, err := repo.ListBooks(ctx)
	if err != nil {
		t.Fatalf("ListBooks: got error %v, want nil", err)
	}
	if len(first) != 1 || len(first[0].Authors) != 2 {
		t.Fatalf("ListBooks: unexpected initial value %+v", first)
	}
	first[0].Authors[0] = "MUTATED"

	second, err := repo.ListBooks(ctx)
	if err != nil {
		t.Fatalf("ListBooks (second): got error %v, want nil", err)
	}
	if second[0].Authors[0] != "Andrew Hunt" {
		t.Errorf("ListBooks (second): authors mutation leaked into stored state: %+v", second[0])
	}
}

func TestInMemoryRepository_SaveBook_DefensiveCopyOnWrite(t *testing.T) {
	// Mutating the slice held by the caller AFTER SaveBook must not reach
	// into stored state — protects the facade against accidental aliasing.
	repo := NewInMemoryRepository()
	ctx := context.Background()
	authors := []string{"Andrew Hunt", "David Thomas"}
	mustSave(t, repo, BookDto{
		BookId:  "b-1",
		Title:   "T",
		Authors: authors,
		Isbn:    Isbn("111"),
	})
	authors[0] = "MUTATED-AFTER-SAVE"

	got, err := repo.FindBookById(ctx, BookId("b-1"))
	if err != nil {
		t.Fatalf("FindBookById: got error %v, want nil", err)
	}
	if got == nil || got.Authors[0] != "Andrew Hunt" {
		t.Errorf("FindBookById: post-save mutation leaked: %+v", got)
	}
}

// -----------------------------------------------------------------------------
// Small assertion helpers — stdlib only, no testify. mustSave is the only
// shared helper because exactly three tests use it; equalStrings already
// lives in schema_test.go and is reused via same-package access.
// -----------------------------------------------------------------------------

func mustSave(t *testing.T, repo *InMemoryRepository, book BookDto) {
	t.Helper()
	if err := repo.SaveBook(context.Background(), book); err != nil {
		t.Fatalf("SaveBook(%q): got error %v, want nil", book.BookId, err)
	}
}
