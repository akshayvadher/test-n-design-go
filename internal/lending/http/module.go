package http

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/lending"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// Deps is the input the composition root supplies to Wire. The Facade is
// constructed by the caller (cmd/library/main.go or
// internal/app/wiring.go) and passed in. The Logger is the same
// *slog.Logger every collaborator receives.
//
// This package owns the Wire seam (instead of internal/lending/module.go)
// because the handlers it dispatches to live here, and putting Wire in
// package lending would create an import cycle (lending → lending/http →
// lending).
type Deps struct {
	Facade *lending.Facade
	Logger *slog.Logger
}

// Wire mounts the lending module's HTTP routes onto r. Route paths match
// the source TS API contract verbatim. Phase 3 ships only the three write
// flows; listing endpoints (overdue loans, member loans) are deferred to
// Phase 4 where fines + the saga consumer actually call them.
func Wire(r chi.Router, deps Deps) {
	handlers := NewHandlers(deps.Facade, deps.Logger)

	r.Post("/loans", sharedhttp.Handle(handlers.Borrow))
	r.Patch("/loans/{loanId}/return", sharedhttp.Handle(handlers.ReturnLoan))
	r.Post("/reservations", sharedhttp.Handle(handlers.Reserve))
}
