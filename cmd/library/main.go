// Package main is the composition root for the library service binary.
//
// main.go performs the full sequential wiring: load config → build logger →
// build the chi router with the locked middleware stack → register routes →
// start the HTTP server → block on SIGINT/SIGTERM → graceful shutdown.
//
// No init() functions exist anywhere in the binary. Every collaborator that
// logs receives the single *slog.Logger constructed here — slog.Default() is
// never read.
//
// os.Exit is called from exactly two places: the config-load failure branch
// (exit 1 before the logger exists) and the shutdown-error branch (exit 1
// after attempting a graceful stop). Clean paths return from main normally.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// readHeaderTimeout caps the time the HTTP server waits for request headers.
// Spec AC: Slice 2 wires the http.Server with ReadHeaderTimeout = 5s.
const readHeaderTimeout = 5 * time.Second

// shutdownTimeout bounds the graceful-shutdown deadline triggered by
// SIGINT/SIGTERM. Spec AC: Slice 2 uses a 10-second timeout.
const shutdownTimeout = 10 * time.Second

// healthzBody is the exact response payload for GET /healthz. It must be
// byte-identical to `{"status":"ok"}` after bytes.TrimSpace, so we hold the
// literal here rather than round-tripping through json.Encoder (which would
// append a trailing newline).
const healthzBody = `{"status":"ok"}`

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	logger := buildLogger(cfg, os.Stdout)
	registry := sharedhttp.NewDomainErrorRegistry()
	// Phase 1 ships an empty registry — every handler error collapses to
	// 500/internal_error. Slice 6 lands accesscontrol and the registration
	// block (UnauthorizedRoleError, UnknownActionError) below.
	router := buildRouter(logger, registry)
	server := buildServer(cfg.HTTPPort, router)

	if err := runServer(server, cfg.HTTPPort, logger); err != nil {
		logger.Error("server shutdown failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// buildLogger constructs the single *slog.Logger used by every collaborator.
// The handler (JSON or text) and level are driven by the validated Config —
// the surrounding switch on LogLevel cannot hit the default branch because
// validateLogLevel rejected anything else.
func buildLogger(cfg *Config, w *os.File) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLogLevel(cfg.LogLevel)}
	var handler slog.Handler
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}
	return slog.New(handler)
}

// parseLogLevel maps a validated log-level string to slog.Level. The validator
// guarantees `level` is one of debug|info|warn|error, so the default branch is
// unreachable; we still return slog.LevelInfo there for defence in depth.
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// buildRouter wires the chi router with the locked Phase-1 middleware stack
// (RequestID → RealIP → slog-adapter Logger → Recoverer → DomainErrorMiddleware)
// and registers the routes owned by the binary itself (currently just /healthz).
//
// The middleware stack is constructed by internal/shared/http.Middlewares so
// every binary uses the same chain. DomainErrorMiddleware is appended
// separately because it needs the registry; Middlewares stays a pure
// (logger) → []middleware function.
func buildRouter(logger *slog.Logger, registry *sharedhttp.DomainErrorRegistry) chi.Router {
	r := chi.NewRouter()
	for _, m := range sharedhttp.Middlewares(logger) {
		r.Use(m)
	}
	r.Use(sharedhttp.DomainErrorMiddleware(registry, logger))

	r.Get("/healthz", healthzHandler)
	return r
}

// healthzHandler responds with the canonical {"status":"ok"} body. The body is
// written as raw bytes (not json.Encoder.Encode) so the response is
// byte-identical to the literal — no trailing newline.
func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(healthzBody))
}

// buildServer assembles the *http.Server with the timeouts mandated by the
// spec. Only ReadHeaderTimeout is set in Phase 1; the broader Read/Write
// timeouts are deferred to a future hardening slice.
func buildServer(port int, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}
}

// runServer starts the HTTP server, blocks until SIGINT/SIGTERM, then
// performs a bounded graceful shutdown. It returns a non-nil error only when
// the listener fails to start cleanly or Shutdown reports an error — both
// trigger an exit-1 in main.
func runServer(server *http.Server, port int, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	listenErrors := make(chan error, 1)
	go func() {
		logger.Info("server listening", slog.Int("port", port))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErrors <- err
			return
		}
		listenErrors <- nil
	}()

	select {
	case err := <-listenErrors:
		return err
	case <-ctx.Done():
		return gracefulShutdown(server, logger)
	}
}

// gracefulShutdown gives in-flight requests up to shutdownTimeout to finish
// before forcing close. Clean termination logs "server stopped" at info; a
// shutdown error propagates to runServer (and on to main's exit-1 branch).
func gracefulShutdown(server *http.Server, logger *slog.Logger) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http server shutdown: %w", err)
	}
	logger.Info("server stopped")
	return nil
}
