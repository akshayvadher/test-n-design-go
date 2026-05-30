# Spec: improving-tdd-demo Go Port — Phase 3 (Lending Module, No Saga Yet)

## Overview

The lending module — and the transactional substrate it sits on. Phase 3 ships `internal/shared/tx` (a `TransactionalContext` interface plus an in-memory implementation and a bun-backed implementation) and `internal/lending` (loans + reservations + the three core flows: `Borrow`, `Reserve`, `ReturnLoan`). The two pieces land together because lending is the first module that needs cross-event-with-write atomicity, and `TransactionalContext` is the abstraction that delivers it without leaking a tx handle across a facade boundary.

By the end of Phase 3 a member can `POST /loans` to borrow a copy, `PATCH /loans/{loanId}/return` to return it, and `POST /reservations` to queue a reservation. Loans persist to Postgres via bun. Reservations queue. `LoanOpened`, `LoanReturned`, and `ReservationQueued` are observable on the in-memory event bus. The auto-loan saga handler is NOT subscribed yet — that's Phase 4 — so `LoanReturned` lands on the bus but does not trigger an auto-loan. Atomicity tests prove tx semantics in both substrates: writes roll back on error, staged events stay buffered until commit, and the in-stage publish order is preserved when commit fires.

The architectural conviction lands in this phase: **`TransactionalContext` is the single entry point for any business operation that writes to >1 aggregate atomically; events are STAGED during the tx and published AFTER commit; cross-module mutations (e.g. catalog mark-copy-unavailable) run AFTER commit AND AFTER event publish; cross-module facade reads happen BEFORE the tx opens**. This is the saga substrate; Phase 4 builds on it without modification.

## Why

Phase 3 unlocks:

- The `TransactionalContext` abstraction — the locked-stack interface D14 names ("one transaction per module; cross-module via events"). The shape proves out in two substrates simultaneously (in-memory + bun), which means Phase 4's auto-loan consumer can use it without renegotiating either substrate.
- The post-commit publish pattern. Phase 1's `InMemoryEventBus` already supports `Publish`, but no module yet uses it under transactional semantics. Phase 3 establishes the rule: domain events are STAGED (`tx.StageEvent(evt)`) during the tx work block and published AFTER commit; bus failures do not roll back the commit; a crash between commit and publish is a known outbox-shaped gap that we accept (matches the TS source per D28).
- The post-commit side-effect pattern for cross-module mutations. `lending.Borrow` writes its own loan inside its tx, stages `LoanOpened`, commits, publishes the event, THEN calls `catalog.MarkCopyUnavailable` — never inside the tx. This is principle 7 from the source's `GUIDE.md` ("cross-module consistency via events and happens-before, never via shared transactions"). The Go port locks it into the lending facade's three flow methods.
- The cross-module facade-call convention. `lending` reads from `catalog` (`FindCopy`, `GetBooks`) and `membership` (`CheckEligibility`) BEFORE its tx opens — these are idempotent reads with no transactional concern; running them before `tx.Run` keeps the tx scope tight to lending's own writes.
- The first module that exercises `accesscontrol.Authorize` at runtime. Phase 1 wired the facade into catalog and membership but no Phase-2 endpoint reached the authorization path (thumbnail endpoints were deferred to Phase 5). Phase 3's `Borrow` calls `accessControl.Authorize(authUser, "lending", "borrow")` as its very first step. The policy entry `lending.borrow → [RoleMember]` declared in Phase 1's `policy.go` finally has a runtime caller.

Without Phase 3, the saga story has nowhere to land — Phase 4's auto-loan consumer assumes `TransactionalContext`, the post-commit publish rule, and a working `lending.Borrow` it can re-invoke from a `LoanReturned` handler. With Phase 3 done, Phase 4 ships only the saga itself plus fines and categories — three modules of new content, zero new architectural substrate.

## In Scope

- `internal/shared/tx/` package: `context.go` declaring `TransactionalContext` interface + `TransactionalContextFactory` type alias. `in_memory.go` declaring `InMemoryTransactionalContext` (stage-and-commit: writes and events buffer during `Run`, apply/publish on success, discard on error). `bun.go` declaring `BunTransactionalContext` (wraps `db.RunInTx`; staged writes execute against the live tx handle inside the callback; events publish AFTER commit). Atomicity unit tests for the in-memory impl in `in_memory_test.go`; atomicity integration tests for the bun impl in `bun_test.go` (`//go:build integration`). Public surface: the interface, the factory type, the two impl structs and their `New*` constructors. No leakage of the `bun.Tx` handle through the interface.
- `internal/lending/` module: every file the per-module template lists — `facade.go`, `types.go`, `schema.go` (minimal; lending takes few external string inputs because the controller deals in opaque IDs), `loan_repository.go`, `reservation_repository.go`, `in_memory_loan_repository.go`, `in_memory_reservation_repository.go`, `bun_loan_repository.go`, `bun_reservation_repository.go`, `sample_data.go`, `configuration.go`, `module.go`, `facade_test.go`, `in_memory_loan_repository_test.go`, `in_memory_reservation_repository_test.go`, `bun_loan_repository_test.go` and `bun_reservation_repository_test.go` (both `//go:build integration`), plus `http/` subpackage with `dto.go`, `handlers.go`, `mapping.go`, `handlers_test.go`.
- `migrations/0003_lending.sql` creating `loans` (`loan_id UUID PK`, `member_id UUID NOT NULL`, `copy_id UUID NOT NULL`, `book_id UUID NOT NULL`, `borrowed_at TIMESTAMPTZ NOT NULL`, `due_date TIMESTAMPTZ NOT NULL`, `returned_at TIMESTAMPTZ NULL`) and `reservations` (`reservation_id UUID PK`, `member_id UUID NOT NULL`, `book_id UUID NOT NULL`, `reserved_at TIMESTAMPTZ NOT NULL`, `fulfilled_at TIMESTAMPTZ NULL`). No FK references to `members`/`books`/`copies` — cross-module FKs are forbidden by the architectural conviction (no cross-module DB joins per `.claude/BOUNDARIES.md`). `atlas.sum` regenerated.
- `internal/app/wiring.go` extended so `Wire` constructs the lending facade with the bun-backed loan + reservation repos, the bun-backed `TransactionalContext` factory (a closure that returns `tx.NewBunTransactionalContext(bunDB, bus)` per call), the catalog + membership + accesscontrol facades, the `EventBus`, and the same `newID`/`clock` injections established in Phase 2; registers Phase-3 domain errors with the `DomainErrorRegistry`; mounts lending's routes via `lendingModule.Wire(r, deps)`; extends `app.Wired` with `LendingFacade *lending.Facade` for integration-test introspection.
- `test/crucial_path/lending_integration_test.go` (`//go:build integration`) — boots the real composition root via `test/support.BootApp`, exercises `POST /loans`, `PATCH /loans/{loanId}/return`, `POST /reservations` end-to-end against testcontainers Postgres + the real bun TransactionalContext, asserts HTTP status + body + bus observation + row-level state.
- `.http/lending.http` activating Phase 3's endpoints (`POST {{baseUrl}}/loans`, `PATCH {{baseUrl}}/loans/<loanId>/return`, `POST {{baseUrl}}/reservations`, plus the read-only listing endpoints).
- Domain events `LoanOpened`, `LoanReturned`, `ReservationQueued` declared in `internal/lending/types.go` implementing `events.DomainEvent` (each with a `Type() string` method returning the corresponding source-fidelity string).

## Out of Scope (deferred to later phases per discovery doc)

- The auto-loan saga consumer (`internal/lending/auto_loan_on_return.go`). Phase 4 Slice 1. Phase 3's `LoanReturned` lands on the bus but no handler is subscribed; the bus emits the event and returns nil (Phase 1's `InMemoryEventBus.Publish` is a no-op when no subscribers exist).
- `ReservationFulfilled`, `ReservationUnfulfilled`, `AutoLoanOpened`, `AutoLoanFailed` events. These are saga events; Phase 4 declares them when the saga lands.
- `internal/fines/` and `internal/categories/` modules. (Phase 4.)
- `internal/lending/Facade.ListOverdueLoans`, `Facade.ListOverdueLoansWithTitles`, `Facade.ListLoansFor`, `Facade.ListActiveLoansWithQueuedReservations`. These read-only listing methods exist in the TS source's `lending.facade.ts` but they are consumed primarily by fines (Phase 4) and by the auto-loan consumer (Phase 4). Phase 3 ships only the three writes (`Borrow`, `Reserve`, `ReturnLoan`) plus the minimum reads required by them. **Recorded as Open Question 1**; recommended default is to defer to Phase 4 so Phase 3 stays focused on the tx substrate + write flows. If kept, they add ~5 facade methods, ~5 handlers, and ~10 facade-test scenarios for ~half a day of work.
- A per-book serialisation lock (`sync.Mutex` map) to prevent double-fulfilment. The lock matters only when the auto-loan saga consumer races against concurrent `Borrow` calls for the same copy. Phase 3 has no consumer; the lock lands in Phase 4 Slice 1 next to the consumer.
- A durable outbox (publish-after-commit gap). The known gap is acknowledged: a crash between `tx.Run` returning success and the post-commit `bus.Publish` running means the event is lost. Discovery D27/D28 accepts this; the spec documents it in the `TransactionalContext` doc-comment and in the lending facade's `ReturnLoan` doc-comment. Phase 3 does NOT ship an outbox table.
- Concurrent-mutation tests across goroutines. Phase 3's atomicity tests are single-goroutine: they prove rollback semantics, not concurrency safety. Phase 4 adds the per-book serialisation tests when the saga consumer lands.
- `go-arch-lint` enforcement of the "no cross-module DB joins" rule. (D25; revisit Phase 5.)
- An OpenLibrary-backed isbngateway. Still Phase 5.
- Authentication beyond the demo `AuthUser` lookup pattern. The lending controller in the TS source uses `lookupAuthUser(memberId)` — a development shortcut that builds an `AuthUser` from a memberId header/body field. The Go port matches: `POST /loans` carries `{"memberId":"...","copyId":"..."}` and the handler constructs `accesscontrol.AuthUser{MemberID: body.MemberId, Role: accesscontrol.RoleMember}` inline. No JWT, no session, no real auth middleware. This is consistent with the TS source's intent — the demo isn't an auth tutorial.

## Slices

Slicing is outside-in within the substrate-then-feature order: the tx substrate lands first (Slices 1–2) because Slice 3+ depends on its public surface; lending's skeleton lands next (Slice 3, types + repos + sample data + configuration) so the facade has somewhere to write; `Borrow` ships as the canonical flow (Slice 4) because it exercises every architectural decision (cross-module reads before tx, own-tx with staged event, post-commit cross-module write); `Reserve` ships as the pure-staged-event variant (Slice 5); `ReturnLoan` ships as the post-commit-side-effect-plus-direct-publish variant (Slice 6, where `LoanReturned` is published *after* the catalog mark-available call, NOT via `tx.StageEvent`, so Phase 4 consumers observe the fully-consistent state); HTTP handlers + crucial-path land last (Slice 7) once all three flows exist.

The TS source's slice was a single drop; the Go port splits it across seven so each architectural rule lands with its own AC block and its own commit. Each slice ends green: `go build`, unit tests, integration tests where the slice ships an integration test.

---

### Slice 1: `internal/shared/tx` — `TransactionalContext` interface + `InMemoryTransactionalContext`

Brings the repository to "any business module can take a `TransactionalContextFactory`, ask for a context per business operation, stage writes and events inside `Run(ctx, work)`, and observe atomicity in-memory." This slice does NOT ship the bun impl — Slice 2 does.

#### Acceptance Criteria — interface + factory type

- [ ] `internal/shared/tx/context.go` declares `type TransactionalContext interface` with three methods:
  - `Run(ctx context.Context, work func(ctx context.Context) error) error`
  - `Stage(apply func(ctx context.Context) error)`
  - `StageEvent(evt events.DomainEvent)`
- [ ] `Run` returns an error wrapping the work function's error on failure, returns nil on success. The signature deliberately drops the generic `<T>` shape the TS source uses — Go has no generic return for an interface method, and the facade methods that need a return value compute it outside `Run` (storing into an outer-scope variable inside `work` is the canonical Go pattern; the facade test ACs in Slice 4 verify this).
- [ ] `Stage(apply)` registers a write closure to execute as part of the tx. In the in-memory impl the closure runs during commit (after `work` returns nil). In the bun impl the closure runs immediately inside the tx callback against the live tx handle. The `apply` closure takes a `context.Context` so bun-backed callers can pass the tx-scoped context down to bun query builders.
- [ ] `StageEvent(evt)` registers a `DomainEvent` to publish AFTER commit. Events publish in stage order. Bus-publish failures do NOT roll back the commit; they are logged at `error` level by the impl and `Run` still returns nil.
- [ ] `context.go` declares `type TransactionalContextFactory = func() TransactionalContext` — a factory invocation produces a fresh context per business operation. This matches the TS source's `TransactionalContextFactory` shape. Callers do NOT reuse a `TransactionalContext` across operations — fresh per op is the contract.
- [ ] The interface's doc comment names the post-commit publish rule explicitly: "Events staged via `StageEvent` publish AFTER the underlying transaction commits, in stage order. A crash between commit and publish drops the event — see discovery D27/D28; we accept this gap rather than ship an outbox in the teaching repo."
- [ ] The interface's doc comment names the post-commit side-effect rule explicitly: "For cross-module mutations that must run AFTER commit (e.g. catalog mark-copy-unavailable from lending.Borrow), the caller runs them OUTSIDE `Run` — NOT via `Stage`. `Stage` is for own-module writes that participate in the tx; cross-module writes are sequenced by the caller after `Run` returns nil."
- [ ] `context.go` exports `TransactionalContext` and `TransactionalContextFactory`. Nothing else.

#### Acceptance Criteria — `InMemoryTransactionalContext`

- [ ] `internal/shared/tx/in_memory.go` exports `InMemoryTransactionalContext struct` with unexported fields: `bus events.EventBus`, `staged []func(context.Context) error`, `events []events.DomainEvent`, `logger *slog.Logger`.
- [ ] `NewInMemoryTransactionalContext(bus events.EventBus, logger *slog.Logger) *InMemoryTransactionalContext` returns a fresh instance with empty buffers.
- [ ] `Stage(apply)` appends the closure to `staged`. Returns nothing.
- [ ] `StageEvent(evt)` appends the event to `events`. Returns nothing.
- [ ] `Run(ctx, work)` executes `work(ctx)`; on `work` returning nil, calls each staged closure in stage order with `ctx`, then publishes each staged event in stage order via `bus.Publish(ctx, evt)`. On `work` returning a non-nil error, discards both buffers (no closures run, no events publish) and returns the wrapped work error.
- [ ] If a staged closure returns an error during commit, `Run` propagates the FIRST closure error (no further closures run, no events publish, the work-time writes that already happened to live state remain — note this is a divergence from the TS source's behaviour where `stage` closures run during commit too; the AC is "behaviourally identical to TS source" — staged closures only execute live-state mutation during commit). Phase 3 commits the same model: stage means "deferred to commit"; if a stage closure errors during commit, downstream events do NOT publish and `Run` returns the error.
- [ ] If `bus.Publish` returns an error for one event during the post-commit publish loop, the impl logs the error at `error` level with structured fields `event_type`, `event_index`, `error`, then continues publishing the remaining events. `Run` still returns nil. Bus failures do NOT roll back the commit. (This matches Phase 1's `InMemoryEventBus.Publish` semantics — handler errors don't fail publish; here we extend the same forgiveness to the publish loop itself.)
- [ ] The buffers (`staged`, `events`) are NOT cleared on construction reuse — a fresh `InMemoryTransactionalContext` is the only valid object per operation. `Run` does not reset the buffers after a successful commit either; reusing a context across operations is a programming error and the facade test in Slice 4 documents the convention via "construct a fresh context per operation."
- [ ] `InMemoryTransactionalContext` is safe to call from a single goroutine; concurrent calls from multiple goroutines on the same instance are NOT supported (and not needed, because the contract is fresh-per-op). No mutex; no race-safety guarantees beyond "don't share across goroutines."

#### Acceptance Criteria — atomicity tests (in-memory)

- [ ] `internal/shared/tx/in_memory_test.go` (unit, no build tag, stdlib `testing` only, package `tx` so test can read unexported helpers if any).
- [ ] Test uses an `InMemoryEventBus` from Phase 1 with a captured-events handler (subscribes to the event types under test; appends each delivered event to a `[]events.DomainEvent` slice; the test reads the slice after `Run` returns).
- [ ] Test uses an `intCell struct { value int }` (or similar) as the "live state" mutated by staged closures. Staged closures increment the cell; the test asserts the cell value after `Run` returns.
- [ ] Declare a tiny event type `testEvent struct { name string }` in the test file implementing `events.DomainEvent` via `Type() string { return "TestEvent" }`. Used for stage-event assertions.
- [ ] AC: Happy path — `Stage(incrementCell)`, `StageEvent(testEvent{"a"})`, `Run` returning nil from work → cell incremented once, captured-events contains one event of type `TestEvent`.
- [ ] AC: Work error → `Run` returns the wrapped work error; cell value is unchanged (staged closure never ran); captured-events is empty (no event was published).
- [ ] AC: Stage order preserved — `Stage(c1)`, `Stage(c2)`, `Stage(c3)` with closures appending their index to a shared slice; on `Run` success, the slice equals `[1, 2, 3]`.
- [ ] AC: StageEvent order preserved — `StageEvent(e1)`, `StageEvent(e2)`, `StageEvent(e3)` of distinct testEvents; captured-events equals `[e1, e2, e3]` after `Run` succeeds.
- [ ] AC: Mixed-order rule — staged writes apply BEFORE staged events publish. `Stage(closure that records 'write' into a shared journal)`, `StageEvent` whose subscriber appends 'event' to the same journal; after `Run` succeeds, journal equals `["write", "event"]`. Proves writes happen-before events at commit time.
- [ ] AC: Stage closure error during commit — `Stage(returnError)` then `StageEvent(testEvent)` then `Run(nil-work)` → `Run` returns the stage closure's error; captured-events is empty (the staged event never published because a stage closure failed first).
- [ ] AC: Bus publish failure does NOT roll back commit — construct a `flakyBus` (unexported in the test file) whose `Publish` returns `errors.New("bus down")` for one specific event type and delegates for others. `Stage(incrementCell)`, `StageEvent(flakyEventType)`, `StageEvent(okEventType)`, `Run(nil-work)` → `Run` returns nil; cell IS incremented (the write committed); `okEventType` IS captured (the loop continued past the failure). Bus failure is logged at `error` level — assert via a `slog.Handler` test double that records log records, NOT a string-match.
- [ ] AC: Empty staged + empty events + nil-work → `Run` returns nil, no captured events, no cell mutation.
- [ ] AC: A second `Run` call on the same instance is a programming-error convention but is not actively prevented; the test file does NOT assert a panic on reuse (the contract is documented by example, not enforced — the facade always uses fresh-per-op).
- [ ] Test runs in well under 100 ms.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go build ./...` passes.
- [ ] `go vet ./...` is clean.
- [ ] No `init()` function in `internal/shared/tx/`.
- [ ] `internal/shared/tx/` imports only stdlib + `internal/shared/events`. NOT `internal/lending`, NOT `internal/catalog`, NOT bun (Slice 2 introduces the bun import in `bun.go`).
- [ ] No file in `internal/shared/tx/` exports a `bun.Tx` or `*sql.Tx` — the interface deliberately hides the underlying handle.

---

### Slice 2: `BunTransactionalContext` + atomicity integration tests

Brings the repository to "the same `TransactionalContext` interface works against a real Postgres tx via bun, with identical atomicity guarantees proven by integration tests." This slice introduces the second substrate so the lending facade (Slice 4+) writes one body of code that works against both in-memory tests AND a real DB.

#### Acceptance Criteria — `BunTransactionalContext`

- [ ] `internal/shared/tx/bun.go` exports `BunTransactionalContext struct` with unexported fields: `db *bun.DB`, `bus events.EventBus`, `events []events.DomainEvent`, `logger *slog.Logger`. (No `staged` slice — bun's tx executes staged closures immediately inside the tx callback, not deferred to commit.)
- [ ] `NewBunTransactionalContext(db *bun.DB, bus events.EventBus, logger *slog.Logger) *BunTransactionalContext` returns a fresh instance.
- [ ] `Stage(apply)`: executes `apply(ctx)` immediately against the current tx-scoped context if one is active (i.e. inside a `Run`); otherwise executes it against the base `context.Background()` (defensive — a stage outside a tx scope is a programming error but does not panic). If `apply` returns an error, the error is captured into an instance field; `Run`'s tx callback returns that captured error after `work` finishes, causing bun to roll back. (Implementation note: tracking the captured stage error inside an unexported field keeps the `Stage` signature `func(apply)` rather than `func(apply) error` — matching the TS source's signature.) **Note**: this divergence from the TS source (the TS source stages a `Promise<void>` and `await`s all of them in a `Promise.all`) is a Go-idiomatic adaptation; the AC is "behaviourally identical: a `Stage`-closure error makes the tx roll back."
- [ ] `StageEvent(evt)`: appends the event to `events`. (Same as `InMemoryTransactionalContext`.)
- [ ] `Run(ctx, work)` calls `db.RunInTx(ctx, &sql.TxOptions{}, func(ctx, tx bun.Tx) error { ... })`. Inside the tx callback: store the tx-scoped context (or the `bun.Tx` handle wrapped in a context value) so subsequent `Stage` calls see it; call `work(ctx)`; if `work` returns an error, return it (bun rolls back); if `work` returns nil, return any captured stage error (also causes rollback); else return nil (bun commits). After `RunInTx` returns nil, publish each staged event in stage order via `bus.Publish(ctx, evt)`. After `RunInTx` returns an error, discard the events buffer and return the wrapped tx error.
- [ ] On `bus.Publish` failure during the post-commit loop, log the error at `error` level with `event_type`, `event_index`, `error`; continue publishing the remaining events; `Run` returns nil. (Same forgiveness rule as in-memory.)
- [ ] The bun-scoped context that `Stage` callbacks see carries the live `bun.Tx` via an unexported context key (`type txKey struct{}` package-private). A helper `func TxFromContext(ctx context.Context) (bun.IDB, bool)` is exported so repositories can resolve the tx handle. (Repositories call `tx.TxFromContext(ctx)` to get the bun handle; if no tx is active, they use the base `*bun.DB`.)
- [ ] `BunTransactionalContext` is safe to call from a single goroutine; concurrent reuse of the same instance across goroutines is NOT supported. Fresh-per-op is the contract.
- [ ] `bun.go` exports `BunTransactionalContext`, `NewBunTransactionalContext`, and `TxFromContext`. No leakage of the raw `bun.Tx` type beyond the `bun.IDB` interface return.

#### Acceptance Criteria — atomicity tests (bun, integration)

- [ ] `internal/shared/tx/bun_test.go` has `//go:build integration` at the top.
- [ ] Test boots a `*bun.DB` via `test/support.StartPostgres(ctx, t)` + `db.NewBunDB`. Creates a tiny `tx_test_widgets (id TEXT PRIMARY KEY, value INTEGER NOT NULL)` table inside the test setup via a direct `db.Exec(ctx, "CREATE TABLE ...")` — does NOT add a migration to `migrations/` for the test fixture. `t.Cleanup` drops the table.
- [ ] Test uses an `InMemoryEventBus` (Phase 1) with a captured-events handler, identical pattern to the in-memory test.
- [ ] AC: Happy path — `Stage(insert widget id="a" value=1)`, `StageEvent(testEvent{"a"})`, `Run(nil-work)` → `Run` returns nil; widget row exists in Postgres after commit; captured-events contains one event.
- [ ] AC: Work error → `Run` returns the wrapped error; the widget row does NOT exist in Postgres (rollback occurred); captured-events is empty.
- [ ] AC: Stage closure error during tx → `Run` returns the wrapped stage error; the widget row does NOT exist in Postgres (rollback occurred — even though the `Stage` closure ran the INSERT, the rollback discards it); captured-events is empty.
- [ ] AC: StageEvent order preserved — `Stage(insert id="a")`, `StageEvent(e1)`, `Stage(insert id="b")`, `StageEvent(e2)`, `Run(nil-work)` → after commit, both rows exist AND captured-events equals `[e1, e2]` in stage order.
- [ ] AC: Bus publish failure does NOT roll back commit — same `flakyBus` pattern as the in-memory test; after `Run` returns nil, the widget row IS in Postgres (commit happened); the non-failing event IS captured.
- [ ] AC: Repository-style usage — a tiny `widgetRepository` declared inline in the test file (unexported) takes a `*bun.DB` and exposes `Insert(ctx, id, value) error`. Inside `Run`, the repo's `Insert` calls `tx.TxFromContext(ctx)` to resolve the live tx handle, then `handle.NewInsert().Model(...).Exec(ctx)`. Outside `Run` (a direct `widgetRepo.Insert` without tx wrapping), the repo falls back to the base `*bun.DB`. The test asserts both code paths work.
- [ ] AC: A direct `widgetRepo.Insert` outside any `Run` succeeds and writes the row (proves the no-tx fallback works).
- [ ] `t.Cleanup` truncates `tx_test_widgets` between tests and drops the table at the end.
- [ ] Test runs in under 5 seconds on a developer laptop (testcontainers cold start dominates).

#### Acceptance Criteria — slice-level hygiene

- [ ] `go build -tags=integration ./...` passes.
- [ ] `go vet ./...` is clean.
- [ ] `internal/shared/tx/bun.go` imports `github.com/uptrace/bun` and the package's own `events` import; no business-module imports.
- [ ] The `TxFromContext` helper's doc comment names the rule: "Repositories use this to resolve the active bun handle. Returns the base `*bun.DB` (wrapped as `bun.IDB`) when no tx is active. Always returns a non-nil handle plus a `bool` indicating whether a tx was active." (The bool may be used by callers that want to assert they ARE inside a tx; the bun repos in Slice 3 use it informationally.)
- [ ] The bus failure log line is asserted via a `slog.Handler` test double, not a string match.
- [ ] `internal/app/wiring.go` is NOT yet modified — the bun TransactionalContext is wired into the composition root in Slice 7 (when the lending facade lands its HTTP surface). Slice 2 ships only the substrate + tests.

---

### Slice 3: `internal/lending` module skeleton (types, schemas, repos, sample data, configuration)

Brings the repository to "the lending package compiles, declares its domain types and events, exposes loan + reservation repositories (in-memory + bun) with the established Phase-2 patterns, ships functional-option sample data builders, and offers a configuration constructor — but no facade methods, no HTTP routes, no tx integration." This slice is pure architecture: the skeleton the next three slices fill in. Tests for the in-memory repos land in this slice; bun-repo contract tests land in Slice 7 alongside the migration.

#### Acceptance Criteria — types + events

- [ ] `internal/lending/types.go` declares `type LoanId string`, `type ReservationId string` (named string types).
- [ ] `types.go` declares `type LoanDto struct { LoanId LoanId; MemberId membership.MemberId; CopyId catalog.CopyId; BookId catalog.BookId; BorrowedAt time.Time; DueDate time.Time; ReturnedAt *time.Time }` — `ReturnedAt` is a pointer for the "not yet returned" sentinel (matches TS source's optional `returnedAt`).
- [ ] `types.go` declares `type ReservationDto struct { ReservationId ReservationId; MemberId membership.MemberId; BookId catalog.BookId; ReservedAt time.Time; FulfilledAt *time.Time }`.
- [ ] `types.go` declares `type ActiveLoanWithQueuedCount struct { Loan LoanDto; QueuedCount int }` (declared even though Phase 3 doesn't use it; Phase 4 will, and shipping it now keeps the type surface stable). **Open Question 1** flags whether this and `OverdueLoanReport` should land in Phase 3 or be deferred entirely to Phase 4.
- [ ] `types.go` declares `type OverdueLoanReport struct { Loan LoanDto; Title string; Authors []string }` (same caveat).
- [ ] `types.go` declares `type LoanOpened struct { LoanId LoanId; MemberId membership.MemberId; CopyId catalog.CopyId; BookId catalog.BookId; BorrowedAt time.Time; DueDate time.Time }` implementing `events.DomainEvent` via `func (e LoanOpened) Type() string { return "LoanOpened" }`. Field order matches TS source 1:1.
- [ ] `types.go` declares `type LoanReturned struct { LoanId LoanId; MemberId membership.MemberId; CopyId catalog.CopyId; BookId catalog.BookId; ReturnedAt time.Time }` implementing `events.DomainEvent` via `func (e LoanReturned) Type() string { return "LoanReturned" }`.
- [ ] `types.go` declares `type ReservationQueued struct { ReservationId ReservationId; MemberId membership.MemberId; BookId catalog.BookId; ReservedAt time.Time }` implementing `events.DomainEvent` via `func (e ReservationQueued) Type() string { return "ReservationQueued" }`.
- [ ] `types.go` does NOT declare `ReservationFulfilled`, `ReservationUnfulfilled`, `AutoLoanOpened`, `AutoLoanFailed` — those are saga events and land in Phase 4.
- [ ] `types.go` declares domain errors with names matching TS source 1:1:
  - `LoanNotFoundError{ LoanId LoanId }` → message `"Loan not found: <loanId>"`.
  - `CopyUnavailableError{ CopyId catalog.CopyId }` → message `"Copy is not available for borrowing: <copyId>"`.
  - `MemberIneligibleError{ MemberId membership.MemberId; Reason string }` → message `"Member <memberId> is not eligible to borrow: <reason>"`.
  - `ReservationNotFoundError{ ReservationId ReservationId }` → message `"Reservation not found: <reservationId>"`. (Phase 3 doesn't return this — no facade method looks up a reservation by id — but the type is declared in this phase so the registry can register it once and Phase 4's `Cancel` and saga flows have somewhere to point. **Recorded as Open Question 2**; recommended default to ship it now.)
- [ ] Each error type implements `Error() string` matching the TS message format and is matchable via `errors.As`.
- [ ] Lending events use value receivers for `Type()` (matches the value-receiver pattern Phase 1's events package uses for `DomainEvent`).

#### Acceptance Criteria — schemas

- [ ] `internal/lending/schema.go` exports `ParseBorrowRequest(memberId, copyId string) (membership.MemberId, catalog.CopyId, error)` — trims both; rejects either blank with `*BorrowValidationError{Reason: "memberId is required"}` or `"copyId is required"`. Returns the typed-newtype values.
- [ ] `ParseReserveRequest(memberId, bookId string) (membership.MemberId, catalog.BookId, error)` — same pattern.
- [ ] `ParseReturnLoanRequest(loanId string) (LoanId, error)` — trims; rejects blank.
- [ ] `schema.go` declares `BorrowValidationError struct { Reason string }` implementing `Error() string` returning `"Invalid borrow request: <reason>"`. Same for `ReserveValidationError` and `ReturnLoanValidationError`. (Phase 3 introduces three new validation error types — they map to HTTP 400 in the registry.)
- [ ] All parsers use stdlib only — no third-party validator.

#### Acceptance Criteria — repository ports

- [ ] `internal/lending/loan_repository.go` declares `type LoanRepository interface` with methods:
  - `SaveLoan(ctx context.Context, loan LoanDto, txc tx.TransactionalContext) error` — note the explicit `txc` parameter (matches TS source which passes the tx as an explicit arg). The repo calls `txc.Stage(func(ctx) error { /* actual write */ })` so the write participates in the tx. The repo does NOT call the underlying DB directly outside the staged closure.
  - `FindLoanById(ctx context.Context, loanId LoanId) (*LoanDto, error)` — read; returns `(nil, nil)` on not found (no tx needed; reads bypass the tx substrate).
  - `ListLoansForMember(ctx context.Context, memberId membership.MemberId) ([]LoanDto, error)`.
  - `ListLoansForBook(ctx context.Context, bookId catalog.BookId) ([]LoanDto, error)`.
  - `ListLoans(ctx context.Context) ([]LoanDto, error)`.
- [ ] `internal/lending/reservation_repository.go` declares `type ReservationRepository interface` with methods:
  - `SaveReservation(ctx context.Context, reservation ReservationDto, txc tx.TransactionalContext) error` — staged via `txc.Stage`.
  - `FindReservationById(ctx context.Context, reservationId ReservationId) (*ReservationDto, error)` — declared even though Phase 3's facade doesn't call it; Phase 4 needs it.
  - `ListReservationsForBook(ctx context.Context, bookId catalog.BookId) ([]ReservationDto, error)`.
  - `ListReservationsForMember(ctx context.Context, memberId membership.MemberId) ([]ReservationDto, error)`.
  - `PendingReservationCountForBook(ctx context.Context, bookId catalog.BookId) (int, error)` — counts reservations where `FulfilledAt == nil`. Declared in Phase 3, consumed in Phase 4.
- [ ] The interfaces are EXPORTED (capitalized) so the bun repo files (Slice 7) and the lending facade (Slice 4) can both reference them. (Phase 2's catalog/membership repos exported their interfaces too — pattern is established.)

#### Acceptance Criteria — in-memory repository impls

- [ ] `internal/lending/in_memory_loan_repository.go` exports `InMemoryLoanRepository struct` + `NewInMemoryLoanRepository() *InMemoryLoanRepository`. Backed by `map[LoanId]LoanDto` + `sync.RWMutex` (concurrent-read safety; same pattern as Phase 1 `InMemoryEventBus`).
- [ ] `SaveLoan(ctx, loan, txc)`: take a snapshot copy of the loan DTO (defensive copy; matches TS `{ ...loan }`), then call `txc.Stage(func(ctx) error { /* write snapshot into map under lock */; return nil })`. Returns nil immediately (the actual mutation happens at commit time).
- [ ] `FindLoanById` returns `(&copyOfRow, nil)` on hit (defensive copy of the stored DTO so callers can't mutate map state), `(nil, nil)` on miss, never an error in the in-memory impl.
- [ ] `ListLoans` returns a snapshot copy of the map's values (slice copy + per-element copy of the DTO). Order: by insertion via a parallel `[]LoanId` slice OR by `LoanId` ascending — match the convention the catalog in-memory repo settled on in Phase 2 Slice 4 (per Phase 2 Open Question 3, default was `ORDER BY <id> ASC`). Phase 3 uses the same rule.
- [ ] `ListLoansForMember` / `ListLoansForBook` filter the map's values, defensive-copy each, return.
- [ ] `internal/lending/in_memory_reservation_repository.go` exports `InMemoryReservationRepository struct` + constructor. Same shape as the loan repo. `PendingReservationCountForBook` counts entries where `FulfilledAt == nil` AND `BookId` matches.
- [ ] `internal/lending/in_memory_loan_repository_test.go` (unit, no build tag) covers every method's happy path + the "not found returns nil" path. `SaveLoan` is tested by constructing a real `InMemoryTransactionalContext` (no mocks), calling `SaveLoan`, calling `tx.Run(ctx, func() error { return nil })`, asserting the row appears in the map post-commit AND does NOT appear if `Run`'s work returns an error.
- [ ] `internal/lending/in_memory_reservation_repository_test.go` — same pattern.

#### Acceptance Criteria — sample data (functional options)

- [ ] `internal/lending/sample_data.go` exports `SampleNewLoan(opts ...LoanOption) LoanDto` returning a default `LoanDto{LoanId: "loan-placeholder-id", MemberId: "member-placeholder-id", CopyId: "copy-placeholder-id", BookId: "book-placeholder-id", BorrowedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), DueDate: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), ReturnedAt: nil}`. Matches the TS sample loan defaults shape.
- [ ] `SampleReturnedLoan(opts ...LoanOption) LoanDto` returns a default like `SampleNewLoan` but with `ReturnedAt: ptr(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC))`.
- [ ] `SampleNewReservation(opts ...ReservationOption) ReservationDto` returning a default with `ReservedAt` set to a deterministic time, `FulfilledAt: nil`.
- [ ] Option types: `type LoanOption func(*LoanDto)`, `type ReservationOption func(*ReservationDto)`. Exported options at minimum: `WithLoanId(LoanId)`, `WithLoanMemberId(membership.MemberId)`, `WithLoanCopyId(catalog.CopyId)`, `WithLoanBookId(catalog.BookId)`, `WithBorrowedAt(time.Time)`, `WithDueDate(time.Time)`, `WithReturnedAt(time.Time)` (sets the pointer); similar for reservations.
- [ ] Override-order is deterministic (last option wins).

#### Acceptance Criteria — configuration

- [ ] `internal/lending/configuration.go` exports `type Overrides struct { Catalog *catalog.Facade; Membership *membership.Facade; AccessControl *accesscontrol.Facade; Loans LoanRepository; Reservations ReservationRepository; Bus events.EventBus; TxFactory tx.TransactionalContextFactory; NewID func() string; Clock func() time.Time; Logger *slog.Logger }`. Every field optional (zero value → use default).
- [ ] `NewFacadeWithOverrides(o Overrides) *Facade` substitutes defaults for missing fields:
  - `Catalog → catalog.NewFacadeWithOverrides(catalog.Overrides{})` (fresh in-memory catalog facade with default deps).
  - `Membership → membership.NewFacadeWithOverrides(membership.Overrides{})`.
  - `AccessControl → accesscontrol.NewFacade()`.
  - `Loans → NewInMemoryLoanRepository()`.
  - `Reservations → NewInMemoryReservationRepository()`.
  - `Bus → events.NewInMemoryEventBus(discardLogger)`.
  - `TxFactory → func() tx.TransactionalContext { return tx.NewInMemoryTransactionalContext(resolvedBus, discardLogger) }` — note the factory closes over the same `Bus` the facade uses (otherwise the test bus and the tx-publish bus would be different instances).
  - `NewID → uuid.NewString`.
  - `Clock → time.Now`.
  - `Logger → slog.New(slog.DiscardHandler)` (or the Phase 2-established fallback for Go 1.23).
- [ ] The default `TxFactory` MUST share the same `Bus` instance with the facade — callers passing a custom `Bus` and a custom `TxFactory` are responsible for wiring them consistently. Document this in the doc comment on `Overrides.TxFactory`: "If you override `Bus`, you must override `TxFactory` to construct a `TransactionalContext` against the same bus instance, otherwise staged events publish to a different bus than the facade's direct `bus.Publish` calls."

#### Acceptance Criteria — facade skeleton

- [ ] `internal/lending/facade.go` exports `type Facade struct` with unexported fields: `catalog *catalog.Facade`, `membership *membership.Facade`, `accessControl *accesscontrol.Facade`, `loans LoanRepository`, `reservations ReservationRepository`, `bus events.EventBus`, `txFactory tx.TransactionalContextFactory`, `newID func() string`, `clock func() time.Time`, `logger *slog.Logger`. Constructor `NewFacade(catalog, membership, accessControl, loans, reservations, bus, txFactory, newID, clock, logger)` returns `*Facade`.
- [ ] No facade methods are implemented in this slice — Slice 4 adds `Borrow`, Slice 5 adds `Reserve`, Slice 6 adds `ReturnLoan`. The constructor and the struct exist so other code can take the facade as a dep.
- [ ] An unexported constant `LoanDurationDays = 14` lives at the top of `facade.go` (matches TS source's `LOAN_DURATION_DAYS = 14`). Slice 4 uses it.
- [ ] An unexported helper `addDays(t time.Time, days int) time.Time` lives at the bottom of `facade.go` returning `t.AddDate(0, 0, days)`. Slice 4 uses it for due-date computation.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go build ./...` passes.
- [ ] `go vet ./...` is clean.
- [ ] No `init()` in `internal/lending/`.
- [ ] No file in `internal/lending/` imports `internal/lending/http/` (the http subdir doesn't exist yet).
- [ ] `internal/lending/` imports per `.claude/BOUNDARIES.md` allowed edges only: `internal/accesscontrol`, `internal/catalog`, `internal/membership`, `internal/shared/events`, `internal/shared/tx`, plus stdlib + `github.com/google/uuid`. NOT `internal/shared/db`, NOT `internal/shared/bookcache`, NOT `internal/shared/isbngateway`, NOT `internal/shared/http` (the HTTP subpackage in Slice 7 imports `shared/http`, not the module root).
- [ ] The slice does NOT touch `internal/app/wiring.go` — wiring lands in Slice 7.

---

### Slice 4: `Facade.Borrow` — cross-module reads + own-tx + staged event + post-commit catalog mutation

Brings the repository to "`lending.Borrow` is the canonical demonstration of every architectural rule Phase 3 introduces, with facade-level tests proving each rule." Slice 4 is the architectural heart of the phase — every other lending method is a variation on this template.

#### Acceptance Criteria — `Borrow` method

- [ ] `Facade.Borrow(ctx context.Context, authUser accesscontrol.AuthUser, copyId catalog.CopyId) (LoanDto, error)` is exported on `*Facade`.
- [ ] Step 1 (BEFORE any tx): `accessControl.Authorize(authUser, accesscontrol.ModuleName("lending"), accesscontrol.ActionName("borrow"))` — returns the wrapped `*UnauthorizedRoleError` or `*UnknownActionError` directly to the caller on failure. NOT inside a tx; authorization is a precondition.
- [ ] Step 2 (BEFORE tx): `membership.CheckEligibility(ctx, membership.MemberId(authUser.MemberID))` — if `!eligibility.Eligible`, return `*MemberIneligibleError{MemberId, Reason: eligibility.Reason}` (default `Reason: "INELIGIBLE"` if `eligibility.Reason == ""`). Cross-module facade read; no tx.
- [ ] Step 3 (BEFORE tx): `catalog.FindCopy(ctx, copyId)` — bubbles `*catalog.CopyNotFoundError` on miss. If `copy.Status != catalog.CopyStatusAvailable`, return `*CopyUnavailableError{CopyId: copyId}`. Cross-module facade read; no tx.
- [ ] Step 4 (BEFORE tx): build the `LoanDto` — `loan := LoanDto{LoanId: LoanId(f.newID()), MemberId: membership.MemberId(authUser.MemberID), CopyId: copyId, BookId: copy.BookId, BorrowedAt: f.clock(), DueDate: addDays(borrowedAt, LoanDurationDays), ReturnedAt: nil}`. (Compute outside tx so the tx body is minimal.)
- [ ] Step 5 (the tx): `txc := f.txFactory(); err := txc.Run(ctx, func(ctx context.Context) error { if err := f.loans.SaveLoan(ctx, loan, txc); err != nil { return err }; txc.StageEvent(LoanOpened{LoanId: loan.LoanId, MemberId: loan.MemberId, CopyId: loan.CopyId, BookId: loan.BookId, BorrowedAt: loan.BorrowedAt, DueDate: loan.DueDate}); return nil })`. If `err != nil`, return `(LoanDto{}, err)`.
- [ ] Step 6 (AFTER tx commit AND after the staged `LoanOpened` event has published): `if err := f.catalog.MarkCopyUnavailable(ctx, copyId); err != nil { ... }`. **Decision point**: what to do when the post-commit catalog mutation fails? The TS source surfaces the error to the caller. The Go port matches: return `(LoanDto{}, err)` on catalog failure. The loan is already persisted and `LoanOpened` is already on the bus — this is the known inconsistency the post-commit rule accepts (saga or eventual reconciliation would close it in a real system; the teaching repo does not). Document this in the method's doc comment.
- [ ] Step 7: return `(loan, nil)` on success.
- [ ] `Borrow`'s doc comment names the architectural rules it embodies, in order: (1) authorize first; (2) cross-module reads before tx; (3) own-tx wraps only own writes + staged events; (4) post-commit cross-module mutation OUTSIDE tx; (5) `LoanOpened` publishes BEFORE the catalog mutation runs (because staged events publish during `Run`'s commit, and `MarkCopyUnavailable` runs after `Run` returns).
- [ ] `Facade.Borrow` is the ONLY exported method this slice adds.

#### Acceptance Criteria — facade test scaffolding

- [ ] `internal/lending/facade_test.go` lives in package `lending`. Uses stdlib `testing` only.
- [ ] A `buildFacade(t *testing.T, opts ...func(*lending.Overrides)) *lending.Facade` test helper constructs a facade via `NewFacadeWithOverrides`, returning the facade. Default ids via `sequentialIds("loan")` for `NewID`; deterministic clock returning `time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)` for `Clock`.
- [ ] A `buildScene(t *testing.T) sceneT` helper returns a struct exposing the facade, the underlying in-memory catalog facade, the underlying in-memory membership facade, the underlying bus, and a captured-events slice subscribed to `LoanOpened` / `LoanReturned` / `ReservationQueued`. Used by every test in this slice.
- [ ] Helper `seedBookAndCopy(t, scene, isbn, condition) (catalog.BookDto, catalog.CopyDto)` adds a book via the underlying catalog facade and registers an AVAILABLE copy — returns both for use in subsequent calls.
- [ ] Helper `registerMember(t, scene, name, email) membership.MemberDto` registers a member via the underlying membership facade.
- [ ] Helper `memberAuth(memberId membership.MemberId) accesscontrol.AuthUser` returns `accesscontrol.AuthUser{MemberID: string(memberId), Role: accesscontrol.RoleMember}`.
- [ ] All helpers live at the top of `facade_test.go`. No shared test package; no fixtures dir.

#### Acceptance Criteria — `Borrow` happy-path tests

- [ ] AC: A `MEMBER` borrowing an AVAILABLE copy returns a `LoanDto` with non-empty `LoanId`, the member's `MemberId`, the copy's `CopyId`, the copy's `BookId`, `BorrowedAt == clock()`, `DueDate == clock() + 14 days`, `ReturnedAt == nil`.
- [ ] AC: After `Borrow`, the loan is findable via `Facade.findLoanById` — wait, the Phase 3 facade doesn't expose `FindLoanById`. **Drop this AC**. Replace with: After `Borrow`, the underlying in-memory loan repo (exposed via the scene helper) contains exactly one loan whose `LoanId` matches the returned DTO.
- [ ] AC: After `Borrow`, the underlying catalog repo's copy for `copyId` has `Status == CopyStatusUnavailable` (assert via `scene.Catalog.FindCopy(ctx, copyId)`).
- [ ] AC: After `Borrow`, the captured-events slice contains exactly one event of type `LoanOpened` whose fields match the returned loan.
- [ ] AC: The staged `LoanOpened` event is observed BEFORE the catalog mark-unavailable runs. Verified via a `journal` slice that the captured-events handler appends `"event:LoanOpened"` to, while a custom `recordingCatalogFacade` decorator (unexported in the test file; wraps the real `*catalog.Facade`) appends `"catalog:MarkCopyUnavailable"` to the same journal in its `MarkCopyUnavailable` method. After `Borrow` succeeds, journal equals `["event:LoanOpened", "catalog:MarkCopyUnavailable"]` — event publishes first (during commit), catalog mutation runs second (after `Run` returns). **This is the canonical AC for the post-commit ordering rule.**

#### Acceptance Criteria — `Borrow` authorization + eligibility tests

- [ ] AC: A non-MEMBER (e.g. `accesscontrol.AuthUser{Role: accesscontrol.RoleAccount}`) attempting to borrow returns `*accesscontrol.UnauthorizedRoleError`. The loan repo remains empty, the copy remains AVAILABLE, no event published.
- [ ] AC: A SUSPENDED member attempting to borrow returns `*MemberIneligibleError{MemberId, Reason: "SUSPENDED"}`. The loan repo remains empty, the copy remains AVAILABLE, no event published. (Seed: register member, suspend via the underlying membership facade, then borrow.)
- [ ] AC: A member borrowing an UNAVAILABLE copy returns `*CopyUnavailableError{CopyId}`. The loan repo remains empty, no event published. (Seed: register copy, mark-unavailable via the underlying catalog facade, then borrow.)
- [ ] AC: A member borrowing an unknown `CopyId` returns `*catalog.CopyNotFoundError`. The loan repo remains empty, no event published.

#### Acceptance Criteria — `Borrow` tx atomicity tests

- [ ] AC: Loan repo failure during the tx — declare an unexported `throwingOnceLoanRepository` in `facade_test.go` (wraps `*InMemoryLoanRepository`, has `armFailureOnNextSave(err error)`, intercepts the next `SaveLoan` call to return the armed error, clears the arming). Arm it; call `Borrow`. The facade returns the armed error; the loan repo's underlying map is empty; the captured-events slice is empty (the staged event never published because `Run`'s work returned an error); the copy remains AVAILABLE (catalog mark-unavailable never ran).
- [ ] AC: `LoanOpened` publishes AFTER the in-memory tx commits, NOT before — verified by a custom `slowSaveLoanRepository` decorator that, when its `SaveLoan` is called, appends `"save:start"` to a journal and the staged closure appends `"save:commit"` to the same journal. The captured `LoanOpened` handler appends `"event:publish"`. After `Borrow` succeeds, the journal contains `"save:start"` then `"save:commit"` (both at commit-time per the in-memory tx model) then `"event:publish"` then (via the recording catalog decorator) `"catalog:MarkCopyUnavailable"`. Proves: stage closure runs during commit; event publishes AFTER stage closures; catalog mutation runs AFTER event publish. (This is the most expensive test in the slice; ~20 lines of decorator code; documents the architecture cleanly.)
- [ ] AC: Catalog mark-unavailable failure AFTER commit — declare a `throwingOnceCatalogFacade` decorator (unexported, wraps `*catalog.Facade`, has `armFailureOnNextMarkCopyUnavailable(err)`). Arm it; call `Borrow`. The facade returns the armed error. The loan repo CONTAINS the new loan (the tx committed). The captured-events slice contains the `LoanOpened` event (it published during commit). The copy remains AVAILABLE (catalog mutation failed). Documents the known post-commit-failure-is-inconsistent gap.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go test ./internal/lending/...` is green and runs in under 200 ms.
- [ ] `go build ./...` is green.
- [ ] No `init()` introduced.
- [ ] The throwing-once decorators (`throwingOnceLoanRepository`, `throwingOnceCatalogFacade`, `recordingCatalogFacade`, `slowSaveLoanRepository`) are unexported and live ONLY in `facade_test.go`. They are NOT imported by any other package; verified by `Grep` for the type names across the codebase returning only the test file.

---

### Slice 5: `Facade.Reserve` — pure staged-event flow (no cross-module write)

Brings the repository to "the pure staged-event variant of the borrow pattern is callable and tested." Reserve is simpler than Borrow because it has no post-commit cross-module mutation — the only side effect is the staged `ReservationQueued` event. This slice exists separately from Slice 4 so the simpler variant has its own commit and its own ACs.

#### Acceptance Criteria — `Reserve` method

- [ ] `Facade.Reserve(ctx context.Context, memberId membership.MemberId, bookId catalog.BookId) (ReservationDto, error)` is exported.
- [ ] Step 1 (BEFORE tx): `membership.CheckEligibility(ctx, memberId)`; on `!Eligible` return `*MemberIneligibleError{MemberId, Reason}`. (No authorization step — the TS source's `reserve` doesn't authorize; it's a member-self-service action where the controller already established the memberId. Match TS source 1:1.)
- [ ] Step 2 (BEFORE tx): build the reservation — `reservation := ReservationDto{ReservationId: ReservationId(f.newID()), MemberId: memberId, BookId: bookId, ReservedAt: f.clock(), FulfilledAt: nil}`.
- [ ] Step 3 (the tx): `txc := f.txFactory(); err := txc.Run(ctx, func(ctx context.Context) error { if err := f.reservations.SaveReservation(ctx, reservation, txc); err != nil { return err }; txc.StageEvent(ReservationQueued{ReservationId: reservation.ReservationId, MemberId: reservation.MemberId, BookId: reservation.BookId, ReservedAt: reservation.ReservedAt}); return nil })`. On failure, return `(ReservationDto{}, err)`.
- [ ] Step 4: return `(reservation, nil)` on success. No post-commit cross-module call; `Reserve` has no `MarkCopyUnavailable` analog.
- [ ] `Reserve`'s doc comment names the variant: "Pure staged-event flow — no post-commit cross-module side effect. Compare with `Borrow` which calls `catalog.MarkCopyUnavailable` after commit."
- [ ] `Reserve` does NOT validate that the `BookId` exists in catalog. The TS source delegates that responsibility to the auto-loan consumer (Phase 4), which fails-soft on unknown books. **Open Question 3** flags whether Phase 3's `Reserve` should pre-validate via `catalog.FindBookByIsbn`-equivalent. Recommended default: **do NOT validate** — match TS source 1:1.

#### Acceptance Criteria — `Reserve` tests

- [ ] AC: A member reserving a book returns a `ReservationDto` with non-empty `ReservationId`, the member's `MemberId`, the book's `BookId`, `ReservedAt == clock()`, `FulfilledAt == nil`.
- [ ] AC: After `Reserve`, the underlying reservation repo contains exactly one reservation; the captured-events slice contains exactly one `ReservationQueued` event whose fields match.
- [ ] AC: A SUSPENDED member reserving returns `*MemberIneligibleError{Reason: "SUSPENDED"}`; the reservation repo remains empty; no event published.
- [ ] AC: An unknown `MemberId` (one not registered with membership) returns `*membership.MemberNotFoundError` (cross-module read surfaces the error); the reservation repo remains empty; no event published.
- [ ] AC: Reservation repo failure during the tx — declare `throwingOnceReservationRepository` unexported in `facade_test.go`. Arm; call `Reserve`. The facade returns the armed error; the reservation repo's map is empty; the captured-events slice is empty.
- [ ] AC: Two concurrent-from-the-user-POV reservations for the same book by different members both succeed and both produce `ReservationQueued` events in stage order across the two `Reserve` calls. (Single-goroutine test — Phase 3 does NOT test goroutine concurrency.)
- [ ] AC: The `Reserve` flow does NOT call any catalog method. Verified via a `recordingCatalogFacade` (unexported in `facade_test.go`) whose `MarkCopyUnavailable`, `MarkCopyAvailable`, `FindCopy`, and other methods record their invocation; after `Reserve` succeeds, the recording is empty.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go test ./internal/lending/...` stays green and under 200 ms.
- [ ] `go build ./...` is green.
- [ ] No `init()` introduced.
- [ ] No new exported surface beyond `Facade.Reserve`.

---

### Slice 6: `Facade.ReturnLoan` — own-tx + post-commit catalog mutation + direct `LoanReturned` publish

Brings the repository to "`ReturnLoan` is callable and demonstrates the variant where `LoanReturned` publishes AFTER the catalog mutation runs — not via `StageEvent` — so future consumers (Phase 4 auto-loan saga) observe the fully-consistent state." This is the most subtle of the three flows because the publish order matters: the saga consumer must see the copy as AVAILABLE when it receives `LoanReturned`, otherwise it would try to auto-borrow an UNAVAILABLE copy.

#### Acceptance Criteria — `ReturnLoan` method

- [ ] `Facade.ReturnLoan(ctx context.Context, loanId LoanId) (LoanDto, error)` is exported.
- [ ] Step 1: `loan, err := f.loans.FindLoanById(ctx, loanId)`; on `err != nil` return; on `loan == nil` return `(LoanDto{}, &LoanNotFoundError{LoanId: loanId})`.
- [ ] Step 2: compute the returned-loan snapshot: `returnedAt := f.clock(); returned := *loan; returned.ReturnedAt = &returnedAt`.
- [ ] Step 3 (the tx): `txc := f.txFactory(); err := txc.Run(ctx, func(ctx context.Context) error { return f.loans.SaveLoan(ctx, returned, txc) })`. NOTE: NO `txc.StageEvent(LoanReturned{...})` here. `LoanReturned` does NOT stage. On failure return.
- [ ] Step 4 (AFTER commit): `if err := f.catalog.MarkCopyAvailable(ctx, returned.CopyId); err != nil { return (LoanDto{}, err) }`.
- [ ] Step 5 (AFTER catalog mutation): `if err := f.bus.Publish(ctx, LoanReturned{LoanId: returned.LoanId, MemberId: returned.MemberId, CopyId: returned.CopyId, BookId: returned.BookId, ReturnedAt: *returned.ReturnedAt}); err != nil { /* log but do not surface; bus errors are non-fatal */ }`. The publish is direct (`bus.Publish`), NOT staged — because it must happen AFTER the catalog mark-available so that Phase 4's consumer observes the consistent state.
- [ ] Step 6: return `(returned, nil)`.
- [ ] `ReturnLoan`'s doc comment names the variant: "`LoanReturned` publishes via `bus.Publish` AFTER the catalog mark-available. NOT via `tx.StageEvent` (which would publish during commit, BEFORE the catalog mutation). Phase 4's auto-loan saga consumer relies on this ordering: by the time it receives `LoanReturned`, the copy is AVAILABLE and a new `lending.Borrow` will succeed. Diverges from the `Borrow` pattern (where `LoanOpened` IS staged) precisely because `Borrow`'s consumers don't need to observe the post-commit catalog state."
- [ ] `ReturnLoan` is idempotent in shape but not in fact — calling it twice for the same `loanId` (without a re-borrow) writes a second `returnedAt` (the latest wins) and publishes a second `LoanReturned`. This matches the TS source's behaviour (no idempotency check). Phase 3 does NOT add an idempotency check; **recorded as Open Question 4** in case the developer wants one.

#### Acceptance Criteria — `ReturnLoan` tests

- [ ] AC: After `Borrow(...)` then `ReturnLoan(loanId)`, the returned `LoanDto` has `ReturnedAt != nil` with the value equal to `clock()`.
- [ ] AC: After return, the underlying loan repo's row for `loanId` has `ReturnedAt` set; `FindLoanById` reflects it (via direct repo access in the test).
- [ ] AC: After return, the underlying catalog's copy is AVAILABLE.
- [ ] AC: After return, the captured-events slice contains a `LoanReturned` event whose `ReturnedAt` matches `clock()`.
- [ ] AC: **Canonical ordering AC**: a journal helper records the sequence: the recording catalog decorator appends `"catalog:MarkCopyAvailable"` when called; the captured `LoanReturned` subscriber appends `"event:LoanReturned"`. After `ReturnLoan` succeeds, the journal equals `["catalog:MarkCopyAvailable", "event:LoanReturned"]` — catalog runs first, event publishes second. **This is the AC that locks in the post-commit-then-publish ordering for `ReturnLoan`.**
- [ ] AC: `ReturnLoan` for an unknown `LoanId` returns `*LoanNotFoundError{LoanId}`. The loan repo is unchanged; no event published; no catalog mutation.
- [ ] AC: Loan repo failure during the tx (`throwingOnceLoanRepository` armed on the next `SaveLoan`) — `ReturnLoan` returns the armed error; the loan's `ReturnedAt` remains nil in the repo (the tx rolled back); the catalog copy remains UNAVAILABLE (catalog mark-available never ran); no `LoanReturned` event published.
- [ ] AC: Catalog mark-available failure AFTER commit (`throwingOnceCatalogFacade.armFailureOnNextMarkCopyAvailable(err)`) — `ReturnLoan` returns the armed error; the loan's `ReturnedAt` IS set in the repo (the tx committed); the copy remains UNAVAILABLE; NO `LoanReturned` event was published (the publish step happens AFTER the catalog mutation, so the catalog failure short-circuits the publish). Documents the known post-commit-failure gap.
- [ ] AC: Bus publish failure during the post-commit-publish step — declare a `flakyBus` decorator (unexported) wrapping the real `InMemoryEventBus` whose `Publish` returns an error for `LoanReturned`. Call `ReturnLoan`. The facade returns nil (bus errors are non-fatal per the doc comment). The loan's `ReturnedAt` is set; the copy is AVAILABLE; the bus error is logged at `error` level (asserted via a test slog handler).
- [ ] AC: `ReturnLoan` for a loan that's already returned (call `ReturnLoan` twice) succeeds both times. The second call writes a new `returnedAt` (the second-call `clock()` value); a second `LoanReturned` is published. (Documents lack of idempotency; not a bug per source-fidelity rule.)
- [ ] AC: The `LoanReturned` event IS observed on the bus even though Phase 3 ships no subscriber to it — verified via the test-only handler the scene helper subscribes. Phase 3 Done criteria item (the spec's Definition of Done) hinges on this AC.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go test ./internal/lending/...` stays green and under 200 ms (the slowSave/recording decorators are cheap).
- [ ] `go build ./...` is green.
- [ ] No `init()` introduced.
- [ ] The bus-failure log assertion uses a test slog handler, not a string match.

---

### Slice 7: HTTP handlers + `module.go` + composition root + `0003_lending.sql` + bun repos + crucial-path integration test

Brings the repository to "every lending facade method is reachable over HTTP at its canonical URL, persists to Postgres via bun, the wiring composition root constructs the lending facade with the bun TransactionalContext factory, and a crucial-path integration test proves the full vertical end-to-end against testcontainers." This is the largest slice in Phase 3 because it ties everything together — but every piece is mechanical now that Slices 1–6 have established the patterns.

#### Acceptance Criteria — migration

- [ ] `migrations/0003_lending.sql` exists. Creates two tables:
  - `loans`: `loan_id UUID PRIMARY KEY`, `member_id UUID NOT NULL`, `copy_id UUID NOT NULL`, `book_id UUID NOT NULL`, `borrowed_at TIMESTAMPTZ NOT NULL`, `due_date TIMESTAMPTZ NOT NULL`, `returned_at TIMESTAMPTZ NULL`. No FK references to `members` / `books` / `copies` (cross-module FKs are forbidden).
  - `reservations`: `reservation_id UUID PRIMARY KEY`, `member_id UUID NOT NULL`, `book_id UUID NOT NULL`, `reserved_at TIMESTAMPTZ NOT NULL`, `fulfilled_at TIMESTAMPTZ NULL`. Same no-FK rule.
- [ ] `migrations/atlas.sum` is regenerated via `atlas migrate hash` and committed.
- [ ] `task migrate:apply` against a fresh dev Postgres creates both tables and is a no-op on second run.
- [ ] `task migrate:status` after apply reports `0001_catalog.sql`, `0002_membership.sql`, and `0003_lending.sql` as applied.

#### Acceptance Criteria — bun loan + reservation repositories

- [ ] `internal/lending/bun_loan_repository.go` exports `BunLoanRepository struct { db *bun.DB }` and `NewBunLoanRepository(db *bun.DB) *BunLoanRepository`. Implements `LoanRepository`.
- [ ] `var _ LoanRepository = (*BunLoanRepository)(nil)` guard at the bottom of the file (Phase 2 convention).
- [ ] Bun struct tags: `LoanRow struct { LoanId LoanId ` `bun:"loan_id,pk"` `; MemberId membership.MemberId ` `bun:"member_id,notnull"` `; CopyId catalog.CopyId ` `bun:"copy_id,notnull"` `; BookId catalog.BookId ` `bun:"book_id,notnull"` `; BorrowedAt time.Time ` `bun:"borrowed_at,notnull"` `; DueDate time.Time ` `bun:"due_date,notnull"` `; ReturnedAt *time.Time ` `bun:"returned_at"` ` }` mapped to table `loans` via `bun:"table:loans"`.
- [ ] `SaveLoan(ctx, loan, txc)` calls `txc.Stage(func(ctx) error { handle, _ := tx.TxFromContext(ctx); _, err := handle.NewInsert().Model(&row).On("CONFLICT (loan_id) DO UPDATE SET member_id = EXCLUDED.member_id, copy_id = EXCLUDED.copy_id, book_id = EXCLUDED.book_id, borrowed_at = EXCLUDED.borrowed_at, due_date = EXCLUDED.due_date, returned_at = EXCLUDED.returned_at").Exec(ctx); return err })`. Same upsert pattern as Phase 2's catalog `SaveBook`.
- [ ] `FindLoanById(ctx, loanId)`: `db.NewSelect().Model(&row).Where("loan_id = ?", loanId).Scan(ctx)`; on `sql.ErrNoRows` return `(nil, nil)`; on other errors wrap and return. Reads bypass the tx context — they go directly through the base `*bun.DB`.
- [ ] `ListLoansForMember` / `ListLoansForBook` / `ListLoans`: bun selects ordered by `loan_id ASC` (Phase 2 convention for in-memory/bun parity).
- [ ] `internal/lending/bun_reservation_repository.go` mirrors the pattern with `ReservationRow` mapped to `reservations`. `PendingReservationCountForBook` uses `db.NewSelect().Model((*ReservationRow)(nil)).Where("book_id = ? AND fulfilled_at IS NULL", bookId).Count(ctx)`.
- [ ] `internal/lending/bun_loan_repository_test.go` (`//go:build integration`): runs the same scenarios as `in_memory_loan_repository_test.go` against testcontainers Postgres. Per Phase 2 convention, the test extracts a `runLoanRepositoryContract(t *testing.T, repo LoanRepository, txFactory tx.TransactionalContextFactory)` helper to share scenarios between in-memory and bun (OR duplicates the scenarios — AC is "behaviour is identical"). Test-level `t.Cleanup` truncates `loans` between tests.
- [ ] `internal/lending/bun_reservation_repository_test.go` (`//go:build integration`): same pattern for reservations.

#### Acceptance Criteria — HTTP DTOs

- [ ] `internal/lending/http/dto.go` exports:
  - `BorrowRequest struct { MemberId string ` `json:"memberId"` `; CopyId string ` `json:"copyId"` ` }`.
  - `LoanResponse struct { LoanId string ` `json:"loanId"` `; MemberId string ` `json:"memberId"` `; CopyId string ` `json:"copyId"` `; BookId string ` `json:"bookId"` `; BorrowedAt time.Time ` `json:"borrowedAt"` `; DueDate time.Time ` `json:"dueDate"` `; ReturnedAt *time.Time ` `json:"returnedAt,omitempty"` ` }`.
  - `ReserveRequest struct { MemberId string ` `json:"memberId"` `; BookId string ` `json:"bookId"` ` }`.
  - `ReservationResponse struct { ReservationId string ` `json:"reservationId"` `; MemberId string ` `json:"memberId"` `; BookId string ` `json:"bookId"` `; ReservedAt time.Time ` `json:"reservedAt"` `; FulfilledAt *time.Time ` `json:"fulfilledAt,omitempty"` ` }`.
- [ ] DTOs are never imported outside `internal/lending/http/`.

#### Acceptance Criteria — mapping

- [ ] `internal/lending/http/mapping.go` exports unexported helpers: `toLoanResponse(LoanDto) LoanResponse`, `toReservationResponse(ReservationDto) ReservationResponse`.
- [ ] Mapping helpers handle `nil` time pointers — `ReturnedAt: dto.ReturnedAt` propagates the pointer as-is so `json:"returnedAt,omitempty"` strips the field when nil.

#### Acceptance Criteria — handlers

- [ ] `internal/lending/http/handlers.go` exports `Handlers struct { facade *lending.Facade; logger *slog.Logger }` + `NewHandlers(facade *lending.Facade, logger *slog.Logger) *Handlers`.
- [ ] Handlers go through `sharedhttp.Handle` so domain errors are middleware-mapped.
- [ ] `Handlers.Borrow(w, r) error`: decode `BorrowRequest` with `DisallowUnknownFields`; parse via `lending.ParseBorrowRequest`; construct `accesscontrol.AuthUser{MemberID: req.MemberId, Role: accesscontrol.RoleMember}` (the demo-auth shortcut); call `facade.Borrow(ctx, authUser, copyId)`; respond 201 + `LoanResponse`.
- [ ] `Handlers.ReturnLoan(w, r) error`: read `:loanId` URL param via `chi.URLParam(r, "loanId")`; parse via `lending.ParseReturnLoanRequest`; call `facade.ReturnLoan(ctx, loanId)`; respond 200 + `LoanResponse`.
- [ ] `Handlers.Reserve(w, r) error`: decode `ReserveRequest` with `DisallowUnknownFields`; parse via `lending.ParseReserveRequest`; call `facade.Reserve(ctx, memberId, bookId)`; respond 201 + `ReservationResponse`.

#### Acceptance Criteria — `module.go` (wire into chi)

- [ ] `internal/lending/module.go` exports `type Deps struct { Facade *lending.Facade; Logger *slog.Logger }` and `func Wire(r chi.Router, deps Deps)`.
- [ ] `Wire` mounts:
  - `POST /loans` → `Borrow`
  - `PATCH /loans/{loanId}/return` → `ReturnLoan`
  - `POST /reservations` → `Reserve`
- [ ] Listing endpoints (`GET /loans/overdue`, `GET /members/{memberId}/loans`, etc.) are NOT mounted in Phase 3 per the Out-of-Scope rule.

#### Acceptance Criteria — composition root

- [ ] `internal/app/wiring.go` Phase-3 changes:
  - Construct `loansRepo := lending.NewBunLoanRepository(bunDB)` and `reservationsRepo := lending.NewBunReservationRepository(bunDB)`.
  - Construct `txFactory := func() tx.TransactionalContext { return tx.NewBunTransactionalContext(bunDB, bus, logger) }`. The factory closes over the same `bus` instance the facade uses.
  - Construct the lending facade via `lending.NewFacadeWithOverrides(lending.Overrides{Catalog: catalogFacade, Membership: membershipFacade, AccessControl: accessControlFacade, Loans: loansRepo, Reservations: reservationsRepo, Bus: bus, TxFactory: txFactory, NewID: uuid.NewString, Clock: time.Now, Logger: logger})`.
  - Register domain errors in `buildDomainErrorRegistry`: `*lending.LoanNotFoundError → 404 "loan_not_found"`, `*lending.ReservationNotFoundError → 404 "reservation_not_found"`, `*lending.CopyUnavailableError → 409 "copy_unavailable"`, `*lending.MemberIneligibleError → 409 "member_ineligible"`, `*lending.BorrowValidationError → 400 "invalid_borrow"`, `*lending.ReserveValidationError → 400 "invalid_reserve"`, `*lending.ReturnLoanValidationError → 400 "invalid_return"`. (Access-control errors `*accesscontrol.UnauthorizedRoleError → 403 "unauthorized_role"` were already registered in Phase 1; the lending borrow path now exercises that registration at runtime.)
  - Mount: `lendingModule.Wire(router, lendingModule.Deps{Facade: lendingFacade, Logger: logger})`.
  - Extend `app.Wired` with `LendingFacade *lending.Facade`.

#### Acceptance Criteria — handler tests

- [ ] `internal/lending/http/handlers_test.go` (unit, no build tag) constructs a real `*lending.Facade` via `lending.NewFacadeWithOverrides`, wraps in `NewHandlers`, exercises each handler via `httptest.NewRecorder` + a chi router so URL-params resolve.
- [ ] AC: `POST /loans` with a valid body returns 201 + `LoanResponse`.
- [ ] AC: `POST /loans` with a SUSPENDED member returns 409 + `ErrorResponse{Error: "member_ineligible"}`.
- [ ] AC: `POST /loans` with an UNAVAILABLE copy returns 409 + `ErrorResponse{Error: "copy_unavailable"}`.
- [ ] AC: `POST /loans` with an unknown `copyId` returns 404 + `ErrorResponse{Error: "copy_not_found"}`.
- [ ] AC: `POST /loans` with a blank `memberId` returns 400 + `ErrorResponse{Error: "invalid_borrow"}`.
- [ ] AC: `POST /loans` with an unknown JSON field returns 400 (driven by `DisallowUnknownFields`).
- [ ] AC: `PATCH /loans/{loanId}/return` for a known loan returns 200 + `LoanResponse` with non-nil `ReturnedAt`.
- [ ] AC: `PATCH /loans/{loanId}/return` for an unknown loan returns 404 + `ErrorResponse{Error: "loan_not_found"}`.
- [ ] AC: `POST /reservations` with a valid body returns 201 + `ReservationResponse`.
- [ ] AC: `POST /reservations` with a SUSPENDED member returns 409 + `member_ineligible`.
- [ ] AC: `POST /reservations` with an unknown `memberId` returns 404 + `member_not_found` (cross-module error from membership facade).
- [ ] AC: Authorization rejection — `POST /loans` body constructs an `AuthUser` with `Role: RoleAccount` (via a test-only handler that overrides the demo-auth shortcut) returns 403 + `unauthorized_role`. **Implementation note**: since the production handler hard-codes `Role: RoleMember`, this AC requires either (a) accepting a `role` query/header in the demo-auth shortcut for test purposes, or (b) testing the authorization path directly at the facade level (already covered in Slice 4). **Recommended default**: drop this handler-test AC; the facade-level test already locks the authorization behaviour. **Recorded as Open Question 5**.
- [ ] Tests assert response bodies via JSON decode + field equality. No raw-string matching.

#### Acceptance Criteria — crucial-path integration test

- [ ] `test/crucial_path/lending_integration_test.go` (`//go:build integration`) does the full vertical: `StartPostgres` + `StartRedis` (Redis used by catalog's bookcache wiring) + `BootApp` against those containers.
- [ ] AC: Seed sequence — `POST /members` to register a member; `POST /books` to add a book; `POST /books/{bookId}/copies` to register a copy.
- [ ] AC: `POST /loans` with `{"memberId": "<id>", "copyId": "<copyId>"}` returns 201 + `LoanResponse` with non-empty `loanId`, populated `borrowedAt` and `dueDate`. Direct row count via `wired.DB`: `SELECT COUNT(*) FROM loans WHERE loan_id = $1` returns 1.
- [ ] AC: After `POST /loans`, a follow-up `GET /copies/{copyId}` — wait, catalog doesn't expose a `GET /copies/{copyId}` endpoint in Phase 2. **Replace with**: after `POST /loans`, the underlying `wired.CatalogFacade.FindCopy(ctx, copyId)` (direct facade call in the test) returns `CopyDto{Status: "UNAVAILABLE"}`. Documents the post-commit cross-module mutation worked.
- [ ] AC: `PATCH /loans/{loanId}/return` returns 200 + `LoanResponse` with non-nil `returnedAt`. Row in `loans` has `returned_at IS NOT NULL`. `wired.CatalogFacade.FindCopy(ctx, copyId)` returns `CopyDto{Status: "AVAILABLE"}`.
- [ ] AC: `POST /reservations` with `{"memberId": "<id>", "bookId": "<bookId>"}` returns 201 + `ReservationResponse`. Row in `reservations` table.
- [ ] AC: Eligibility — `PATCH /members/{id}/suspend` (already shipped in Phase 2), then `POST /loans` returns 409 + `member_ineligible`. The loans table count is unchanged from before the call.
- [ ] AC: Tx atomicity at the integration tier — drop a foreign-key-style invariant by simulating a copy that's already UNAVAILABLE via `PATCH /copies/{copyId}/unavailable` (Phase 2 endpoint), then `POST /loans` returns 409 + `copy_unavailable`; the loans table count is unchanged; the `LoanOpened` event was NOT published (verified by capturing events via the wired bus — `wired.Bus` exposed for test introspection; OR by an `eventCapturingBus` injected via `app.Deps` at boot time).
- [ ] AC: `LoanReturned` is observable on the bus — the test subscribes to `LoanReturned` via `wired.Bus.Subscribe(...)` BEFORE calling `PATCH /loans/{loanId}/return`; asserts the event arrives within 100ms of the PATCH returning 200; asserts the event's `LoanId` matches the loan being returned. (This is the test that proves Phase 3's "auto-loan saga handler not yet listening; verify event is on the bus" criterion.)
- [ ] AC: `t.Cleanup` truncates `loans` AND `reservations` after each test; the catalog/membership cleanups already established in Phase 2 also run.
- [ ] The test exposes `wired.DB` (Phase 2 already did this) and `wired.Bus` (NEW — extend `app.Wired` to expose the bus for integration-test introspection).

#### Acceptance Criteria — `.http` file activation

- [ ] `.http/lending.http` exists with active requests using `{{baseUrl}}`: `POST {{baseUrl}}/loans` (sample body with `memberId` + `copyId`), `PATCH {{baseUrl}}/loans/<loanId>/return`, `POST {{baseUrl}}/reservations` (sample body with `memberId` + `bookId`).
- [ ] The Phase-3 placeholder `### POST {{baseUrl}}/loans — Phase 3 (lending)` line in `.http/healthz.http` (if it survived Phase 2's cleanup) is removed; if `.http/lending.http` already covers the endpoint, the placeholder is redundant.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go test ./...` (unit) is green; total unit suite stays well under 1 second (target: under 700ms — Phase 3 adds ~30 facade scenarios + ~12 HTTP handler scenarios + tx-substrate unit tests).
- [ ] `go test -tags=integration ./...` is green and runs in under 90 seconds (Phase 1's healthz + Phase 2's catalog + membership crucial-path + Phase 2's bun-repo contract + Phase 2's Redis-adapter + Phase 3's tx-bun atomicity + Phase 3's lending bun-repo contract + Phase 3's lending crucial-path).
- [ ] `go vet ./...` clean; `gofmt -w .` no-op.
- [ ] No `init()` introduced in any Phase-3 file.

---

## File Map

| Slice | Files created (or significantly modified) |
| --- | --- |
| 1 | `internal/shared/tx/context.go`, `internal/shared/tx/in_memory.go`, `internal/shared/tx/in_memory_test.go` |
| 2 | `internal/shared/tx/bun.go`, `internal/shared/tx/bun_test.go` (`//go:build integration`) |
| 3 | `internal/lending/types.go`, `internal/lending/schema.go`, `internal/lending/loan_repository.go`, `internal/lending/reservation_repository.go`, `internal/lending/in_memory_loan_repository.go`, `internal/lending/in_memory_loan_repository_test.go`, `internal/lending/in_memory_reservation_repository.go`, `internal/lending/in_memory_reservation_repository_test.go`, `internal/lending/sample_data.go`, `internal/lending/configuration.go`, `internal/lending/facade.go` (skeleton: struct + constructor + `LoanDurationDays` constant + `addDays` helper; no facade methods yet) |
| 4 | `internal/lending/facade.go` (extended: `Borrow` method), `internal/lending/facade_test.go` (declares unexported `sequentialIds`, `throwingOnceLoanRepository`, `throwingOnceCatalogFacade`, `recordingCatalogFacade`, `slowSaveLoanRepository`, `flakyBus` test decorators + scene helpers + `Borrow` scenarios) |
| 5 | `internal/lending/facade.go` (extended: `Reserve` method), `internal/lending/facade_test.go` (extended: `throwingOnceReservationRepository` + `Reserve` scenarios) |
| 6 | `internal/lending/facade.go` (extended: `ReturnLoan` method), `internal/lending/facade_test.go` (extended: `ReturnLoan` scenarios) |
| 7 | `migrations/0003_lending.sql`, `migrations/atlas.sum` (regenerated), `internal/lending/bun_loan_repository.go`, `internal/lending/bun_loan_repository_test.go` (`//go:build integration`), `internal/lending/bun_reservation_repository.go`, `internal/lending/bun_reservation_repository_test.go` (`//go:build integration`), `internal/lending/http/dto.go`, `internal/lending/http/mapping.go`, `internal/lending/http/handlers.go`, `internal/lending/http/handlers_test.go`, `internal/lending/module.go`, `internal/app/wiring.go` (modified: lending facade construction + bun tx factory + error registry + `Wire` call + `app.Wired.LendingFacade` + `app.Wired.Bus`), `test/crucial_path/lending_integration_test.go` (`//go:build integration`), `.http/lending.http` |

No file is created in more than one slice. Slices 4–6 each extend `internal/lending/facade.go` and `internal/lending/facade_test.go`; each extension is additive (no rewrites of earlier-slice code).

**Slice-ordering note**: Slices 1–6 run in declared order — each depends on the previous. Slice 7 depends on Slices 1–6 (uses the tx interface, all three facade methods, the repos, the sample data) and on Phase 2's catalog + membership facades being live in the composition root.

## Idiom Enforcement (every slice must follow)

Every slice in Phase 3 (and every slice in every later phase) follows these conventions. Carried forward from Phase 1/2; new Phase-3 additions called out at the end.

- **Manual constructor wiring.** No `wire`, no `fx`. `internal/app/wiring.go` constructs collaborators in order; the lending facade gets the catalog/membership/accesscontrol facades, the loan/reservation repos, the bus, and the tx factory passed in explicitly.
- **HTTP DTOs live in `<module>/http/dto.go`** and never escape that sub-package. Phase 3 introduces `internal/lending/http/` following the same rule.
- **Stdlib testing only.** No `testify`. All Phase-3 tests use `t.Run`, `t.Errorf`, `t.Fatalf`, `errors.As`, `errors.Is`. Test slog handlers for log-record assertions live as tiny test helpers (~10 lines) in the test files that need them.
- **Hand-written validation.** No `go-playground/validator`. Phase 3's `ParseBorrowRequest` / `ParseReserveRequest` / `ParseReturnLoanRequest` are 3-5 lines each — trim + reject-blank + return typed-newtype.
- **testcontainers-go reaches podman via `DOCKER_HOST`.** Already documented from Phase 1.
- **No mocks in tests.** Spec-local decorator wrappers (unexported, declared in the test file) for fault injection. Phase 3 declares: `throwingOnceLoanRepository`, `throwingOnceReservationRepository`, `throwingOnceCatalogFacade`, `recordingCatalogFacade`, `slowSaveLoanRepository`, `flakyBus`. Each is unexported, each lives only in `facade_test.go`, none is imported by any other package.
- **`log/slog` everywhere.** Every Phase-3 component takes a `*slog.Logger` constructor parameter. The default in `NewFacadeWithOverrides` is a discard handler.
- **Functional options for sample data.** Phase 3 introduces `SampleNewLoan`, `SampleReturnedLoan`, `SampleNewReservation` via `func(*<Dto>)` options.
- **No `init()` for module wiring.** Verified per slice by absence of any `init()` in `internal/shared/tx/`, `internal/lending/`, `internal/lending/http/`.
- **Source-fidelity names.** Match TS source 1:1: `LoanOpened` (not `LoanCreated`), `LoanReturned` (not `LoanClosed`), `ReservationQueued` (not `ReservationPending`), `CopyUnavailableError` (not `CopyAlreadyBorrowedError`), `MemberIneligibleError` (not `MemberSuspendedError`), `BorrowedAt`/`DueDate`/`ReturnedAt` (not `OpenedAt`/`ExpiresAt`/`ClosedAt`). Per `.claude/MEMORY.md` source-fidelity rule.
- **Defensive slice/struct copies in in-memory repos.** Phase 2 established the rule for `catalog.ListBooks` returning copies; Phase 3's `lending.ListLoans*` follows it (per-element struct copy, slice copy at the outer layer).
- **Bun repository: `var _ Repository = ...` guard + `(nil, nil)` on `sql.ErrNoRows`.** Phase 2 conventions; Phase 3's bun loan + reservation repos follow.
- **`DisallowUnknownFields` on JSON decoders.** Phase 2 established the rule for `UpdateBookRequest`; Phase 3 applies it to `BorrowRequest` and `ReserveRequest`.
- **Handlers return `error` → `sharedhttp.Handle`.** Phase 1 established; Phase 3 follows.

**New Phase-3 conventions** (carry forward to Phase 4+):

- **`TransactionalContext` is the single entry point for any business operation that writes to >1 aggregate atomically.** A facade method that writes to its own repo AND stages a domain event opens a tx via `f.txFactory()` and runs the writes + event-staging inside `tx.Run`. Single-write operations that do NOT publish events MAY skip the tx (Phase 3 has none of these in lending; all three flows stage at least one event).
- **Events are STAGED via `StageEvent(evt)` during the tx; they publish AFTER commit in stage order.** Bus failures during the post-commit publish are logged but do NOT fail `Run`. Crash between commit and publish is a known outbox-shaped gap; accepted per D27/D28.
- **Post-commit side effects (cross-module mutations called from a facade method) go OUTSIDE `tx.Run`, AFTER the staged events have published.** Phase 3's `Borrow` runs `catalog.MarkCopyUnavailable` after `Run` returns nil; the order is: own-write → staged-event-publish → cross-module-write. `ReturnLoan` is the exception: it publishes `LoanReturned` AFTER the cross-module catalog mark-available, NOT via `StageEvent`, so post-commit consumers (Phase 4 saga) observe the catalog as AVAILABLE.
- **Cross-module facade READS happen BEFORE the tx opens.** `lending.Borrow` calls `catalog.FindCopy` and `membership.CheckEligibility` before `f.txFactory()` is invoked. Reads are idempotent and have no transactional concern; running them outside the tx keeps the tx scope tight to lending's own writes.
- **Repository writes participate in the tx via `txc.Stage(apply)`.** The in-memory repo defers `apply` to commit; the bun repo runs `apply` immediately inside the tx callback against the live tx handle resolved via `tx.TxFromContext(ctx)`. The repo's `SaveX(ctx, dto, txc)` signature is the seam — facades never call repo writes outside a tx context.
- **Bun tx handle never escapes `internal/shared/tx`.** Repositories resolve it via `tx.TxFromContext(ctx)` which returns the broader `bun.IDB` interface. No facade ever sees a `bun.Tx` directly. No repo signature names `bun.Tx`.
- **One-`TransactionalContext`-per-operation.** Facades call `f.txFactory()` per business operation; never reuse a context across operations. Documented in the `TransactionalContextFactory` doc comment.

## Definition of Done — Phase 3

Phase 3 is done when **all** of the following are true. Each item is verified manually (developer laptop) or by `task test` / `task test:integration`.

### Functional

- [ ] `task up && task migrate:apply && task run` boots the server on port 3000 with all three migrations applied. `task migrate:status` reports `0001_catalog.sql`, `0002_membership.sql`, `0003_lending.sql` as applied.
- [ ] `curl -X POST localhost:3000/members -d '{"name":"Ada","email":"ada@example.com"}'` returns 201 + a `MemberResponse` (Phase 2 carry-over).
- [ ] `curl -X POST localhost:3000/books -d '{"title":"X","authors":["A"],"isbn":"978-0135957059"}'` returns 201 + a `BookResponse` (Phase 2 carry-over).
- [ ] `curl -X POST localhost:3000/books/<bookId>/copies -d '{"condition":"GOOD"}'` returns 201 + a `CopyResponse{Status:"AVAILABLE"}` (Phase 2 carry-over).
- [ ] `curl -X POST localhost:3000/loans -d '{"memberId":"<id>","copyId":"<copyId>"}'` returns 201 + a `LoanResponse` with non-empty `loanId`, populated `borrowedAt` (current time), `dueDate` (14 days later), and `returnedAt: null` (omitted from JSON via `omitempty`).
- [ ] After the borrow, a direct `GET /copies/{copyId}` equivalent — or, since Phase 2 doesn't ship one, a direct row check via `SELECT status FROM copies WHERE copy_id = $1` — returns `UNAVAILABLE`.
- [ ] `curl -X PATCH localhost:3000/loans/<loanId>/return` returns 200 + a `LoanResponse` with non-nil `returnedAt`. The copy's status flips back to `AVAILABLE`.
- [ ] `curl -X POST localhost:3000/reservations -d '{"memberId":"<id>","bookId":"<bookId>"}'` returns 201 + a `ReservationResponse`.
- [ ] `curl -X PATCH localhost:3000/members/<id>/suspend` (Phase 2), then `curl -X POST localhost:3000/loans -d '{...}'` returns 409 + `{"error":"member_ineligible","message":"Member <id> is not eligible to borrow: SUSPENDED",...}`.
- [ ] `curl -X PATCH localhost:3000/copies/<copyId>/unavailable` (Phase 2), then `curl -X POST localhost:3000/loans -d '{...}'` for that copyId returns 409 + `{"error":"copy_unavailable",...}`.
- [ ] An unknown `copyId` in `POST /loans` returns 404 + `{"error":"copy_not_found"}` (cross-module catalog error).
- [ ] An unknown `loanId` in `PATCH /loans/<loanId>/return` returns 404 + `{"error":"loan_not_found"}`.

### Atomicity invariants

- [ ] `InMemoryTransactionalContext` rolls back on work-function error: write does NOT apply to live state, staged event is NOT published.
- [ ] `InMemoryTransactionalContext` commits on work-function success: writes apply in stage order, then events publish in stage order.
- [ ] `InMemoryTransactionalContext` continues publishing remaining events when one event's publish fails; the bus failure is logged at `error` level; `Run` returns nil.
- [ ] `BunTransactionalContext` rolls back on work-function error: row does NOT exist in Postgres, staged event is NOT published.
- [ ] `BunTransactionalContext` rolls back on stage-closure error: row does NOT exist in Postgres (even though the INSERT ran inside the tx, the rollback discards it), staged event is NOT published.
- [ ] `BunTransactionalContext` commits on work + stage success: row exists in Postgres, events publish in stage order.
- [ ] `BunTransactionalContext` continues publishing remaining events when one event's publish fails; the bus failure is logged at `error` level; `Run` returns nil.
- [ ] `lending.Borrow` exhibits the canonical post-commit ordering: own-write commits → `LoanOpened` publishes (during commit) → `catalog.MarkCopyUnavailable` runs (after commit). When the catalog mutation fails, the loan is persisted and `LoanOpened` is on the bus but the copy remains AVAILABLE — the documented inconsistency.
- [ ] `lending.ReturnLoan` exhibits the inverted ordering: own-write commits → `catalog.MarkCopyAvailable` runs (after commit) → `LoanReturned` publishes (after catalog mutation, via direct `bus.Publish`, NOT `StageEvent`). When `bus.Publish` of `LoanReturned` fails, the loan is persisted and the copy is AVAILABLE but the event is lost — the documented inconsistency.
- [ ] `lending.Reserve` exhibits the pure-staged-event variant: own-write commits → `ReservationQueued` publishes (during commit). No cross-module mutation.
- [ ] `LoanOpened`, `LoanReturned`, and `ReservationQueued` are all observable on the in-memory bus during integration tests (verified via `wired.Bus.Subscribe(...)` in the crucial-path test).
- [ ] `LoanReturned` is published on the bus but does NOT trigger any auto-loan — no consumer is registered in Phase 3.

### Test suite

- [ ] `task test` (unit, no build tags) is green and completes in well under 1 second (target: under 700 ms). Includes: tx in-memory atomicity tests, lending in-memory repo tests, lending facade tests (~25 scenarios across Slices 4/5/6), lending HTTP handler tests, plus all Phase-1/2 unit tests.
- [ ] `task test:integration` is green and completes in under 90 seconds on a developer laptop including testcontainers cold start. Includes: tx bun atomicity tests, lending bun-repo contract tests, lending crucial-path integration test, plus all Phase-1/2 integration tests.

### Quality + hygiene

- [ ] `task fmt` (`gofmt -w .` + `go mod tidy`) and `task lint` (`go vet ./...`) pass with zero output.
- [ ] No new third-party direct dep beyond what Phase 2 already pulled in. Phase 3 introduces no new library dependency — bun is already there, events package is internal, slog is stdlib, accesscontrol/catalog/membership/shared/* are internal.
- [ ] No `init()` function in any Phase-3 file.
- [ ] No file under `internal/lending/` imports a forbidden module per `.claude/BOUNDARIES.md` (forbidden: `internal/fines`, `internal/categories`, `internal/chat`, `internal/shared/bookcache`, `internal/shared/isbngateway`, `internal/shared/chatgateway`, `internal/shared/filestorage`).
- [ ] No file under `internal/shared/tx/` imports a business module (`internal/accesscontrol`, `internal/catalog`, `internal/membership`, `internal/lending`).
- [ ] Every TS scenario in `lending.facade.spec.ts` that covers `borrow` / `reserve` / `returnLoan` (and is NOT a saga consumer scenario or a listing scenario) has a Go counterpart in `internal/lending/facade_test.go`. Verified by reading the two files side by side.
- [ ] `.claude/BOUNDARIES.md` reflects Phase 3's new module: `lending` depends on `accesscontrol`, `catalog`, `membership`, `shared/events`, `shared/tx`, `shared/db`, `shared/http`. `shared/tx` depends on `shared/events` (and `shared/db` only for the bun impl via the `*bun.DB` constructor parameter — note this exception explicitly, similar to Phase 2's `shared/bookcache → catalog` exception).

## Open Questions

These are decisions a developer should make before Slice 1 begins, OR record explicitly as accepted defaults. All have a recommended default aligned with discovery + source fidelity; flag any disagreement before slicing.

1. **Read-only listing methods in Phase 3 vs Phase 4.** The TS source's `lending.facade.ts` exposes `ListOverdueLoans`, `ListOverdueLoansWithTitles`, `ListLoansFor`, `ListActiveLoansWithQueuedReservations`. Recommended default: **defer to Phase 4** where fines + the saga consumer actually call them. Adding them to Phase 3 costs ~5 facade methods, ~5 handlers, ~10 facade-test scenarios for half a day. Phase 3 stays focused on the tx substrate + write flows. If kept, slot them into Slice 7 as additional handler scenarios; the facade methods land in a new Slice 4.5 between Slices 4 and 5. **Flag for confirmation**: if the developer wants them, add the work to Slice 7's scope.
2. **`ReservationNotFoundError` declared but unused in Phase 3.** Recommended default: **declare it now** so the error registry has a stable entry from Phase 3 onward; Phase 4's `Cancel` and the saga consumer use it. The alternative is to declare it in Phase 4 with the saga, but the error registry surface staying stable across phases reads better. **Flag if you'd rather defer.**
3. **`Reserve` validates BookId via catalog.** TS source does not pre-validate; the auto-loan consumer fails-soft on unknown books. Recommended default: **match TS** (no pre-validation). Alternative: add `catalog.FindBookById` to the catalog facade (Phase 2 doesn't expose it; only `FindBookByIsbn`) and call it from `Reserve`. The cost is a new catalog method, a new test, and a deviation from the source. **Flag if you want pre-validation.**
4. **`ReturnLoan` idempotency.** TS source does NOT check whether the loan is already returned — calling it twice writes a second `returnedAt` and publishes a second `LoanReturned`. Recommended default: **match TS** (no idempotency check). Alternative: short-circuit when `loan.ReturnedAt != nil`, return `(LoanDto, nil)` without writing or publishing. **Flag if you want idempotency.**
5. **Handler-level authorization rejection test for `POST /loans`.** The production handler hard-codes `Role: RoleMember` in its demo-auth shortcut. A test that exercises the 403-unauthorized-role path through the HTTP layer requires test-only handler support for varying roles. Recommended default: **drop the HTTP-layer authorization rejection test**; the facade-level test (Slice 4) already locks the behaviour and the HTTP layer is a thin pass-through. **Flag if you want HTTP-layer coverage of authorization.**
6. **Should `app.Wired` expose `Bus`?** Phase 3 needs it for the crucial-path test's `LoanReturned` subscription assertion. Recommended default: **yes** — extend `app.Wired` with `Bus events.EventBus`. The alternative is to inject a capturing-bus decorator via `app.Deps` at boot time, which complicates the deps shape. **Recommended.**
7. **In-memory `Stage` execution timing.** The TS source runs staged closures during commit. The Go in-memory impl also defers to commit (per Slice 1 AC). An alternative is to run them immediately during `Stage(apply)` to match the bun impl's timing — but then the in-memory impl can't roll back (since the live-state write already happened). Recommended default: **defer to commit** (matches TS source and gives genuine rollback semantics in-memory). The asymmetry with bun's "stage runs immediately" is documented in the interface doc-comment. **Locked.**

## Phase 3 → Phase 4 Handoff

When Phase 4 starts, the spec-builder for that phase can assume:

1. The dev loop works — `task up`, `task migrate:apply`, `task run`, `task test`, `task test:integration` are stable. Three business migrations are in `migrations/`.
2. `internal/shared/tx.TransactionalContext` is the canonical transactional substrate. Both `InMemoryTransactionalContext` and `BunTransactionalContext` ship and are tested for the atomicity invariants Phase 3's DoD names. Phase 4's saga consumer constructs a fresh `TransactionalContext` per claim/un-fulfil operation via the same `TransactionalContextFactory` injected into the lending module.
3. `internal/shared/tx.TxFromContext(ctx) (bun.IDB, bool)` is the canonical seam for bun repositories to resolve the active tx handle. Phase 4's fines bun repo follows the same pattern.
4. `internal/lending.Facade` exposes `Borrow`, `Reserve`, `ReturnLoan`. Phase 4's auto-loan saga consumer subscribes to `LoanReturned` and calls `lending.Borrow` directly (re-using the same authorization, eligibility, and tx semantics).
5. `internal/lending.LoanRepository` exposes `ListLoansForMember`, `ListLoansForBook`, `ListLoans`. Phase 4's overdue-loan flow (fines) uses `ListLoans` + a date filter (or Phase 4 adds a `ListOverdueLoans` method to the facade if Open Question 1 was answered "defer to Phase 4" — yes, it was).
6. `internal/lending.ReservationRepository` exposes `ListReservationsForBook`, `ListReservationsForMember`, `PendingReservationCountForBook`. Phase 4's auto-loan saga uses `ListReservationsForBook` to pick the earliest queued reservation when a `LoanReturned` event fires.
7. The events `LoanOpened`, `LoanReturned`, `ReservationQueued` are declared in `internal/lending/types.go` implementing `events.DomainEvent`. Phase 4 ADDS `ReservationFulfilled`, `ReservationUnfulfilled`, `AutoLoanOpened`, `AutoLoanFailed` to the same file.
8. `internal/lending/Facade.ReturnLoan` publishes `LoanReturned` via direct `bus.Publish` AFTER the catalog mark-available runs. Phase 4's saga consumer relies on this ordering — when it receives `LoanReturned`, the copy is observed as AVAILABLE and an immediate `lending.Borrow(reservingMember, copy)` succeeds without retry.
9. `internal/app.Wire` returns a `Wired` carrying `Router`, `DB`, `Bus`, `CatalogFacade`, `MembershipFacade`, `LendingFacade`, `Close`. Phase 4 extends with `FinesFacade` and `CategoriesFacade`.
10. `internal/app/wiring.go`'s `buildDomainErrorRegistry` registers eleven Phase-1/2/3 domain errors (`unauthorized_role`, `unknown_action`, `book_not_found`, `copy_not_found`, `duplicate_isbn`, `invalid_book`, `invalid_copy`, `member_not_found`, `duplicate_email`, `invalid_member`, `loan_not_found`, `reservation_not_found`, `copy_unavailable`, `member_ineligible`, `invalid_borrow`, `invalid_reserve`, `invalid_return`). Phase 4 extends with `fine_not_found`, `invalid_fine`, `category_not_found`, `duplicate_category`, etc.
11. The per-module file convention is now battle-tested across three modules (catalog + membership + lending). Phase 4's `fines` and `categories` modules follow the same template.
12. The "no-mocks, in-memory doubles + spec-local Throwing-Once decorator wrappers" discipline is locked. Phase 4's saga consumer tests will declare unexported `throwingOnceLendingFacade`, `claimingReservationRepository`, etc., inside the saga consumer test file.
13. The `accesscontrol` facade's runtime authorization path is now exercised in production code (lending.Borrow). Phase 4's fines + categories MAY also call `accessControl.Authorize` — `accesscontrol.policy.go` declares the policy entries; Phase 4 just adds rows for the new actions.
14. The post-commit publish pattern is documented in the `TransactionalContext` doc comment AND in each of `Borrow` / `Reserve` / `ReturnLoan`'s doc comments. Phase 4's saga consumer's `attemptAutoLoan` flow follows the same pattern: own tx → stage `ReservationFulfilled` → on success `lending.Borrow` runs outside the tx; on failure a SECOND tx un-stages with `ReservationUnfulfilled` + fires `AutoLoanFailed` outside.

No Phase 3 file or AC needs to change to enable Phase 4.

[ ] Reviewed
