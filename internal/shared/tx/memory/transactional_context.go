package memory

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// TransactionalContext is the in-memory substrate for the
// tx.TransactionalContext interface. It buffers staged write closures
// and staged events during Run's work callback, then applies the writes
// (in stage order) and publishes the events (in stage order) on
// successful completion.
//
// Semantics matrix:
//
//   - work returns error: discard staged closures + events, return
//     wrapped work error.
//   - work returns nil, stage closure returns error: return the first
//     stage closure error, do not run remaining stage closures, do not
//     publish any events.
//   - work + stages succeed: publish staged events in stage order; log +
//     skip any individual publish error; Run returns nil.
//
// The contract is single-shot and single-goroutine: construct a fresh
// instance per business operation via tx.TransactionalContextFactory.
// The implementation does NOT clear its buffers after Run returns, so
// reusing an instance across operations is a programming error.
type TransactionalContext struct {
	bus    events.EventBus
	logger *slog.Logger
	staged []func(ctx context.Context) error
	events []events.DomainEvent
}

// Compile-time assertion that *TransactionalContext satisfies the
// tx.TransactionalContext port.
var _ tx.TransactionalContext = (*TransactionalContext)(nil)

// NewTransactionalContext returns a fresh *TransactionalContext with
// empty stage + event buffers. The bus is where staged events publish
// after a successful commit; the logger receives error-level records
// when an individual post-commit publish fails (bus failures do not
// roll back the commit).
func NewTransactionalContext(bus events.EventBus, logger *slog.Logger) *TransactionalContext {
	return &TransactionalContext{
		bus:    bus,
		logger: logger,
	}
}

// Stage appends apply to the staged-write buffer. The closure runs
// during commit (after work returns nil), receiving the same context
// Run was called with. A stage closure that returns an error aborts the
// commit: no further stage closures run, no events publish, and Run
// returns the stage error.
func (c *TransactionalContext) Stage(apply func(ctx context.Context) error) {
	c.staged = append(c.staged, apply)
}

// StageEvent appends evt to the staged-event buffer. Events publish
// after every staged closure has run successfully, in stage order, via
// the bus the context was constructed with.
func (c *TransactionalContext) StageEvent(evt events.DomainEvent) {
	c.events = append(c.events, evt)
}

// Run executes work(ctx) and, on success, commits: applies staged
// closures in order then publishes staged events in order. See the type
// doc for the full semantics matrix.
func (c *TransactionalContext) Run(ctx context.Context, work func(ctx context.Context) error) error {
	if err := work(ctx); err != nil {
		return fmt.Errorf("tx work: %w", err)
	}
	if err := c.applyStaged(ctx); err != nil {
		return err
	}
	c.publishStaged(ctx)
	return nil
}

// applyStaged runs every staged closure in order. The first error short-
// circuits the loop: no later closures run, no events publish, and Run
// returns the error.
func (c *TransactionalContext) applyStaged(ctx context.Context) error {
	for _, apply := range c.staged {
		if err := apply(ctx); err != nil {
			return err
		}
	}
	return nil
}

// publishStaged publishes every staged event in order. A bus failure
// for one event is logged at error level but does not abort the loop or
// fail Run — the commit has already happened and the bus is
// fire-and-forget at the publisher boundary.
func (c *TransactionalContext) publishStaged(ctx context.Context) {
	for index, evt := range c.events {
		if err := c.bus.Publish(ctx, evt); err != nil {
			c.logger.Error("staged event publish failed",
				slog.String("event_type", evt.Type()),
				slog.Int("event_index", index),
				slog.String("error", err.Error()),
			)
		}
	}
}
