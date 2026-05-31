// facade_test.go is Slice 2's facade-level spec — a 1:1 port of
// apps/library/src/catalog/catalog.facade.spec.ts from the source TypeScript
// repository, scoped to the methods Phase 2 ships (no thumbnail / file-storage
// scenarios; those are deferred to Phase 5).
//
// Lives in package catalog_test (external test package) so it can import
// the in-memory adapter from internal/catalog/driven/memory without
// creating an import cycle. Every symbol is qualified with the catalog.*
// prefix.
//
// Stdlib testing only — t.Run for nested describe blocks, errors.As for
// typed-error assertions, no testify, no mock library.
//
// Spec-local decorators (throwingOnceIsbnLookupGateway, throwingOnceBookCacheGateway,
// recordingRepository) live at the bottom of the file. They mirror the
// equivalent TS classes; their existence here — not in any other package —
// is the proof that fault-injection state never leaks into production.
package catalog_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	catalogmemory "github.com/akshayvadher/test-n-design-go/internal/catalog/driven/memory"
	"github.com/akshayvadher/test-n-design-go/internal/shared/bookcache"
	"github.com/akshayvadher/test-n-design-go/internal/shared/isbngateway"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// sequentialIds returns a deterministic id generator over a closed counter so
// minted catalog.BookId / catalog.CopyId values are predictable in assertions. Default prefix
// is "id". Mirrors the TS source's sequentialIds.
func sequentialIds(prefix string) func() string {
	if prefix == "" {
		prefix = "id"
	}
	counter := 0
	return func() string {
		counter++
		return prefix + "-" + itoa(counter)
	}
}

// itoa is a tiny non-allocating int→string used only by sequentialIds so the
// closure does not pull strconv into otherwise-pure tests. Counter is bounded
// by the test count (low hundreds at most).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// buildFacade constructs a catalog.Facade with deterministic ids and the default
// in-memory substrates from configuration.go. Mirrors the TS buildFacade.
func buildFacade(t *testing.T) *catalog.Facade {
	t.Helper()
	return catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{NewID: sequentialIds("id")})
}

// buildFacadeWithGateway constructs a catalog.Facade wired to a seeded in-memory
// IsbnLookupGateway. Mirrors the TS buildFacadeWithGateway. The returned
// gateway pointer lets callers seed further entries inside a test.
func buildFacadeWithGateway(t *testing.T, seed map[string]isbngateway.BookMetadata) (*catalog.Facade, *isbngateway.InMemoryIsbnLookupGateway) {
	t.Helper()
	gateway := isbngateway.NewInMemoryIsbnLookupGateway()
	for isbn, metadata := range seed {
		gateway.Seed(isbn, metadata)
	}
	facade := catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{
		NewID:             sequentialIds("book"),
		IsbnLookupGateway: gateway,
	})
	return facade, gateway
}

// buildCacheScene constructs a catalog.Facade with a fresh in-memory cache. The cache
// pointer lets callers seed entries directly and assert post-conditions.
// Mirrors the TS buildScene used across the cache / update / delete blocks.
func buildCacheScene(t *testing.T) (*bookcache.InMemoryBookCacheGateway, *catalog.Facade) {
	t.Helper()
	cache := bookcache.NewInMemoryBookCacheGateway()
	facade := catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{
		NewID:            sequentialIds("book"),
		BookCacheGateway: cache,
	})
	return cache, facade
}

// mustAddBook is a tiny helper for arrange-phase calls where a t.Fatalf on
// failure is cleaner than a four-line err check. Test-only.
func mustAddBook(t *testing.T, facade *catalog.Facade, dto catalog.NewBookDto) catalog.BookDto {
	t.Helper()
	book, err := facade.AddBook(context.Background(), dto)
	if err != nil {
		t.Fatalf("AddBook(%+v) returned unexpected error: %v", dto, err)
	}
	return book
}

// mustRegisterCopy mirrors mustAddBook for the copy lifecycle scenarios.
func mustRegisterCopy(t *testing.T, facade *catalog.Facade, bookId catalog.BookId, dto catalog.NewCopyDto) catalog.CopyDto {
	t.Helper()
	copy, err := facade.RegisterCopy(context.Background(), bookId, dto)
	if err != nil {
		t.Fatalf("RegisterCopy(%s, %+v) returned unexpected error: %v", bookId, dto, err)
	}
	return copy
}

// assertInvalidBook fails the test if err is not *catalog.InvalidBookError.
// (Renamed from assertInvalidBookError to avoid collision with the
// reason-substring helper in schema_test.go.)
func assertInvalidBook(t *testing.T, err error) {
	t.Helper()
	var target *catalog.InvalidBookError
	if !errors.As(err, &target) {
		t.Fatalf("expected *catalog.InvalidBookError, got %T (%v)", err, err)
	}
}

// assertBookNotFound fails the test if err is not *catalog.BookNotFoundError.
func assertBookNotFound(t *testing.T, err error) {
	t.Helper()
	var target *catalog.BookNotFoundError
	if !errors.As(err, &target) {
		t.Fatalf("expected *catalog.BookNotFoundError, got %T (%v)", err, err)
	}
}

// assertCopyNotFound fails the test if err is not *catalog.CopyNotFoundError.
func assertCopyNotFound(t *testing.T, err error) {
	t.Helper()
	var target *catalog.CopyNotFoundError
	if !errors.As(err, &target) {
		t.Fatalf("expected *catalog.CopyNotFoundError, got %T (%v)", err, err)
	}
}

// assertDuplicateIsbn fails the test if err is not *catalog.DuplicateIsbnError.
func assertDuplicateIsbn(t *testing.T, err error) {
	t.Helper()
	var target *catalog.DuplicateIsbnError
	if !errors.As(err, &target) {
		t.Fatalf("expected *catalog.DuplicateIsbnError, got %T (%v)", err, err)
	}
}

// assertInvalidCopy fails the test if err is not *catalog.InvalidCopyError.
func assertInvalidCopy(t *testing.T, err error) {
	t.Helper()
	var target *catalog.InvalidCopyError
	if !errors.As(err, &target) {
		t.Fatalf("expected *catalog.InvalidCopyError, got %T (%v)", err, err)
	}
}

// cacheBookDto returns the wire shape the catalog facade stores in the cache
// for a given domain catalog.BookDto. Used by tests that seed the cache directly.
func cacheBookDto(book catalog.BookDto) bookcache.BookDto {
	return bookcache.BookDto{
		BookId:  string(book.BookId),
		Title:   book.Title,
		Authors: append([]string(nil), book.Authors...),
		Isbn:    string(book.Isbn),
	}
}

// -----------------------------------------------------------------------------
// CatalogFacade — basic CRUD (port of TS describe block 1)
// -----------------------------------------------------------------------------

func TestCatalogFacade_BasicCRUD(t *testing.T) {
	ctx := context.Background()

	t.Run("adds a book and finds it by isbn", func(t *testing.T) {
		facade := buildFacade(t)

		added, err := facade.AddBook(ctx, catalog.SampleNewBook(catalog.WithIsbn("978-0134685991")))
		if err != nil {
			t.Fatalf("AddBook failed: %v", err)
		}

		found, err := facade.FindBook(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("FindBook failed: %v", err)
		}
		if !reflect.DeepEqual(found, added) {
			t.Errorf("FindBook returned %+v, want %+v", found, added)
		}
	})

	t.Run("lists all books in the order they were added", func(t *testing.T) {
		facade := buildFacade(t)
		first := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		second := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0135957059"))

		books, err := facade.ListBooks(ctx)
		if err != nil {
			t.Fatalf("ListBooks failed: %v", err)
		}
		want := []catalog.BookDto{first, second}
		if !reflect.DeepEqual(books, want) {
			t.Errorf("ListBooks returned %+v, want %+v", books, want)
		}
	})

	t.Run("registers a copy of an existing book and finds the copy", func(t *testing.T) {
		facade := buildFacade(t)
		book := mustAddBook(t, facade, catalog.SampleNewBook())
		copy := mustRegisterCopy(t, facade, book.BookId, catalog.SampleNewCopy(catalog.WithBookId(book.BookId)))

		found, err := facade.FindCopy(ctx, copy.CopyId)
		if err != nil {
			t.Fatalf("FindCopy failed: %v", err)
		}
		if !reflect.DeepEqual(found, copy) {
			t.Errorf("FindCopy returned %+v, want %+v", found, copy)
		}
	})

	t.Run("registers new copies as available by default", func(t *testing.T) {
		facade := buildFacade(t)
		book := mustAddBook(t, facade, catalog.SampleNewBook())

		copy := mustRegisterCopy(t, facade, book.BookId, catalog.SampleNewCopy(catalog.WithBookId(book.BookId)))

		if copy.Status != catalog.CopyStatusAvailable {
			t.Errorf("new copy status = %s, want %s", copy.Status, catalog.CopyStatusAvailable)
		}
	})

	t.Run("marks an unavailable copy available again", func(t *testing.T) {
		facade := buildFacade(t)
		book := mustAddBook(t, facade, catalog.SampleNewBook())
		copy := mustRegisterCopy(t, facade, book.BookId, catalog.SampleNewCopy(catalog.WithBookId(book.BookId)))
		if _, err := facade.MarkCopyUnavailable(ctx, copy.CopyId); err != nil {
			t.Fatalf("MarkCopyUnavailable failed: %v", err)
		}

		updated, err := facade.MarkCopyAvailable(ctx, copy.CopyId)
		if err != nil {
			t.Fatalf("MarkCopyAvailable failed: %v", err)
		}
		if updated.Status != catalog.CopyStatusAvailable {
			t.Errorf("returned copy status = %s, want %s", updated.Status, catalog.CopyStatusAvailable)
		}

		fresh, err := facade.FindCopy(ctx, copy.CopyId)
		if err != nil {
			t.Fatalf("FindCopy failed: %v", err)
		}
		if fresh.Status != catalog.CopyStatusAvailable {
			t.Errorf("FindCopy status = %s, want %s", fresh.Status, catalog.CopyStatusAvailable)
		}
	})

	t.Run("marks an available copy unavailable", func(t *testing.T) {
		facade := buildFacade(t)
		book := mustAddBook(t, facade, catalog.SampleNewBook())
		copy := mustRegisterCopy(t, facade, book.BookId, catalog.SampleNewCopy(catalog.WithBookId(book.BookId)))

		updated, err := facade.MarkCopyUnavailable(ctx, copy.CopyId)
		if err != nil {
			t.Fatalf("MarkCopyUnavailable failed: %v", err)
		}
		if updated.Status != catalog.CopyStatusUnavailable {
			t.Errorf("returned copy status = %s, want %s", updated.Status, catalog.CopyStatusUnavailable)
		}

		fresh, err := facade.FindCopy(ctx, copy.CopyId)
		if err != nil {
			t.Fatalf("FindCopy failed: %v", err)
		}
		if fresh.Status != catalog.CopyStatusUnavailable {
			t.Errorf("FindCopy status = %s, want %s", fresh.Status, catalog.CopyStatusUnavailable)
		}
	})

	t.Run("returns catalog.BookNotFoundError when finding an unknown isbn", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.FindBook(ctx, "978-0000000000")
		assertBookNotFound(t, err)
	})

	t.Run("returns catalog.BookNotFoundError when registering a copy for an unknown book", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.RegisterCopy(ctx, "unknown-book-id", catalog.SampleNewCopy(catalog.WithBookId("unknown-book-id")))
		assertBookNotFound(t, err)
	})

	t.Run("returns catalog.CopyNotFoundError when marking an unknown copy available", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.MarkCopyAvailable(ctx, "unknown-copy-id")
		assertCopyNotFound(t, err)
	})

	t.Run("returns catalog.CopyNotFoundError when marking an unknown copy unavailable", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.MarkCopyUnavailable(ctx, "unknown-copy-id")
		assertCopyNotFound(t, err)
	})

	t.Run("rejects adding a book with a blank title", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.AddBook(ctx, catalog.SampleNewBook(catalog.WithTitle("")))
		assertInvalidBook(t, err)

		_, err = facade.AddBook(ctx, catalog.SampleNewBook(catalog.WithTitle("   ")))
		assertInvalidBook(t, err)
	})

	t.Run("rejects adding a book with no authors", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.AddBook(ctx, catalog.SampleNewBook(catalog.WithAuthors([]string{})))
		assertInvalidBook(t, err)

		_, err = facade.AddBook(ctx, catalog.SampleNewBook(catalog.WithAuthors([]string{"  "})))
		assertInvalidBook(t, err)
	})

	t.Run("rejects adding a book with a malformed isbn", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.AddBook(ctx, catalog.SampleNewBook(catalog.WithIsbn("")))
		assertInvalidBook(t, err)

		_, err = facade.AddBook(ctx, catalog.SampleNewBook(catalog.WithIsbn("123")))
		assertInvalidBook(t, err)

		_, err = facade.AddBook(ctx, catalog.SampleNewBook(catalog.WithIsbn("not-an-isbn")))
		assertInvalidBook(t, err)
	})

	t.Run("accepts well-formed isbn-10 and isbn-13 with or without hyphens", func(t *testing.T) {
		facade := buildFacade(t)

		isbn13Hyphenated, err := facade.AddBook(ctx, catalog.SampleNewBook(catalog.WithIsbn("978-0134685991")))
		if err != nil {
			t.Fatalf("AddBook isbn-13 hyphenated failed: %v", err)
		}
		isbn13Plain, err := facade.AddBook(ctx, catalog.SampleNewBook(catalog.WithIsbn("9780135957059")))
		if err != nil {
			t.Fatalf("AddBook isbn-13 plain failed: %v", err)
		}
		isbn10, err := facade.AddBook(ctx, catalog.SampleNewBook(catalog.WithIsbn("0-306-40615-2")))
		if err != nil {
			t.Fatalf("AddBook isbn-10 hyphenated failed: %v", err)
		}

		gotHy, err := facade.FindBook(ctx, "978-0134685991")
		if err != nil || !reflect.DeepEqual(gotHy, isbn13Hyphenated) {
			t.Errorf("FindBook(978-0134685991) = %+v, %v; want %+v", gotHy, err, isbn13Hyphenated)
		}
		gotPlain, err := facade.FindBook(ctx, "9780135957059")
		if err != nil || !reflect.DeepEqual(gotPlain, isbn13Plain) {
			t.Errorf("FindBook(9780135957059) = %+v, %v; want %+v", gotPlain, err, isbn13Plain)
		}
		got10, err := facade.FindBook(ctx, "0-306-40615-2")
		if err != nil || !reflect.DeepEqual(got10, isbn10) {
			t.Errorf("FindBook(0-306-40615-2) = %+v, %v; want %+v", got10, err, isbn10)
		}
	})

	t.Run("trims surrounding whitespace from title authors and isbn on addBook", func(t *testing.T) {
		facade := buildFacade(t)

		book, err := facade.AddBook(ctx, catalog.NewBookDto{
			Title:   "  The Pragmatic Programmer  ",
			Authors: []string{"  Andrew Hunt  ", "  David Thomas  "},
			Isbn:    "  978-0135957059  ",
		})
		if err != nil {
			t.Fatalf("AddBook failed: %v", err)
		}

		if book.Title != "The Pragmatic Programmer" {
			t.Errorf("book.Title = %q, want %q", book.Title, "The Pragmatic Programmer")
		}
		if !reflect.DeepEqual(book.Authors, []string{"Andrew Hunt", "David Thomas"}) {
			t.Errorf("book.Authors = %+v, want [Andrew Hunt David Thomas]", book.Authors)
		}
		if book.Isbn != "978-0135957059" {
			t.Errorf("book.Isbn = %q, want %q", book.Isbn, "978-0135957059")
		}
	})

	t.Run("rejects registering a copy with an invalid condition", func(t *testing.T) {
		facade := buildFacade(t)
		book := mustAddBook(t, facade, catalog.SampleNewBook())

		_, err := facade.RegisterCopy(ctx, book.BookId, catalog.NewCopyDto{
			BookId:    book.BookId,
			Condition: catalog.CopyCondition("BROKEN"),
		})
		assertInvalidCopy(t, err)
	})

	t.Run("rejects adding a book with an isbn that already exists", func(t *testing.T) {
		facade := buildFacade(t)
		mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		_, err := facade.AddBook(ctx, catalog.SampleNewBookWithIsbn("978-0134685991"))
		assertDuplicateIsbn(t, err)
	})
}

// -----------------------------------------------------------------------------
// GetBooks (port of TS describe block 2)
// -----------------------------------------------------------------------------

func TestCatalogFacade_GetBooks(t *testing.T) {
	ctx := context.Background()

	t.Run("returns empty slice for an empty bookIds slice without throwing", func(t *testing.T) {
		// seed two books to prove the short-circuit is not "nothing to return"
		facade := buildFacade(t)
		mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0135957059"))

		books, err := facade.GetBooks(ctx, []catalog.BookId{})
		if err != nil {
			t.Fatalf("GetBooks([]) returned err: %v", err)
		}
		if len(books) != 0 {
			t.Errorf("GetBooks([]) returned %+v, want empty slice", books)
		}
	})

	t.Run("returns each catalog.BookDto whose id matches in any order", func(t *testing.T) {
		facade := buildFacade(t)
		bookA := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		bookB := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0135957059"))

		books, err := facade.GetBooks(ctx, []catalog.BookId{bookA.BookId, bookB.BookId})
		if err != nil {
			t.Fatalf("GetBooks returned err: %v", err)
		}
		if len(books) != 2 {
			t.Fatalf("GetBooks returned %d books, want 2", len(books))
		}
		if !containsBook(books, bookA) || !containsBook(books, bookB) {
			t.Errorf("GetBooks returned %+v, want both bookA=%+v and bookB=%+v", books, bookA, bookB)
		}
	})

	t.Run("silently drops ids that do not match any book", func(t *testing.T) {
		facade := buildFacade(t)
		bookA := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		books, err := facade.GetBooks(ctx, []catalog.BookId{bookA.BookId, "non-existent-id"})
		if err != nil {
			t.Fatalf("GetBooks returned err: %v", err)
		}
		if !reflect.DeepEqual(books, []catalog.BookDto{bookA}) {
			t.Errorf("GetBooks returned %+v, want [%+v]", books, bookA)
		}
	})

	t.Run("returns one row per matching book when caller passes duplicate ids", func(t *testing.T) {
		facade := buildFacade(t)
		bookA := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		bookB := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0135957059"))

		books, err := facade.GetBooks(ctx, []catalog.BookId{bookA.BookId, bookA.BookId, bookB.BookId})
		if err != nil {
			t.Fatalf("GetBooks returned err: %v", err)
		}
		if len(books) != 2 {
			t.Errorf("GetBooks dedup returned %d books, want 2", len(books))
		}
		if !containsBook(books, bookA) || !containsBook(books, bookB) {
			t.Errorf("GetBooks returned %+v, want both bookA and bookB", books)
		}
	})

	t.Run("returns empty slice when none of the given bookIds match", func(t *testing.T) {
		facade := buildFacade(t)
		mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		books, err := facade.GetBooks(ctx, []catalog.BookId{"ghost-1", "ghost-2"})
		if err != nil {
			t.Fatalf("GetBooks returned err: %v", err)
		}
		if len(books) != 0 {
			t.Errorf("GetBooks unknown ids returned %+v, want empty slice", books)
		}
	})

	t.Run("does not touch the repository on empty input (short-circuit)", func(t *testing.T) {
		// recordingRepository wraps the real in-memory repo and counts
		// ListBooksByIds invocations. Two seeded books prove the short-circuit
		// fires irrespective of repo contents.
		recorder := &recordingRepository{inner: catalogmemory.NewRepository()}
		facade := catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{
			Repository: recorder,
			NewID:      sequentialIds("id"),
		})
		mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0135957059"))

		books, err := facade.GetBooks(ctx, []catalog.BookId{})
		if err != nil {
			t.Fatalf("GetBooks returned err: %v", err)
		}
		if len(books) != 0 {
			t.Errorf("GetBooks returned %+v, want empty slice", books)
		}
		if recorder.listBooksByIdsCallCount != 0 {
			t.Errorf("ListBooksByIds called %d times, want 0 (short-circuit broken)", recorder.listBooksByIdsCallCount)
		}
	})
}

// containsBook reports whether want is present in books (order-insensitive).
func containsBook(books []catalog.BookDto, want catalog.BookDto) bool {
	for _, b := range books {
		if reflect.DeepEqual(b, want) {
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------------
// AddBook — ISBN enrichment (port of TS describe block 3)
// -----------------------------------------------------------------------------

func TestCatalogFacade_AddBook_IsbnEnrichment(t *testing.T) {
	ctx := context.Background()

	t.Run("fills missing title from the gateway and keeps client authors", func(t *testing.T) {
		facade, _ := buildFacadeWithGateway(t, map[string]isbngateway.BookMetadata{
			"978-0134685991": {Title: "From Gateway", Authors: []string{"Gateway Author"}},
		})

		book, err := facade.AddBook(ctx, catalog.NewBookDto{
			Authors: []string{"Client Author"},
			Isbn:    "978-0134685991",
		})
		if err != nil {
			t.Fatalf("AddBook failed: %v", err)
		}
		if book.Title != "From Gateway" {
			t.Errorf("book.Title = %q, want %q", book.Title, "From Gateway")
		}
		if !reflect.DeepEqual(book.Authors, []string{"Client Author"}) {
			t.Errorf("book.Authors = %+v, want [Client Author]", book.Authors)
		}
	})

	t.Run("fills missing authors from the gateway and keeps client title", func(t *testing.T) {
		facade, _ := buildFacadeWithGateway(t, map[string]isbngateway.BookMetadata{
			"978-0134685991": {Title: "Gateway Title", Authors: []string{"Gateway Author"}},
		})

		book, err := facade.AddBook(ctx, catalog.NewBookDto{
			Title:   "Client Title",
			Authors: []string{},
			Isbn:    "978-0134685991",
		})
		if err != nil {
			t.Fatalf("AddBook failed: %v", err)
		}
		if book.Title != "Client Title" {
			t.Errorf("book.Title = %q, want %q", book.Title, "Client Title")
		}
		if !reflect.DeepEqual(book.Authors, []string{"Gateway Author"}) {
			t.Errorf("book.Authors = %+v, want [Gateway Author]", book.Authors)
		}
	})

	t.Run("keeps the client title even when the gateway has a different one", func(t *testing.T) {
		facade, _ := buildFacadeWithGateway(t, map[string]isbngateway.BookMetadata{
			"978-0134685991": {Title: "Gateway Title", Authors: []string{"Gateway Author"}},
		})

		book, err := facade.AddBook(ctx, catalog.NewBookDto{
			Title:   "Client Title",
			Authors: []string{"Client Author"},
			Isbn:    "978-0134685991",
		})
		if err != nil {
			t.Fatalf("AddBook failed: %v", err)
		}
		if book.Title != "Client Title" {
			t.Errorf("book.Title = %q, want %q", book.Title, "Client Title")
		}
	})

	t.Run("keeps the client authors even when the gateway has different ones", func(t *testing.T) {
		facade, _ := buildFacadeWithGateway(t, map[string]isbngateway.BookMetadata{
			"978-0134685991": {Title: "Gateway Title", Authors: []string{"Gateway Author"}},
		})

		book, err := facade.AddBook(ctx, catalog.NewBookDto{
			Title:   "Client Title",
			Authors: []string{"Real Client"},
			Isbn:    "978-0134685991",
		})
		if err != nil {
			t.Fatalf("AddBook failed: %v", err)
		}
		if !reflect.DeepEqual(book.Authors, []string{"Real Client"}) {
			t.Errorf("book.Authors = %+v, want [Real Client]", book.Authors)
		}
	})

	t.Run("succeeds with client data when the gateway returns nil", func(t *testing.T) {
		facade, _ := buildFacadeWithGateway(t, nil)

		book, err := facade.AddBook(ctx, catalog.NewBookDto{
			Title:   "Client Title",
			Authors: []string{"Client Author"},
			Isbn:    "978-0134685991",
		})
		if err != nil {
			t.Fatalf("AddBook failed: %v", err)
		}
		if book.Title != "Client Title" {
			t.Errorf("book.Title = %q, want %q", book.Title, "Client Title")
		}
		if !reflect.DeepEqual(book.Authors, []string{"Client Author"}) {
			t.Errorf("book.Authors = %+v, want [Client Author]", book.Authors)
		}
	})

	t.Run("fails with catalog.InvalidBookError when the title is missing on both sides", func(t *testing.T) {
		facade, _ := buildFacadeWithGateway(t, nil)

		_, err := facade.AddBook(ctx, catalog.NewBookDto{
			Authors: []string{"Client Author"},
			Isbn:    "978-0134685991",
		})
		assertInvalidBook(t, err)
	})

	t.Run("fails with catalog.InvalidBookError when title and authors are missing on both sides", func(t *testing.T) {
		facade, _ := buildFacadeWithGateway(t, nil)

		_, err := facade.AddBook(ctx, catalog.NewBookDto{Isbn: "978-0134685991"})
		assertInvalidBook(t, err)
	})

	t.Run("enriches before the duplicate-ISBN check and compares on the merged ISBN", func(t *testing.T) {
		facade, _ := buildFacadeWithGateway(t, map[string]isbngateway.BookMetadata{
			"978-0134685991": {Title: "Gateway Title", Authors: []string{"Gateway Author"}},
		})
		mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		_, err := facade.AddBook(ctx, catalog.NewBookDto{Isbn: "978-0134685991"})
		assertDuplicateIsbn(t, err)
	})

	t.Run("honours the IsbnLookupGateway override passed to NewFacadeWithOverrides", func(t *testing.T) {
		facade, _ := buildFacadeWithGateway(t, map[string]isbngateway.BookMetadata{
			"978-0134685991": {Title: "Gateway Title", Authors: []string{"Gateway Author"}},
		})

		book, err := facade.AddBook(ctx, catalog.NewBookDto{Isbn: "978-0134685991"})
		if err != nil {
			t.Fatalf("AddBook failed: %v", err)
		}
		if book.Title != "Gateway Title" {
			t.Errorf("book.Title = %q, want %q", book.Title, "Gateway Title")
		}
		if !reflect.DeepEqual(book.Authors, []string{"Gateway Author"}) {
			t.Errorf("book.Authors = %+v, want [Gateway Author]", book.Authors)
		}
	})

	t.Run("defaults to a fresh InMemoryIsbnLookupGateway when no override is supplied", func(t *testing.T) {
		facade := catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{NewID: sequentialIds("book")})

		_, err := facade.AddBook(ctx, catalog.NewBookDto{Isbn: "978-0134685991"})
		assertInvalidBook(t, err)
	})

	t.Run("returns a catalog.BookDto that reflects the merged persisted shape", func(t *testing.T) {
		facade, _ := buildFacadeWithGateway(t, map[string]isbngateway.BookMetadata{
			"978-0134685991": {Title: "Gateway Title", Authors: []string{"Gateway Author"}},
		})

		book, err := facade.AddBook(ctx, catalog.NewBookDto{
			Authors: []string{"Client Author"},
			Isbn:    "978-0134685991",
		})
		if err != nil {
			t.Fatalf("AddBook failed: %v", err)
		}
		want := catalog.BookDto{
			BookId:  book.BookId,
			Title:   "Gateway Title",
			Authors: []string{"Client Author"},
			Isbn:    "978-0134685991",
		}
		if !reflect.DeepEqual(book, want) {
			t.Errorf("AddBook returned %+v, want %+v", book, want)
		}
		found, err := facade.FindBook(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("FindBook failed: %v", err)
		}
		if !reflect.DeepEqual(found, book) {
			t.Errorf("FindBook returned %+v, want %+v (round-trip)", found, book)
		}
	})
}

// -----------------------------------------------------------------------------
// AddBook — gateway failures (port of TS describe block 4)
// -----------------------------------------------------------------------------

func TestCatalogFacade_AddBook_GatewayFailures(t *testing.T) {
	ctx := context.Background()

	buildWrappedScene := func(t *testing.T, seed map[string]isbngateway.BookMetadata) (*catalog.Facade, *throwingOnceIsbnLookupGateway) {
		t.Helper()
		inner := isbngateway.NewInMemoryIsbnLookupGateway()
		for isbn, metadata := range seed {
			inner.Seed(isbn, metadata)
		}
		wrapped := &throwingOnceIsbnLookupGateway{inner: inner}
		facade := catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{
			NewID:             sequentialIds("book"),
			IsbnLookupGateway: wrapped,
		})
		return facade, wrapped
	}

	t.Run("surfaces the gateway error to the caller when FindByIsbn fails", func(t *testing.T) {
		facade, gateway := buildWrappedScene(t, map[string]isbngateway.BookMetadata{
			"978-0134685991": {Title: "Gateway Title", Authors: []string{"Gateway Author"}},
		})
		armed := errors.New("isbn service is down")
		gateway.armFailureOnNextLookup(armed)

		_, err := facade.AddBook(ctx, catalog.NewBookDto{
			Title:   "Client Title",
			Authors: []string{"Client Author"},
			Isbn:    "978-0134685991",
		})
		if !errors.Is(err, armed) {
			t.Errorf("AddBook err = %v, want %v", err, armed)
		}
	})

	t.Run("persists nothing after a gateway failure", func(t *testing.T) {
		facade, gateway := buildWrappedScene(t, map[string]isbngateway.BookMetadata{
			"978-0134685991": {Title: "Gateway Title", Authors: []string{"Gateway Author"}},
		})
		armed := errors.New("isbn service is down")
		gateway.armFailureOnNextLookup(armed)

		_, err := facade.AddBook(ctx, catalog.NewBookDto{
			Title:   "Client Title",
			Authors: []string{"Client Author"},
			Isbn:    "978-0134685991",
		})
		if !errors.Is(err, armed) {
			t.Fatalf("AddBook err = %v, want %v", err, armed)
		}

		_, err = facade.FindBook(ctx, "978-0134685991")
		assertBookNotFound(t, err)

		books, err := facade.ListBooks(ctx)
		if err != nil {
			t.Fatalf("ListBooks failed: %v", err)
		}
		if len(books) != 0 {
			t.Errorf("ListBooks returned %+v, want empty slice", books)
		}
	})

	t.Run("succeeds on the next call after a single armed failure", func(t *testing.T) {
		facade, gateway := buildWrappedScene(t, map[string]isbngateway.BookMetadata{
			"978-0134685991": {Title: "Gateway Title", Authors: []string{"Gateway Author"}},
		})
		armed := errors.New("isbn service is down")
		gateway.armFailureOnNextLookup(armed)

		_, err := facade.AddBook(ctx, catalog.NewBookDto{
			Authors: []string{"Client Author"},
			Isbn:    "978-0134685991",
		})
		if !errors.Is(err, armed) {
			t.Fatalf("first AddBook err = %v, want %v", err, armed)
		}

		book, err := facade.AddBook(ctx, catalog.NewBookDto{
			Authors: []string{"Client Author"},
			Isbn:    "978-0134685991",
		})
		if err != nil {
			t.Fatalf("second AddBook failed: %v", err)
		}
		if book.Title != "Gateway Title" {
			t.Errorf("book.Title = %q, want %q", book.Title, "Gateway Title")
		}
		if !reflect.DeepEqual(book.Authors, []string{"Client Author"}) {
			t.Errorf("book.Authors = %+v, want [Client Author]", book.Authors)
		}
		if book.Isbn != "978-0134685991" {
			t.Errorf("book.Isbn = %q, want %q", book.Isbn, "978-0134685991")
		}
	})
}

// -----------------------------------------------------------------------------
// FindBook — cache read-through (port of TS describe block 5)
// -----------------------------------------------------------------------------

func TestCatalogFacade_FindBook_CacheReadThrough(t *testing.T) {
	ctx := context.Background()

	t.Run("returns the cached catalog.BookDto on cache hit without consulting the repository", func(t *testing.T) {
		cache, facade := buildCacheScene(t)
		seeded := catalog.BookDto{
			BookId:  "book-seeded",
			Title:   "Seeded From Cache",
			Authors: []string{"Cache Author"},
			Isbn:    "978-0134685991",
		}
		if err := cache.Set(ctx, "978-0134685991", cacheBookDto(seeded)); err != nil {
			t.Fatalf("cache.Set failed: %v", err)
		}

		found, err := facade.FindBook(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("FindBook failed: %v", err)
		}
		if !reflect.DeepEqual(found, seeded) {
			t.Errorf("FindBook returned %+v, want %+v", found, seeded)
		}
	})

	t.Run("cache miss + repo hit returns the repo book and populates the cache", func(t *testing.T) {
		cache, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		gotPre, _ := cache.Get(ctx, "978-0134685991")
		if gotPre != nil {
			t.Fatalf("precondition: cache.Get(978-0134685991) = %+v, want nil", gotPre)
		}

		found, err := facade.FindBook(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("FindBook failed: %v", err)
		}
		if !reflect.DeepEqual(found, added) {
			t.Errorf("FindBook returned %+v, want %+v", found, added)
		}
		gotPost, err := cache.Get(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("cache.Get failed: %v", err)
		}
		want := cacheBookDto(added)
		if gotPost == nil || !reflect.DeepEqual(*gotPost, want) {
			t.Errorf("cache.Get after FindBook = %+v, want %+v", gotPost, want)
		}
	})

	t.Run("cache miss + repo miss returns catalog.BookNotFoundError and does not negative-cache", func(t *testing.T) {
		cache, facade := buildCacheScene(t)

		_, err := facade.FindBook(ctx, "978-0000000000")
		assertBookNotFound(t, err)

		got, err := cache.Get(ctx, "978-0000000000")
		if err != nil {
			t.Fatalf("cache.Get failed: %v", err)
		}
		if got != nil {
			t.Errorf("cache.Get(978-0000000000) = %+v, want nil (no negative caching)", got)
		}
	})

	t.Run("AddBook does NOT populate the cache", func(t *testing.T) {
		cache, facade := buildCacheScene(t)

		mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		got, err := cache.Get(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("cache.Get failed: %v", err)
		}
		if got != nil {
			t.Errorf("cache.Get after AddBook = %+v, want nil", got)
		}
	})

	t.Run("two consecutive FindBook calls: first populates cache, second reads from cache", func(t *testing.T) {
		cache, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		gotPre, _ := cache.Get(ctx, "978-0134685991")
		if gotPre != nil {
			t.Fatalf("precondition: cache should be empty, got %+v", gotPre)
		}

		first, err := facade.FindBook(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("first FindBook failed: %v", err)
		}
		if !reflect.DeepEqual(first, added) {
			t.Errorf("first FindBook = %+v, want %+v", first, added)
		}
		gotMid, err := cache.Get(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("cache.Get failed: %v", err)
		}
		if gotMid == nil || !reflect.DeepEqual(*gotMid, cacheBookDto(added)) {
			t.Errorf("cache.Get after first FindBook = %+v, want populated", gotMid)
		}

		second, err := facade.FindBook(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("second FindBook failed: %v", err)
		}
		if !reflect.DeepEqual(second, added) {
			t.Errorf("second FindBook = %+v, want %+v", second, added)
		}
	})

	t.Run("NewFacadeWithOverrides honours the BookCacheGateway override; default-built facade has empty cache", func(t *testing.T) {
		overrideCache := bookcache.NewInMemoryBookCacheGateway()
		seeded := catalog.BookDto{
			BookId:  "book-override",
			Title:   "Override Hit",
			Authors: []string{"Override Author"},
			Isbn:    "978-0134685991",
		}
		if err := overrideCache.Set(ctx, "978-0134685991", cacheBookDto(seeded)); err != nil {
			t.Fatalf("overrideCache.Set failed: %v", err)
		}
		facadeWithOverride := catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{
			NewID:            sequentialIds("book"),
			BookCacheGateway: overrideCache,
		})

		found, err := facadeWithOverride.FindBook(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("FindBook with override failed: %v", err)
		}
		if !reflect.DeepEqual(found, seeded) {
			t.Errorf("FindBook with override returned %+v, want %+v", found, seeded)
		}

		facadeWithDefault := catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{NewID: sequentialIds("book")})
		_, err = facadeWithDefault.FindBook(ctx, "978-0000000000")
		assertBookNotFound(t, err)
	})

	t.Run("FindBook returns *catalog.BookNotFoundError class on a miss", func(t *testing.T) {
		_, facade := buildCacheScene(t)

		_, err := facade.FindBook(ctx, "978-0000000000")
		assertBookNotFound(t, err)
	})
}

// -----------------------------------------------------------------------------
// UpdateBook (port of TS describe block 6)
// -----------------------------------------------------------------------------

func TestCatalogFacade_UpdateBook(t *testing.T) {
	ctx := context.Background()

	t.Run("updates only the title and preserves authors bookId and isbn", func(t *testing.T) {
		_, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		updated, err := facade.UpdateBook(ctx, added.BookId, catalog.SampleUpdateBook(catalog.WithUpdateTitle("New Title"), catalog.WithUpdateAuthorsNil()))
		if err != nil {
			t.Fatalf("UpdateBook failed: %v", err)
		}

		want := catalog.BookDto{
			BookId:  added.BookId,
			Title:   "New Title",
			Authors: added.Authors,
			Isbn:    added.Isbn,
		}
		if !reflect.DeepEqual(updated, want) {
			t.Errorf("UpdateBook returned %+v, want %+v", updated, want)
		}
	})

	t.Run("updates only the authors and preserves title bookId and isbn", func(t *testing.T) {
		_, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		updated, err := facade.UpdateBook(ctx, added.BookId, catalog.SampleUpdateBook(catalog.WithUpdateTitleNil(), catalog.WithUpdateAuthors([]string{"New Author A", "New Author B"})))
		if err != nil {
			t.Fatalf("UpdateBook failed: %v", err)
		}

		want := catalog.BookDto{
			BookId:  added.BookId,
			Title:   added.Title,
			Authors: []string{"New Author A", "New Author B"},
			Isbn:    added.Isbn,
		}
		if !reflect.DeepEqual(updated, want) {
			t.Errorf("UpdateBook returned %+v, want %+v", updated, want)
		}
	})

	t.Run("updates title and authors atomically in a single returned DTO", func(t *testing.T) {
		_, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		updated, err := facade.UpdateBook(ctx, added.BookId, catalog.SampleUpdateBook(
			catalog.WithUpdateTitle("Both Updated Title"),
			catalog.WithUpdateAuthors([]string{"Both Updated Author"}),
		))
		if err != nil {
			t.Fatalf("UpdateBook failed: %v", err)
		}

		want := catalog.BookDto{
			BookId:  added.BookId,
			Title:   "Both Updated Title",
			Authors: []string{"Both Updated Author"},
			Isbn:    added.Isbn,
		}
		if !reflect.DeepEqual(updated, want) {
			t.Errorf("UpdateBook returned %+v, want %+v", updated, want)
		}
	})

	t.Run("write-through cache: subsequent FindBook returns the updated DTO", func(t *testing.T) {
		_, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		updated, err := facade.UpdateBook(ctx, added.BookId, catalog.SampleUpdateBook(
			catalog.WithUpdateTitle("Updated Title"),
			catalog.WithUpdateAuthors([]string{"Updated Author"}),
		))
		if err != nil {
			t.Fatalf("UpdateBook failed: %v", err)
		}

		found, err := facade.FindBook(ctx, added.Isbn)
		if err != nil {
			t.Fatalf("FindBook failed: %v", err)
		}
		if !reflect.DeepEqual(found, updated) {
			t.Errorf("FindBook returned %+v, want %+v", found, updated)
		}
		if found.Title != "Updated Title" {
			t.Errorf("found.Title = %q, want Updated Title", found.Title)
		}
		if !reflect.DeepEqual(found.Authors, []string{"Updated Author"}) {
			t.Errorf("found.Authors = %+v, want [Updated Author]", found.Authors)
		}
	})

	t.Run("write-through cache: cache.Get returns the updated catalog.BookDto directly", func(t *testing.T) {
		cache, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		gotPre, _ := cache.Get(ctx, string(added.Isbn))
		if gotPre != nil {
			t.Fatalf("precondition: cache should be empty, got %+v", gotPre)
		}

		updated, err := facade.UpdateBook(ctx, added.BookId, catalog.SampleUpdateBook(
			catalog.WithUpdateTitle("Cache Verified Title"),
			catalog.WithUpdateAuthors([]string{"Cache Verified Author"}),
		))
		if err != nil {
			t.Fatalf("UpdateBook failed: %v", err)
		}

		got, err := cache.Get(ctx, string(added.Isbn))
		if err != nil {
			t.Fatalf("cache.Get failed: %v", err)
		}
		want := cacheBookDto(updated)
		if got == nil || !reflect.DeepEqual(*got, want) {
			t.Errorf("cache.Get after UpdateBook = %+v, want %+v", got, want)
		}
	})

	t.Run("returns catalog.BookNotFoundError for an unknown bookId and does not mutate the cache", func(t *testing.T) {
		cache, facade := buildCacheScene(t)
		unrelatedIsbn := "978-0135957059"
		gotPre, _ := cache.Get(ctx, unrelatedIsbn)
		if gotPre != nil {
			t.Fatalf("precondition: cache should be empty, got %+v", gotPre)
		}

		_, err := facade.UpdateBook(ctx, "unknown-book-id", catalog.SampleUpdateBook(catalog.WithUpdateTitle("x"), catalog.WithUpdateAuthorsNil()))
		assertBookNotFound(t, err)

		got, err := cache.Get(ctx, unrelatedIsbn)
		if err != nil {
			t.Fatalf("cache.Get failed: %v", err)
		}
		if got != nil {
			t.Errorf("cache.Get(%s) = %+v, want nil", unrelatedIsbn, got)
		}
	})

	t.Run("returns catalog.InvalidBookError for an empty patch", func(t *testing.T) {
		_, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		_, err := facade.UpdateBook(ctx, added.BookId, catalog.UpdateBookDto{})
		assertInvalidBook(t, err)
	})

	t.Run("returns catalog.InvalidBookError for a whitespace-only title", func(t *testing.T) {
		_, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		_, err := facade.UpdateBook(ctx, added.BookId, catalog.SampleUpdateBook(catalog.WithUpdateTitle("   "), catalog.WithUpdateAuthorsNil()))
		assertInvalidBook(t, err)
	})

	t.Run("returns catalog.InvalidBookError for empty or all-blank authors slice", func(t *testing.T) {
		_, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		_, err := facade.UpdateBook(ctx, added.BookId, catalog.SampleUpdateBook(catalog.WithUpdateTitleNil(), catalog.WithUpdateAuthors([]string{})))
		assertInvalidBook(t, err)

		_, err = facade.UpdateBook(ctx, added.BookId, catalog.SampleUpdateBook(catalog.WithUpdateTitleNil(), catalog.WithUpdateAuthors([]string{"", "   "})))
		assertInvalidBook(t, err)
	})

	// The TS scenario "throws catalog.InvalidBookError when isbn is supplied" tests the
	// case where a caller sneaks an `isbn` field into the patch object. In Go
	// the equivalent enforcement is compile-time: catalog.UpdateBookDto has no catalog.Isbn
	// field (see types.go line 76). The "isbn cannot be updated" enforcement
	// at the HTTP DTO mapping layer is Slice 3's responsibility.
}

// -----------------------------------------------------------------------------
// DeleteBook (port of TS describe block 7 — non-thumbnail scenarios only)
// -----------------------------------------------------------------------------

func TestCatalogFacade_DeleteBook(t *testing.T) {
	ctx := context.Background()

	t.Run("resolves without error for an existing book", func(t *testing.T) {
		_, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		if err := facade.DeleteBook(ctx, added.BookId); err != nil {
			t.Errorf("DeleteBook returned err: %v, want nil", err)
		}
	})

	t.Run("makes FindBook(isbn) return catalog.BookNotFoundError after deletion", func(t *testing.T) {
		_, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))

		if err := facade.DeleteBook(ctx, added.BookId); err != nil {
			t.Fatalf("DeleteBook failed: %v", err)
		}

		_, err := facade.FindBook(ctx, added.Isbn)
		assertBookNotFound(t, err)
	})

	t.Run("leaves cache empty after deleting a never-cached book", func(t *testing.T) {
		cache, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		gotPre, _ := cache.Get(ctx, string(added.Isbn))
		if gotPre != nil {
			t.Fatalf("precondition: cache should be empty, got %+v", gotPre)
		}

		if err := facade.DeleteBook(ctx, added.BookId); err != nil {
			t.Fatalf("DeleteBook failed: %v", err)
		}

		got, err := cache.Get(ctx, string(added.Isbn))
		if err != nil {
			t.Fatalf("cache.Get failed: %v", err)
		}
		if got != nil {
			t.Errorf("cache.Get after delete = %+v, want nil", got)
		}
	})

	t.Run("evicts a previously-seeded cache entry on delete", func(t *testing.T) {
		cache, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		if err := cache.Set(ctx, string(added.Isbn), cacheBookDto(added)); err != nil {
			t.Fatalf("cache.Set failed: %v", err)
		}

		if err := facade.DeleteBook(ctx, added.BookId); err != nil {
			t.Fatalf("DeleteBook failed: %v", err)
		}

		got, err := cache.Get(ctx, string(added.Isbn))
		if err != nil {
			t.Fatalf("cache.Get failed: %v", err)
		}
		if got != nil {
			t.Errorf("cache.Get after delete = %+v, want nil (evicted)", got)
		}
	})

	t.Run("returns catalog.BookNotFoundError for an unknown bookId and does not modify the cache", func(t *testing.T) {
		cache, facade := buildCacheScene(t)
		unrelatedIsbn := "978-0135957059"
		unrelated := catalog.BookDto{
			BookId:  "book-unrelated",
			Title:   "Unrelated",
			Authors: []string{"Unrelated Author"},
			Isbn:    catalog.Isbn(unrelatedIsbn),
		}
		if err := cache.Set(ctx, unrelatedIsbn, cacheBookDto(unrelated)); err != nil {
			t.Fatalf("cache.Set failed: %v", err)
		}

		err := facade.DeleteBook(ctx, "unknown-book-id")
		assertBookNotFound(t, err)

		got, err := cache.Get(ctx, unrelatedIsbn)
		if err != nil {
			t.Fatalf("cache.Get failed: %v", err)
		}
		want := cacheBookDto(unrelated)
		if got == nil || !reflect.DeepEqual(*got, want) {
			t.Errorf("cache.Get(%s) after failed delete = %+v, want %+v (unmodified)", unrelatedIsbn, got, want)
		}
	})

	t.Run("only evicts the deleted book and leaves other cache entries intact", func(t *testing.T) {
		cache, facade := buildCacheScene(t)
		bookA := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		bookB := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0135957059"))
		if err := cache.Set(ctx, string(bookA.Isbn), cacheBookDto(bookA)); err != nil {
			t.Fatalf("cache.Set(A) failed: %v", err)
		}
		if err := cache.Set(ctx, string(bookB.Isbn), cacheBookDto(bookB)); err != nil {
			t.Fatalf("cache.Set(B) failed: %v", err)
		}

		if err := facade.DeleteBook(ctx, bookA.BookId); err != nil {
			t.Fatalf("DeleteBook(A) failed: %v", err)
		}

		gotA, err := cache.Get(ctx, string(bookA.Isbn))
		if err != nil {
			t.Fatalf("cache.Get(A) failed: %v", err)
		}
		if gotA != nil {
			t.Errorf("cache.Get(A) after delete = %+v, want nil", gotA)
		}
		gotB, err := cache.Get(ctx, string(bookB.Isbn))
		if err != nil {
			t.Fatalf("cache.Get(B) failed: %v", err)
		}
		want := cacheBookDto(bookB)
		if gotB == nil || !reflect.DeepEqual(*gotB, want) {
			t.Errorf("cache.Get(B) after delete A = %+v, want %+v (intact)", gotB, want)
		}
	})

	t.Run("returns catalog.BookNotFoundError on a second delete of the same bookId", func(t *testing.T) {
		_, facade := buildCacheScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		if err := facade.DeleteBook(ctx, added.BookId); err != nil {
			t.Fatalf("first DeleteBook failed: %v", err)
		}

		err := facade.DeleteBook(ctx, added.BookId)
		assertBookNotFound(t, err)
	})

	t.Run("allows AddBook with the same isbn after DeleteBook", func(t *testing.T) {
		_, facade := buildCacheScene(t)
		original := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		if err := facade.DeleteBook(ctx, original.BookId); err != nil {
			t.Fatalf("DeleteBook failed: %v", err)
		}

		recreated, err := facade.AddBook(ctx, catalog.SampleNewBookWithIsbn("978-0134685991"))
		if err != nil {
			t.Fatalf("AddBook recreate failed: %v", err)
		}
		if recreated.Isbn != "978-0134685991" {
			t.Errorf("recreated.Isbn = %q, want %q", recreated.Isbn, "978-0134685991")
		}
		if recreated.BookId == original.BookId {
			t.Errorf("recreated.BookId = %s, want a fresh id (original was %s)", recreated.BookId, original.BookId)
		}
		found, err := facade.FindBook(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("FindBook failed: %v", err)
		}
		if !reflect.DeepEqual(found, recreated) {
			t.Errorf("FindBook returned %+v, want %+v", found, recreated)
		}
	})
}

// -----------------------------------------------------------------------------
// Cache gateway failures (port of TS describe block 8)
// -----------------------------------------------------------------------------

func TestCatalogFacade_CacheGatewayFailures(t *testing.T) {
	ctx := context.Background()

	buildScene := func(t *testing.T) (*throwingOnceBookCacheGateway, *catalog.Facade) {
		t.Helper()
		inner := bookcache.NewInMemoryBookCacheGateway()
		throwing := &throwingOnceBookCacheGateway{inner: inner}
		facade := catalogmemory.NewFacadeWithOverrides(catalogmemory.Overrides{
			BookCacheGateway: throwing,
			NewID:            sequentialIds("book"),
		})
		return throwing, facade
	}

	t.Run("cache.Set fails on FindBook miss-then-populate; next FindBook recovers", func(t *testing.T) {
		throwing, facade := buildScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		armed := errors.New("redis SET failed")
		throwing.armFailureOnNextSet(armed)

		_, err := facade.FindBook(ctx, "978-0134685991")
		if !errors.Is(err, armed) {
			t.Errorf("FindBook err = %v, want %v", err, armed)
		}

		recovered, err := facade.FindBook(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("follow-up FindBook failed: %v", err)
		}
		if !reflect.DeepEqual(recovered, added) {
			t.Errorf("follow-up FindBook = %+v, want %+v", recovered, added)
		}
	})

	t.Run("cache.Get fails on FindBook; next FindBook recovers", func(t *testing.T) {
		throwing, facade := buildScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		armed := errors.New("redis GET failed")
		throwing.armFailureOnNextGet(armed)

		_, err := facade.FindBook(ctx, "978-0134685991")
		if !errors.Is(err, armed) {
			t.Errorf("FindBook err = %v, want %v", err, armed)
		}

		recovered, err := facade.FindBook(ctx, "978-0134685991")
		if err != nil {
			t.Fatalf("follow-up FindBook failed: %v", err)
		}
		if !reflect.DeepEqual(recovered, added) {
			t.Errorf("follow-up FindBook = %+v, want %+v", recovered, added)
		}
	})

	t.Run("cache.Set fails during UpdateBook write-through; repo is source of truth", func(t *testing.T) {
		throwing, facade := buildScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		armed := errors.New("redis SET failed mid-update")
		throwing.armFailureOnNextSet(armed)

		_, err := facade.UpdateBook(ctx, added.BookId, catalog.SampleUpdateBook(catalog.WithUpdateTitle("Updated Title"), catalog.WithUpdateAuthorsNil()))
		if !errors.Is(err, armed) {
			t.Errorf("UpdateBook err = %v, want %v", err, armed)
		}

		// The repo write was durable. The follow-up FindBook on a cache MISS
		// will read from repo and try to populate the cache; since the arming
		// is single-shot it has already been cleared, so this call succeeds.
		found, err := facade.FindBook(ctx, added.Isbn)
		if err != nil {
			t.Fatalf("follow-up FindBook failed: %v", err)
		}
		if found.Title != "Updated Title" {
			t.Errorf("follow-up FindBook title = %q, want Updated Title", found.Title)
		}
	})

	t.Run("cache.Evict fails during DeleteBook; repo delete is durable", func(t *testing.T) {
		throwing, facade := buildScene(t)
		added := mustAddBook(t, facade, catalog.SampleNewBookWithIsbn("978-0134685991"))
		armed := errors.New("redis EVICT failed")
		throwing.armFailureOnNextEvict(armed)

		err := facade.DeleteBook(ctx, added.BookId)
		if !errors.Is(err, armed) {
			t.Errorf("DeleteBook err = %v, want %v", err, armed)
		}

		_, err = facade.FindBook(ctx, added.Isbn)
		assertBookNotFound(t, err)
	})
}

// -----------------------------------------------------------------------------
// Spec-local decorators (mirror the TS ThrowingOnce* / Recording* classes)
//
// These types live in the test file because the source spec's Principle 5
// requires the fault-injection state to never leak into production. Each
// decorator wraps a real in-memory implementation, holds an "armed" error
// that arms once and clears on use, and delegates to the inner type
// otherwise.
// -----------------------------------------------------------------------------

// throwingOnceIsbnLookupGateway decorates an IsbnLookupGateway and returns the
// armed error on the next FindByIsbn call, then clears the arming. Mirrors
// the TS ThrowingOnceIsbnLookupGateway.
type throwingOnceIsbnLookupGateway struct {
	inner       isbngateway.IsbnLookupGateway
	armedLookup error
}

func (g *throwingOnceIsbnLookupGateway) armFailureOnNextLookup(err error) {
	g.armedLookup = err
}

func (g *throwingOnceIsbnLookupGateway) FindByIsbn(ctx context.Context, isbn string) (*isbngateway.BookMetadata, error) {
	if g.armedLookup != nil {
		err := g.armedLookup
		g.armedLookup = nil
		return nil, err
	}
	return g.inner.FindByIsbn(ctx, isbn)
}

// throwingOnceBookCacheGateway decorates a BookCacheGateway and returns the
// armed error on the next Get / Set / Evict, then clears that arming.
// Mirrors the TS ThrowingOnceBookCacheGateway.
type throwingOnceBookCacheGateway struct {
	inner      bookcache.BookCacheGateway
	armedGet   error
	armedSet   error
	armedEvict error
}

func (c *throwingOnceBookCacheGateway) armFailureOnNextGet(err error)   { c.armedGet = err }
func (c *throwingOnceBookCacheGateway) armFailureOnNextSet(err error)   { c.armedSet = err }
func (c *throwingOnceBookCacheGateway) armFailureOnNextEvict(err error) { c.armedEvict = err }

func (c *throwingOnceBookCacheGateway) Get(ctx context.Context, isbn string) (*bookcache.BookDto, error) {
	if c.armedGet != nil {
		err := c.armedGet
		c.armedGet = nil
		return nil, err
	}
	return c.inner.Get(ctx, isbn)
}

func (c *throwingOnceBookCacheGateway) Set(ctx context.Context, isbn string, book bookcache.BookDto) error {
	if c.armedSet != nil {
		err := c.armedSet
		c.armedSet = nil
		return err
	}
	return c.inner.Set(ctx, isbn, book)
}

func (c *throwingOnceBookCacheGateway) Evict(ctx context.Context, isbn string) error {
	if c.armedEvict != nil {
		err := c.armedEvict
		c.armedEvict = nil
		return err
	}
	return c.inner.Evict(ctx, isbn)
}

// recordingRepository wraps a real catalog.Repository and counts ListBooksByIds calls
// so the GetBooks([]) short-circuit can be asserted as "repo not touched."
// Mirrors the spec-local recording pattern from the TS source.
type recordingRepository struct {
	inner                   catalog.Repository
	listBooksByIdsCallCount int
}

func (r *recordingRepository) SaveBook(ctx context.Context, book catalog.BookDto) error {
	return r.inner.SaveBook(ctx, book)
}

func (r *recordingRepository) FindBookById(ctx context.Context, bookId catalog.BookId) (*catalog.BookDto, error) {
	return r.inner.FindBookById(ctx, bookId)
}

func (r *recordingRepository) FindBookByIsbn(ctx context.Context, isbn catalog.Isbn) (*catalog.BookDto, error) {
	return r.inner.FindBookByIsbn(ctx, isbn)
}

func (r *recordingRepository) ListBooks(ctx context.Context) ([]catalog.BookDto, error) {
	return r.inner.ListBooks(ctx)
}

func (r *recordingRepository) ListBooksByIds(ctx context.Context, bookIds []catalog.BookId) ([]catalog.BookDto, error) {
	r.listBooksByIdsCallCount++
	return r.inner.ListBooksByIds(ctx, bookIds)
}

func (r *recordingRepository) DeleteBook(ctx context.Context, bookId catalog.BookId) error {
	return r.inner.DeleteBook(ctx, bookId)
}

func (r *recordingRepository) SaveCopy(ctx context.Context, copy catalog.CopyDto) error {
	return r.inner.SaveCopy(ctx, copy)
}

func (r *recordingRepository) FindCopyById(ctx context.Context, copyId catalog.CopyId) (*catalog.CopyDto, error) {
	return r.inner.FindCopyById(ctx, copyId)
}
