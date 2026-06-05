package bun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	upstreambun "github.com/uptrace/bun"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
)

// BookRow is the bun-mapped persistent shape of a book. JSON tags are
// intentionally absent — this struct never crosses the HTTP boundary;
// the HTTP DTOs in internal/catalog/driving/http own that.
//
// Column names match migrations/0001_catalog.sql verbatim. The `authors`
// column is a Postgres TEXT[]; bun's `array` tag opts into pg array
// (de)serialization via pgdriver's lib/pq-compatible encoder.
type BookRow struct {
	upstreambun.BaseModel `bun:"table:books"`

	BookId  catalog.BookId `bun:"book_id,pk"`
	Title   string         `bun:"title,notnull"`
	Authors []string       `bun:"authors,array,notnull"`
	Isbn    catalog.Isbn   `bun:"isbn,notnull,unique"`
}

// CopyRow is the bun-mapped persistent shape of a copy. Mirrors BookRow's
// shape rules — column names match the migration, no JSON tags.
type CopyRow struct {
	upstreambun.BaseModel `bun:"table:copies"`

	CopyId    catalog.CopyId        `bun:"copy_id,pk"`
	BookId    catalog.BookId        `bun:"book_id,notnull"`
	Condition catalog.CopyCondition `bun:"condition,notnull"`
	Status    catalog.CopyStatus    `bun:"status,notnull"`
}

// Repository is the Postgres-backed catalog.Repository implementation.
// Every method satisfies the same contract as the in-memory Repository:
// Find* returns (nil, nil) on miss; non-nil errors signal infrastructure
// failure.
type Repository struct {
	db *upstreambun.DB
}

// Compile-time assertion that *Repository satisfies the catalog driven
// port.
var _ catalog.Repository = (*Repository)(nil)

// NewRepository constructs a *Repository bound to db. The caller owns
// the *bun.DB lifecycle (open + close); Repository does not close it.
func NewRepository(db *upstreambun.DB) *Repository {
	return &Repository{db: db}
}

// SaveBook upserts the book by its primary key. Matches the TS source's
// `onConflictDoUpdate` semantics: a save against an existing book_id
// overwrites title/authors/isbn in place.
func (r *Repository) SaveBook(ctx context.Context, book catalog.BookDto) error {
	row := toBookRow(book)
	_, err := r.db.NewInsert().
		Model(&row).
		On("CONFLICT (book_id) DO UPDATE").
		Set("title = EXCLUDED.title").
		Set("authors = EXCLUDED.authors").
		Set("isbn = EXCLUDED.isbn").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("save book %q: %w", book.BookId, err)
	}
	return nil
}

// FindBookById returns the book row by primary key, or (nil, nil) on miss.
func (r *Repository) FindBookById(ctx context.Context, bookId catalog.BookId) (*catalog.BookDto, error) {
	var row BookRow
	err := r.db.NewSelect().Model(&row).Where("book_id = ?", bookId).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find book by id %q: %w", bookId, err)
	}
	book := toBookDto(row)
	return &book, nil
}

// FindBookByIsbn returns the book row by isbn, or (nil, nil) on miss.
func (r *Repository) FindBookByIsbn(ctx context.Context, isbn catalog.Isbn) (*catalog.BookDto, error) {
	var row BookRow
	err := r.db.NewSelect().Model(&row).Where("isbn = ?", isbn).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find book by isbn %q: %w", isbn, err)
	}
	book := toBookDto(row)
	return &book, nil
}

// ListBooks returns every book ordered by book_id ASC. UUIDs are not
// insertion-monotonic, but the order is deterministic — which is the
// only contract the facade depends on.
func (r *Repository) ListBooks(ctx context.Context) ([]catalog.BookDto, error) {
	var rows []BookRow
	err := r.db.NewSelect().Model(&rows).OrderExpr("book_id ASC").Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list books: %w", err)
	}
	return toBookDtos(rows), nil
}

// ListBooksByIds returns one row per matching book in book_id order. An
// empty input short-circuits with a non-nil empty slice — the database
// is not consulted.
func (r *Repository) ListBooksByIds(ctx context.Context, bookIds []catalog.BookId) ([]catalog.BookDto, error) {
	if len(bookIds) == 0 {
		return []catalog.BookDto{}, nil
	}
	var rows []BookRow
	err := r.db.NewSelect().
		Model(&rows).
		Where("book_id IN (?)", upstreambun.In(bookIds)).
		OrderExpr("book_id ASC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list books by ids: %w", err)
	}
	return toBookDtos(rows), nil
}

// DeleteBook removes the book row by primary key. Deleting a book with
// outstanding copies fails at the FK constraint — matches the TS source's
// no-cascade behaviour. The facade pre-checks existence and raises
// BookNotFoundError before reaching this method.
func (r *Repository) DeleteBook(ctx context.Context, bookId catalog.BookId) error {
	_, err := r.db.NewDelete().
		Model((*BookRow)(nil)).
		Where("book_id = ?", bookId).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete book %q: %w", bookId, err)
	}
	return nil
}

// SaveCopy upserts the copy by primary key. The TS source uses the same
// on-conflict-do-update shape so toggling a copy's status (Slice 2's
// MarkCopyAvailable / MarkCopyUnavailable flow) becomes one INSERT, not a
// load-then-UPDATE round trip.
func (r *Repository) SaveCopy(ctx context.Context, copy catalog.CopyDto) error {
	row := toCopyRow(copy)
	_, err := r.db.NewInsert().
		Model(&row).
		On("CONFLICT (copy_id) DO UPDATE").
		Set("book_id = EXCLUDED.book_id").
		Set("condition = EXCLUDED.condition").
		Set("status = EXCLUDED.status").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("save copy %q: %w", copy.CopyId, err)
	}
	return nil
}

// FindCopyById returns the copy row by primary key, or (nil, nil) on miss.
func (r *Repository) FindCopyById(ctx context.Context, copyId catalog.CopyId) (*catalog.CopyDto, error) {
	var row CopyRow
	err := r.db.NewSelect().Model(&row).Where("copy_id = ?", copyId).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find copy by id %q: %w", copyId, err)
	}
	copy := toCopyDto(row)
	return &copy, nil
}

// toBookRow converts a domain BookDto into the bun row. Authors is
// defensively copied so internal state and caller state do not share the
// backing array.
func toBookRow(book catalog.BookDto) BookRow {
	return BookRow{
		BookId:  book.BookId,
		Title:   book.Title,
		Authors: append([]string(nil), book.Authors...),
		Isbn:    book.Isbn,
	}
}

// toBookDto converts a bun row back into a domain BookDto. Authors is
// defensively copied for the same reason as toBookRow.
func toBookDto(row BookRow) catalog.BookDto {
	return catalog.BookDto{
		BookId:  row.BookId,
		Title:   row.Title,
		Authors: append([]string(nil), row.Authors...),
		Isbn:    row.Isbn,
	}
}

// toBookDtos converts a slice of bun rows into a fresh slice of domain
// BookDtos, preserving order.
func toBookDtos(rows []BookRow) []catalog.BookDto {
	books := make([]catalog.BookDto, 0, len(rows))
	for _, row := range rows {
		books = append(books, toBookDto(row))
	}
	return books
}

// toCopyRow converts a domain CopyDto into the bun row.
func toCopyRow(copy catalog.CopyDto) CopyRow {
	return CopyRow{
		CopyId:    copy.CopyId,
		BookId:    copy.BookId,
		Condition: copy.Condition,
		Status:    copy.Status,
	}
}

// toCopyDto converts a bun row back into a domain CopyDto.
func toCopyDto(row CopyRow) catalog.CopyDto {
	return catalog.CopyDto{
		CopyId:    row.CopyId,
		BookId:    row.BookId,
		Condition: row.Condition,
		Status:    row.Status,
	}
}
