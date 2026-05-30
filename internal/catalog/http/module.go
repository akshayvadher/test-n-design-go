package http

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// Deps is the input the composition root supplies to Wire. The Facade is
// constructed by the caller (cmd/library/main.go or
// internal/app/wiring.go) and passed in. The Logger is the same
// *slog.Logger every collaborator receives — no slog.Default() reads from
// inside business code.
//
// This package owns the Wire seam (instead of internal/catalog/module.go
// in package catalog) because the handlers it dispatches to live here.
// Putting Wire in package catalog would force catalog to import
// catalog/http, and catalog/http already imports catalog for the Facade
// and the domain DTOs — that would be an import cycle. The composition
// root simply imports `cataloghttp` and calls `cataloghttp.Wire(...)`.
type Deps struct {
	Facade *catalog.Facade
	Logger *slog.Logger
}

// Wire mounts the catalog module's HTTP routes onto r. Route paths match
// the source TS API contract verbatim — including the use of {isbn} (not
// {bookId}) for the FindBook lookup so callers can look up a book by the
// natural identifier they typed at the keyboard.
//
// The route table is intentionally explicit; no for-loops over a slice of
// (method, path, handler) triples. The straight-line form makes it
// trivially auditable against the spec and against the .http file.
func Wire(r chi.Router, deps Deps) {
	handlers := NewHandlers(deps.Facade, deps.Logger)

	r.Post("/books", sharedhttp.Handle(handlers.AddBook))
	r.Get("/books", sharedhttp.Handle(handlers.ListBooks))
	r.Get("/books/{isbn}", sharedhttp.Handle(handlers.FindBook))
	r.Patch("/books/{bookId}", sharedhttp.Handle(handlers.UpdateBook))
	r.Delete("/books/{bookId}", sharedhttp.Handle(handlers.DeleteBook))
	r.Post("/books/{bookId}/copies", sharedhttp.Handle(handlers.RegisterCopy))
	r.Patch("/copies/{copyId}/available", sharedhttp.Handle(handlers.MarkCopyAvailable))
	r.Patch("/copies/{copyId}/unavailable", sharedhttp.Handle(handlers.MarkCopyUnavailable))
}
