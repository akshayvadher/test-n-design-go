# Sagas

A saga is a consumer that subscribes to a domain event and orchestrates a multi-step business workflow across its own transactional boundaries. It is the codebase's only mechanism for cross-module write consistency: when module A's commit needs to ripple into module B, A publishes an event and a saga consumer running with B's substrates does the follow-up work.

This document walks through the one saga the codebase ships — the auto-loan-on-return saga in `internal/lending/auto_loan_on_return.go` — and the architectural invariants every future saga must hold.

## The auto-loan-on-return saga

When a loan returns, the saga walks the pending reservation queue for that book and tries to open a fresh loan for the next eligible reserver. The trigger event is `LoanReturned`. The terminal outcomes are `AutoLoanOpened` (success) or `AutoLoanFailed` (failure).

The consumer lives in `internal/lending/auto_loan_on_return.go`. It depends on the lending facade itself, the membership facade, the reservation repository, the event bus, and the same `TransactionalContextFactory` the lending facade was wired with. It does not depend on any other module.

### The walk-through

```
LoanReturned arrives
    │
    ▼
acquire per-book mutex (bookLocks[BookId])
    │
    ▼
list pending reservations for the returned BookId (FIFO by ReservedAt)
    │
    ▼
for each reservation (in queue order):
    │
    ├─ membership.CheckEligibility(MemberId)
    │      │
    │      └─ if ineligible: skip; continue to next reservation
    │
    └─ attempt auto-loan
           │
           ├─ tx 1: claim
           │      ├─ write FulfilledAt onto reservation row
           │      └─ stage ReservationFulfilled event
           │
           ├─ lending.Borrow (OUTSIDE the tx)
           │      │
           │      ├─ success → publish AutoLoanOpened (outside any tx)
           │      │              and return
           │      │
           │      └─ failure → tx 2: un-fulfil
           │                          ├─ write FulfilledAt=nil back
           │                          └─ stage ReservationUnfulfilled event
           │                   then publish AutoLoanFailed (outside the tx,
           │                   so it lands on the bus even if the un-fulfil
           │                   tx rolls back)
           │
           └─ return after the first attempted reservation
              (the saga does NOT walk further down the queue after one
               attempt — matches the TS source's `return` after first try)
```

Six steps, five observable invariants. The next section names them.

## Four atomicity invariants

Every saga in this codebase respects four atomicity invariants. They are stated in lending's own terms here but generalise.

**1. Claim is atomic with `ReservationFulfilled`.** The reservation's `FulfilledAt` write and the `ReservationFulfilled` event publish are bundled in a single `TransactionalContext.Run`. If the row write fails, the staged event is discarded — no `ReservationFulfilled` reaches the bus. If the staged-event publish fails, that failure is logged and skipped (the commit has already happened); the durable state is consistent regardless.

```go
// internal/lending/auto_loan_on_return.go (claimReservation)
txc := c.txFactory()
err := txc.Run(ctx, func(ctx context.Context) error {
    if err := c.reservations.SaveReservation(ctx, claimed, txc); err != nil {
        return err
    }
    txc.StageEvent(ReservationFulfilled{...})
    return nil
})
```

**2. Un-fulfil is atomic with `ReservationUnfulfilled`.** The mirror of invariant 1. If the un-fulfil tx fails, no `ReservationUnfulfilled` event reaches the bus — but `AutoLoanFailed` still does, because it is published OUTSIDE the un-fulfil tx (invariant 4).

**3. The downstream `Borrow` runs OUTSIDE the claim tx.** Borrow has its own tx infrastructure inside the lending facade; nesting it inside the saga's claim tx would either block on the inner tx or perform writes the outer tx cannot roll back. The saga's `attemptAutoLoan` calls `claimReservation()` (one tx, returns), then `lending.Borrow()` (its own tx, separate handle).

**4. `AutoLoanFailed` publishes regardless of un-fulfil success.** `AutoLoanFailed` lands on the bus even when the un-fulfil tx rolls back. The failure signal is decoupled from the un-fulfil atomicity boundary on purpose: subscribers that need to know "the auto-loan attempt failed" should receive that signal whether or not we successfully restored the reservation to pending.

The consequence: a partially-failed saga can leave the reservation in a "fulfilled but no loan" state. The operator investigates via the error log (the consumer logs at error level with structured fields naming `book_id`, `reservation_id`, `member_id`, `step`, `error`). The reservation is observable as "claimed" until manual intervention or a future reconciliation flow. This is documented and accepted.

## Per-aggregate serialisation

Two concurrent `LoanReturned` events for copies of the SAME book could both inspect the queue, both pick the head-of-queue reservation, both write `FulfilledAt`, and both call `Borrow`. The first `Borrow` succeeds; the second fails with `CopyUnavailable` — and now the same reservation has been claimed twice.

The saga prevents this with a lazy-allocated per-book mutex:

```go
// internal/lending/auto_loan_on_return.go
type AutoLoanOnReturnConsumer struct {
    ...
    bookLocksMu sync.Mutex
    bookLocks   map[catalog.BookId]*sync.Mutex
}

func (c *AutoLoanOnReturnConsumer) acquireBookLock(bookId catalog.BookId) *sync.Mutex {
    c.bookLocksMu.Lock()
    defer c.bookLocksMu.Unlock()
    if existing, ok := c.bookLocks[bookId]; ok {
        return existing
    }
    fresh := &sync.Mutex{}
    c.bookLocks[bookId] = fresh
    return fresh
}
```

`handleLoanReturned` calls `acquireBookLock(returned.BookId).Lock()` before processing and releases on return. Concurrent returns of copies of the same book serialise; concurrent returns of different books run in parallel.

Two known gaps:

- **The mutex map grows unbounded.** A book that has ever been returned never relinquishes its mutex. For a teaching repository this is fine; a production deployment would prune on a TTL or weak-reference scheme.
- **The mutex is in-process only.** Scaling the binary horizontally requires replacing the mutex with a row-level DB lock. The real fix would be a unique constraint on `(book_id, member_id) WHERE fulfilled_at IS NULL` plus optimistic-concurrency retry — deliberately deferred to a future phase.

## Explicit Start / Stop lifecycle

The consumer does not subscribe in its constructor. It exposes `Start(ctx) error` and `Stop(ctx) error`:

```go
// internal/lending/auto_loan_on_return.go
func (c *AutoLoanOnReturnConsumer) Start(_ context.Context) error {
    c.lifecycleMu.Lock()
    defer c.lifecycleMu.Unlock()
    if c.started {
        return nil
    }
    c.unsubscribe = c.bus.Subscribe(LoanReturned{}.Type(), c.handleLoanReturned)
    c.started = true
    return nil
}

func (c *AutoLoanOnReturnConsumer) Stop(_ context.Context) error {
    c.lifecycleMu.Lock()
    defer c.lifecycleMu.Unlock()
    if !c.started {
        return nil
    }
    if c.unsubscribe != nil {
        c.unsubscribe()
        c.unsubscribe = nil
    }
    c.started = false
    return nil
}
```

The composition root in `internal/app/wiring.go` calls `consumer.Start(ctx)` after every module is wired. The `Close` function returned from `Wire` calls `consumer.Stop(ctx)` BEFORE closing the bun DB, so handlers do not run against a torn-down substrate.

Both methods are idempotent. Subscribe-twice is a no-op; Stop without a prior Start is a no-op. This means tests can `t.Cleanup(func() { _ = consumer.Stop(ctx) })` unconditionally.

There is no `init()` anywhere in the binary. Saga consumers are not background goroutines that spawn on package import — they are explicit subscriptions that the composition root opens and closes.

## Saga consumers swallow their own errors

The `handleLoanReturned` function returns `nil` unconditionally:

```go
func (c *AutoLoanOnReturnConsumer) handleLoanReturned(ctx context.Context, evt events.DomainEvent) error {
    returned, ok := evt.(LoanReturned)
    if !ok {
        c.logger.Warn("auto-loan consumer received non-LoanReturned event", ...)
        return nil
    }
    lock := c.acquireBookLock(returned.BookId)
    lock.Lock()
    defer lock.Unlock()
    c.processReturn(ctx, returned)
    return nil
}
```

`processReturn` and its callees log every failure path with structured slog fields:

```go
c.logger.Error("auto-loan: claim reservation failed",
    slog.String("book_id", string(reservation.BookId)),
    slog.String("reservation_id", string(reservation.ReservationId)),
    slog.String("member_id", string(reservation.MemberId)),
    slog.String("step", "claim"),
    slog.String("error", err.Error()),
)
```

The rationale: an error returned from a bus handler aborts the fanout to other subscribers. A saga consumer should not poison the bus for unrelated handlers. The contract is that the consumer logs at error level with enough structured context for the operator to investigate, and returns nil so the bus continues delivering.

This means saga failures are observable in the logs, not in the HTTP response of whatever caused the original event. A return that triggers a saga failure still returns 200 — the HTTP user closed the loan successfully, and the downstream saga is a fire-and-forget side effect.

## The known gap: no durable outbox

`InMemoryEventBus` publishes events synchronously. A crash between commit and publish — within the few microseconds of the `publishStaged` loop in `TransactionalContext.Run` — drops the staged event. There is no transactional outbox table, no resumable journal.

Two consequences:

- If the binary crashes between the lending tx commit and the bus publish, `LoanReturned` is lost. The saga never fires. The loan is closed; the reservation queue is not walked.
- If the saga consumer's own tx commits but the subsequent `bus.Publish(AutoLoanOpened)` fails (and the binary crashes before the retry), the auto-loan opened but its event never reached subscribers.

The teaching repo accepts both gaps. A production deployment would ship a transactional outbox: writes go into an `outbox` table inside the same tx as the business write, a poller drains the outbox table into the bus, and consumers acknowledge their position. The architecture supports this drop-in replacement — `EventBus` is an interface, `TransactionalContext.StageEvent` is the only producer call site, and consumers subscribe through the bus.

## How to add a new saga

The auto-loan consumer is the reference template. To add a new saga:

1. **Declare the trigger event** in the publishing module's `types.go`. It must satisfy `events.DomainEvent` (implement `Type() string`).
2. **Stage or publish the trigger** in the publishing module's facade. Stage when the event must publish during commit (typical); publish via `bus.Publish` directly when the event must observe the post-commit catalog/cross-module state (rare — see `lending.ReturnLoan` for the one current case).
3. **Create the consumer file** in the consuming module: `<consuming_module>/<trigger_event>_consumer.go` or, for a saga that spans concepts, a descriptive name like `auto_loan_on_return.go`.
4. **Follow the four-invariant template**: each multi-step workflow uses one `TransactionalContext` per atomic step, runs cross-module facade calls OUTSIDE the txes, and publishes terminal-state events OUTSIDE any tx so they survive rollback of the local cleanup tx.
5. **Add per-aggregate serialisation** if two concurrent triggers for the same aggregate could write the same row. The lazy-allocated mutex map in `auto_loan_on_return.go` is the reference shape.
6. **Expose `Start(ctx) error` and `Stop(ctx) error`.** Both must be idempotent. Both must use a `lifecycleMu` to serialise subscribe/unsubscribe.
7. **Wire the consumer in `internal/app/wiring.go`** AFTER every facade it depends on is built. Call `Start` after route mounting. Add `Stop` to the `Close` function BEFORE the substrate releases.
8. **Write a `consumer_test.go`** that builds a full scene with shared substrates, dispatches the trigger event through the bus, and asserts on the resulting state + emitted events. Use throwing-once decorators for the failure paths — never mocks.

See `internal/lending/auto_loan_on_return.go` for the canonical implementation and `internal/lending/auto_loan_on_return_test.go` for the canonical test shape.
