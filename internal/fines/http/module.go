package http

import (
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/fines"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// Deps is the input the composition root supplies to Wire. The Facade is
// constructed by the caller (internal/app/wiring.go) and passed in. The
// Logger is the same *slog.Logger every collaborator receives. Clock is
// the wall-clock function the AssessFinesFor / ProcessOverdueLoans
// handlers read `now` through.
//
// This package owns the Wire seam (instead of internal/fines/module.go)
// because the handlers it dispatches to live here, and putting Wire in
// package fines would create an import cycle (fines → fines/http →
// fines).
type Deps struct {
	Facade *fines.Facade
	Logger *slog.Logger
	Clock  func() time.Time
}

// Wire mounts the fines module's HTTP routes onto r. Route paths match
// the source TS API contract verbatim.
func Wire(r chi.Router, deps Deps) {
	handlers := NewHandlers(deps.Facade, deps.Logger, deps.Clock)

	r.Post("/members/{memberId}/fines/assessments", sharedhttp.Handle(handlers.AssessFinesFor))
	r.Post("/fines/batch/process", sharedhttp.Handle(handlers.ProcessOverdueLoans))
	r.Get("/members/{memberId}/fines", sharedhttp.Handle(handlers.ListFinesFor))
	r.Get("/fines/{fineId}", sharedhttp.Handle(handlers.FindFine))
	r.Patch("/fines/{fineId}/paid", sharedhttp.Handle(handlers.PayFine))
}
