//go:build integration

package support

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/akshayvadher/test-n-design-go/internal/app"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/fines"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
)

// readHeaderTimeout matches the production binary's http.Server setting so
// the test app exercises the same handler timeouts production does.
const readHeaderTimeout = 5 * time.Second

// shutdownTimeout caps the per-test BootedApp.Shutdown deadline. 5s is the
// spec's prescribed value — generous for in-flight requests on localhost,
// strict enough that a stuck handler fails the test instead of hanging the
// suite.
const shutdownTimeout = 5 * time.Second

// AppConfig carries the inputs BootApp needs from the test. DatabaseURL and
// RedisURL come from the StartPostgres / StartRedis helpers; the test does
// not need to think about port allocation (BootApp picks a free one via
// net.Listen and reports it back through BootedApp.BaseURL).
type AppConfig struct {
	// DatabaseURL is the Postgres DSN BootApp passes to app.Wire (which in
	// turn passes it into db.NewBunDB with an empty PoolConfig — Phase 1
	// relies on the hardcoded conservative defaults).
	DatabaseURL string

	// RedisURL is the Redis DSN BootApp passes to app.Wire. When set, the
	// catalog facade is wired with the Redis-backed bookcache; otherwise it
	// falls back to the in-memory cache. Integration tests that need to
	// exercise the cache substrate pass a URL produced by StartRedis.
	RedisURL string
}

// BootedApp is the handle BootApp returns. BaseURL is the canonical
// http://localhost:<port> string for the running server; tests issue requests
// against it via net/http.
//
// Logger and DB are exposed so integration tests can introspect (read log
// output, run queries) without re-wiring. Phase 1's healthz smoke does not
// use them, but the spec AC requires the field set so Phase 2+ specs do not
// have to extend BootedApp before introspection is possible.
type BootedApp struct {
	BaseURL          string
	Logger           *slog.Logger
	DB               *bun.DB
	Bus              events.EventBus
	CatalogFacade    *catalog.Facade
	MembershipFacade *membership.Facade
	LendingFacade    *lending.Facade
	FinesFacade      *fines.Facade
	Shutdown         func(ctx context.Context) error
}

// BootApp brings up the full composition root against the supplied
// containers, listens on a free localhost port, and returns the handle the
// test uses to issue requests. t.Cleanup is registered to call Shutdown with
// a bounded 5s deadline.
//
// BootApp goes through app.Wire — the EXACT same wiring path
// cmd/library/main.go uses. The seam between production and tests is the
// *http.Server: production binds to the configured port, tests bind to a
// free port chosen here.
//
// On any setup failure t.Fatalf is invoked so the test stops immediately and
// already-allocated resources (listener, bun DB) are released via the
// matching cleanup branches.
func BootApp(ctx context.Context, t testing.TB, cfg AppConfig) BootedApp {
	t.Helper()

	port := pickFreePort(t)
	logger := testLogger(t)

	wired, err := app.Wire(ctx, app.Deps{
		Logger:      logger,
		DatabaseURL: cfg.DatabaseURL,
		RedisURL:    cfg.RedisURL,
	})
	if err != nil {
		t.Fatalf("wire app: %v", err)
	}

	server := &http.Server{
		Addr:              fmt.Sprintf("localhost:%d", port),
		Handler:           wired.Router,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	listenErrors := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErrors <- err
		}
	}()

	baseURL := fmt.Sprintf("http://localhost:%d", port)
	waitForListening(t, baseURL, listenErrors)

	booted := BootedApp{
		BaseURL:          baseURL,
		Logger:           logger,
		DB:               wired.DB,
		Bus:              wired.Bus,
		CatalogFacade:    wired.CatalogFacade,
		MembershipFacade: wired.MembershipFacade,
		LendingFacade:    wired.LendingFacade,
		FinesFacade:      wired.FinesFacade,
		Shutdown: func(stopCtx context.Context) error {
			shutdownErr := server.Shutdown(stopCtx)
			closeErr := wired.Close()
			return errors.Join(shutdownErr, closeErr)
		},
	}

	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := booted.Shutdown(stopCtx); err != nil {
			t.Logf("shutdown booted app: %v", err)
		}
	})

	return booted
}

// pickFreePort asks the kernel for an unused TCP port on localhost by
// listening on :0 and closing the socket before the server reopens it.
// There's an inherent (tiny) race window between Close and ListenAndServe;
// for a single-test laptop process the window is acceptable. If the race
// becomes a problem, swap to a net.Listener passed straight into
// http.Server.Serve.
func pickFreePort(t testing.TB) int {
	t.Helper()
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("release free port listener: %v", err)
	}
	return port
}

// testLogger builds a slog.Logger that routes records through t.Logf so the
// log output interleaves correctly with `go test -v` output and is dropped
// otherwise. Level info matches production; debug-level bun query lines stay
// quiet by default.
func testLogger(t testing.TB) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(testWriter{t: t}, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// testWriter adapts t.Logf to io.Writer so slog records reach the test
// transcript without bypassing testing's per-test buffering.
type testWriter struct {
	t testing.TB
}

// Write forwards the formatted slog line into t.Logf. Trailing newlines are
// trimmed because t.Logf appends its own.
func (w testWriter) Write(p []byte) (int, error) {
	n := len(p)
	for n > 0 && (p[n-1] == '\n' || p[n-1] == '\r') {
		n--
	}
	w.t.Logf("%s", p[:n])
	return len(p), nil
}

// waitForListening polls the running server until /healthz answers (or a
// short deadline elapses). It also returns early if the listener goroutine
// pushed an error, so a port-bind failure surfaces as a t.Fatalf instead of
// a 30s timeout.
//
// The probe is `GET /healthz` because the route is unconditionally mounted
// by app.Wire — any wired server answers it. The function makes no
// assertions on the body; that is the smoke test's job.
func waitForListening(t testing.TB, baseURL string, listenErrors <-chan error) {
	t.Helper()
	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case err := <-listenErrors:
			t.Fatalf("http server listen: %v", err)
		default:
		}
		resp, err := client.Get(baseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not start accepting connections at %s within 5s", baseURL)
}
