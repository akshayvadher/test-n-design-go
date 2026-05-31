package http

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/chat"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// Deps is the input the composition root supplies to Wire. The
// Facade is constructed by the caller and passed in; the Logger is
// the same *slog.Logger every collaborator receives.
//
// This package owns the Wire seam (instead of internal/chat/module.go)
// because the handlers it dispatches to live here, and putting Wire in
// package chat would create an import cycle (chat → chat/http → chat).
type Deps struct {
	Facade *chat.Facade
	Logger *slog.Logger
}

// Wire mounts the chat module's HTTP routes onto r. POST /chat is the
// only endpoint: a streaming SSE response keyed off the validated
// request body.
func Wire(r chi.Router, deps Deps) {
	handlers := NewHandlers(deps.Facade, deps.Logger)
	r.Post("/chat", sharedhttp.Handle(handlers.StreamChat))
}
