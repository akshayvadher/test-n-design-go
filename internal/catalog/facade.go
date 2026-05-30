package catalog

import (
	"context"
	"log/slog"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/shared/bookcache"
	"github.com/akshayvadher/test-n-design-go/internal/shared/isbngateway"
)

// Facade is the only public surface of the catalog module. Every business
// operation — adding a book, finding a copy, flipping a copy's status —
// goes through one of its exported methods. Unexported fields keep its
// collaborators encapsulated; the composition root wires them via
// NewFacade and tests substitute them via NewFacadeWithOverrides.
//
// Phase 2 does NOT expose AttachThumbnail / ReadThumbnail / RemoveThumbnail
// — file-storage is deferred to Phase 5. The accessControl dependency is
// nevertheless held as a field so Phase 5's diff is purely additive (no
// constructor signature change).
type Facade struct {
	repository    Repository
	newID         func() string
	isbnGateway   isbngateway.IsbnLookupGateway
	cache         bookcache.BookCacheGateway
	accessControl *accesscontrol.Facade
	logger        *slog.Logger
}

// NewFacade wires the Facade with explicit dependencies. The composition
// root passes the concrete implementations; tests use
// NewFacadeWithOverrides which fills the same arguments from an Overrides
// struct with in-memory defaults.
func NewFacade(
	repository Repository,
	newID func() string,
	isbnGateway isbngateway.IsbnLookupGateway,
	cache bookcache.BookCacheGateway,
	accessControl *accesscontrol.Facade,
	logger *slog.Logger,
) *Facade {
	return &Facade{
		repository:    repository,
		newID:         newID,
		isbnGateway:   isbnGateway,
		cache:         cache,
		accessControl: accessControl,
		logger:        logger,
	}
}

// AddBook enriches the inbound dto with gateway-supplied metadata
// (client-supplied fields win; gateway fills only missing/blank slots),
// validates the merged shape, rejects a duplicate ISBN, mints a fresh
// BookId, persists the book, and returns the saved value. The cache is
// NOT touched — only FindBook, UpdateBook and DeleteBook write through.
func (f *Facade) AddBook(ctx context.Context, dto NewBookDto) (BookDto, error) {
	isbn, err := ParseIsbn(string(dto.Isbn))
	if err != nil {
		return BookDto{}, err
	}
	merged, err := f.enrichAndParse(ctx, dto, isbn)
	if err != nil {
		return BookDto{}, err
	}
	if err := f.rejectDuplicateIsbn(ctx, merged.Isbn); err != nil {
		return BookDto{}, err
	}
	book := BookDto{
		BookId:  BookId(f.newID()),
		Title:   merged.Title,
		Authors: merged.Authors,
		Isbn:    merged.Isbn,
	}
	if err := f.repository.SaveBook(ctx, book); err != nil {
		return BookDto{}, err
	}
	return book, nil
}

// FindBook reads through the cache: on hit, return the cached value; on
// miss, consult the repository, populate the cache on a repo hit, and
// surface BookNotFoundError on a repo miss. Cache misses do NOT
// negative-cache.
func (f *Facade) FindBook(ctx context.Context, isbn Isbn) (BookDto, error) {
	cached, err := f.cache.Get(ctx, string(isbn))
	if err != nil {
		return BookDto{}, err
	}
	if cached != nil {
		return bookFromCache(*cached), nil
	}
	book, err := f.repository.FindBookByIsbn(ctx, isbn)
	if err != nil {
		return BookDto{}, err
	}
	if book == nil {
		return BookDto{}, &BookNotFoundError{Identifier: string(isbn)}
	}
	if err := f.cache.Set(ctx, string(isbn), bookToCache(*book)); err != nil {
		return BookDto{}, err
	}
	return *book, nil
}

// UpdateBook applies the patch onto the existing record, persists the
// updated value, and write-throughs the cache by the existing book's
// ISBN. UnknownBookId returns BookNotFoundError. Cache write failures
// propagate; the repository write is durable.
func (f *Facade) UpdateBook(ctx context.Context, bookId BookId, dto UpdateBookDto) (BookDto, error) {
	parsed, err := ParseUpdateBook(dto)
	if err != nil {
		return BookDto{}, err
	}
	existing, err := f.repository.FindBookById(ctx, bookId)
	if err != nil {
		return BookDto{}, err
	}
	if existing == nil {
		return BookDto{}, &BookNotFoundError{Identifier: string(bookId)}
	}
	updated := applyBookPatch(*existing, parsed)
	if err := f.repository.SaveBook(ctx, updated); err != nil {
		return BookDto{}, err
	}
	if err := f.cache.Set(ctx, string(existing.Isbn), bookToCache(updated)); err != nil {
		return BookDto{}, err
	}
	return updated, nil
}

// DeleteBook removes the book from the repository and evicts its ISBN
// from the cache. UnknownBookId returns BookNotFoundError. Cache eviction
// failures propagate; the repository delete is durable.
func (f *Facade) DeleteBook(ctx context.Context, bookId BookId) error {
	existing, err := f.repository.FindBookById(ctx, bookId)
	if err != nil {
		return err
	}
	if existing == nil {
		return &BookNotFoundError{Identifier: string(bookId)}
	}
	if err := f.repository.DeleteBook(ctx, bookId); err != nil {
		return err
	}
	if err := f.cache.Evict(ctx, string(existing.Isbn)); err != nil {
		return err
	}
	return nil
}

// ListBooks returns every book in the repository in insertion order.
func (f *Facade) ListBooks(ctx context.Context) ([]BookDto, error) {
	return f.repository.ListBooks(ctx)
}

// GetBooks returns the subset of books whose ids appear in bookIds. An
// empty input short-circuits with an empty result — the repository is
// not consulted. Matches the source TS facade line 181 behaviour.
func (f *Facade) GetBooks(ctx context.Context, bookIds []BookId) ([]BookDto, error) {
	if len(bookIds) == 0 {
		return []BookDto{}, nil
	}
	return f.repository.ListBooksByIds(ctx, bookIds)
}

// RegisterCopy validates the dto, asserts the parent book exists, mints a
// fresh CopyId, and persists the copy with default Status AVAILABLE.
func (f *Facade) RegisterCopy(ctx context.Context, bookId BookId, dto NewCopyDto) (CopyDto, error) {
	parsed, err := ParseNewCopy(dto)
	if err != nil {
		return CopyDto{}, err
	}
	book, err := f.repository.FindBookById(ctx, bookId)
	if err != nil {
		return CopyDto{}, err
	}
	if book == nil {
		return CopyDto{}, &BookNotFoundError{Identifier: string(bookId)}
	}
	copy := CopyDto{
		CopyId:    CopyId(f.newID()),
		BookId:    bookId,
		Condition: parsed.Condition,
		Status:    CopyStatusAvailable,
	}
	if err := f.repository.SaveCopy(ctx, copy); err != nil {
		return CopyDto{}, err
	}
	return copy, nil
}

// FindCopy loads the copy by id. Unknown id returns CopyNotFoundError.
func (f *Facade) FindCopy(ctx context.Context, copyId CopyId) (CopyDto, error) {
	copy, err := f.repository.FindCopyById(ctx, copyId)
	if err != nil {
		return CopyDto{}, err
	}
	if copy == nil {
		return CopyDto{}, &CopyNotFoundError{CopyId: copyId}
	}
	return *copy, nil
}

// MarkCopyAvailable flips the copy's status to AVAILABLE.
func (f *Facade) MarkCopyAvailable(ctx context.Context, copyId CopyId) (CopyDto, error) {
	return f.updateCopyStatus(ctx, copyId, CopyStatusAvailable)
}

// MarkCopyUnavailable flips the copy's status to UNAVAILABLE.
func (f *Facade) MarkCopyUnavailable(ctx context.Context, copyId CopyId) (CopyDto, error) {
	return f.updateCopyStatus(ctx, copyId, CopyStatusUnavailable)
}

// updateCopyStatus loads the copy, flips its status, and saves it back.
// Unknown id returns CopyNotFoundError.
func (f *Facade) updateCopyStatus(ctx context.Context, copyId CopyId, status CopyStatus) (CopyDto, error) {
	existing, err := f.repository.FindCopyById(ctx, copyId)
	if err != nil {
		return CopyDto{}, err
	}
	if existing == nil {
		return CopyDto{}, &CopyNotFoundError{CopyId: copyId}
	}
	updated := *existing
	updated.Status = status
	if err := f.repository.SaveCopy(ctx, updated); err != nil {
		return CopyDto{}, err
	}
	return updated, nil
}

// enrichAndParse merges client-supplied fields with gateway-supplied fields
// under the rule "client wins; gateway fills missing/blank slots", then
// validates the merged shape. The supplied isbn has already been parsed —
// the merged dto carries it verbatim.
func (f *Facade) enrichAndParse(ctx context.Context, dto NewBookDto, isbn Isbn) (NewBookDto, error) {
	enrichment, err := f.isbnGateway.FindByIsbn(ctx, string(isbn))
	if err != nil {
		return NewBookDto{}, err
	}
	merged := mergeBookEnrichment(dto, enrichment)
	merged.Isbn = isbn
	return ParseNewBook(merged)
}

// rejectDuplicateIsbn returns DuplicateIsbnError if a book with the merged
// isbn already exists in the repository.
func (f *Facade) rejectDuplicateIsbn(ctx context.Context, isbn Isbn) error {
	existing, err := f.repository.FindBookByIsbn(ctx, isbn)
	if err != nil {
		return err
	}
	if existing != nil {
		return &DuplicateIsbnError{Isbn: isbn}
	}
	return nil
}

// mergeBookEnrichment applies the client-wins merge rule.
//
//   - Title: client wins on non-blank; otherwise gateway's title fills.
//   - Authors: client wins on non-empty slice; otherwise gateway's
//     authors fill.
//   - Isbn: ignored here — the caller supplies the already-parsed value.
func mergeBookEnrichment(dto NewBookDto, enrichment *isbngateway.BookMetadata) NewBookDto {
	merged := NewBookDto{
		Title:   dto.Title,
		Authors: dto.Authors,
		Isbn:    dto.Isbn,
	}
	if enrichment == nil {
		return merged
	}
	if merged.Title == "" {
		merged.Title = enrichment.Title
	}
	if len(merged.Authors) == 0 {
		merged.Authors = enrichment.Authors
	}
	return merged
}

// applyBookPatch builds the updated BookDto from the existing record plus
// the (already-parsed) patch fields.
func applyBookPatch(existing BookDto, patch UpdateBookDto) BookDto {
	updated := existing
	if patch.Title != nil {
		updated.Title = *patch.Title
	}
	if patch.Authors != nil {
		updated.Authors = *patch.Authors
	}
	return updated
}

// bookToCache converts a domain BookDto into the cache's wire shape.
func bookToCache(book BookDto) bookcache.BookDto {
	return bookcache.BookDto{
		BookId:  string(book.BookId),
		Title:   book.Title,
		Authors: book.Authors,
		Isbn:    string(book.Isbn),
	}
}

// bookFromCache converts the cache's wire shape back into a domain BookDto.
func bookFromCache(cached bookcache.BookDto) BookDto {
	return BookDto{
		BookId:  BookId(cached.BookId),
		Title:   cached.Title,
		Authors: cached.Authors,
		Isbn:    Isbn(cached.Isbn),
	}
}
