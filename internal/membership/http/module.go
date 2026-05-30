package http

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/membership"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// Deps is the input the composition root supplies to Wire. The Facade is
// constructed by the caller (cmd/library/main.go or
// internal/app/wiring.go) and passed in.
type Deps struct {
	Facade *membership.Facade
	Logger *slog.Logger
}

// Wire mounts the membership module's HTTP routes onto r. Route paths
// match the source TS API contract verbatim.
func Wire(r chi.Router, deps Deps) {
	handlers := NewHandlers(deps.Facade, deps.Logger)

	r.Post("/members", sharedhttp.Handle(handlers.RegisterMember))
	r.Get("/members/{id}", sharedhttp.Handle(handlers.FindMember))
	r.Patch("/members/{id}/suspend", sharedhttp.Handle(handlers.Suspend))
	r.Patch("/members/{id}/reactivate", sharedhttp.Handle(handlers.Reactivate))
	r.Patch("/members/{id}/tier", sharedhttp.Handle(handlers.UpgradeTier))
	r.Get("/members/{id}/eligibility", sharedhttp.Handle(handlers.CheckEligibility))
}
