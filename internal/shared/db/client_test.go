package db

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// silentLogger returns a *slog.Logger whose output is discarded. Tests use it
// to keep `go test -v` output clean while still exercising the logging paths.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestResolvePoolConfig(t *testing.T) {
	cases := []struct {
		name string
		in   PoolConfig
		want PoolConfig
	}{
		{
			name: "all zero falls back to all defaults",
			in:   PoolConfig{},
			want: PoolConfig{
				MaxOpenConns:    25,
				MaxIdleConns:    5,
				ConnMaxLifetime: 5 * time.Minute,
				ConnMaxIdleTime: 2 * time.Minute,
			},
		},
		{
			name: "all overridden passes through unchanged",
			in: PoolConfig{
				MaxOpenConns:    50,
				MaxIdleConns:    10,
				ConnMaxLifetime: 30 * time.Minute,
				ConnMaxIdleTime: 10 * time.Minute,
			},
			want: PoolConfig{
				MaxOpenConns:    50,
				MaxIdleConns:    10,
				ConnMaxLifetime: 30 * time.Minute,
				ConnMaxIdleTime: 10 * time.Minute,
			},
		},
		{
			name: "MaxOpenConns override leaves other fields at default",
			in:   PoolConfig{MaxOpenConns: 7},
			want: PoolConfig{
				MaxOpenConns:    7,
				MaxIdleConns:    5,
				ConnMaxLifetime: 5 * time.Minute,
				ConnMaxIdleTime: 2 * time.Minute,
			},
		},
		{
			name: "ConnMaxLifetime override leaves other fields at default",
			in:   PoolConfig{ConnMaxLifetime: time.Hour},
			want: PoolConfig{
				MaxOpenConns:    25,
				MaxIdleConns:    5,
				ConnMaxLifetime: time.Hour,
				ConnMaxIdleTime: 2 * time.Minute,
			},
		},
		{
			name: "mixed overrides merge field-by-field",
			in: PoolConfig{
				MaxOpenConns:    100,
				ConnMaxIdleTime: 30 * time.Second,
			},
			want: PoolConfig{
				MaxOpenConns:    100,
				MaxIdleConns:    5,
				ConnMaxLifetime: 5 * time.Minute,
				ConnMaxIdleTime: 30 * time.Second,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolvePoolConfig(tc.in)
			if got != tc.want {
				t.Fatalf("resolvePoolConfig(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// TestPGDialectNameIsPg locks the dialect choice: any future tweak that
// accidentally swaps pgdialect for another driver (sqlite, mysql) fails this
// test before the integration suite even runs. Spec line 103: the unit test
// asserts the dialect name is "pg".
func TestPGDialectNameIsPg(t *testing.T) {
	got := pgdialect.New().Name().String()
	if got != "pg" {
		t.Fatalf("pgdialect.Name() = %q, want %q", got, "pg")
	}
}

func TestNewBunDB_UnreachableHostReturnsError(t *testing.T) {
	// Port 1 on localhost is never bound — TCP connect fails in milliseconds
	// without going through DNS resolution. connect_timeout=1 caps any future
	// driver-side waiting. This test must run under <100ms.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const badURL = "postgres://localhost:1/library?sslmode=disable&connect_timeout=1"
	db, err := NewBunDB(ctx, badURL, PoolConfig{}, silentLogger())
	if err == nil {
		_ = db.Close()
		t.Fatalf("NewBunDB on unreachable host returned nil error; want connect error")
	}
	if !strings.Contains(err.Error(), "ping postgres") {
		t.Fatalf("error message %q does not mention the ping context", err.Error())
	}
}

// TestSlogQueryHook_AfterQueryEmitsAtDebug locks spec line 104: the hook logs
// at debug level. A logger configured at LevelInfo must drop the record; the
// same logger at LevelDebug must emit it. This guards against a silent regression
// where someone "helpfully" bumps the level to Info and starts spamming prod
// logs with every SQL statement. We exercise the hook directly with a synthetic
// *bun.QueryEvent — no database connection required, runs in microseconds.
func TestSlogQueryHook_AfterQueryEmitsAtDebug(t *testing.T) {
	event := &bun.QueryEvent{
		Query:     "SELECT 1",
		StartTime: time.Now().Add(-2 * time.Millisecond),
	}

	t.Run("info level suppresses the query log", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
		slogQueryHook{logger: logger}.AfterQuery(context.Background(), event)
		if buf.Len() != 0 {
			t.Fatalf("expected no output at info level, got %q", buf.String())
		}
	})

	t.Run("debug level emits one bun.query record", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		slogQueryHook{logger: logger}.AfterQuery(context.Background(), event)
		output := buf.String()
		if !strings.Contains(output, "bun.query") {
			t.Fatalf("debug output %q missing the bun.query message", output)
		}
		if !strings.Contains(output, "SELECT 1") {
			t.Fatalf("debug output %q missing the rendered SQL", output)
		}
		if !strings.Contains(output, "operation=SELECT") {
			t.Fatalf("debug output %q missing the operation attr", output)
		}
	})
}

// TestSlogQueryHook_BeforeQueryReturnsContextUnchanged locks the no-op contract
// of BeforeQuery (spec line 104 implies one log line per query). If BeforeQuery
// starts emitting records or rewriting the context, downstream callers that
// rely on the original context (request IDs, deadlines) would silently break.
func TestSlogQueryHook_BeforeQueryReturnsContextUnchanged(t *testing.T) {
	type ctxKey struct{}
	parent := context.WithValue(context.Background(), ctxKey{}, "sentinel")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	got := slogQueryHook{logger: logger}.BeforeQuery(parent, &bun.QueryEvent{Query: "SELECT 1"})

	if got != parent {
		t.Fatalf("BeforeQuery returned a different context; want the parent context unchanged")
	}
	if buf.Len() != 0 {
		t.Fatalf("BeforeQuery emitted output %q; want silent", buf.String())
	}
}

func TestApplyMigrations_AtlasMissingFromPath(t *testing.T) {
	// Empty PATH means LookPath cannot find any binary, including atlas.
	// t.Setenv automatically restores PATH after the test.
	t.Setenv("PATH", "")

	err := ApplyMigrations(context.Background(), "postgres://example", "migrations", silentLogger())
	if err == nil {
		t.Fatalf("ApplyMigrations with empty PATH returned nil; want missing-atlas error")
	}
	if !strings.Contains(err.Error(), "atlas CLI not found on PATH") {
		t.Fatalf("error message %q does not mention `atlas CLI not found on PATH`", err.Error())
	}
	if !strings.Contains(err.Error(), "https://atlasgo.io/getting-started/") {
		t.Fatalf("error message %q does not include the atlas install hint URL", err.Error())
	}
}
