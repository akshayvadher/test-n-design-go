package memory

import (
	"context"
	"sync"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
)

// Repository is the in-memory catalog.Repository implementation. It is
// safe for concurrent use. Books are stored alongside the insertion order
// in which SaveBook first observed them so ListBooks returns them in that
// order — matching the source TS in-memory repository (`Map<K, V>` in JS
// preserves insertion order).
//
// A SaveBook against an existing BookId updates the stored value in place
// and does NOT change the book's position in the order slice — the source
// TS behaviour for `Map.set` on an existing key.
type Repository struct {
	mu         sync.RWMutex
	booksById  map[catalog.BookId]catalog.BookDto
	bookOrder  []catalog.BookId
	copiesById map[catalog.CopyId]catalog.CopyDto
}

// Compile-time assertion that *Repository satisfies the catalog driven
// port.
var _ catalog.Repository = (*Repository)(nil)

// NewRepository constructs an empty in-memory Repository.
func NewRepository() *Repository {
	return &Repository{
		booksById:  map[catalog.BookId]catalog.BookDto{},
		bookOrder:  []catalog.BookId{},
		copiesById: map[catalog.CopyId]catalog.CopyDto{},
	}
}

// SaveBook upserts the book. New books are appended to the order slice;
// existing books retain their original position.
func (r *Repository) SaveBook(_ context.Context, book catalog.BookDto) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, existed := r.booksById[book.BookId]; !existed {
		r.bookOrder = append(r.bookOrder, book.BookId)
	}
	r.booksById[book.BookId] = cloneBookDto(book)
	return nil
}

// FindBookById returns a copy of the stored book, or (nil, nil) on miss.
func (r *Repository) FindBookById(_ context.Context, bookId catalog.BookId) (*catalog.BookDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	book, ok := r.booksById[bookId]
	if !ok {
		return nil, nil
	}
	copied := cloneBookDto(book)
	return &copied, nil
}

// FindBookByIsbn scans the books in insertion order for a matching ISBN.
// Returns (nil, nil) on miss.
func (r *Repository) FindBookByIsbn(_ context.Context, isbn catalog.Isbn) (*catalog.BookDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, id := range r.bookOrder {
		book := r.booksById[id]
		if book.Isbn == isbn {
			copied := cloneBookDto(book)
			return &copied, nil
		}
	}
	return nil, nil
}

// ListBooks returns every stored book in insertion order. The returned
// slice is freshly allocated; callers can mutate it without affecting the
// repository.
func (r *Repository) ListBooks(_ context.Context) ([]catalog.BookDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	books := make([]catalog.BookDto, 0, len(r.bookOrder))
	for _, id := range r.bookOrder {
		books = append(books, cloneBookDto(r.booksById[id]))
	}
	return books, nil
}

// ListBooksByIds returns one row per matching book in insertion order.
// Duplicate ids in the input slice produce a single output row each.
// Unknown ids are silently dropped. An empty input returns an empty
// (non-nil) slice.
func (r *Repository) ListBooksByIds(_ context.Context, bookIds []catalog.BookId) ([]catalog.BookDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	wanted := make(map[catalog.BookId]struct{}, len(bookIds))
	for _, id := range bookIds {
		wanted[id] = struct{}{}
	}
	books := make([]catalog.BookDto, 0, len(wanted))
	for _, id := range r.bookOrder {
		if _, ok := wanted[id]; ok {
			books = append(books, cloneBookDto(r.booksById[id]))
		}
	}
	return books, nil
}

// DeleteBook removes the book and its slot in the order slice. Deleting a
// missing book is a no-op — the facade pre-checks existence and raises
// BookNotFoundError before reaching this method.
func (r *Repository) DeleteBook(_ context.Context, bookId catalog.BookId) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.booksById[bookId]; !ok {
		return nil
	}
	delete(r.booksById, bookId)
	r.bookOrder = removeBookId(r.bookOrder, bookId)
	return nil
}

// SaveCopy upserts the copy.
func (r *Repository) SaveCopy(_ context.Context, copy catalog.CopyDto) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.copiesById[copy.CopyId] = copy
	return nil
}

// FindCopyById returns the stored copy by value, or (nil, nil) on miss.
func (r *Repository) FindCopyById(_ context.Context, copyId catalog.CopyId) (*catalog.CopyDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	copy, ok := r.copiesById[copyId]
	if !ok {
		return nil, nil
	}
	return &copy, nil
}

// cloneBookDto returns a defensive copy of book so internal state and
// returned values do not share the Authors slice backing array.
func cloneBookDto(book catalog.BookDto) catalog.BookDto {
	clone := book
	if book.Authors != nil {
		clone.Authors = make([]string, len(book.Authors))
		copy(clone.Authors, book.Authors)
	}
	return clone
}

// removeBookId returns a new slice with the first occurrence of id removed.
func removeBookId(order []catalog.BookId, id catalog.BookId) []catalog.BookId {
	for index, candidate := range order {
		if candidate == id {
			return append(order[:index], order[index+1:]...)
		}
	}
	return order
}
