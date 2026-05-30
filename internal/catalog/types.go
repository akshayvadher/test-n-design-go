// Package catalog is the bounded-context module for the library's book and
// copy catalogue. The Facade is the only public surface that other modules
// or the HTTP layer should depend on; the repository, the in-memory adapter,
// the schema parsers and the sample-data builders are exported only so
// composition-root wiring and same-package tests can reference them.
//
// Phase 2 ships the facade-level shape (this slice), the facade tests
// (Slice 2), the HTTP surface (Slice 3) and the bun-backed Postgres
// repository (Slice 4). Thumbnail-related types and methods are deferred to
// Phase 5 — see the phase-2 spec's Out-of-Scope section for the rationale.
//
// The package depends on the standard library + internal/shared/bookcache +
// internal/shared/isbngateway + internal/accesscontrol. It does NOT depend
// on chi, bun, viper or any HTTP framework: those concerns live in
// internal/catalog/http (added in Slice 3) and in the composition root.
package catalog

import "fmt"

// BookId is the catalogue-internal identifier for a book. Named so call
// sites cannot accidentally swap it with a CopyId or an Isbn without a
// compiler complaint.
type BookId string

// CopyId is the catalogue-internal identifier for a physical copy of a book.
type CopyId string

// Isbn is the canonical ISBN string as the caller supplied it (after
// trimming and validation). Named for the same type-safety reason as
// BookId.
type Isbn string

// CopyStatus is the lifecycle state of a copy: AVAILABLE means it can be
// borrowed; UNAVAILABLE covers every "not on the shelf" reason (lent,
// reserved-not-yet-picked-up, damaged-in-process, etc.). The closed set
// matches the source TS CopyStatus union 1:1.
type CopyStatus string

// CopyStatus values. These are the only CopyStatus literals the rest of the
// codebase should reference — never raw string literals.
const (
	CopyStatusAvailable   CopyStatus = "AVAILABLE"
	CopyStatusUnavailable CopyStatus = "UNAVAILABLE"
)

// CopyCondition is the physical condition of a copy as recorded at
// registration time. The closed set matches the source TS CopyCondition
// union 1:1.
type CopyCondition string

// CopyCondition values.
const (
	CopyConditionNew  CopyCondition = "NEW"
	CopyConditionGood CopyCondition = "GOOD"
	CopyConditionFair CopyCondition = "FAIR"
	CopyConditionPoor CopyCondition = "POOR"
)

// NewBookDto is the inbound shape for adding a book. Title and Authors are
// optional from the caller's perspective — the ISBN gateway can fill them
// — but the struct always carries zero-values. The facade's enrichment
// step merges gateway-supplied fields into blank slots before schema
// validation.
type NewBookDto struct {
	Title   string
	Authors []string
	Isbn    Isbn
}

// UpdateBookDto is the inbound shape for patching a book. Pointer fields
// disambiguate "field absent" (nil) from "field present with empty value"
// (non-nil pointing at zero value). The schema parser rejects "both nil"
// as InvalidBookError. ISBN is intentionally absent: a book's ISBN is
// immutable; the HTTP DTO mapping layer rejects an inbound JSON body that
// carries an `isbn` field.
type UpdateBookDto struct {
	Title   *string
	Authors *[]string
}

// BookDto is the canonical persisted shape of a book. Phase 2 has no
// Thumbnail field; Phase 5 adds it as an optional pointer.
type BookDto struct {
	BookId  BookId
	Title   string
	Authors []string
	Isbn    Isbn
}

// NewCopyDto is the inbound shape for registering a copy of a known book.
// BookId is on the dto for parity with the source TS; the facade also
// receives it as a parameter so the route can place it in the URL.
type NewCopyDto struct {
	BookId    BookId
	Condition CopyCondition
}

// CopyDto is the canonical persisted shape of a copy.
type CopyDto struct {
	CopyId    CopyId
	BookId    BookId
	Condition CopyCondition
	Status    CopyStatus
}

// BookNotFoundError is returned when a lookup by BookId or by Isbn finds
// no record. Identifier is the raw string the caller supplied so the
// surfaced error names the missing thing.
type BookNotFoundError struct {
	Identifier string
}

// Error implements error on a pointer receiver so errors.As resolves
// *BookNotFoundError targets through wrapping layers.
func (e *BookNotFoundError) Error() string {
	return fmt.Sprintf("Book not found: %s", e.Identifier)
}

// CopyNotFoundError is returned when a lookup by CopyId finds no record.
type CopyNotFoundError struct {
	CopyId CopyId
}

func (e *CopyNotFoundError) Error() string {
	return fmt.Sprintf("Copy not found: %s", e.CopyId)
}

// DuplicateIsbnError is returned by AddBook when a book with the same
// (merged) ISBN already exists in the repository.
type DuplicateIsbnError struct {
	Isbn Isbn
}

func (e *DuplicateIsbnError) Error() string {
	return fmt.Sprintf("A book with ISBN %s already exists", e.Isbn)
}

// InvalidBookError is returned by the book-related parsers when the input
// fails validation. Reason is the validator's first failure message.
type InvalidBookError struct {
	Reason string
}

func (e *InvalidBookError) Error() string {
	return fmt.Sprintf("Invalid book: %s", e.Reason)
}

// InvalidCopyError is returned by ParseNewCopy when the input fails
// validation.
type InvalidCopyError struct {
	Reason string
}

func (e *InvalidCopyError) Error() string {
	return fmt.Sprintf("Invalid copy: %s", e.Reason)
}
