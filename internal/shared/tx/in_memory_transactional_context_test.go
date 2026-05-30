// in_memory_transactional_context_test.go covers Slice 1's behavioural ACs
// and the atomicity invariants from the Phase 3 spec's DoD section against
// the real InMemoryTransactionalContext + real InMemoryEventBus. Stdlib
// testing only; no mocks; spec-local decorators (flakyBus, recordingHandler)
// are unexported and live in this file.
//
// Invariants exercised:
//
//   - work error → no stages run, no events publish, error is wrapped
//   - stage closures run in registration order
//   - staged events publish in registration order
//   - stage closure error mid-commit → later closures don't run, events
//     don't publish, Run returns the stage error UNWRAPPED so errors.Is works
//   - staged writes happen-before staged events (writes commit first)
//   - bus.Publish failure for one event does not stop later publishes;
//     Run returns nil; failure is logged at error level with structured
//     fields (assertion via a slog.Handler test double, not string match)
//   - mixed event types all publish
//   - empty Run (no stages, no events, nil work) succeeds with no side
//     effects
//   - StageEvent without any Stage still publishes
//   - Stage without any StageEvent still commits
package tx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// testEvent is the synthetic DomainEvent used throughout this file. Declared
// locally so the tx package itself owns no business imports.
type testEvent struct {
	name string
}

func (e testEvent) Type() string { return e.name }

// silentLogger returns a *slog.Logger that drops every record. Used when the
// test does not need to inspect log output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// captureEvents subscribes a recording handler against the supplied event
// types on bus, returning the appended slice (read after Run returns) and an
// unsubscribe helper for tests that care about teardown ordering.
func captureEvents(t *testing.T, bus *events.InMemoryEventBus, types ...string) *[]events.DomainEvent {
	t.Helper()
	var (
		captured []events.DomainEvent
		mu       sync.Mutex
	)
	for _, evtType := range types {
		bus.Subscribe(evtType, func(_ context.Context, evt events.DomainEvent) error {
			mu.Lock()
			captured = append(captured, evt)
			mu.Unlock()
			return nil
		})
	}
	return &captured
}

// -----------------------------------------------------------------------------
// Spec-local decorators
// -----------------------------------------------------------------------------

// flakyBus delegates Publish to the inner bus for every event type EXCEPT
// failOn, where it returns the supplied error WITHOUT delegating. Subscribe
// passes through unchanged so handler registration still works.
type flakyBus struct {
	inner  *events.InMemoryEventBus
	failOn string
	err    error
}

func (b *flakyBus) Publish(ctx context.Context, evt events.DomainEvent) error {
	if evt.Type() == b.failOn {
		return b.err
	}
	return b.inner.Publish(ctx, evt)
}

func (b *flakyBus) Subscribe(eventType string, handler func(ctx context.Context, evt events.DomainEvent) error) events.Unsubscribe {
	return b.inner.Subscribe(eventType, handler)
}

// recordingHandler captures every log record into a slice for structured-
// field assertions. Replaces string-match assertions that break when slog's
// rendering changes.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, record.Clone())
	h.mu.Unlock()
	return nil
}
func (h *recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *recordingHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func TestInMemory_HappyPath_StageAndEventCommitAndPublish(t *testing.T) {
	bus := events.NewInMemoryEventBus(silentLogger())
	captured := captureEvents(t, bus, "TestEvent")
	ctx := context.Background()

	cell := 0
	txc := NewInMemoryTransactionalContext(bus, silentLogger())
	txc.Stage(func(_ context.Context) error { cell++; return nil })
	txc.StageEvent(testEvent{name: "TestEvent"})

	if err := txc.Run(ctx, func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if cell != 1 {
		t.Fatalf("cell = %d, want 1 (staged closure should have run)", cell)
	}
	if len(*captured) != 1 {
		t.Fatalf("captured events = %d, want 1", len(*captured))
	}
	if (*captured)[0].Type() != "TestEvent" {
		t.Fatalf("captured[0].Type() = %q, want %q", (*captured)[0].Type(), "TestEvent")
	}
}

func TestInMemory_WorkError_DiscardsStagesAndEvents(t *testing.T) {
	bus := events.NewInMemoryEventBus(silentLogger())
	captured := captureEvents(t, bus, "TestEvent")
	ctx := context.Background()

	cell := 0
	txc := NewInMemoryTransactionalContext(bus, silentLogger())
	txc.Stage(func(_ context.Context) error { cell++; return nil })
	txc.StageEvent(testEvent{name: "TestEvent"})

	workErr := errors.New("work failed")
	got := txc.Run(ctx, func(_ context.Context) error { return workErr })

	if !errors.Is(got, workErr) {
		t.Fatalf("Run error = %v, want it to wrap %v", got, workErr)
	}
	if !strings.Contains(got.Error(), "tx work") {
		t.Fatalf("Run error %q missing 'tx work' wrapping prefix", got.Error())
	}
	if cell != 0 {
		t.Fatalf("cell = %d, want 0 (staged closure must NOT run on work error)", cell)
	}
	if len(*captured) != 0 {
		t.Fatalf("captured events = %d, want 0 (no event must publish on work error)", len(*captured))
	}
}

func TestInMemory_StageOrderPreservedAtCommit(t *testing.T) {
	bus := events.NewInMemoryEventBus(silentLogger())
	ctx := context.Background()

	var order []int
	txc := NewInMemoryTransactionalContext(bus, silentLogger())
	txc.Stage(func(_ context.Context) error { order = append(order, 1); return nil })
	txc.Stage(func(_ context.Context) error { order = append(order, 2); return nil })
	txc.Stage(func(_ context.Context) error { order = append(order, 3); return nil })

	if err := txc.Run(ctx, func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := []int{1, 2, 3}
	if !equalInts(order, want) {
		t.Fatalf("stage order = %v, want %v", order, want)
	}
}

func TestInMemory_StageEventOrderPreserved(t *testing.T) {
	bus := events.NewInMemoryEventBus(silentLogger())
	captured := captureEvents(t, bus, "E1", "E2", "E3")
	ctx := context.Background()

	txc := NewInMemoryTransactionalContext(bus, silentLogger())
	txc.StageEvent(testEvent{name: "E1"})
	txc.StageEvent(testEvent{name: "E2"})
	txc.StageEvent(testEvent{name: "E3"})

	if err := txc.Run(ctx, func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := []string{(*captured)[0].Type(), (*captured)[1].Type(), (*captured)[2].Type()}
	want := []string{"E1", "E2", "E3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("captured[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestInMemory_WritesHappenBeforeEvents(t *testing.T) {
	bus := events.NewInMemoryEventBus(silentLogger())
	var journal []string
	bus.Subscribe("TestEvent", func(_ context.Context, _ events.DomainEvent) error {
		journal = append(journal, "event")
		return nil
	})
	ctx := context.Background()

	txc := NewInMemoryTransactionalContext(bus, silentLogger())
	txc.Stage(func(_ context.Context) error { journal = append(journal, "write"); return nil })
	txc.StageEvent(testEvent{name: "TestEvent"})

	if err := txc.Run(ctx, func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := []string{"write", "event"}
	if !equalStrings(journal, want) {
		t.Fatalf("journal = %v, want %v (writes must happen-before events)", journal, want)
	}
}

func TestInMemory_StageClosureError_AbortsCommit(t *testing.T) {
	bus := events.NewInMemoryEventBus(silentLogger())
	captured := captureEvents(t, bus, "TestEvent")
	ctx := context.Background()

	stageErr := errors.New("stage closure failed")
	var laterRan bool
	txc := NewInMemoryTransactionalContext(bus, silentLogger())
	txc.Stage(func(_ context.Context) error { return stageErr })
	txc.Stage(func(_ context.Context) error { laterRan = true; return nil })
	txc.StageEvent(testEvent{name: "TestEvent"})

	got := txc.Run(ctx, func(_ context.Context) error { return nil })
	if !errors.Is(got, stageErr) {
		t.Fatalf("Run error = %v, want errors.Is == stageErr", got)
	}
	if got != stageErr {
		t.Fatalf("Run error = %v (%T), want unwrapped %v so errors.Is works directly", got, got, stageErr)
	}
	if laterRan {
		t.Fatal("later stage closure ran; want short-circuit on stage error")
	}
	if len(*captured) != 0 {
		t.Fatalf("captured events = %d, want 0 (no event must publish when a stage closure failed)", len(*captured))
	}
}

func TestInMemory_BusPublishFailure_DoesNotRollback(t *testing.T) {
	inner := events.NewInMemoryEventBus(silentLogger())
	captured := captureEvents(t, inner, "Ok")
	publishErr := errors.New("bus down")
	bus := &flakyBus{inner: inner, failOn: "Flaky", err: publishErr}

	logHandler := &recordingHandler{}
	logger := slog.New(logHandler)
	ctx := context.Background()

	cell := 0
	txc := NewInMemoryTransactionalContext(bus, logger)
	txc.Stage(func(_ context.Context) error { cell++; return nil })
	txc.StageEvent(testEvent{name: "Flaky"})
	txc.StageEvent(testEvent{name: "Ok"})

	if err := txc.Run(ctx, func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Run returned error: %v (want nil; bus failures don't roll back)", err)
	}
	if cell != 1 {
		t.Fatalf("cell = %d, want 1 (write must have committed)", cell)
	}
	if len(*captured) != 1 || (*captured)[0].Type() != "Ok" {
		t.Fatalf("captured = %v, want one 'Ok' event (loop must continue past flaky publish)", *captured)
	}

	assertLoggedPublishFailure(t, logHandler.snapshot(), "Flaky", 0, publishErr)
}

func assertLoggedPublishFailure(t *testing.T, records []slog.Record, eventType string, eventIndex int, wantErr error) {
	t.Helper()
	for _, record := range records {
		if record.Level != slog.LevelError {
			continue
		}
		attrs := collectAttrs(record)
		if attrs["event_type"] != eventType {
			continue
		}
		if attrs["event_index"] != int64(eventIndex) {
			continue
		}
		if attrs["error"] != wantErr.Error() {
			continue
		}
		return
	}
	t.Fatalf("no error-level log record matched event_type=%q event_index=%d error=%q; records=%v",
		eventType, eventIndex, wantErr.Error(), records)
}

func collectAttrs(record slog.Record) map[string]any {
	out := map[string]any{}
	record.Attrs(func(attr slog.Attr) bool {
		out[attr.Key] = attr.Value.Any()
		return true
	})
	return out
}

func TestInMemory_MultipleEventTypes_AllPublish(t *testing.T) {
	bus := events.NewInMemoryEventBus(silentLogger())
	captured := captureEvents(t, bus, "A", "B", "C")
	ctx := context.Background()

	txc := NewInMemoryTransactionalContext(bus, silentLogger())
	txc.StageEvent(testEvent{name: "A"})
	txc.StageEvent(testEvent{name: "B"})
	txc.StageEvent(testEvent{name: "C"})

	if err := txc.Run(ctx, func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(*captured) != 3 {
		t.Fatalf("captured = %d events, want 3", len(*captured))
	}
}

func TestInMemory_EmptyRun_NoSideEffects(t *testing.T) {
	bus := events.NewInMemoryEventBus(silentLogger())
	captured := captureEvents(t, bus, "TestEvent")
	ctx := context.Background()

	txc := NewInMemoryTransactionalContext(bus, silentLogger())
	if err := txc.Run(ctx, func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(*captured) != 0 {
		t.Fatalf("captured = %d events, want 0", len(*captured))
	}
}

func TestInMemory_StageEventOnly_StillPublishes(t *testing.T) {
	bus := events.NewInMemoryEventBus(silentLogger())
	captured := captureEvents(t, bus, "TestEvent")
	ctx := context.Background()

	txc := NewInMemoryTransactionalContext(bus, silentLogger())
	txc.StageEvent(testEvent{name: "TestEvent"})
	if err := txc.Run(ctx, func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(*captured) != 1 {
		t.Fatalf("captured = %d, want 1", len(*captured))
	}
}

func TestInMemory_StageOnly_StillCommits(t *testing.T) {
	bus := events.NewInMemoryEventBus(silentLogger())
	captured := captureEvents(t, bus, "TestEvent")
	ctx := context.Background()

	cell := 0
	txc := NewInMemoryTransactionalContext(bus, silentLogger())
	txc.Stage(func(_ context.Context) error { cell++; return nil })
	if err := txc.Run(ctx, func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cell != 1 {
		t.Fatalf("cell = %d, want 1", cell)
	}
	if len(*captured) != 0 {
		t.Fatalf("captured = %d, want 0 (no events were staged)", len(*captured))
	}
}

// -----------------------------------------------------------------------------
// Tiny slice helpers
// -----------------------------------------------------------------------------

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
