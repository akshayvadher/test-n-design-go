// middleware_test.go exercises the Phase-1 middleware contract end-to-end
// against an httptest.NewRecorder + a real chi router + a real registry. No
// mocks, no testify — just stdlib testing, errors.As, and a synthetic error
// type declared locally so the test file has no dependency on the
// accesscontrol module (per BOUNDARIES.md — shared/http NEVER imports
// business modules).
//
// The ACs covered (Slice 4):
//
//   - registered error → registered status + code in JSON body
//   - wrapped registered error still matches via errors.As
//   - unregistered error → 500 + internal_error, raw message NOT in body
//   - nil return → middleware does nothing (response untouched)
//   - panic in handler → Recoverer turns it into 500 + internal_error
package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// Synthetic error types — declared locally so the test file owns no business
// imports. These mirror the shape of accesscontrol.UnauthorizedRoleError /
// UnknownActionError but are intentionally unrelated types so a test failure
// here means a middleware bug, not an accesscontrol bug.
// -----------------------------------------------------------------------------

type sampleDomainError struct {
	Detail string
}

func (e *sampleDomainError) Error() string {
	return "sample domain error: " + e.Detail
}

type otherDomainError struct{}

func (e *otherDomainError) Error() string {
	return "other domain error"
}

// wrappedError lets us prove errors.As walks the chain — the middleware
// must match a sampleDomainError even when it's hidden behind a wrap.
type wrappedError struct {
	inner error
}

func (w *wrappedError) Error() string {
	return "wrapped: " + w.inner.Error()
}

func (w *wrappedError) Unwrap() error {
	return w.inner
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// newTestLogger returns a slog.Logger writing to the provided buffer so
// tests can assert on log output without touching stdout. Level is set to
// debug so every line emitted by production code is captured.
func newTestLogger(buf io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// newTestRegistry returns a registry pre-loaded with one sample mapping.
// Tests that need an empty registry construct one directly via
// NewDomainErrorRegistry().
func newTestRegistry() *DomainErrorRegistry {
	reg := NewDomainErrorRegistry()
	reg.Register(&sampleDomainError{}, http.StatusForbidden, "sample_forbidden")
	return reg
}

// wrapWithMiddleware runs the handler through DomainErrorMiddleware and
// returns the recorded response. Each test sets up its own chain explicitly
// — we don't share router state between tests.
func wrapWithMiddleware(t *testing.T, registry *DomainErrorRegistry, logger *slog.Logger, h http.Handler) *http.Response {
	t.Helper()
	wrapped := DomainErrorMiddleware(registry, logger)(h)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	return rec.Result()
}

// decodeErrorResponse reads the response body into an ErrorResponse for
// field-by-field assertions. Tests treat a decode error as fatal.
func decodeErrorResponse(t *testing.T, body io.ReadCloser) ErrorResponse {
	t.Helper()
	defer body.Close()
	var out ErrorResponse
	if err := json.NewDecoder(body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func TestDomainErrorMiddleware_RegisteredError_WritesStatusAndCode(t *testing.T) {
	registry := newTestRegistry()
	var logBuf bytes.Buffer
	logger := newTestLogger(&logBuf)

	handler := Handle(func(w http.ResponseWriter, r *http.Request) error {
		return &sampleDomainError{Detail: "missing-thing"}
	})

	resp := wrapWithMiddleware(t, registry, logger, handler)
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusForbidden; got != want {
		t.Errorf("status: got %d, want %d", got, want)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q, want application/json prefix", ct)
	}

	body := decodeErrorResponse(t, resp.Body)
	if body.Error != "sample_forbidden" {
		t.Errorf("error code: got %q, want %q", body.Error, "sample_forbidden")
	}
	if !strings.Contains(body.Message, "missing-thing") {
		t.Errorf("message: got %q, want it to contain %q", body.Message, "missing-thing")
	}
}

func TestDomainErrorMiddleware_WrappedRegisteredError_MatchesViaErrorsAs(t *testing.T) {
	registry := newTestRegistry()
	logger := newTestLogger(io.Discard)

	handler := Handle(func(w http.ResponseWriter, r *http.Request) error {
		return fmt.Errorf("outer context: %w", &wrappedError{inner: &sampleDomainError{Detail: "deep"}})
	})

	resp := wrapWithMiddleware(t, registry, logger, handler)
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusForbidden; got != want {
		t.Errorf("status: got %d, want %d", got, want)
	}
	body := decodeErrorResponse(t, resp.Body)
	if body.Error != "sample_forbidden" {
		t.Errorf("error code: got %q, want %q (errors.As walk failed)", body.Error, "sample_forbidden")
	}
}

func TestDomainErrorMiddleware_UnregisteredError_WritesInternalErrorAndLogsRaw(t *testing.T) {
	registry := newTestRegistry()
	var logBuf bytes.Buffer
	logger := newTestLogger(&logBuf)

	rawMessage := "secret-internals: file=/etc/shadow"
	handler := Handle(func(w http.ResponseWriter, r *http.Request) error {
		return errors.New(rawMessage)
	})

	resp := wrapWithMiddleware(t, registry, logger, handler)
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusInternalServerError; got != want {
		t.Errorf("status: got %d, want %d", got, want)
	}

	body := decodeErrorResponse(t, resp.Body)
	if body.Error != "internal_error" {
		t.Errorf("error code: got %q, want %q", body.Error, "internal_error")
	}
	if body.Message != "internal server error" {
		t.Errorf("message: got %q, want %q", body.Message, "internal server error")
	}
	if strings.Contains(body.Message, rawMessage) {
		t.Errorf("body leaked raw error: %q", body.Message)
	}

	if !strings.Contains(logBuf.String(), rawMessage) {
		t.Errorf("expected raw error %q in log output, got %q", rawMessage, logBuf.String())
	}
}

func TestDomainErrorMiddleware_NilError_DoesNotTouchResponse(t *testing.T) {
	registry := newTestRegistry()
	logger := newTestLogger(io.Discard)

	const handlerBody = `{"hello":"world"}`
	handler := Handle(func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, handlerBody)
		return nil
	})

	resp := wrapWithMiddleware(t, registry, logger, handler)
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusTeapot; got != want {
		t.Errorf("status: got %d, want %d (middleware overwrote handler response)", got, want)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(bodyBytes) != handlerBody {
		t.Errorf("body: got %q, want %q", string(bodyBytes), handlerBody)
	}
}

func TestMiddlewares_RecovererCatchesPanic(t *testing.T) {
	registry := newTestRegistry()
	var logBuf bytes.Buffer
	logger := newTestLogger(&logBuf)

	// Build the full Middlewares() stack + DomainErrorMiddleware. The
	// Recoverer must be upstream of the panicking handler so the panic is
	// converted into a 500 response before it reaches the test goroutine.
	stack := Middlewares(logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	wrapped := http.Handler(handler)
	wrapped = DomainErrorMiddleware(registry, logger)(wrapped)
	// Apply middlewares in reverse so the outermost ends up first in the call chain.
	for i := len(stack) - 1; i >= 0; i-- {
		wrapped = stack[i](wrapped)
	}

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusInternalServerError; got != want {
		t.Errorf("status: got %d, want %d", got, want)
	}
}

func TestMiddlewares_ReturnsStackInOrder(t *testing.T) {
	logger := newTestLogger(io.Discard)
	stack := Middlewares(logger)
	if got, want := len(stack), 4; got != want {
		t.Fatalf("stack length: got %d, want %d", got, want)
	}
}

func TestMiddlewares_LoggerEmitsStructuredFields(t *testing.T) {
	var logBuf bytes.Buffer
	logger := newTestLogger(&logBuf)
	stack := Middlewares(logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "ok")
	})

	wrapped := http.Handler(handler)
	for i := len(stack) - 1; i >= 0; i-- {
		wrapped = stack[i](wrapped)
	}

	req := httptest.NewRequest(http.MethodGet, "/things", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	logLine := logBuf.String()
	for _, field := range []string{`"method"`, `"path"`, `"status"`, `"bytes"`, `"duration_ms"`, `"request_id"`, `"remote_ip"`} {
		if !strings.Contains(logLine, field) {
			t.Errorf("log line missing field %s — got %q", field, logLine)
		}
	}
	if !strings.Contains(logLine, `"status":202`) {
		t.Errorf("expected status 202 in log line, got %q", logLine)
	}
}

func TestDomainErrorRegistry_RegisterAndLookup_DifferentTypesIsolated(t *testing.T) {
	reg := NewDomainErrorRegistry()
	reg.Register(&sampleDomainError{}, http.StatusForbidden, "sample_forbidden")
	reg.Register(&otherDomainError{}, http.StatusBadRequest, "other_bad")

	status, code, ok := reg.Lookup(&otherDomainError{})
	if !ok {
		t.Fatalf("expected lookup of otherDomainError to match")
	}
	if status != http.StatusBadRequest || code != "other_bad" {
		t.Errorf("otherDomainError: got status=%d code=%q, want 400 other_bad", status, code)
	}

	status, code, ok = reg.Lookup(&sampleDomainError{Detail: "x"})
	if !ok {
		t.Fatalf("expected lookup of sampleDomainError to match")
	}
	if status != http.StatusForbidden || code != "sample_forbidden" {
		t.Errorf("sampleDomainError: got status=%d code=%q, want 403 sample_forbidden", status, code)
	}
}

func TestDomainErrorRegistry_Lookup_NilReturnsNotFound(t *testing.T) {
	reg := newTestRegistry()
	_, _, ok := reg.Lookup(nil)
	if ok {
		t.Errorf("expected nil error to miss the registry")
	}
}

func TestDomainErrorRegistry_Lookup_UnknownErrorMisses(t *testing.T) {
	reg := newTestRegistry()
	_, _, ok := reg.Lookup(errors.New("not registered"))
	if ok {
		t.Errorf("expected unknown error to miss the registry")
	}
}

func TestWriteJSON_SetsContentTypeAndStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	payload := map[string]string{"hello": "world"}

	if err := WriteJSON(rec, http.StatusCreated, payload); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	resp := rec.Result()
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusCreated; got != want {
		t.Errorf("status: got %d, want %d", got, want)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q, want application/json prefix", ct)
	}

	var decoded map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["hello"] != "world" {
		t.Errorf("payload: got %v, want hello=world", decoded)
	}
}
