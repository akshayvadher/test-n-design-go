package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/akshayvadher/test-n-design-go/internal/chat"
)

// Handlers is the bundle of chi-bound endpoint functions for the
// chat module.
type Handlers struct {
	facade *chat.Facade
	logger *slog.Logger
}

// NewHandlers constructs a Handlers bundle.
func NewHandlers(facade *chat.Facade, logger *slog.Logger) *Handlers {
	return &Handlers{facade: facade, logger: logger}
}

// StreamChat decodes the request, opens a streaming chat completion via
// the facade, and renders each ChatFrame as one SSE event. Pre-stream
// errors (decode failure, validation failure, gateway setup failure)
// return through the error return so sharedhttp.Handle / the
// DomainErrorMiddleware can translate them into JSON error responses.
// Once the SSE stream has begun, errors are written into the stream as
// error frames and StreamChat returns nil (the HTTP response has
// already been committed at that point).
func (h *Handlers) StreamChat(w http.ResponseWriter, r *http.Request) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming unsupported: ResponseWriter is not an http.Flusher")
	}

	req, err := decodeChatRequest(r.Body)
	if err != nil {
		return &chat.InvalidChatRequestError{Reason: err.Error()}
	}

	frames, err := h.facade.Stream(r.Context(), toFacadeRequest(req))
	if err != nil {
		return err
	}

	writeSSEHeaders(w)
	flusher.Flush()

	h.pumpFrames(r.Context(), w, flusher, frames, r.RemoteAddr)
	return nil
}

// pumpFrames writes one SSE event per frame and flushes after each. A
// write error closes the loop early (the client has typically gone
// away); the surrounding facade goroutine will notice the cancelled
// context and stop emitting.
func (h *Handlers) pumpFrames(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	frames <-chan chat.ChatFrame,
	remoteAddr string,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			if err := writeSSEFrame(w, frame); err != nil {
				h.logger.Info("chat stream write failed",
					slog.String("error", err.Error()),
					slog.String("remote_addr", remoteAddr),
				)
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSEHeaders sets the headers required for a server-sent-events
// stream. X-Accel-Buffering: no instructs nginx (and many reverse
// proxies) not to buffer the response.
func writeSSEHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
}

// writeSSEFrame renders one ChatFrame as a two-line SSE event: an
// `event:` line carrying the frame type and a `data:` line carrying a
// JSON-encoded payload. JSON encoding the data is safe across
// newlines, quotes, and unicode — raw OpenAI deltas can include any of
// them. Each frame ends with a blank line per the SSE protocol.
func writeSSEFrame(w io.Writer, frame chat.ChatFrame) error {
	data, err := json.Marshal(frameData(frame))
	if err != nil {
		return fmt.Errorf("marshal frame data: %w", err)
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", frame.Type, data)
	return err
}

// frameData projects a ChatFrame onto the wire payload. Delta frames
// carry { "content": "..." }; error frames carry { "message": "..." };
// done frames carry an empty object.
func frameData(frame chat.ChatFrame) any {
	switch frame.Type {
	case chat.FrameTypeDelta:
		return map[string]string{"content": frame.Content}
	case chat.FrameTypeError:
		return map[string]string{"message": errorMessage(frame.Err)}
	default:
		return map[string]any{}
	}
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// decodeChatRequest parses the body into a ChatRequest with
// DisallowUnknownFields so unrecognised properties surface as
// validation errors rather than being silently ignored.
func decodeChatRequest(body io.Reader) (ChatRequest, error) {
	var req ChatRequest
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return ChatRequest{}, err
	}
	return req, nil
}

// toFacadeRequest maps the HTTP DTO to the facade DTO. The structs
// share a shape; the indirection enforces the boundary.
func toFacadeRequest(req ChatRequest) chat.ChatRequest {
	messages := make([]chat.ChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = chat.ChatMessage{Role: m.Role, Content: m.Content}
	}
	return chat.ChatRequest{Messages: messages}
}
