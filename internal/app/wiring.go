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
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/uptrace/bun"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	cataloghttp "github.com/akshayvadher/test-n-design-go/internal/catalog/http"
	"github.com/akshayvadher/test-n-design-go/internal/categories"
	categoriesbun "github.com/akshayvadher/test-n-design-go/internal/categories/driven/bun"
	categorieshttp "github.com/akshayvadher/test-n-design-go/internal/categories/driving/http"
	"github.com/akshayvadher/test-n-design-go/internal/chat"
	chathttp "github.com/akshayvadher/test-n-design-go/internal/chat/http"
	"github.com/akshayvadher/test-n-design-go/internal/fines"
	fineshttp "github.com/akshayvadher/test-n-design-go/internal/fines/http"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	lendinghttp "github.com/akshayvadher/test-n-design-go/internal/lending/http"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	membershiphttp "github.com/akshayvadher/test-n-design-go/internal/membership/http"
	"github.com/akshayvadher/test-n-design-go/internal/shared/bookcache"
	"github.com/akshayvadher/test-n-design-go/internal/shared/chatgateway"
	"github.com/akshayvadher/test-n-design-go/internal/shared/db"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
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

	// RedisURL is the Redis DSN (e.g. redis://localhost:6379/0). When set,
	// Wire constructs a *redis.Client and wires the catalog facade with a
	// bookcache.NewRedisBookCacheGateway. Empty means "use the in-memory
	// cache" — the unit-test and Phase-1-style integration paths can omit
	// Redis entirely without touching the network.
	RedisURL string
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

	// MembershipFacade is the membership module's facade. Integration tests
	// use it to assert that HTTP-driven writes actually persisted. Slice 5
	// wires it with the bun-backed repository so every HTTP-driven write
	// hits Postgres.
	MembershipFacade *membership.Facade

	// LendingFacade is the lending module's facade. Integration tests use
	// it to assert post-commit cross-module mutations (e.g. catalog flips a
	// copy to UNAVAILABLE after Borrow) by calling FindCopy via the catalog
	// facade right after a Borrow returns 201.
	LendingFacade *lending.Facade

	// AutoLoanConsumer is the saga subscriber that turns a LoanReturned into
	// an auto-loan for the head-of-queue eligible reservation. It is Start()ed
	// by Wire after route mounting and Stop()ped by Close before the DB is
	// closed (so the consumer detaches from the bus before the substrate it
	// writes through goes away). Integration tests can introspect it for
	// lifecycle assertions, though the behavioural ACs already cover the
	// observable outcome.
	AutoLoanConsumer *lending.AutoLoanOnReturnConsumer

	// FinesFacade is the fines module's facade. Integration tests use it
	// to drive AssessFinesFor / ProcessOverdueLoans / PayFine against the
	// same bun-backed repository the HTTP-driven writes hit.
	FinesFacade *fines.Facade

	// CategoriesFacade is the categories module's facade. Integration
	// tests use it to assert that HTTP-driven writes actually persisted
	// (e.g. by calling FindCategoryById against the same facade the
	// router is bound to). Slice 5 wires it with the bun-backed
	// repository so every HTTP-driven write hits Postgres.
	CategoriesFacade *categories.Facade

	// ChatFacade is the chat module's facade. Tests can hold a
	// reference to exercise the streaming surface without going
	// through HTTP. Phase 5 wires it with the deterministic in-memory
	// gateway by default so `task run` boots without an OpenAI key.
	ChatFacade *chat.Facade

	// Bus is the in-process event bus the lending facade publishes
	// LoanOpened / LoanReturned / ReservationQueued events through.
	// Exposed so integration tests can subscribe and assert that domain
	// events surface end-to-end, including the Phase-3 "LoanReturned is
	// observable on the bus even with no consumer subscribed" criterion.
	Bus events.EventBus

	// Close stops every consumer Wire started and releases every resource
	// Wire allocated (currently: the AutoLoanConsumer subscription, the bun
	// DB connection pool, the redis client when wired). Consumers stop
	// BEFORE the DB is closed so handlers do not run against a torn-down
	// substrate. Callers MUST invoke Close on every path. Idempotent.
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
// Order of operations:
//
//  1. build deps (bun DB, book-cache, error registry, router, bus)
//  2. build facades (catalog, membership, lending, fines, categories)
//  3. wire HTTP routes for each module
//  4. construct + Start consumers (AutoLoanOnReturnConsumer)
//  5. return Wired with a Close that Stops consumers first, then closes DB
//
// On any failure Wire releases every resource it has already allocated and
// returns a wrapped error. The returned *Wired is nil on error.
func Wire(ctx context.Context, deps Deps) (*Wired, error) {
	bunDB, err := db.NewBunDB(ctx, deps.DatabaseURL, db.PoolConfig{}, deps.Logger)
	if err != nil {
		return nil, fmt.Errorf("wire bun db: %w", err)
	}

	cache, redisClient, err := buildBookCache(deps)
	if err != nil {
		_ = bunDB.Close()
		return nil, fmt.Errorf("wire book cache: %w", err)
	}

	registry := buildDomainErrorRegistry()
	router := buildRouter(deps.Logger, registry)
	router.Get("/healthz", healthzHandler)

	catalogFacade := catalog.NewFacadeWithOverrides(catalog.Overrides{
		Repository:       catalog.NewBunRepository(bunDB),
		BookCacheGateway: cache,
		Logger:           deps.Logger,
	})
	cataloghttp.Wire(router, cataloghttp.Deps{Facade: catalogFacade, Logger: deps.Logger})

	membershipFacade := membership.NewFacadeWithOverrides(membership.Overrides{
		Repository: membership.NewBunRepository(bunDB),
		Logger:     deps.Logger,
	})
	membershiphttp.Wire(router, membershiphttp.Deps{Facade: membershipFacade, Logger: deps.Logger})

	bus := events.NewInMemoryEventBus(deps.Logger)
	accessControlFacade := accesscontrol.NewFacade()
	lendingWiring := lending.WireBunFacade(
		bunDB,
		bus,
		catalogFacade,
		membershipFacade,
		accessControlFacade,
		deps.Logger,
	)
	lendingFacade := lendingWiring.Facade
	lendinghttp.Wire(router, lendinghttp.Deps{Facade: lendingFacade, Logger: deps.Logger})

	finesConfig := loadFinesConfig()
	finesFacade := fines.NewFacadeWithOverrides(fines.Overrides{
		Lending:    lendingFacade,
		Membership: membershipFacade,
		Repository: fines.NewBunFineRepository(bunDB),
		Bus:        bus,
		Config:     &finesConfig,
		Logger:     deps.Logger,
	})
	fineshttp.Wire(router, fineshttp.Deps{Facade: finesFacade, Logger: deps.Logger, Clock: time.Now})

	categoriesFacade := categories.NewFacade(
		categoriesbun.NewRepository(bunDB),
		uuid.NewString,
		time.Now,
		deps.Logger,
	)
	categorieshttp.Wire(router, categorieshttp.Deps{Facade: categoriesFacade, Logger: deps.Logger})

	chatFacade := chat.NewFacade(chatgateway.NewInMemoryChatGateway(), deps.Logger)
	chathttp.Wire(router, chathttp.Deps{Facade: chatFacade, Logger: deps.Logger})

	autoLoanConsumer := lending.NewAutoLoanOnReturnConsumer(lending.AutoLoanOnReturnConsumerDeps{
		Bus:          bus,
		Membership:   membershipFacade,
		Reservations: lendingWiring.Reservations,
		Lending:      lendingFacade,
		TxFactory:    lendingWiring.TxFactory,
		Clock:        time.Now,
		Logger:       deps.Logger.With("component", "auto-loan-consumer"),
	})
	if err := autoLoanConsumer.Start(ctx); err != nil {
		_ = bunDB.Close()
		if redisClient != nil {
			_ = redisClient.Close()
		}
		return nil, fmt.Errorf("start auto-loan consumer: %w", err)
	}

	return &Wired{
		Router:           router,
		DB:               bunDB,
		CatalogFacade:    catalogFacade,
		MembershipFacade: membershipFacade,
		LendingFacade:    lendingFacade,
		AutoLoanConsumer: autoLoanConsumer,
		FinesFacade:      finesFacade,
		CategoriesFacade: categoriesFacade,
		ChatFacade:       chatFacade,
		Bus:              bus,
		Close:            buildCloser(autoLoanConsumer, bunDB, redisClient),
	}, nil
}

// loadFinesConfig reads the fines module's two policy knobs from the
// environment, falling back to the locked defaults from
// internal/fines/configuration.go. The two variables are:
//
//   - FINES_DAILY_RATE_CENTS         (default 25)
//   - FINES_SUSPENSION_THRESHOLD_CENTS (default 1000)
//
// Non-numeric values are ignored and the default is used (matches the
// "silent fallback only when the value is missing" Phase-1 config style;
// fines is non-critical enough that a typo in the env doesn't need to
// crash the whole binary).
func loadFinesConfig() fines.FinesConfig {
	cfg := fines.FinesConfig{
		DailyRateCents:           fines.DefaultDailyRateCents,
		SuspensionThresholdCents: fines.DefaultSuspensionThresholdCents,
	}
	if raw, ok := os.LookupEnv("FINES_DAILY_RATE_CENTS"); ok {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			cfg.DailyRateCents = fines.AmountCents(n)
		}
	}
	if raw, ok := os.LookupEnv("FINES_SUSPENSION_THRESHOLD_CENTS"); ok {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			cfg.SuspensionThresholdCents = fines.AmountCents(n)
		}
	}
	return cfg
}

// buildBookCache constructs the BookCacheGateway the catalog facade depends on.
// When deps.RedisURL is set, it parses the URL, opens a *redis.Client, and
// returns a Redis-backed gateway plus the client (so the caller can close it).
// When RedisURL is empty, it returns the in-memory gateway and a nil client —
// the unit-test and Phase-1-style integration paths skip Redis entirely.
func buildBookCache(deps Deps) (bookcache.BookCacheGateway, *redis.Client, error) {
	if deps.RedisURL == "" {
		return bookcache.NewInMemoryBookCacheGateway(), nil, nil
	}
	opts, err := redis.ParseURL(deps.RedisURL)
	if err != nil {
		return nil, nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	return bookcache.NewRedisBookCacheGateway(client, bookcache.DefaultRedisTTL, deps.Logger), client, nil
}

// buildCloser composes a Close function that stops the saga consumer first,
// then releases the bun DB pool and (when present) the Redis client. Consumers
// stop BEFORE the DB closes so handlers don't run against a torn-down
// substrate. Errors are joined so callers see every failure rather than only
// the first. Stop is bounded by a short context — the consumer's only
// shutdown work is detaching from the bus, which is synchronous and cheap.
func buildCloser(consumer *lending.AutoLoanOnReturnConsumer, bunDB *bun.DB, redisClient *redis.Client) func() error {
	return func() error {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopErr := consumer.Stop(stopCtx)
		dbErr := bunDB.Close()
		if redisClient == nil {
			return errors.Join(stopErr, dbErr)
		}
		return errors.Join(stopErr, dbErr, redisClient.Close())
	}
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
	registry.Register(&membership.InvalidMemberError{}, http.StatusBadRequest, "invalid_member")
	registry.Register(&membership.MemberNotFoundError{}, http.StatusNotFound, "member_not_found")
	registry.Register(&membership.DuplicateEmailError{}, http.StatusConflict, "duplicate_email")
	registry.Register(&lending.LoanNotFoundError{}, http.StatusNotFound, "loan_not_found")
	registry.Register(&lending.ReservationNotFoundError{}, http.StatusNotFound, "reservation_not_found")
	registry.Register(&lending.CopyUnavailableError{}, http.StatusConflict, "copy_unavailable")
	registry.Register(&lending.MemberIneligibleError{}, http.StatusConflict, "member_ineligible")
	registry.Register(&lending.BorrowValidationError{}, http.StatusBadRequest, "invalid_borrow")
	registry.Register(&lending.ReserveValidationError{}, http.StatusBadRequest, "invalid_reserve")
	registry.Register(&lending.ReturnLoanValidationError{}, http.StatusBadRequest, "invalid_return")
	registry.Register(&fines.FineNotFoundError{}, http.StatusNotFound, "fine_not_found")
	registry.Register(&fines.FineAlreadyPaidError{}, http.StatusConflict, "fine_already_paid")
	registry.Register(&fines.InvalidFineError{}, http.StatusBadRequest, "invalid_fine")
	registry.Register(&categories.CategoryNotFoundError{}, http.StatusNotFound, "category_not_found")
	registry.Register(&categories.DuplicateCategoryError{}, http.StatusConflict, "duplicate_category")
	registry.Register(&categories.InvalidCategoryError{}, http.StatusBadRequest, "invalid_category")
	registry.Register(&categories.InvalidCategoriesQueryError{}, http.StatusBadRequest, "invalid_categories_query")
	registry.Register(&chat.InvalidChatRequestError{}, http.StatusBadRequest, "invalid_chat_request")
	registry.Register(&chat.ChatGatewayError{}, http.StatusBadGateway, "chat_gateway_error")
	registry.Register(&chatgateway.EmptyMessagesError{}, http.StatusBadRequest, "empty_messages")
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
