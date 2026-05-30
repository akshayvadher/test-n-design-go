//go:build integration

// Package support contains the integration-test harness: testcontainers
// helpers that start Postgres + Redis on demand, and an app factory that
// boots the full composition root against those containers.
//
// All files here carry the `integration` build tag so `task test` (no tag)
// completely ignores this package — only `task test:integration` (which
// passes `-tags=integration`) compiles it. The unit suite therefore stays
// container-free and sub-second.
//
// # DOCKER_HOST setup (Windows + podman)
//
// testcontainers-go locates the container engine via the DOCKER_HOST env
// var. On Windows with podman the value is the named pipe of your podman
// machine — typically:
//
//	DOCKER_HOST=npipe:////./pipe/podman-machine-default
//
// Discover the exact path with:
//
//	podman machine inspect | jq -r '.[].ConnectionInfo.PodmanPipe.Path'
//
// On Linux:   DOCKER_HOST=unix:///run/user/$UID/podman/podman.sock
// On macOS:   DOCKER_HOST=$(podman machine inspect | jq -r '.[].ConnectionInfo.PodmanSocket.Path')
//
// See `Taskfile.yml`'s comment block for the canonical instructions. These
// helpers DO NOT hardcode the pipe path — they rely on testcontainers-go's
// default DOCKER_HOST discovery, so the developer sets DOCKER_HOST in their
// shell once and the integration suite picks it up automatically.
package support

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
// directory. `go test` runs each package in its own directory (not the repo
// root), so relative paths like `migrations` won't resolve from the
// test/integration package. Walking up from this file's runtime location
// keeps the helper self-contained and survives any future relayout of the
// test/ tree as long as `test/support/` stays two levels deep.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	// file = .../test/support/testcontainers.go  →  ../../migrations
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
// connections, applies the migrations under migrations/ via db.ApplyMigrations
// (the SAME path `task migrate:apply` uses — no parallel implementation), and
// returns a PostgresContainer carrying the connection URL.
//
// The container's lifetime is bound to t.Cleanup: when the test (or its
// parent) finishes, Terminate is called. A non-nil termination error is
// reported via t.Logf rather than t.Errorf so a slow cleanup does not mark a
// passing test as failed.
//
// Implementation notes:
//   - testcontainers-go discovers podman via DOCKER_HOST; this function does
//     not touch the env var.
//   - The wait strategy is testcontainers-go's WaitForLog with the standard
//     "database system is ready to accept connections" line (occurrence 2 —
//     Postgres logs the line twice during init).
//   - Migrations are applied with a discard logger because the test owns
//     its own slog; passing the test's logger here would interleave bun
//     query lines with atlas migrate output in t.Logf.
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
// t.Cleanup. Mirrors StartPostgres's shape so test setup is symmetric.
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

// discardWriter is an io.Writer that ignores everything. Cheaper than
// io.Discard wrapping because slog only calls Write — no Close, no flush.
type discardWriter struct{}

// Write satisfies io.Writer by reporting every byte as accepted without doing
// anything. Returning len(p) keeps slog's handler from retrying.
func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
