package events

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
)

// InMemoryEventBus is the Phase-1 EventBus implementation. It stores
// handlers in a per-event-type slice (preserving registration order) and
// guards mutation with a sync.RWMutex so concurrent Publish calls fan out
// without contention against each other.
//
// Publish snapshots the handler slice under RLock then iterates the snapshot
// without holding any lock — handlers are free to call Subscribe, Publish or
// the returned Unsubscribe without deadlocking. The trade-off is that a
// handler subscribed during a Publish call is not invoked by that same call,
// which matches the source TS bus's behaviour.
type InMemoryEventBus struct {
	logger   *slog.Logger
	mu       sync.RWMutex
	handlers map[string][]func(ctx context.Context, evt DomainEvent) error
}

// NewInMemoryEventBus wires an InMemoryEventBus with the supplied logger.
// The logger is required (no slog.Default fallback): consumers receive their
// logger via constructor injection, matching the rest of Phase 1's wiring.
func NewInMemoryEventBus(logger *slog.Logger) *InMemoryEventBus {
	return &InMemoryEventBus{
		logger:   logger,
		handlers: map[string][]func(ctx context.Context, evt DomainEvent) error{},
	}
}

// Publish dispatches evt to every handler registered for evt.Type() in
// registration order. Each handler runs synchronously; Publish returns only
// after the last handler returns.
//
// Handler errors and panics are absorbed: they are logged at error level
// with the event type and the Publish-time handler index, and the remaining
// handlers still run. Publish always returns nil — the bus is fire-and-forget
// at the publisher boundary.
func (b *InMemoryEventBus) Publish(ctx context.Context, evt DomainEvent) error {
	handlers := b.snapshotHandlers(evt.Type())
	for index, handler := range handlers {
		b.invokeHandler(ctx, evt, handler, index)
	}
	return nil
}

// Subscribe registers handler against eventType and returns an Unsubscribe
// closure that removes it. Subscribing to an event type with no current
// handlers is allowed: the closure removes nothing on first call and is a
// no-op on subsequent calls.
func (b *InMemoryEventBus) Subscribe(
	eventType string,
	handler func(ctx context.Context, evt DomainEvent) error,
) Unsubscribe {
	b.mu.Lock()
	b.handlers[eventType] = append(b.handlers[eventType], handler)
	subscriptionID := len(b.handlers[eventType]) - 1
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			b.removeHandler(eventType, subscriptionID)
		})
	}
}

// snapshotHandlers copies the handler slice under RLock so iteration during
// Publish does not race with Subscribe / Unsubscribe. The returned slice is
// safe to iterate without holding the lock.
func (b *InMemoryEventBus) snapshotHandlers(eventType string) []func(ctx context.Context, evt DomainEvent) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	registered := b.handlers[eventType]
	if len(registered) == 0 {
		return nil
	}

	snapshot := make([]func(ctx context.Context, evt DomainEvent) error, 0, len(registered))
	for _, handler := range registered {
		if handler == nil {
			continue
		}
		snapshot = append(snapshot, handler)
	}
	return snapshot
}

// invokeHandler runs one handler with panic + error recovery so a misbehaving
// subscriber cannot abort fanout. The handler_index field reflects the
// position within the Publish-time snapshot, not the original subscription
// order — that's what the spec mandates.
func (b *InMemoryEventBus) invokeHandler(
	ctx context.Context,
	evt DomainEvent,
	handler func(ctx context.Context, evt DomainEvent) error,
	index int,
) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error("event handler panicked",
				slog.String("event_type", evt.Type()),
				slog.Int("handler_index", index),
				slog.Any("panic", r),
				slog.String("stack", string(debug.Stack())),
			)
		}
	}()

	if err := handler(ctx, evt); err != nil {
		b.logger.Error("event handler returned error",
			slog.String("event_type", evt.Type()),
			slog.Int("handler_index", index),
			slog.String("error", err.Error()),
		)
	}
}

// removeHandler nils out the handler slot under Write lock. We keep the slot
// rather than reslicing so other Unsubscribe closures that captured later
// subscriptionIDs continue to point at the right entries; snapshotHandlers
// filters nils out at Publish time.
func (b *InMemoryEventBus) removeHandler(eventType string, subscriptionID int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	registered, ok := b.handlers[eventType]
	if !ok {
		return
	}
	if subscriptionID < 0 || subscriptionID >= len(registered) {
		return
	}
	registered[subscriptionID] = nil
	b.handlers[eventType] = registered
}
