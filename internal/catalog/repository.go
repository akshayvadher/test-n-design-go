package catalog

import "context"

// Repository is the persistence port the catalog facade depends on. The
// in-memory adapter ships in this slice; the bun-backed Postgres adapter
// lands in Slice 4.
//
// Find* methods return (nil, nil) on "no rows" — the facade is responsible
// for translating that into BookNotFoundError or CopyNotFoundError. A
// non-nil error indicates infrastructure failure (decode, transport, …)
// and is propagated unchanged.
type Repository interface {
	SaveBook(ctx context.Context, book BookDto) error
	FindBookById(ctx context.Context, bookId BookId) (*BookDto, error)
	FindBookByIsbn(ctx context.Context, isbn Isbn) (*BookDto, error)
	ListBooks(ctx context.Context) ([]BookDto, error)
	ListBooksByIds(ctx context.Context, bookIds []BookId) ([]BookDto, error)
	DeleteBook(ctx context.Context, bookId BookId) error

	SaveCopy(ctx context.Context, copy CopyDto) error
	FindCopyById(ctx context.Context, copyId CopyId) (*CopyDto, error)
}
