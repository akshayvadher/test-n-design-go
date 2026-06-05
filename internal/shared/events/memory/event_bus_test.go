// in_memory_event_bus_test.go covers Slice 5's behavioural ACs end-to-end
// against the real InMemoryEventBus. Stdlib testing only, no testify, no
// mocks: handlers are inline closures that record into shared slices.
//
// The ACs exercised:
//
//   - delivery order: handlers run in registration order
//   - multi-subscriber fanout: every registered handler is invoked once
//   - handler error → error logged, fanout continues, Publish returns nil
//   - handler panic → panic recovered + logged, fanout continues, Publish returns nil
//   - events.Unsubscribe → handler is not invoked on subsequent Publish
//   - events.Unsubscribe is idempotent (second call is a no-op)
//   - no subscribers → Publish returns nil with no log noise
//   - concurrent Publish from N goroutines is race-free (verified under -race)
package memory

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// testEvent is the synthetic events.DomainEvent used throughout this file. Declared
// locally so the test owns no business imports — events stays a stdlib-only
// package per BOUNDARIES.md.
type testEvent struct {
	name string
}

func (e testEvent) Type() string { return e.name }

// newBusWithBuffer returns a bus whose logger writes to a buffer the test can
// later inspect for the structured fields the spec mandates.
func newBusWithBuffer(t *testing.T) (*Bus, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewBus(logger), buf
}

// recordingHandler returns a handler that appends its supplied index into
// invocations. Used by order + fanout tests to assert the recorded sequence.
func recordingHandler(invocations *[]int, index int) func(ctx context.Context, evt events.DomainEvent) error {
	return func(ctx context.Context, evt events.DomainEvent) error {
		*invocations = append(*invocations, index)
		return nil
	}
}

// -----------------------------------------------------------------------------
// Delivery order + fanout (table-driven)
// -----------------------------------------------------------------------------

func TestPublishInvokesHandlersInRegistrationOrder(t *testing.T) {
	cases := []struct {
		name            string
		subscriberCount int
		expected        []int
	}{
		{name: "three subscribers", subscriberCount: 3, expected: []int{0, 1, 2}},
		{name: "five subscribers", subscriberCount: 5, expected: []int{0, 1, 2, 3, 4}},
		{name: "single subscriber", subscriberCount: 1, expected: []int{0}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus, _ := newBusWithBuffer(t)
			var invocations []int

			for index := 0; index < tc.subscriberCount; index++ {
				bus.Subscribe("test.event", recordingHandler(&invocations, index))
			}

			if err := bus.Publish(context.Background(), testEvent{name: "test.event"}); err != nil {
				t.Fatalf("Publish returned non-nil error: %v", err)
			}

			if !equalInts(invocations, tc.expected) {
				t.Fatalf("invocation order mismatch: got %v, want %v", invocations, tc.expected)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Handler error: Publish returns nil, fanout continues, error is logged
// -----------------------------------------------------------------------------

func TestPublishContinuesAfterHandlerError(t *testing.T) {
	bus, logBuf := newBusWithBuffer(t)
	var invocations []int

	bus.Subscribe("test.event", recordingHandler(&invocations, 0))
	bus.Subscribe("test.event", func(ctx context.Context, evt events.DomainEvent) error {
		invocations = append(invocations, 1)
		return errors.New("boom")
	})
	bus.Subscribe("test.event", recordingHandler(&invocations, 2))

	if err := bus.Publish(context.Background(), testEvent{name: "test.event"}); err != nil {
		t.Fatalf("Publish should swallow handler errors, got: %v", err)
	}

	if !equalInts(invocations, []int{0, 1, 2}) {
		t.Fatalf("all three handlers should run; got %v", invocations)
	}

	output := logBuf.String()
	requireSubstring(t, output, "event handler returned error")
	requireSubstring(t, output, "event_type=test.event")
	requireSubstring(t, output, "handler_index=1")
	requireSubstring(t, output, "error=boom")
}

// -----------------------------------------------------------------------------
// Handler panic: Publish returns nil, fanout continues, panic is logged with stack
// -----------------------------------------------------------------------------

func TestPublishRecoversHandlerPanic(t *testing.T) {
	bus, logBuf := newBusWithBuffer(t)
	var invocations []int

	bus.Subscribe("test.event", recordingHandler(&invocations, 0))
	bus.Subscribe("test.event", func(ctx context.Context, evt events.DomainEvent) error {
		invocations = append(invocations, 1)
		panic("boom-panic")
	})
	bus.Subscribe("test.event", recordingHandler(&invocations, 2))

	if err := bus.Publish(context.Background(), testEvent{name: "test.event"}); err != nil {
		t.Fatalf("Publish should recover panics and return nil, got: %v", err)
	}

	if !equalInts(invocations, []int{0, 1, 2}) {
		t.Fatalf("all three handlers should run after a panic; got %v", invocations)
	}

	output := logBuf.String()
	requireSubstring(t, output, "event handler panicked")
	requireSubstring(t, output, "event_type=test.event")
	requireSubstring(t, output, "handler_index=1")
	requireSubstring(t, output, "boom-panic")
	requireSubstring(t, output, "stack=")
}

// -----------------------------------------------------------------------------
// events.Unsubscribe semantics (table-driven over the variants)
// -----------------------------------------------------------------------------

func TestUnsubscribeStopsHandlerFromBeingInvoked(t *testing.T) {
	cases := []struct {
		name        string
		unsubscribe func(unsub events.Unsubscribe)
	}{
		{name: "single unsubscribe", unsubscribe: func(u events.Unsubscribe) { u() }},
		{name: "double unsubscribe is idempotent", unsubscribe: func(u events.Unsubscribe) { u(); u() }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus, _ := newBusWithBuffer(t)
			var invocations []int

			unsub := bus.Subscribe("test.event", recordingHandler(&invocations, 0))
			bus.Subscribe("test.event", recordingHandler(&invocations, 1))

			tc.unsubscribe(unsub)

			if err := bus.Publish(context.Background(), testEvent{name: "test.event"}); err != nil {
				t.Fatalf("Publish returned non-nil error: %v", err)
			}

			if !equalInts(invocations, []int{1}) {
				t.Fatalf("unsubscribed handler must not run; got %v", invocations)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// No-subscriber publish and never-handlers-subscribed
// -----------------------------------------------------------------------------

func TestPublishWithNoSubscribersIsNoOp(t *testing.T) {
	bus, logBuf := newBusWithBuffer(t)

	if err := bus.Publish(context.Background(), testEvent{name: "test.unknown"}); err != nil {
		t.Fatalf("Publish with no subscribers must return nil, got: %v", err)
	}

	if logBuf.Len() != 0 {
		t.Fatalf("Publish with no subscribers should not log; got %q", logBuf.String())
	}
}

func TestSubscribeToUnusedEventTypeReturnsNoOpUnsubscribe(t *testing.T) {
	bus, _ := newBusWithBuffer(t)

	unsub := bus.Subscribe("never.fires", func(ctx context.Context, evt events.DomainEvent) error {
		return nil
	})

	// Calling events.Unsubscribe before any Publish must not panic and must be safe.
	unsub()
	unsub()
}

// -----------------------------------------------------------------------------
// Concurrent Publish — 50 goroutines × 1 publish each → handler called 50 times
//
// The assertion itself works without -race; -race is what proves the RWMutex
// discipline is correct. Slice 1's Taskfile `test` target runs with -race.
// -----------------------------------------------------------------------------

func TestConcurrentPublishIsSafe(t *testing.T) {
	t.Parallel()

	const publisherCount = 50

	bus, _ := newBusWithBuffer(t)
	var counter atomic.Int64

	bus.Subscribe("concurrent.event", func(ctx context.Context, evt events.DomainEvent) error {
		counter.Add(1)
		return nil
	})

	var wg sync.WaitGroup
	wg.Add(publisherCount)
	for i := 0; i < publisherCount; i++ {
		go func() {
			defer wg.Done()
			_ = bus.Publish(context.Background(), testEvent{name: "concurrent.event"})
		}()
	}
	wg.Wait()

	if got := counter.Load(); got != publisherCount {
		t.Fatalf("handler should be invoked %d times; got %d", publisherCount, got)
	}
}

// -----------------------------------------------------------------------------
// Event-type isolation: handlers for "a" must not fire for "b"
// -----------------------------------------------------------------------------

func TestPublishOnlyInvokesHandlersForMatchingEventType(t *testing.T) {
	bus, _ := newBusWithBuffer(t)
	var invocations []int

	bus.Subscribe("event.a", recordingHandler(&invocations, 0))
	bus.Subscribe("event.b", recordingHandler(&invocations, 1))

	if err := bus.Publish(context.Background(), testEvent{name: "event.a"}); err != nil {
		t.Fatalf("Publish returned non-nil error: %v", err)
	}

	if !equalInts(invocations, []int{0}) {
		t.Fatalf("only event.a's handler should run; got %v", invocations)
	}
}

// -----------------------------------------------------------------------------
// Small assertion helpers — stdlib only, no testify.
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

func requireSubstring(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected log output to contain %q; got %q", needle, haystack)
	}
}
