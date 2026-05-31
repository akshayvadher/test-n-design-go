package http

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/categories"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// Deps is the input the composition root supplies to Wire. The Facade
// is constructed by the caller (internal/app/wiring.go) and passed in.
// The Logger is the same *slog.Logger every collaborator receives.
//
// This package owns the Wire seam (instead of internal/categories/module.go)
// because the handlers it dispatches to live here, and putting Wire in
// package categories would create an import cycle
// (categories → categories/http → categories).
type Deps struct {
	Facade *categories.Facade
	Logger *slog.Logger
}

// Wire mounts the categories module's HTTP routes onto r. Route paths
// match the source TS API contract verbatim.
//
// Note the ordering: GET /categories (the prefix search) is registered
// before GET /categories/{id} so chi's tree dispatches the literal
// route to ListByPrefix and the variable route to FindCategoryById.
// chi's router is path-segment-based so this is for readability, not
// for correctness — either order works.
func Wire(r chi.Router, deps Deps) {
	handlers := NewHandlers(deps.Facade, deps.Logger)

	r.Post("/categories", sharedhttp.Handle(handlers.CreateCategory))
	r.Get("/categories", sharedhttp.Handle(handlers.ListByPrefix))
	r.Get("/categories/{id}", sharedhttp.Handle(handlers.FindCategoryById))
}
