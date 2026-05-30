//go:build integration

// Package support contains the integration-test harness: the BootApp
// factory that wires the full composition root, plus thin re-export
// shims for the test/containers helpers (StartPostgres, StartRedis).
//
// All files here carry the `integration` build tag so `task test` (no tag)
// completely ignores this package — only `task test:integration` (which
// passes `-tags=integration`) compiles it. The unit suite therefore stays
// container-free and sub-second.
//
// The actual testcontainers logic lives in test/containers so packages
// that need only a real Postgres (e.g. internal/shared/tx integration
// tests) can import it without dragging in internal/app (and through it,
// every business module — which would create import cycles for modules
// that internal/app itself imports).
package support

import (
	"context"
	"testing"

	"github.com/akshayvadher/test-n-design-go/test/containers"
)

// PostgresContainer is the handle StartPostgres returns. Aliased to the
// canonical type in test/containers so callers see no API change.
type PostgresContainer = containers.PostgresContainer

// RedisContainer is the handle StartRedis returns. Aliased to the
// canonical type in test/containers.
type RedisContainer = containers.RedisContainer

// StartPostgres spins up a Postgres container, waits until it accepts
// connections, applies the migrations under migrations/ via
// db.ApplyMigrations, and returns a PostgresContainer carrying the
// connection URL.
func StartPostgres(ctx context.Context, t testing.TB) PostgresContainer {
	return containers.StartPostgres(ctx, t)
}

// StartRedis spins up a Redis container, waits until it accepts
// connections, and returns a RedisContainer carrying the redis:// URL.
func StartRedis(ctx context.Context, t testing.TB) RedisContainer {
	return containers.StartRedis(ctx, t)
}
