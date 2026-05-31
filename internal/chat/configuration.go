package chat

import (
	"log/slog"

	"github.com/akshayvadher/test-n-design-go/internal/shared/chatgateway"
)

// Overrides is the test-substitution extension point for the chat
// Facade. Every field is optional — a zero value means "use the
// default."
type Overrides struct {
	Gateway chatgateway.ChatGateway
	Logger  *slog.Logger
}

// NewFacadeWithOverrides constructs a Facade applying the supplied
// Overrides on top of the in-memory defaults. Defaults: a fresh
// InMemoryChatGateway and a discard logger.
func NewFacadeWithOverrides(o Overrides) *Facade {
	gateway := o.Gateway
	if gateway == nil {
		gateway = chatgateway.NewInMemoryChatGateway()
	}
	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return NewFacade(gateway, logger)
}
