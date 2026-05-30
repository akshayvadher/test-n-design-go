//go:build integration

// Package containers owns the testcontainers helpers — StartPostgres and
// StartRedis — used by both the per-package integration tests and by
// test/support.BootApp. It depends only on the standard library +
// testcontainers-go + internal/shared/db.
//
// Splitting this away from test/support (which imports internal/app) is
// necessary to avoid an import cycle: internal/shared/tx (Phase 2 +
// Phase 3) hosts an integration test that needs a real Postgres; if it
// imported test/support, and test/support → internal/app → internal/lending
// → internal/shared/tx, the build would fail. test/containers lives outside
// that graph so the tx integration tests can spin up Postgres without the
// composition root coming along for the ride.
//
// DOCKER_HOST setup (Windows + podman):
//
// testcontainers-go locates the container engine via the DOCKER_HOST env
// var. On Windows with podman the value is the named pipe of your podman
// machine — typically:
//
//	DOCKER_HOST=npipe:////./pipe/podman-machine-default
//
// See Taskfile.yml's comment block for the canonical instructions.
package containers

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/akshayvadher/test-n-design-go/internal/shared/db"
)

// postgresImage is the Postgres image the integration suite spins up. Pinned
// to match compose.yaml's `postgres:16-alpine` so dev and CI agree on the
// engine version.
const postgresImage = "postgres:16-alpine"

// redisImage is the Redis image the integration suite spins up. Pinned to
// match compose.yaml's `redis:7-alpine`.
const redisImage = "redis:7-alpine"

// postgresStartupTimeout caps how long StartPostgres waits for the container
// to become ready before failing the test. 60s is generous for cold-start
// laptops; testcontainers-go's internal wait strategy usually returns in
// 3–10s on a warm machine.
const postgresStartupTimeout = 60 * time.Second

// migrationsDir returns the absolute path to the repo-root `migrations/`
// directory. Walking up from this file's runtime location keeps the helper
// self-contained.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	// file = .../test/containers/testcontainers.go  →  ../../migrations
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations")
}

// PostgresContainer is the handle StartPostgres returns. URL is the canonical
// connection string (sslmode=disable) suitable for passing into
// db.NewBunDB and db.ApplyMigrations. Teardown is registered via
// t.Cleanup so callers do not need to track the container themselves.
type PostgresContainer struct {
	URL string
}

// RedisContainer is the handle StartRedis returns. URL is the canonical
// redis:// connection string. Teardown is registered via t.Cleanup.
type RedisContainer struct {
	URL string
}

// StartPostgres spins up a Postgres container, waits until it accepts
// connections, applies the migrations under migrations/ via
// db.ApplyMigrations, and returns a PostgresContainer carrying the
// connection URL. The container's lifetime is bound to t.Cleanup.
func StartPostgres(ctx context.Context, t testing.TB) PostgresContainer {
	t.Helper()

	container, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("library"),
		tcpostgres.WithUsername("library"),
		tcpostgres.WithPassword("library"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(postgresStartupTimeout),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	registerContainerCleanup(t, container, "postgres")

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres connection string: %v", err)
	}

	if err := db.ApplyMigrations(ctx, url, migrationsDir(), discardLogger()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	return PostgresContainer{URL: url}
}

// StartRedis spins up a Redis container, waits until it accepts connections,
// and returns a RedisContainer carrying the redis:// URL. Teardown via
// t.Cleanup.
func StartRedis(ctx context.Context, t testing.TB) RedisContainer {
	t.Helper()

	container, err := tcredis.Run(ctx, redisImage)
	if err != nil {
		t.Fatalf("start redis container: %v", err)
	}
	registerContainerCleanup(t, container, "redis")

	url, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("redis connection string: %v", err)
	}

	return RedisContainer{URL: url}
}

// registerContainerCleanup binds container teardown to t.Cleanup. Cleanup uses
// a fresh background context with a bounded deadline so an already-cancelled
// test context does not strand the container.
func registerContainerCleanup(t testing.TB, container testcontainers.Container, label string) {
	t.Helper()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := container.Terminate(stopCtx); err != nil {
			t.Logf("terminate %s container: %v", label, err)
		}
	})
}

// discardLogger returns a *slog.Logger that drops every record. Used by
// StartPostgres so atlas migrate's stdout does not bleed into the test
// transcript at info level.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// discardWriter is an io.Writer that ignores everything.
type discardWriter struct{}

// Write satisfies io.Writer by reporting every byte as accepted.
func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
