// handlers_test.go is the HTTP-level spec for the chat module. It uses
// httptest.NewServer (NOT httptest.NewRecorder, which does not satisfy
// http.Flusher correctly for streaming bodies) and a real HTTP client
// reading the response body line by line.
//
// Stdlib testing only — no testify, no mock library.
package http

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/chat"
	"github.com/akshayvadher/test-n-design-go/internal/shared/chatgateway"
	chatgatewaymemory "github.com/akshayvadher/test-n-design-go/internal/shared/chatgateway/memory"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// -----------------------------------------------------------------------------
// Happy path streaming
// -----------------------------------------------------------------------------

func TestStreamChat_EmitsDeltaFramesThenDone(t *testing.T) {
	srv := buildServer(t, chatgatewaymemory.NewGateway())

	frames := postChat(t, srv, `{"messages":[{"role":"user","content":"hello world"}]}`)

	want := []sseFrame{
		{Event: chat.FrameTypeDelta, Data: `{"content":"hello "}`},
		{Event: chat.FrameTypeDelta, Data: `{"content":"world "}`},
		{Event: chat.FrameTypeDone, Data: `{}`},
	}
	assertFramesEqual(t, frames, want)
}

func TestStreamChat_SetsSSEHeaders(t *testing.T) {
	srv := buildServer(t, chatgatewaymemory.NewGateway())

	resp := mustPost(t, srv, `{"messages":[{"role":"user","content":"hi"}]}`)
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status: got %d, want %d", got, want)
	}
	expected := map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	}
	for header, want := range expected {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("header %s: got %q, want %q", header, got, want)
		}
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("drain body: %v", err)
	}
}

func TestStreamChat_AssistantHistoryUsesLastMessage(t *testing.T) {
	srv := buildServer(t, chatgatewaymemory.NewGateway())

	body := `{"messages":[` +
		`{"role":"user","content":"ignored"},` +
		`{"role":"assistant","content":"also ignored"},` +
		`{"role":"user","content":"echo me"}` +
		`]}`
	frames := postChat(t, srv, body)

	want := []sseFrame{
		{Event: chat.FrameTypeDelta, Data: `{"content":"echo "}`},
		{Event: chat.FrameTypeDelta, Data: `{"content":"me "}`},
		{Event: chat.FrameTypeDone, Data: `{}`},
	}
	assertFramesEqual(t, frames, want)
}

// -----------------------------------------------------------------------------
// Error path framing — pre-stream errors are JSON envelopes, not SSE.
// -----------------------------------------------------------------------------

func TestStreamChat_InvalidJSONReturns400(t *testing.T) {
	srv := buildServer(t, chatgatewaymemory.NewGateway())
	assertErrorResponse(t, srv, `{not json`, http.StatusBadRequest, "invalid_chat_request")
}

func TestStreamChat_EmptyMessagesReturns400(t *testing.T) {
	srv := buildServer(t, chatgatewaymemory.NewGateway())
	assertErrorResponse(t, srv, `{"messages":[]}`, http.StatusBadRequest, "invalid_chat_request")
}

func TestStreamChat_InvalidRoleReturns400(t *testing.T) {
	srv := buildServer(t, chatgatewaymemory.NewGateway())
	assertErrorResponse(t, srv, `{"messages":[{"role":"robot","content":"hi"}]}`, http.StatusBadRequest, "invalid_chat_request")
}

func TestStreamChat_UnknownFieldReturns400(t *testing.T) {
	srv := buildServer(t, chatgatewaymemory.NewGateway())
	body := `{"messages":[{"role":"user","content":"hi"}],"system_prompt":"ignored"}`
	assertErrorResponse(t, srv, body, http.StatusBadRequest, "invalid_chat_request")
}

func TestStreamChat_GatewaySetupErrorReturns502(t *testing.T) {
	gateway := &throwingChatGateway{setupErr: errors.New("simulated setup failure")}
	srv := buildServer(t, gateway)
	assertErrorResponse(t, srv, `{"messages":[{"role":"user","content":"hi"}]}`, http.StatusBadGateway, "chat_gateway_error")
}

func TestStreamChat_PerTokenGatewayErrorRendersErrorFrame(t *testing.T) {
	gateway := &throwingChatGateway{streamErr: errors.New("midway")}
	srv := buildServer(t, gateway)

	frames := postChat(t, srv, `{"messages":[{"role":"user","content":"hi"}]}`)

	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d (%+v)", len(frames), frames)
	}
	if frames[0].Event != chat.FrameTypeError {
		t.Fatalf("event: got %q, want %q", frames[0].Event, chat.FrameTypeError)
	}
	if frames[0].Data != `{"message":"midway"}` {
		t.Fatalf("data: got %q, want %q", frames[0].Data, `{"message":"midway"}`)
	}
}

// -----------------------------------------------------------------------------
// Server helper
// -----------------------------------------------------------------------------

func buildServer(t *testing.T, gateway chatgateway.ChatGateway) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	facade := chat.NewFacadeWithOverrides(chat.Overrides{Gateway: gateway, Logger: logger})

	registry := sharedhttp.NewDomainErrorRegistry()
	registry.Register(&chat.InvalidChatRequestError{}, http.StatusBadRequest, "invalid_chat_request")
	registry.Register(&chat.ChatGatewayError{}, http.StatusBadGateway, "chat_gateway_error")
	registry.Register(&chatgateway.EmptyMessagesError{}, http.StatusBadRequest, "empty_messages")

	router := chi.NewRouter()
	router.Use(sharedhttp.DomainErrorMiddleware(registry, logger))
	Wire(router, Deps{Facade: facade, Logger: logger})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

func mustPost(t *testing.T, srv *httptest.Server, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/chat", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	return resp
}

func postChat(t *testing.T, srv *httptest.Server, body string) []sseFrame {
	t.Helper()
	resp := mustPost(t, srv, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d, want 200 (body: %s)", resp.StatusCode, buf)
	}
	return readSSEStream(t, resp.Body)
}

func assertErrorResponse(t *testing.T, srv *httptest.Server, body string, wantStatus int, wantCode string) {
	t.Helper()
	resp := mustPost(t, srv, body)
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d, want %d (body: %s)", resp.StatusCode, wantStatus, buf)
	}
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Error != wantCode {
		t.Fatalf("error code: got %q, want %q", envelope.Error, wantCode)
	}
}

// -----------------------------------------------------------------------------
// SSE parser
// -----------------------------------------------------------------------------

type sseFrame struct {
	Event string
	Data  string
}

// readSSEStream reads frames in the form
//
//	event: <type>
//	data: <payload>
//	<blank line>
//
// from r until EOF. Multi-line `data:` is not supported (the handler
// emits single-line data only). Each frame is captured once the blank
// terminator is seen.
func readSSEStream(t *testing.T, r io.Reader) []sseFrame {
	t.Helper()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		out     []sseFrame
		current sseFrame
		hasAny  bool
	)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if hasAny {
				out = append(out, current)
				current = sseFrame{}
				hasAny = false
			}
		case strings.HasPrefix(line, "event: "):
			current.Event = strings.TrimPrefix(line, "event: ")
			hasAny = true
		case strings.HasPrefix(line, "data: "):
			current.Data = strings.TrimPrefix(line, "data: ")
			hasAny = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan sse: %v", err)
	}
	if hasAny {
		out = append(out, current)
	}
	return out
}

func assertFramesEqual(t *testing.T, got, want []sseFrame) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("frame count: got %d (%+v), want %d (%+v)", len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("frame[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// -----------------------------------------------------------------------------
// throwingChatGateway — spec-local fault injector.
// -----------------------------------------------------------------------------

type throwingChatGateway struct {
	setupErr  error
	streamErr error
}

func (g *throwingChatGateway) Stream(_ context.Context, _ []chatgateway.ChatMessage) (<-chan chatgateway.ChatDelta, error) {
	if g.setupErr != nil {
		return nil, g.setupErr
	}
	ch := make(chan chatgateway.ChatDelta, 1)
	if g.streamErr != nil {
		ch <- chatgateway.ChatDelta{Err: g.streamErr}
	}
	close(ch)
	return ch, nil
}
