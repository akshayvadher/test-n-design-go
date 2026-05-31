// Package categories is the bounded-context module for the library's
// curated set of book categories. The Facade is the only public surface
// other modules or the HTTP layer should depend on; the repository, the
// in-memory adapter, the schema parser and the sample-data builders are
// exported only so composition-root wiring and same-package tests can
// reference them.
//
// Categories is the simplest module in the codebase: no cross-module
// dependencies, no events, no transactional integration. It ships
// vertically in one slice — facade, repos, HTTP surface, integration
// test — because the surface area is small and there is no new
// architectural ground to break.
//
// The package depends on the standard library + log/slog + bun + uuid
// only. It does NOT depend on chi: that concern lives in
// internal/categories/http and in the composition root.
package categories

import (
	"fmt"
	"time"
)

// CategoryId is the categories-internal identifier for a category. Named
// so call sites cannot accidentally swap it with another string-keyed
// identifier (BookId, MemberId, …) without a compiler complaint.
type CategoryId string

// CategoryDto is the canonical persisted shape of a category. The Go
// port keeps the field name CategoryId — even though the TS source
// names it `id` — so the <Module>Id newtype pattern reads consistently
// across modules. The HTTP DTO maps to the wire key `"id"` to preserve
// API compatibility.
type CategoryDto struct {
	CategoryId CategoryId
	Name       string
	CreatedAt  time.Time
}

// CategoryNotFoundError is returned when a lookup by CategoryId (or any
// other identifier the facade understands) finds no record. Identifier
// is the raw string the caller supplied so the surfaced error names the
// missing thing — matching the TS source's single-string constructor.
type CategoryNotFoundError struct {
	Identifier string
}

// Error implements error on a pointer receiver so errors.As resolves
// *CategoryNotFoundError targets through wrapping layers.
func (e *CategoryNotFoundError) Error() string {
	return fmt.Sprintf("Category not found: %s", e.Identifier)
}

// DuplicateCategoryError is returned by Save / CreateCategory when a
// second category attempts to share the same Name (case-insensitive at
// both the in-memory and the database layer).
type DuplicateCategoryError struct {
	Name string
}

func (e *DuplicateCategoryError) Error() string {
	return fmt.Sprintf("A category with name %s already exists", e.Name)
}

// InvalidCategoryError is returned by ParseNewCategory when the input
// fails validation. Reason is the first validator complaint.
type InvalidCategoryError struct {
	Reason string
}

func (e *InvalidCategoryError) Error() string {
	return fmt.Sprintf("Invalid category: %s", e.Reason)
}

// InvalidCategoriesQueryError is returned by ParseStartsWith when the
// `startsWith` query parameter is missing or blank.
type InvalidCategoriesQueryError struct {
	Reason string
}

func (e *InvalidCategoriesQueryError) Error() string {
	return fmt.Sprintf("Invalid categories query: %s", e.Reason)
}
