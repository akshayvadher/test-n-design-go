package chat

import (
	"context"
	"log/slog"

	"github.com/akshayvadher/test-n-design-go/internal/shared/chatgateway"
)

// frameBufferSize is the outer-channel buffer size. Locked at 8: small
// enough to backpressure on a slow HTTP client, large enough that
// single-digit-token gusts from the gateway don't block the forwarding
// goroutine.
const frameBufferSize = 8

// Facade is the chat module's only public surface. It validates the
// inbound request, delegates to the underlying ChatGateway, and
// projects the gateway's ChatDelta stream onto a ChatFrame stream the
// HTTP layer can render as SSE.
type Facade struct {
	gateway chatgateway.ChatGateway
	logger  *slog.Logger
}

// NewFacade wires the Facade with explicit dependencies. The
// composition root passes the concrete implementations; tests use
// NewFacadeWithOverrides to apply in-memory defaults.
func NewFacade(gateway chatgateway.ChatGateway, logger *slog.Logger) *Facade {
	return &Facade{gateway: gateway, logger: logger}
}

// Stream validates the request, opens a streaming chat completion via
// the gateway, and returns a channel of ChatFrame ending in a single
// Done frame on success or a single Error frame on per-token gateway
// failure. Setup-time gateway errors (auth failure, network
// unreachable) return through the error return wrapped in
// *ChatGatewayError — no channel is opened in that case.
//
// The returned channel is buffered (size frameBufferSize). Cancelling
// ctx causes the forwarding goroutine to stop after the next delta;
// the channel closes shortly thereafter. Done frames are best-effort —
// if ctx is cancelled before the gateway completes, no Done frame is
// emitted.
func (f *Facade) Stream(ctx context.Context, req ChatRequest) (<-chan ChatFrame, error) {
	parsed, err := ParseChatRequest(req.Messages)
	if err != nil {
		return nil, err
	}
	deltas, err := f.gateway.Stream(ctx, toGatewayMessages(parsed.Messages))
	if err != nil {
		return nil, &ChatGatewayError{Cause: err}
	}

	frames := make(chan ChatFrame, frameBufferSize)
	go forwardFrames(ctx, deltas, frames)
	return frames, nil
}

// forwardFrames pumps deltas onto the frames channel, translating
// per-token errors into a terminal Error frame. A clean stream ends
// with a single Done frame. The function honours ctx cancellation by
// returning without writing further frames; the outer channel is
// always closed before the goroutine returns.
func forwardFrames(ctx context.Context, deltas <-chan chatgateway.ChatDelta, frames chan<- ChatFrame) {
	defer close(frames)
	for delta := range deltas {
		if delta.Err != nil {
			emit(ctx, frames, ChatFrame{Type: FrameTypeError, Err: delta.Err})
			return
		}
		if !emit(ctx, frames, ChatFrame{Type: FrameTypeDelta, Content: delta.Content}) {
			return
		}
	}
	emit(ctx, frames, ChatFrame{Type: FrameTypeDone})
}

// emit writes frame to frames or returns false if ctx is cancelled.
func emit(ctx context.Context, frames chan<- ChatFrame, frame ChatFrame) bool {
	select {
	case <-ctx.Done():
		return false
	case frames <- frame:
		return true
	}
}

// toGatewayMessages maps the facade-level ChatMessage slice onto the
// gateway's ChatMessage slice. The two structs share a shape — the
// indirection enforces the module boundary: chat-facade callers do not
// import chatgateway.
func toGatewayMessages(messages []ChatMessage) []chatgateway.ChatMessage {
	out := make([]chatgateway.ChatMessage, len(messages))
	for i, m := range messages {
		out[i] = chatgateway.ChatMessage{Role: m.Role, Content: m.Content}
	}
	return out
}
