//go:build integration

// bun_transactional_context_test.go covers Slice 2's bun-substrate atomicity
// ACs against a real Postgres via test/containers.StartPostgres. Stdlib testing
// only; spec-local decorators (flakyBunBus, recordingHandler) and a tiny
// inline tx_test_widgets table (created/torn-down per test, NOT a migration)
// keep the fixture out of the production schema.
//
// Invariants exercised:
//
//   - Happy path: staged INSERT + StageEvent → row persists after commit AND
//     event is captured on the bus.
//   - Work error → bun rolls back, row absent, no event published.
//   - Stage closure error → bun rolls back (even though the closure already
//     ran the INSERT inside the live tx), row absent, no event published,
//     Run returns the stage error unwrapped so errors.Is matches.
//   - Multiple Stages commit atomically: every staged INSERT is visible after
//     Run returns nil; staged events publish AFTER commit in stage order.
//   - Bus publish failure for one event does NOT roll back commit: the row
//     IS in Postgres after Run returns nil and a non-failing later event
//     still publishes.
//   - Repository-style usage: an inline widgetRepository resolves the live
//     tx handle via TxFromContext when inside Run, and falls back to the
//     base *bun.DB when called outside any Run — both paths persist rows.
//   - The TxFromContext fallback path: a direct insert outside Run succeeds.
package tx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/akshayvadher/test-n-design-go/internal/shared/db"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	eventsmemory "github.com/akshayvadher/test-n-design-go/internal/shared/events/memory"
	"github.com/akshayvadher/test-n-design-go/test/containers"
)

// -----------------------------------------------------------------------------
// Fixture: tx_test_widgets
// -----------------------------------------------------------------------------
//
// The fixture table lives ONLY in the test setup — no migration entry.
// Rationale: production migrations should never carry test-only tables, and
// the bun tx contract is substrate-agnostic, so a tiny throwaway table is
// enough to prove transactional semantics end-to-end without polluting the
// real schema.

// widget is the bun model for tx_test_widgets. Keeping the model package-
// private (test-file-scoped) is required for the integration build tag —
// nothing outside this file should reference it.
type widget struct {
	bun.BaseModel `bun:"table:tx_test_widgets,alias:w"`

	ID    string `bun:"id,pk"`
	Value int    `bun:"value,notnull"`
}

const createWidgetsSQL = `CREATE TABLE IF NOT EXISTS tx_test_widgets (
		id    TEXT PRIMARY KEY,
		value INTEGER NOT NULL
	)`

const dropWidgetsSQL = `DROP TABLE IF EXISTS tx_test_widgets`

const truncateWidgetsSQL = `TRUNCATE TABLE tx_test_widgets`

// setupBunTx boots a postgres testcontainer, wires a *bun.DB through the
// project's NewBunDB, creates the fixture table, and registers cleanup that
// drops it. Returns the live db handle and a bus the test can inspect.
func setupBunTx(t *testing.T) (*bun.DB, *eventsmemory.Bus) {
	t.Helper()
	ctx := context.Background()

	pg := containers.StartPostgres(ctx, t)
	bunDB, err := db.NewBunDB(ctx, pg.URL, db.PoolConfig{}, txTestLogger())
	if err != nil {
		t.Fatalf("NewBunDB: %v", err)
	}
	t.Cleanup(func() {
		if _, dropErr := bunDB.ExecContext(context.Background(), dropWidgetsSQL); dropErr != nil {
			t.Logf("drop tx_test_widgets: %v", dropErr)
		}
		if closeErr := bunDB.Close(); closeErr != nil {
			t.Logf("close bun.DB: %v", closeErr)
		}
	})

	if _, err := bunDB.ExecContext(ctx, createWidgetsSQL); err != nil {
		t.Fatalf("create tx_test_widgets: %v", err)
	}

	bus := eventsmemory.NewBus(txTestLogger())
	return bunDB, bus
}

// truncateWidgets clears the fixture between sub-tests sharing the same
// containerised Postgres. Cheaper than tearing the table down and recreating
// it; safe because the schema is invariant across the suite.
func truncateWidgets(t *testing.T, bunDB *bun.DB) {
	t.Helper()
	if _, err := bunDB.ExecContext(context.Background(), truncateWidgetsSQL); err != nil {
		t.Fatalf("truncate tx_test_widgets: %v", err)
	}
}

// txTestLogger returns a *slog.Logger whose output is discarded. The bun
// query hook logs at debug; the integration tests do not assert on those
// records, so we keep the test transcript clean.
func txTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// -----------------------------------------------------------------------------
// Repository-style fixture
// -----------------------------------------------------------------------------

// widgetRepository is the AC-mandated inline repo. It resolves the active
// bun handle via TxFromContext so Stage-time inserts join the live tx; when
// called outside any Run it falls back to the base *bun.DB.
type widgetRepository struct {
	db *bun.DB
}

func newWidgetRepository(db *bun.DB) *widgetRepository {
	return &widgetRepository{db: db}
}

func (r *widgetRepository) Insert(ctx context.Context, id string, value int) error {
	handle, _ := TxFromContext(ctx)
	if handle == nil {
		handle = r.db
	}
	_, err := handle.NewInsert().Model(&widget{ID: id, Value: value}).Exec(ctx)
	return err
}

// widgetExists returns true iff a row with id is present in tx_test_widgets.
// Lives outside the repository to keep the repo's surface minimal.
func widgetExists(t *testing.T, bunDB *bun.DB, id string) bool {
	t.Helper()
	count, err := bunDB.NewSelect().Model((*widget)(nil)).Where("id = ?", id).Count(context.Background())
	if err != nil {
		t.Fatalf("widget count for id=%q: %v", id, err)
	}
	return count == 1
}

// -----------------------------------------------------------------------------
// Spec-local decorators
// -----------------------------------------------------------------------------

// flakyBunBus is the bun-suite twin of flakyBus from the in-memory test
// file. Returns the supplied error when an event of type failOn is published;
// delegates every other publish + every Subscribe to the inner bus.
type flakyBunBus struct {
	inner  *eventsmemory.Bus
	failOn string
	err    error
}

func (b *flakyBunBus) Publish(ctx context.Context, evt events.DomainEvent) error {
	if evt.Type() == b.failOn {
		return b.err
	}
	return b.inner.Publish(ctx, evt)
}

func (b *flakyBunBus) Subscribe(eventType string, handler func(ctx context.Context, evt events.DomainEvent) error) events.Unsubscribe {
	return b.inner.Subscribe(eventType, handler)
}

// capturedSink records every event the bus delivers to subscribers it is
// attached to. The slice is read after Run returns.
func capturedSink(t *testing.T, bus *eventsmemory.Bus, types ...string) *[]events.DomainEvent {
	t.Helper()
	var (
		got []events.DomainEvent
		mu  sync.Mutex
	)
	for _, evtType := range types {
		bus.Subscribe(evtType, func(_ context.Context, evt events.DomainEvent) error {
			mu.Lock()
			got = append(got, evt)
			mu.Unlock()
			return nil
		})
	}
	return &got
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func TestBunTx_HappyPath_StageInsertAndEvent(t *testing.T) {
	bunDB, bus := setupBunTx(t)
	captured := capturedSink(t, bus, "TestEvent")
	repo := newWidgetRepository(bunDB)
	ctx := context.Background()

	txc := NewBunTransactionalContext(bunDB, bus, txTestLogger())
	err := txc.Run(ctx, func(rctx context.Context) error {
		txc.Stage(func(sctx context.Context) error {
			return repo.Insert(sctx, "a", 1)
		})
		txc.StageEvent(testEvent{name: "TestEvent"})
		return nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !widgetExists(t, bunDB, "a") {
		t.Fatal("widget id=a missing after successful commit")
	}
	if len(*captured) != 1 || (*captured)[0].Type() != "TestEvent" {
		t.Fatalf("captured = %v, want one TestEvent", *captured)
	}
}

func TestBunTx_WorkError_RollsBackAndDiscardsEvents(t *testing.T) {
	bunDB, bus := setupBunTx(t)
	captured := capturedSink(t, bus, "TestEvent")
	repo := newWidgetRepository(bunDB)
	ctx := context.Background()

	workErr := errors.New("work failed")
	txc := NewBunTransactionalContext(bunDB, bus, txTestLogger())
	err := txc.Run(ctx, func(rctx context.Context) error {
		txc.Stage(func(sctx context.Context) error {
			return repo.Insert(sctx, "w", 7)
		})
		txc.StageEvent(testEvent{name: "TestEvent"})
		return workErr
	})

	if !errors.Is(err, workErr) {
		t.Fatalf("Run error = %v, want errors.Is == workErr", err)
	}
	if widgetExists(t, bunDB, "w") {
		t.Fatal("widget id=w persisted after work error; expected rollback")
	}
	if len(*captured) != 0 {
		t.Fatalf("captured = %v, want 0 events on work error", *captured)
	}
}

func TestBunTx_StageClosureError_RollsBackAndDiscardsEvents(t *testing.T) {
	bunDB, bus := setupBunTx(t)
	captured := capturedSink(t, bus, "TestEvent")
	repo := newWidgetRepository(bunDB)
	ctx := context.Background()

	stageErr := errors.New("stage closure failed")
	txc := NewBunTransactionalContext(bunDB, bus, txTestLogger())
	err := txc.Run(ctx, func(rctx context.Context) error {
		txc.Stage(func(sctx context.Context) error {
			if insertErr := repo.Insert(sctx, "s", 5); insertErr != nil {
				return insertErr
			}
			return stageErr
		})
		txc.StageEvent(testEvent{name: "TestEvent"})
		return nil
	})

	if !errors.Is(err, stageErr) {
		t.Fatalf("Run error = %v, want errors.Is == stageErr", err)
	}
	if err != stageErr {
		t.Fatalf("Run error = %v (%T), want unwrapped stageErr so errors.Is matches by identity", err, err)
	}
	if widgetExists(t, bunDB, "s") {
		t.Fatal("widget id=s persisted after stage error; rollback should have discarded it")
	}
	if len(*captured) != 0 {
		t.Fatalf("captured = %v, want 0 events when a stage closure failed", *captured)
	}
}

func TestBunTx_MultipleStages_OrderPreservedAndAllCommit(t *testing.T) {
	bunDB, bus := setupBunTx(t)
	captured := capturedSink(t, bus, "E1", "E2")
	repo := newWidgetRepository(bunDB)
	ctx := context.Background()

	txc := NewBunTransactionalContext(bunDB, bus, txTestLogger())
	err := txc.Run(ctx, func(rctx context.Context) error {
		txc.Stage(func(sctx context.Context) error { return repo.Insert(sctx, "a", 1) })
		txc.StageEvent(testEvent{name: "E1"})
		txc.Stage(func(sctx context.Context) error { return repo.Insert(sctx, "b", 2) })
		txc.StageEvent(testEvent{name: "E2"})
		return nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !widgetExists(t, bunDB, "a") || !widgetExists(t, bunDB, "b") {
		t.Fatal("both staged widgets must persist atomically after commit")
	}
	if len(*captured) != 2 {
		t.Fatalf("captured = %d, want 2", len(*captured))
	}
	if (*captured)[0].Type() != "E1" || (*captured)[1].Type() != "E2" {
		t.Fatalf("event order = [%s, %s], want [E1, E2]", (*captured)[0].Type(), (*captured)[1].Type())
	}
}

func TestBunTx_StagedEventsPublishAfterCommit(t *testing.T) {
	bunDB, bus := setupBunTx(t)
	// The handler asserts the row is ALREADY visible when it fires, proving
	// the publish runs AFTER commit (not inside the tx, where the INSERT
	// would only be visible to the tx's own session).
	bus.Subscribe("TestEvent", func(_ context.Context, _ events.DomainEvent) error {
		if !widgetExists(t, bunDB, "after") {
			t.Error("event handler fired BEFORE commit became visible to outside readers")
		}
		return nil
	})
	repo := newWidgetRepository(bunDB)
	ctx := context.Background()

	txc := NewBunTransactionalContext(bunDB, bus, txTestLogger())
	err := txc.Run(ctx, func(rctx context.Context) error {
		txc.Stage(func(sctx context.Context) error { return repo.Insert(sctx, "after", 1) })
		txc.StageEvent(testEvent{name: "TestEvent"})
		return nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestBunTx_BusPublishFailure_DoesNotRollbackCommit(t *testing.T) {
	bunDB, inner := setupBunTx(t)
	captured := capturedSink(t, inner, "Ok")
	publishErr := errors.New("bus down")
	bus := &flakyBunBus{inner: inner, failOn: "Flaky", err: publishErr}

	logHandler := &recordingHandler{}
	logger := slog.New(logHandler)
	repo := newWidgetRepository(bunDB)
	ctx := context.Background()

	txc := NewBunTransactionalContext(bunDB, bus, logger)
	err := txc.Run(ctx, func(rctx context.Context) error {
		txc.Stage(func(sctx context.Context) error { return repo.Insert(sctx, "durable", 9) })
		txc.StageEvent(testEvent{name: "Flaky"})
		txc.StageEvent(testEvent{name: "Ok"})
		return nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v (want nil; bus failure must not roll back commit)", err)
	}
	if !widgetExists(t, bunDB, "durable") {
		t.Fatal("widget id=durable missing after Run returned nil; commit must have happened")
	}
	if len(*captured) != 1 || (*captured)[0].Type() != "Ok" {
		t.Fatalf("captured = %v, want one Ok event (loop must continue past flaky publish)", *captured)
	}
	assertLoggedPublishFailure(t, logHandler.snapshot(), "Flaky", 0, publishErr)
}

func TestBunTx_RepositoryFallback_OutsideRunUsesBaseDB(t *testing.T) {
	bunDB, bus := setupBunTx(t)
	repo := newWidgetRepository(bunDB)
	ctx := context.Background()

	// Direct insert, no Run, no TxFromContext-resolvable handle — repo must
	// fall back to its injected *bun.DB.
	if err := repo.Insert(ctx, "direct", 42); err != nil {
		t.Fatalf("repo.Insert outside Run: %v", err)
	}
	if !widgetExists(t, bunDB, "direct") {
		t.Fatal("widget id=direct missing after direct insert; fallback path must persist the row")
	}

	// Sanity: the bus is untouched (the test ran no Stage/StageEvent).
	_ = bus
}

func TestBunTx_TxFromContext_InsideRunReportsActiveTx(t *testing.T) {
	bunDB, bus := setupBunTx(t)
	ctx := context.Background()

	var sawTx bool
	txc := NewBunTransactionalContext(bunDB, bus, txTestLogger())
	err := txc.Run(ctx, func(rctx context.Context) error {
		handle, ok := TxFromContext(rctx)
		if !ok || handle == nil {
			t.Error("TxFromContext inside Run must return the live tx handle and ok=true")
		}
		sawTx = ok
		return nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !sawTx {
		t.Fatal("TxFromContext did not report an active tx inside Run")
	}

	// Outside Run: the helper must report no active tx.
	if handle, ok := TxFromContext(ctx); ok || handle != nil {
		t.Fatalf("TxFromContext outside Run = (%v, %v), want (nil, false)", handle, ok)
	}
}

// Speed sanity — the suite must finish well under 30s on a warm laptop. The
// testcontainers cold start dominates, but the actual tx work per test is
// sub-millisecond. We do not gate on time inside individual tests.
var _ = time.Second
