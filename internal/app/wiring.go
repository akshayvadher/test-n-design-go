// Package app is the shared composition root for the library binary and
// integration-test app factory. It exists so cmd/library/main.go and
// test/support/app_factory.go go through one wiring path — the same bun
// client, the same chi middleware stack, the same domain-error registry, and
// the same /healthz route.
//
// app does NOT own the *http.Server, the listener, or signal handling. Those
// belong to the caller (main wires its own server bound to the configured
// port; BootApp wires its own server bound to a free port chosen with
// net.Listen). The seam is the chi.Router returned by Wire.
//
// No init() functions live in this package.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	cataloghttp "github.com/akshayvadher/test-n-design-go/internal/catalog/http"
	"github.com/akshayvadher/test-n-design-go/internal/shared/db"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
	"github.com/uptrace/bun"
)

// Deps carries the inputs Wire needs from the caller. The caller (main or
// BootApp) constructs the slog logger and supplies the database URL; Wire
// owns everything downstream of those.
type Deps struct {
	// Logger is the single *slog.Logger every collaborator receives — no
	// slog.Default() reads from inside business code. Required.
	Logger *slog.Logger

	// DatabaseURL is the Postgres DSN passed to db.NewBunDB. Required.
	// PoolConfig is intentionally not exposed: Phase 1 relies on the
	// hardcoded conservative defaults via db.PoolConfig{}.
	DatabaseURL string
}

// Wired is the package-public handle Wire returns. The caller mounts Router
// onto its own *http.Server and is responsible for calling Close before
// exit (or via t.Cleanup in tests).
type Wired struct {
	// Router is the chi.Router with the locked middleware stack and the
	// /healthz route already mounted.
	Router chi.Router

	// DB is the bun client constructed against Deps.DatabaseURL. Exposed so
	// integration tests can introspect (run queries, count rows). Production
	// callers typically pass it down into module facade constructors.
	DB *bun.DB

	// CatalogFacade is the catalog module's facade. Integration tests use
	// it to assert that HTTP-driven writes actually persisted (e.g. by
	// calling FindBook against the same facade the router is bound to).
	// Slice 4 wires it with the bun-backed repository so every HTTP-driven
	// write hits Postgres.
	CatalogFacade *catalog.Facade

	// Close releases every resource Wire allocated (currently: the bun DB
	// connection pool). Callers MUST invoke Close on every path. Idempotent.
	Close func() error
}

// Wire constructs the shared composition root: bun DB → domain-error registry
// → chi router with the locked middleware stack → /healthz route → catalog
// module routes. The returned Wired bundles everything; the caller mounts
// Router onto its own *http.Server.
//
// Wire intentionally does NOT start a listener, install signal handlers, or
// own the *http.Server. Those concerns differ between the production binary
// (configured port, SIGINT/SIGTERM, 10s shutdown) and integration tests
// (free port via net.Listen, t.Cleanup teardown, 5s shutdown).
//
// On any failure Wire releases every resource it has already allocated and
// returns a wrapped error. The returned *Wired is nil on error.
func Wire(ctx context.Context, deps Deps) (*Wired, error) {
	bunDB, err := db.NewBunDB(ctx, deps.DatabaseURL, db.PoolConfig{}, deps.Logger)
	if err != nil {
		return nil, fmt.Errorf("wire bun db: %w", err)
	}

	registry := buildDomainErrorRegistry()
	router := buildRouter(deps.Logger, registry)
	router.Get("/healthz", healthzHandler)

	catalogFacade := catalog.NewFacadeWithOverrides(catalog.Overrides{
		Repository: catalog.NewBunRepository(bunDB),
		Logger:     deps.Logger,
	})
	cataloghttp.Wire(router, cataloghttp.Deps{Facade: catalogFacade, Logger: deps.Logger})

	return &Wired{
		Router:        router,
		DB:            bunDB,
		CatalogFacade: catalogFacade,
		Close:         bunDB.Close,
	}, nil
}

// buildDomainErrorRegistry constructs the registry with every Phase-1 +
// Phase-2 catalog domain error registered. Later phases extend this block
// when they introduce their own error types.
func buildDomainErrorRegistry() *sharedhttp.DomainErrorRegistry {
	registry := sharedhttp.NewDomainErrorRegistry()
	registry.Register(&accesscontrol.UnauthorizedRoleError{}, http.StatusForbidden, "unauthorized_role")
	registry.Register(&accesscontrol.UnknownActionError{}, http.StatusForbidden, "unknown_action")
	registry.Register(&catalog.InvalidBookError{}, http.StatusBadRequest, "invalid_book")
	registry.Register(&catalog.InvalidCopyError{}, http.StatusBadRequest, "invalid_copy")
	registry.Register(&catalog.BookNotFoundError{}, http.StatusNotFound, "book_not_found")
	registry.Register(&catalog.CopyNotFoundError{}, http.StatusNotFound, "copy_not_found")
	registry.Register(&catalog.DuplicateIsbnError{}, http.StatusConflict, "duplicate_isbn")
	return registry
}

// buildRouter wires the chi router with the locked middleware stack
// (RequestID → RealIP → slog-adapter Logger → Recoverer → DomainErrorMiddleware).
// DomainErrorMiddleware is appended separately because it needs the registry
// parameter; Middlewares stays a pure (logger) → []middleware function.
func buildRouter(logger *slog.Logger, registry *sharedhttp.DomainErrorRegistry) chi.Router {
	r := chi.NewRouter()
	for _, m := range sharedhttp.Middlewares(logger) {
		r.Use(m)
	}
	r.Use(sharedhttp.DomainErrorMiddleware(registry, logger))
	return r
}

// healthzBody is the exact /healthz response payload. It must be byte-identical
// to `{"status":"ok"}` after bytes.TrimSpace, so we hold the literal here
// rather than round-tripping through json.Encoder (which would append a
// trailing newline).
const healthzBody = `{"status":"ok"}`

// healthzHandler responds with the canonical {"status":"ok"} body. The body is
// written as raw bytes (not json.Encoder.Encode) so the response is
// byte-identical to the literal — no trailing newline.
func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(healthzBody))
}
