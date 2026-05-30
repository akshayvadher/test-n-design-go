package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// internalErrorCode is the snake_case code emitted to the client when a
// handler returns an unregistered error. The raw error text is logged at
// error level but never escapes into the response body.
const internalErrorCode = "internal_error"

// internalErrorMessage is the human-readable body sent alongside
// internalErrorCode. Kept constant so clients can string-match without
// information leakage from wrapped errors.
const internalErrorMessage = "internal server error"

// Middlewares returns the locked chi middleware stack in order:
// RequestID → RealIP → slog adapter Logger → Recoverer → DomainErrorMiddleware.
// The DomainErrorRegistry is constructed by the caller (composition root)
// and passed in via DomainErrorMiddleware separately — Middlewares wires
// only the four request-shaping layers that don't need a registry.
//
// Callers compose the full stack as:
//
//	for _, m := range http.Middlewares(logger) { r.Use(m) }
//	r.Use(http.DomainErrorMiddleware(registry, logger))
//
// We keep DomainErrorMiddleware out of this slice so Middlewares stays a
// pure (logger) → []middleware function, matching the spec signature.
func Middlewares(logger *slog.Logger) []func(http.Handler) http.Handler {
	return []func(http.Handler) http.Handler{
		middleware.RequestID,
		middleware.RealIP,
		loggerMiddleware(logger),
		middleware.Recoverer,
	}
}

// DomainErrorRegistry maps registered error sentinels to the HTTP status and
// snake_case code the middleware should emit. Lookup walks the errors.As
// chain so wrapped errors still match — the entry stored is the empty value
// of the target type, and errors.As pivots on type identity.
type DomainErrorRegistry struct {
	entries []registryEntry
}

// registryEntry holds the target sentinel alongside its mapping. The target
// is stored as a pointer to an interface holding the empty value of the
// concrete type — errors.As needs a non-nil pointer to a value of the target
// type to perform the chain walk.
type registryEntry struct {
	target error
	status int
	code   string
}

// NewDomainErrorRegistry returns an empty registry. Callers register error
// types via Register; Lookup is safe to call against an empty registry
// (returns ok=false for every input).
func NewDomainErrorRegistry() *DomainErrorRegistry {
	return &DomainErrorRegistry{}
}

// Register associates a target error sentinel with an HTTP status + code.
// The target must be a non-nil pointer to a value whose dynamic type is the
// concrete error type the registry should match against (e.g.
// &accesscontrol.UnauthorizedRoleError{}). Lookup performs an errors.As walk
// using that target type.
//
// Registering the same target twice is permitted — last write wins.
func (r *DomainErrorRegistry) Register(target error, status int, code string) {
	r.entries = append(r.entries, registryEntry{target: target, status: status, code: code})
}

// Lookup walks the registry in registration order. For each entry it
// attempts errors.As against a fresh zero-value of the entry's target type;
// the first match wins. Returns ok=false when no entry matches.
func (r *DomainErrorRegistry) Lookup(err error) (int, string, bool) {
	if err == nil {
		return 0, "", false
	}
	for _, entry := range r.entries {
		if errorMatchesTarget(err, entry.target) {
			return entry.status, entry.code, true
		}
	}
	return 0, "", false
}

// errorMatchesTarget runs errors.As against a fresh pointer whose dynamic
// type matches the registered target. errors.As requires `*T` on the right
// hand side where `T` is the concrete error type to match — reflect.New
// gives us that pointer without leaking the registry's stored value (which
// would mutate on a successful As call).
//
// The registered target is, by convention, a pointer to the concrete error
// type (e.g. &accesscontrol.UnauthorizedRoleError{}). errors.As's contract
// is: if err's chain contains a value assignable to T (or *T), assign it to
// the pointer. We pass `*Ptr` where Ptr is the same kind as target, which
// matches the way the source TS code's domain errors are thrown by value.
func errorMatchesTarget(err error, target error) bool {
	holder := reflect.New(reflect.TypeOf(target)).Interface()
	return errors.As(err, holder)
}

// DomainErrorMiddleware translates handler errors into HTTP responses.
//
// The middleware seeds the request context with an errHolder. Handlers
// wrapped by Handle store their returned error into the holder before
// next.ServeHTTP returns. After ServeHTTP returns, the middleware reads the
// holder: nil means the handler succeeded (or wasn't wrapped by Handle);
// non-nil triggers registry lookup → WriteJSON.
//
// Errors registered with the registry surface their snake_case code and
// status. Unregistered errors collapse to 500 + internal_error + a log line
// carrying the raw error message (which never reaches the client body).
func DomainErrorMiddleware(registry *DomainErrorRegistry, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			holder := &errHolder{}
			ctx := context.WithValue(r.Context(), errKey{}, holder)

			next.ServeHTTP(w, r.WithContext(ctx))

			if holder.err == nil {
				return
			}

			writeDomainErrorResponse(w, r, holder.err, registry, logger)
		})
	}
}

// writeDomainErrorResponse encapsulates the post-ServeHTTP branch logic:
// registered → status+code; unregistered → 500 + internal_error + log line.
// Extracted so DomainErrorMiddleware stays a thin orchestrator.
func writeDomainErrorResponse(
	w http.ResponseWriter,
	r *http.Request,
	err error,
	registry *DomainErrorRegistry,
	logger *slog.Logger,
) {
	requestID := middleware.GetReqID(r.Context())

	if status, code, ok := registry.Lookup(err); ok {
		_ = WriteJSON(w, status, ErrorResponse{
			Error:     code,
			Message:   err.Error(),
			RequestID: requestID,
		})
		return
	}

	logger.Error("unregistered handler error",
		slog.String("error", err.Error()),
		slog.String("request_id", requestID),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
	)

	_ = WriteJSON(w, http.StatusInternalServerError, ErrorResponse{
		Error:     internalErrorCode,
		Message:   internalErrorMessage,
		RequestID: requestID,
	})
}

// Handle adapts an error-returning handler into an http.HandlerFunc. The
// returned function stores the handler's error into the request-scoped
// errHolder so DomainErrorMiddleware can read it after ServeHTTP returns.
//
// If Handle is called without DomainErrorMiddleware in the chain (no holder
// in context), the returned error is silently dropped — the middleware must
// be wired upstream for error translation to take effect.
//
// If Handle's wrapped function is invoked multiple times in the same
// request (nested middlewares, replay handlers), the holder records the
// most recent non-nil error: later calls overwrite earlier ones.
func Handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		if err == nil {
			return
		}
		if holder, ok := r.Context().Value(errKey{}).(*errHolder); ok {
			holder.err = err
		}
	}
}

// errKey is the unexported context key used to stash the per-request
// errHolder. The struct{} type prevents collisions with any string-keyed
// context value elsewhere in the binary.
type errKey struct{}

// errHolder is the pointer-receiver carrier the middleware seeds into the
// request context. Handle mutates errHolder.err; DomainErrorMiddleware
// reads it after next.ServeHTTP returns.
type errHolder struct {
	err error
}

// loggerMiddleware emits one info-level slog line per request with the
// fields the spec mandates: method, path, status, bytes, duration_ms,
// request_id, remote_ip. It wraps the ResponseWriter with statusRecorder
// to capture status + bytes — chi's middleware.Logger writes to a separate
// io.Writer, which doesn't match the spec's structured-slog requirement.
func loggerMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(recorder, r)

			logger.Info("http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", recorder.status),
				slog.Int("bytes", recorder.bytes),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("request_id", middleware.GetReqID(r.Context())),
				slog.String("remote_ip", r.RemoteAddr),
			)
		})
	}
}

// statusRecorder wraps http.ResponseWriter to capture the status code and
// bytes written for the slog log line. It defaults to 200 because handlers
// that call Write without WriteHeader implicitly produce a 200.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

// WriteHeader records the first status passed (subsequent calls are no-ops
// at the net/http layer too). The wroteHeader latch prevents a stray
// WriteHeader-after-Write from corrupting the recorded status.
func (s *statusRecorder) WriteHeader(status int) {
	if s.wroteHeader {
		s.ResponseWriter.WriteHeader(status)
		return
	}
	s.status = status
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(status)
}

// Write records the byte count alongside the underlying write. It triggers
// WriteHeader(200) implicitly if the handler never called WriteHeader,
// mirroring net/http's behaviour.
func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}
