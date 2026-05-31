// Package chatgateway is the outbound port the chat module uses to stream a
// chat-completion response from an underlying LLM. The package is consumed
// only by the chat module — any other module that wants chat capabilities
// routes through chat.Facade, not this port.
//
// The contract: ChatGateway.Stream returns a receive-only channel of
// ChatDelta values. Implementations MUST close the returned channel when
// the stream ends (normally or on error). A per-token error is delivered
// in-band via ChatDelta.Err — the implementation writes one delta carrying
// the error and then closes the channel. Callers MUST honour ctx
// cancellation by ranging the channel until it closes; the implementation
// is responsible for noticing ctx.Done() and tearing down its background
// goroutine.
//
// Setup errors (empty messages list, malformed configuration) are returned
// through the error return of Stream — no channel is opened in that case.
package chatgateway

import "context"

// Role constants mirror the OpenAI vocabulary so the values flow through
// to the wire format unchanged.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

// ChatMessage is one turn of a chat conversation. Field order matches the
// TS source 1:1 (role, then content).
type ChatMessage struct {
	Role    string
	Content string
}

// ChatDelta is one streamed chunk from the gateway. A non-nil Err signals
// that the stream truncated mid-flight — Content is empty in that case and
// the channel closes immediately after the error delta.
type ChatDelta struct {
	Content string
	Err     error
}

// ChatGateway streams response deltas for a multi-turn chat completion.
type ChatGateway interface {
	Stream(ctx context.Context, messages []ChatMessage) (<-chan ChatDelta, error)
}

// EmptyMessagesError is returned by Stream when the messages slice is
// empty. Maps to HTTP 400 via the domain-error registry.
type EmptyMessagesError struct{}

// Error implements error on a pointer receiver so errors.As resolves
// *EmptyMessagesError targets through wrapping layers.
func (*EmptyMessagesError) Error() string {
	return "chat gateway: messages must be non-empty"
}
