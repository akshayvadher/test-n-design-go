// Package chat is the bounded-context module for the library's chat
// surface. The Facade is the only public surface other modules or the
// HTTP layer should depend on; the schema parser and the sample-data
// builders are exported only so composition-root wiring and
// same-package tests can reference them.
//
// Chat is a leaf module: stateless, no DB, no events, no
// TransactionalContext integration. It depends on the standard library
// + log/slog + internal/shared/chatgateway. It does NOT depend on chi:
// that concern lives in internal/chat/http and the composition root.
package chat

import "fmt"

// Frame type discriminants for the streaming response. The SSE handler
// renders these verbatim as the `event:` line value, so they MUST be
// stable strings that match the TS source's discriminator vocabulary.
const (
	FrameTypeDelta = "delta"
	FrameTypeError = "error"
	FrameTypeDone  = "done"
)

// ChatMessage is one turn of the conversation. Field order and JSON-tag
// shape (when projected through the HTTP DTO) match the TS source
// (`role`, then `content`).
type ChatMessage struct {
	Role    string
	Content string
}

// ChatRequest is the validated facade-level request DTO. Schema rules
// live in ParseChatRequest.
type ChatRequest struct {
	Messages []ChatMessage
}

// ChatFrame is one streamed frame written to the SSE response. Type
// discriminates delta / error / done. Content carries the textual token
// for delta frames; Err carries the failure cause for error frames.
// Done frames carry neither.
type ChatFrame struct {
	Type    string
	Content string
	Err     error
}

// InvalidChatRequestError is returned by ParseChatRequest when the
// input fails validation. Reason is the first validator complaint.
// Pointer-receiver so errors.As resolves *InvalidChatRequestError
// through wrapping layers.
type InvalidChatRequestError struct {
	Reason string
}

func (e *InvalidChatRequestError) Error() string {
	return fmt.Sprintf("Invalid chat request: %s", e.Reason)
}

// ChatGatewayError wraps a setup-time gateway failure (the gateway
// returned a non-nil error from Stream BEFORE any deltas were emitted).
// Per-token gateway errors are streamed in-band as error frames and do
// NOT surface through this type.
type ChatGatewayError struct {
	Cause error
}

func (e *ChatGatewayError) Error() string {
	return fmt.Sprintf("Chat gateway error: %v", e.Cause)
}

// Unwrap exposes the underlying cause so errors.Is / errors.As walk
// through the wrapper.
func (e *ChatGatewayError) Unwrap() error {
	return e.Cause
}
