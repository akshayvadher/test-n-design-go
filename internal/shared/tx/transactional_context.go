// Package tx owns the TransactionalContext abstraction every business module
// uses to wrap a single atomic business operation. A TransactionalContext
// instance is created and used inside ONE module (never shared across module
// boundaries — cross-module consistency is achieved via events, not via a
// shared transaction; see .claude/BOUNDARIES.md).
//
// Phase 3 ships two implementations of the interface:
//
//   - InMemoryTransactionalContext — stage-and-commit semantics against
//     in-memory state, used by unit tests and the in-memory wiring path.
//   - BunTransactionalContext (added in Slice 2) — wraps bun.DB.RunInTx and
//     executes staged closures inside the live tx callback.
//
// The interface deliberately hides the underlying transaction handle: a bun.Tx
// or *sql.Tx must never leak through this surface. Repositories that need the
// live handle resolve it from context via a substrate-specific helper exported
// alongside the bun impl.
//
// Per BOUNDARIES.md this package depends only on stdlib + internal/shared/events.
package tx

import (
	"context"

	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
)

// TransactionalContext is the abstraction every business operation uses to
// atomically combine its own-module writes with the post-commit publication of
// domain events. The contract:
//
//   - Run(ctx, work) executes work(ctx). If work returns an error, NOTHING the
//     work staged runs: staged closures are discarded, staged events are
//     discarded, and Run returns the wrapped work error.
//
//   - Stage(apply) registers a write closure to execute as part of the tx. In
//     the in-memory impl the closure runs during commit (after work returns
//     nil), in stage order. In the bun impl the closure runs immediately
//     inside the tx callback against the live tx handle. Either way, a stage
//     closure that returns an error aborts the commit: no further stage
//     closures run, no events publish, and Run returns the stage error.
//
//   - StageEvent(evt) registers a DomainEvent to publish AFTER the underlying
//     transaction commits, in stage order. A crash between commit and publish
//     drops the event — see discovery D27/D28; we accept this gap rather than
//     ship an outbox in the teaching repo. Bus-publish failures during the
//     post-commit loop are LOGGED at error level but do NOT roll back the
//     commit; Run still returns nil.
//
//   - For cross-module mutations that must run AFTER commit (e.g. catalog
//     mark-copy-unavailable from lending.Borrow), the caller runs them
//     OUTSIDE Run — NOT via Stage. Stage is for own-module writes that
//     participate in the tx; cross-module writes are sequenced by the caller
//     after Run returns nil.
//
// A TransactionalContext is single-shot: callers construct a fresh instance
// per business operation via TransactionalContextFactory and do NOT reuse it
// across operations. It is safe to use from a single goroutine only.
type TransactionalContext interface {
	Run(ctx context.Context, work func(ctx context.Context) error) error
	Stage(apply func(ctx context.Context) error)
	StageEvent(evt events.DomainEvent)
}

// TransactionalContextFactory produces a fresh TransactionalContext per
// invocation. Modules receive the factory via constructor injection and call
// it once per business operation. The factory closes over the dependencies
// the chosen substrate needs (an EventBus for both substrates, plus a *bun.DB
// for the bun substrate).
type TransactionalContextFactory = func() TransactionalContext
