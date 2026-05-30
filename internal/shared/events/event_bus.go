// Package events provides the in-process domain-event bus that Phase 1 ships
// with. The bus is intentionally minimal: a single Publish that fans out
// synchronously to every handler subscribed for the event's Type(), and a
// Subscribe that returns an Unsubscribe closure.
//
// Phase 1 ships only the in-memory implementation (InMemoryEventBus).
// Durable transports (outbox, Redis Streams, Kafka) are out of scope per the
// phase-1 spec — when they arrive, they implement EventBus and consumers
// switch transports at the composition root with no module-level changes.
//
// Per BOUNDARIES.md the events package depends on the standard library only.
// No bun, no chi, no business modules.
package events

import "context"

// DomainEvent is the marker any event payload must implement so the bus can
// dispatch it. Type() is the string key handlers subscribe against — by
// convention modules use dotted lowercase identifiers (e.g. "lending.loan_opened").
type DomainEvent interface {
	Type() string
}

// Unsubscribe removes the subscription it was returned from. Calling it twice
// is safe — the second call is a no-op. After Unsubscribe runs, subsequent
// Publish calls for the same event type do not invoke the original handler.
type Unsubscribe func()

// EventBus is the publisher/subscriber surface. Publish is fire-and-forget at
// the publisher boundary: handler errors are logged and swallowed, the call
// returns nil. Subscribe registers a handler against an event type and hands
// back an Unsubscribe closure.
//
// Implementations may dispatch synchronously (InMemoryEventBus) or
// asynchronously (durable buses in later phases). Phase 1 ships only the
// synchronous in-memory variant.
type EventBus interface {
	Publish(ctx context.Context, evt DomainEvent) error
	Subscribe(eventType string, handler func(ctx context.Context, evt DomainEvent) error) Unsubscribe
}
