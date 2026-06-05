// Package main is the composition root for the library service binary.
//
// main.go performs the sequential boot: load config → build logger → call
// app.Wire to construct the bun client + chi router + middleware stack +
// /healthz route → build the *http.Server → start listening → block on
// SIGINT/SIGTERM → graceful shutdown → release wired resources.
//
// The wiring itself lives in internal/app so the integration test harness
// (test/support/app_factory.go) goes through the same path — there is
// exactly one composition root, exercised by both production and tests.
//
// No init() functions exist anywhere in the binary. Every collaborator that
// logs receives the single *slog.Logger constructed here — slog.Default() is
// never read.
//
// os.Exit is called from exactly two places: the config-load failure branch
// (exit 1 before the logger exists) and the runServer failure branch (exit 1
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

	"github.com/akshayvadher/test-n-design-go/internal/app"
	"github.com/akshayvadher/test-n-design-go/internal/shared/db"
)

// readHeaderTimeout caps the time the HTTP server waits for request headers.
// Spec AC: Slice 2 wires the http.Server with ReadHeaderTimeout = 5s.
const readHeaderTimeout = 5 * time.Second

// shutdownTimeout bounds the graceful-shutdown deadline triggered by
// SIGINT/SIGTERM. Spec AC: Slice 2 uses a 10-second timeout.
const shutdownTimeout = 10 * time.Second

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	logger := buildLogger(cfg, os.Stdout)

	// `library migrate` applies pending schema migrations in-process and exits.
	// k8s runs this as the initContainer using the SAME image as the server —
	// no atlas CLI, no separate migrations image. Any other arg (or none) boots
	// the HTTP server.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := runMigrate(cfg, logger); err != nil {
			logger.Error("migrate", slog.String("error", err.Error()))
			os.Exit(1)
		}
		return
	}

	ctx := context.Background()
	wired, err := app.Wire(ctx, app.Deps{
		Logger:      logger,
		DatabaseURL: cfg.DatabaseURL,
		RedisURL:    cfg.RedisURL,
	})
	if err != nil {
		logger.Error("wire app", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer func() {
		if err := wired.Close(); err != nil {
			logger.Error("close wired resources", slog.String("error", err.Error()))
		}
	}()

	server := buildServer(cfg.HTTPPort, wired.Router)

	if err := runServer(server, cfg.HTTPPort, logger); err != nil {
		logger.Error("server shutdown failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// runMigrate opens a bun pool against the configured DATABASE_URL, applies all
// pending embedded migrations in-process via db.RunBunMigrations, then closes
// the pool. It is the `library migrate` subcommand's whole body — the binary
// exits 0 on success, 1 on failure (handled by the caller in main).
func runMigrate(cfg *Config, logger *slog.Logger) error {
	ctx := context.Background()
	bunDB, err := db.NewBunDB(ctx, cfg.DatabaseURL, db.PoolConfig{}, logger)
	if err != nil {
		return fmt.Errorf("open db for migrate: %w", err)
	}
	defer func() { _ = bunDB.Close() }()
	return db.RunBunMigrations(ctx, bunDB, logger)
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
