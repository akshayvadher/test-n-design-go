//go:build integration

// redis_test.go covers the Redis-backed BookCacheGateway against a real
// redis:7-alpine container. The unit suite (no build tags) drives the
// InMemoryBookCacheGateway in in_memory_test.go; this file pins the
// contract the catalog facade depends on when LIBRARY_REDIS_URL is set
// in production wiring.
//
// The container is started directly via testcontainers-go's redis module
// rather than going through test/support.StartRedis. The detour avoids an
// import cycle: test/support → internal/app → internal/shared/bookcache.
// The image pin (redis:7-alpine) and DOCKER_HOST discovery semantics are
// identical to test/support.StartRedis — see test/support/testcontainers.go
// for the canonical helper used by the crucial-path integration tests.
//
// Build-tag gated: `task test` (the unit suite) ignores this file
// entirely. Run with `task test:integration`.
package bookcache

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// redisImage is the Redis container image pinned to compose.yaml's
// `redis:7-alpine`, matching test/support.StartRedis 1:1.
const redisImage = "redis:7-alpine"

// containerTerminateTimeout caps how long the t.Cleanup hook waits for the
// container to terminate before logging a warning. 30s matches
// test/support.registerContainerCleanup.
const containerTerminateTimeout = 30 * time.Second

// -----------------------------------------------------------------------------
// Test substrate helpers.
// -----------------------------------------------------------------------------

// newRedisGatewayForTest spins up a redis container, opens a *redis.Client,
// constructs the gateway with the supplied TTL, registers Close on
// t.Cleanup, and returns the gateway. ttl=0 lets the gateway fall back to
// DefaultRedisTTL.
func newRedisGatewayForTest(ctx context.Context, t *testing.T, ttl time.Duration) *RedisBookCacheGateway {
	t.Helper()
	url := startRedisContainer(ctx, t)

	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse redis url %q: %v", url, err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Logf("close redis client: %v", err)
		}
	})

	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}

	return NewRedisBookCacheGateway(client, ttl, discardLogger())
}

// startRedisContainer launches a redis container, registers cleanup on
// t.Cleanup, and returns the redis:// connection URL. Inlined to avoid the
// test/support → internal/app → internal/shared/bookcache import cycle. The
// container image and cleanup semantics match test/support.StartRedis 1:1.
func startRedisContainer(ctx context.Context, t *testing.T) string {
	t.Helper()
	container, err := tcredis.Run(ctx, redisImage)
	if err != nil {
		t.Fatalf("start redis container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), containerTerminateTimeout)
		defer cancel()
		if err := container.Terminate(stopCtx); err != nil {
			t.Logf("terminate redis container: %v", err)
		}
	})
	url, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("redis connection string: %v", err)
	}
	return url
}

// discardLogger returns a slog.Logger that drops every record so test output
// stays focused on assertions.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// equalBookDto compares two BookDtos for round-trip equality without pulling
// in a third-party assertion library. Authors are compared element-wise.
func equalBookDto(a, b BookDto) bool {
	if a.BookId != b.BookId || a.Title != b.Title || a.Isbn != b.Isbn {
		return false
	}
	if len(a.Authors) != len(b.Authors) {
		return false
	}
	for i := range a.Authors {
		if a.Authors[i] != b.Authors[i] {
			return false
		}
	}
	return true
}

// -----------------------------------------------------------------------------
// Set + Get — JSON round-trip preserves every field.
// -----------------------------------------------------------------------------

func TestRedisBookCacheGateway_SetThenGet_RoundTripsBookDto(t *testing.T) {
	ctx := context.Background()
	cache := newRedisGatewayForTest(ctx, t, 0)
	want := BookDto{
		BookId:  "b-1",
		Title:   "The Pragmatic Programmer",
		Authors: []string{"Andrew Hunt", "David Thomas"},
		Isbn:    "978-0135957059",
	}

	if err := cache.Set(ctx, want.Isbn, want); err != nil {
		t.Fatalf("Set: got error %v, want nil", err)
	}

	got, err := cache.Get(ctx, want.Isbn)
	if err != nil {
		t.Fatalf("Get: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("Get: got nil, want %+v", want)
	}
	if !equalBookDto(*got, want) {
		t.Errorf("Get: got %+v, want %+v", *got, want)
	}
}

// -----------------------------------------------------------------------------
// Get on a never-set key — (nil, nil), NOT an error. The catalog facade
// relies on this signal to fall through to the repository on cache miss.
// -----------------------------------------------------------------------------

func TestRedisBookCacheGateway_Get_MissReturnsNilNil(t *testing.T) {
	ctx := context.Background()
	cache := newRedisGatewayForTest(ctx, t, 0)

	got, err := cache.Get(ctx, "isbn-never-set")
	if err != nil {
		t.Fatalf("Get on miss: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("Get on miss: got %+v, want nil", got)
	}
}

// -----------------------------------------------------------------------------
// Evict removes the entry; subsequent Get returns (nil, nil).
// -----------------------------------------------------------------------------

func TestRedisBookCacheGateway_Evict_RemovesExistingEntry(t *testing.T) {
	ctx := context.Background()
	cache := newRedisGatewayForTest(ctx, t, 0)
	stored := BookDto{BookId: "b-1", Title: "T", Isbn: "isbn-1"}
	if err := cache.Set(ctx, stored.Isbn, stored); err != nil {
		t.Fatalf("Set: got error %v, want nil", err)
	}

	if err := cache.Evict(ctx, stored.Isbn); err != nil {
		t.Fatalf("Evict: got error %v, want nil", err)
	}

	got, err := cache.Get(ctx, stored.Isbn)
	if err != nil {
		t.Fatalf("Get after Evict: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("Get after Evict: got %+v, want nil", got)
	}
}

// -----------------------------------------------------------------------------
// Evict on a missing key is idempotent — no error, no panic.
// -----------------------------------------------------------------------------

func TestRedisBookCacheGateway_Evict_MissingKeyIsNoOp(t *testing.T) {
	ctx := context.Background()
	cache := newRedisGatewayForTest(ctx, t, 0)

	if err := cache.Evict(ctx, "never-set"); err != nil {
		t.Errorf("Evict on missing key: got error %v, want nil", err)
	}
}

// -----------------------------------------------------------------------------
// Set overwrites: last write wins.
// -----------------------------------------------------------------------------

func TestRedisBookCacheGateway_Set_OverwritesExistingEntry(t *testing.T) {
	ctx := context.Background()
	cache := newRedisGatewayForTest(ctx, t, 0)
	first := BookDto{BookId: "b-1", Title: "First", Isbn: "isbn-1"}
	second := BookDto{BookId: "b-1", Title: "Second", Authors: []string{"A"}, Isbn: "isbn-1"}

	if err := cache.Set(ctx, "isbn-1", first); err != nil {
		t.Fatalf("Set first: got error %v, want nil", err)
	}
	if err := cache.Set(ctx, "isbn-1", second); err != nil {
		t.Fatalf("Set second: got error %v, want nil", err)
	}

	got, err := cache.Get(ctx, "isbn-1")
	if err != nil {
		t.Fatalf("Get: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("Get: got nil, want %+v", second)
	}
	if !equalBookDto(*got, second) {
		t.Errorf("Get: got %+v, want %+v (last write should win)", *got, second)
	}
}

// -----------------------------------------------------------------------------
// TTL expiry: a very short TTL plus a sleep beyond it yields a cache miss.
// Uses a 1-second TTL and a 1.2-second wait to keep flakiness low while still
// proving the gateway honors the configured duration.
// -----------------------------------------------------------------------------

func TestRedisBookCacheGateway_Set_HonorsConfiguredTTL(t *testing.T) {
	ctx := context.Background()
	cache := newRedisGatewayForTest(ctx, t, 1*time.Second)
	book := BookDto{BookId: "b-1", Isbn: "isbn-ttl"}

	if err := cache.Set(ctx, book.Isbn, book); err != nil {
		t.Fatalf("Set: got error %v, want nil", err)
	}

	time.Sleep(1200 * time.Millisecond)

	got, err := cache.Get(ctx, book.Isbn)
	if err != nil {
		t.Fatalf("Get after TTL: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("Get after TTL: got %+v, want nil (entry should have expired)", got)
	}
}
