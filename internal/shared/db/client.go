// Package db owns the bun client factory and the atlas migration runner.
//
// This package is the canonical Postgres entry point for every business
// module: it returns a `*bun.DB` wired with the locked driver
// (`github.com/uptrace/bun/driver/pgdriver`), pool sizing, and a debug-level
// slog query hook. No business types live here — repositories own those.
//
// The package depends only on stdlib + bun (`bun`, `bun/dialect/pgdialect`,
// `bun/driver/pgdriver`). It must not import any other `internal/*` package.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// PoolConfig holds the optional database/sql pool tuning knobs that NewBunDB
// applies to the underlying *sql.DB. Each field is independently optional:
// zero values fall back to the module default (NOT to database/sql's native
// zero behaviour, which means "unbounded" for MaxOpenConns and "no limit" for
// the lifetimes — those defaults are not what we want).
//
// Field defaults — conservative defaults, laptop-dev friendly and suitable for
// a small prod deployment; override in cmd/library/main.go for high-throughput
// deployments:
//
//   - MaxOpenConns    = 25
//     Total open connections (in-use + idle). Caps load on Postgres.
//   - MaxIdleConns    = 5
//     Idle connections kept in the pool, ready for reuse without a TCP
//     handshake. Keep small to avoid hoarding server-side resources.
//   - ConnMaxLifetime = 5 * time.Minute
//     Hard ceiling on a single connection's age. Forces rotation so
//     long-lived connections do not accumulate Postgres state (prepared
//     plans, idle-in-transaction locks).
//   - ConnMaxIdleTime = 2 * time.Minute
//     How long a connection may sit idle before being closed. Frees server
//     slots during quiet periods.
//
// Zero on any field falls back to the matching default above. Non-zero values
// override on a per-field basis (set one, the rest still take the defaults).
// Phase 1 has no env-var plumbing for these — overrides are construction-time
// only.
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// Module defaults for PoolConfig zero fields. Documented on PoolConfig above.
const (
	defaultMaxOpenConns    = 25
	defaultMaxIdleConns    = 5
	defaultConnMaxLifetime = 5 * time.Minute
	defaultConnMaxIdleTime = 2 * time.Minute
)

// NewBunDB opens a *bun.DB against databaseURL using bun's native pgdriver,
// applies pool sizing from pool (merged with module defaults), registers a
// debug-level slog query hook on the passed logger, and verifies connectivity
// via PingContext before returning.
//
// On a ping failure the underlying *sql.DB is closed and the returned error
// wraps the ping error so callers see both the surface and the cause.
//
// The returned *bun.DB is safe for concurrent use across goroutines: the
// underlying database/sql.DB handles pooling, and the bun query hook below is
// stateless. Callers own closing the *bun.DB (defer db.Close()).
func NewBunDB(ctx context.Context, databaseURL string, pool PoolConfig, logger *slog.Logger) (*bun.DB, error) {
	connector := pgdriver.NewConnector(pgdriver.WithDSN(databaseURL))
	sqlDB := sql.OpenDB(connector)

	resolved := resolvePoolConfig(pool)
	sqlDB.SetMaxOpenConns(resolved.MaxOpenConns)
	sqlDB.SetMaxIdleConns(resolved.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(resolved.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(resolved.ConnMaxIdleTime)

	bunDB := bun.NewDB(sqlDB, pgdialect.New(), bun.WithDiscardUnknownColumns())
	bunDB = bunDB.WithQueryHook(slogQueryHook{logger: logger})

	if err := bunDB.PingContext(ctx); err != nil {
		_ = bunDB.Close()
		return nil, fmt.Errorf("ping postgres at %q: %w", databaseURL, err)
	}
	return bunDB, nil
}

// resolvePoolConfig returns a PoolConfig with every zero field replaced by the
// module default. Non-zero fields pass through unchanged. Pure function —
// testable without a database.
func resolvePoolConfig(pool PoolConfig) PoolConfig {
	resolved := pool
	if resolved.MaxOpenConns == 0 {
		resolved.MaxOpenConns = defaultMaxOpenConns
	}
	if resolved.MaxIdleConns == 0 {
		resolved.MaxIdleConns = defaultMaxIdleConns
	}
	if resolved.ConnMaxLifetime == 0 {
		resolved.ConnMaxLifetime = defaultConnMaxLifetime
	}
	if resolved.ConnMaxIdleTime == 0 {
		resolved.ConnMaxIdleTime = defaultConnMaxIdleTime
	}
	return resolved
}

// slogQueryHook implements bun.QueryHook by logging every executed query at
// debug level. At info+ the SQL is invisible — important so tests and prod
// do not spam logs. The hook is stateless and therefore safe for concurrent
// use across goroutines.
type slogQueryHook struct {
	logger *slog.Logger
}

// BeforeQuery is a no-op: the hook only emits a single line on completion so
// each query produces one log record, not two. We return the context unchanged.
func (h slogQueryHook) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

// AfterQuery emits one debug-level log line per executed query carrying the
// rendered SQL, the operation (SELECT/INSERT/...), the elapsed duration, and
// the error if any. Logging happens only when the logger is at debug.
func (h slogQueryHook) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	attrs := []any{
		slog.String("operation", event.Operation()),
		slog.String("query", event.Query),
		slog.Duration("duration", time.Since(event.StartTime)),
	}
	if event.Err != nil {
		attrs = append(attrs, slog.String("error", event.Err.Error()))
	}
	h.logger.LogAttrs(ctx, slog.LevelDebug, "bun.query", toAttrs(attrs)...)
}

// toAttrs converts the []any built above into []slog.Attr for LogAttrs.
// LogAttrs avoids the per-call allocation that slog.Logger.Debug would incur.
func toAttrs(in []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(in))
	for _, v := range in {
		if a, ok := v.(slog.Attr); ok {
			out = append(out, a)
		}
	}
	return out
}
