# Spec: improving-tdd-demo Go Port — Phase 4 (Auto-Loan Saga + Fines + Categories)

## Overview

The architectural punchline lands in this phase. Phase 3 left `LoanReturned` on the bus without a subscriber; Phase 4 wires the `AutoLoanOnReturnConsumer` to it and ships the saga choreography that makes a returned copy flow immediately into the next pending reserver's hands. Alongside the saga, Phase 4 ships the two remaining business modules: `fines` (assess + pay + auto-suspend over a configurable threshold) and `categories` (a curated taxonomy independent of every other module).

By the end of Phase 4 a `POST /loans` followed by a `POST /reservations` (for the same book by a different member) followed by `PATCH /loans/{id}/return` results in a new loan opening automatically for the reserver — no second `POST /loans` call. The `LoanReturned → ReservationFulfilled → LoanOpened → AutoLoanOpened` event chain is observable on the bus. On failure (e.g. the borrow rejects because the second member is suspended) the consumer un-fulfils the claim and emits `AutoLoanFailed` — leaving the reserver in queue for the next return.

The architectural conviction this phase locks in: **a saga consumer owns its own `TransactionalContextFactory`; each saga step (claim, un-fulfil) bundles ONE module-local write with ONE staged event inside its own tx; the cross-module effect (`lending.Borrow`) runs OUTSIDE the tx between the claim and the `AutoLoanOpened` publish; `AutoLoanFailed` is published OUTSIDE any tx (so even when the un-fulfil tx rolls back, the failure remains observable); per-aggregate serialisation (a `sync.Mutex` map keyed by `BookId`) prevents two concurrent returns of the same book from both claim-writing the same head-of-queue reservation; saga consumers are started and stopped EXPLICITLY by the composition root — no goroutines start from constructors**. Fines and categories ship as straightforward business modules using the locked Phase 1–3 template; they exist to round out the demo's surface area and prove the per-module template scales to the full source.

## Why

Phase 4 unlocks:

- The saga substrate Phase 3 prepared. `internal/shared/tx.TransactionalContext` and the post-commit publish pattern were built so this consumer could ship without renegotiating either. Phase 4's `AutoLoanOnReturnConsumer` uses the same `TransactionalContextFactory` shape the lending facade injects, with one extra atomicity claim: claim + `ReservationFulfilled` are bundled inside one tx; un-fulfil + `ReservationUnfulfilled` are bundled inside a SEPARATE tx; `AutoLoanFailed` is fired outside both. This is the architectural payoff the spec has been building towards since Phase 3 — the consumer atomicity tests (Slice 2 of this phase) are the canonical demonstration that the tx substrate "earns its keep" exactly here.
- Per-aggregate serialisation. The consumer's `sync.Mutex` map keyed by `BookId` is the single-node equivalent of the DB unique constraint Phase 4 deliberately does NOT add. The mutex tightens the race (two concurrent returns of different copies of the same book serialise their consumer runs, so the head-of-queue reservation is claim-written before the second handler observes the pending list) but does not eliminate it; the comment in `auto_loan_on_return.go` documents the gap and points at the unique-constraint fix Phase 5+ MAY revisit.
- Saga lifecycle. The consumer exposes `Start(ctx) error` / `Stop(ctx) error` methods called explicitly by the composition root after `Wire` returns and before graceful shutdown. This locks in the convention D26 named ("explicit Start/Stop on module structs; no `init()` for module wiring") and gives Phase 5's chat module a template to follow when it lands its SSE handler's connection registry.
- The `fines` module — the last facade that READS across multiple modules synchronously. Fines reads from `lending` (`ListLoansFor`, `ListOverdueLoans`) and `membership` (`FindMember`, `Suspend`); it writes its own `fines` table; it publishes `FineAssessed` and `MemberAutoSuspended`. The cross-module reads happen BEFORE the (per-fine) writes, matching the Phase-3 rule. Auto-suspend is the only cross-module WRITE the fines facade performs — it calls `membership.Suspend` AFTER its own per-fine tx commits, matching the post-commit cross-module-write rule.
- The `categories` module — the curated taxonomy. Categories has zero cross-module dependencies in its business logic; it exists to prove the per-module template is reusable for a "leaf" module that just exposes CRUD. Shipping it after the saga is intentional: a developer reading the spec sees the saga (the hard part) first and the leaf module (the easy part) last — the contrast makes the saga's architectural complexity feel earned, not gratuitous.
- The cross-module crucial-path tests that exercise the full chain. Slice 6 ships a `test/crucial_path/saga_integration_test.go` that boots the full app via `test/support.BootApp`, drives `POST /loans → POST /reservations → PATCH /loans/{id}/return` end-to-end against real Postgres + Redis, and asserts the auto-loan-of-reserver chain works through the HTTP surface. This is the integration test that proves Phase 4's Done criteria.

Without Phase 4, the Go port is "lending without the saga" — a sophisticated cross-module facade demo, but not yet the architectural artifact the TS source is. With Phase 4 done, Phase 5 ships only chat + docs + the skill — surface-area work, no new architectural substrate.

## In Scope

- `internal/lending/auto_loan_on_return.go` exporting `AutoLoanOnReturnConsumer struct` + `NewAutoLoanOnReturnConsumer(deps AutoLoanOnReturnConsumerDeps) *AutoLoanOnReturnConsumer` + `Start(ctx context.Context) error` + `Stop(ctx context.Context) error`. The consumer subscribes to `LoanReturned` via `bus.Subscribe("LoanReturned", handle)` and unsubscribes via the returned `events.Unsubscribe` closure on `Stop`. Per-`BookId` serialisation via a `sync.Mutex` map (lazily-allocated mutex per book, dropped after the handler returns if no waiter is queued). The consumer owns its own `TransactionalContextFactory`; same factory instance the lending facade was wired with.
- `internal/lending/auto_loan_on_return_test.go` (unit, no build tag) porting every applicable scenario from `auto-loan-on-return.consumer.spec.ts`: happy path (head-of-queue reserver receives the auto-loan); empty-queue no-op; eligibility-cascade (skip suspended reservations, stop at first eligible); start/stop lifecycle; `AutoLoanOpened` payload fidelity; failure-policy (borrow throws → un-fulfil claim + `AutoLoanFailed`); failure-policy when un-fulfil ALSO throws (`AutoLoanFailed` still fires with the ORIGINAL borrow error); claim-first concurrency under the per-book mutex; claim-tx rollback (the canonical atomicity AC); un-fulfil-tx rollback (the second canonical atomicity AC); `AutoLoanFailed` fires OUTSIDE the un-fulfil tx (the third canonical atomicity AC); per-book mutex prevents double-fulfilment under concurrent returns of the same book (the fourth canonical atomicity AC).
- `internal/lending/types.go` extended with four new event types: `ReservationFulfilled{ReservationId, MemberId, BookId, FulfilledAt}`, `ReservationUnfulfilled{ReservationId, MemberId, BookId, UnfulfilledAt}`, `AutoLoanOpened{BookId, LoanId, MemberId, ReservationId, OpenedAt}`, `AutoLoanFailed{BookId, ReservationId, MemberId, Reason, FailedAt}`. Each implements `events.DomainEvent` via a `Type() string` value-receiver method returning the source-fidelity string ("ReservationFulfilled", "ReservationUnfulfilled", "AutoLoanOpened", "AutoLoanFailed"). Field order matches TS source 1:1.
- `internal/lending/reservation_repository.go` extended with `ListPendingReservationsForBook(ctx context.Context, bookId catalog.BookId) ([]ReservationDto, error)` — returns reservations where `BookId == bookId AND FulfilledAt == nil`, ordered by `reserved_at ASC` (queue order). In-memory + bun impls extended. **Note**: Phase 3 declared `PendingReservationCountForBook` but not `ListPendingReservationsForBook`; the listing variant is new in Phase 4 because the saga consumer needs the actual reservations, not just a count.
- `internal/lending/facade.go` extended with **two listing methods** the saga + fines need: `ListLoansFor(ctx context.Context, memberId membership.MemberId) ([]LoanDto, error)` (delegates to `loans.ListLoansForMember`) and `ListOverdueLoans(ctx context.Context, now time.Time) ([]LoanDto, error)` (filters `loans.ListLoans` by `returnedAt == nil AND dueDate.Before(now)`). Both are read-only; no tx; no events. **Open Question 1** flagged the option of deferring these to Phase 4 from Phase 3 — confirmed deferred; they land here.
- `internal/fines/` module: every file the per-module template lists — `facade.go`, `types.go`, `schema.go` (minimal; fines accepts memberId/fineId path params only), `repository.go`, `in_memory_repository.go`, `bun_repository.go`, `sample_data.go`, `configuration.go`, `module.go`, `facade_test.go`, `in_memory_repository_test.go`, `bun_repository_test.go` (`//go:build integration`), plus `http/` subpackage with `dto.go`, `handlers.go`, `mapping.go`, `handlers_test.go`.
- `internal/categories/` module: same template — `facade.go`, `types.go`, `schema.go` (parses `NewCategoryDto{Name}`), `repository.go`, `in_memory_repository.go`, `bun_repository.go`, `sample_data.go`, `configuration.go`, `module.go`, `facade_test.go`, `in_memory_repository_test.go`, `bun_repository_test.go` (`//go:build integration`), `http/` subpackage.
- `migrations/0004_fines.sql` creating `fines` (`fine_id UUID PK`, `member_id UUID NOT NULL`, `loan_id UUID NOT NULL`, `amount_cents BIGINT NOT NULL`, `assessed_at TIMESTAMPTZ NOT NULL`, `paid_at TIMESTAMPTZ NULL`). No FK references to `members` / `loans` (cross-module FKs forbidden). `atlas.sum` regenerated.
- `migrations/0005_categories.sql` creating `categories` (`category_id UUID PK`, `name TEXT NOT NULL UNIQUE`, `created_at TIMESTAMPTZ NOT NULL`). UNIQUE on `name` enforces the duplicate-category invariant at the DB layer (matches TS source's `DuplicateCategoryError`). `atlas.sum` regenerated.
- `internal/app/wiring.go` extended so `Wire` constructs the fines + categories facades alongside lending; registers Phase-4 domain errors; mounts the new HTTP routes via `finesModule.Wire(r, deps)` and `categoriesModule.Wire(r, deps)`; constructs and **explicitly starts** the `AutoLoanOnReturnConsumer` after route mounting; extends `app.Wired` with `FinesFacade *fines.Facade`, `CategoriesFacade *categories.Facade`, `AutoLoanConsumer *lending.AutoLoanOnReturnConsumer`, and a `Close` function that calls `consumer.Stop(ctx)` before closing the DB.
- `test/crucial_path/fines_integration_test.go` (`//go:build integration`) exercising `POST /members/{memberId}/fines/assessments`, `POST /fines/batch/process`, `GET /members/{memberId}/fines`, `GET /fines/{fineId}`, `PATCH /fines/{fineId}/paid` end-to-end against testcontainers Postgres.
- `test/crucial_path/categories_integration_test.go` (`//go:build integration`) exercising `POST /categories`, `GET /categories?startsWith=...`, `GET /categories/{id}`.
- `test/crucial_path/saga_integration_test.go` (`//go:build integration`) exercising the full chain: `POST /loans → POST /reservations → PATCH /loans/{id}/return → assert reservation fulfilled → assert new loan exists for reserver → assert copy UNAVAILABLE → assert `AutoLoanOpened` on the bus`.
- `.http/fines.http`, `.http/categories.http`, `.http/saga.http` activating Phase-4 endpoints with sample request bodies.
- `FinesConfig` struct with `DailyRateCents int64` + `SuspensionThresholdCents int64` fields. Defaults: daily rate = 25 cents (matches TS source's `DAILY_RATE_CENTS = 25`); suspension threshold = 1000 cents (matches TS source's `SUSPENSION_THRESHOLD_CENTS = 1000`). Loaded via `viper` from env vars `FINES_DAILY_RATE_CENTS` and `FINES_SUSPENSION_THRESHOLD_CENTS` with the defaults baked in.

## Out of Scope (deferred to later phases per discovery doc)

- A DB unique constraint on `(book_id, member_id) WHERE fulfilled_at IS NULL` for reservations. The TS source comments explicitly that this is "the real fix" for the claim-race, deliberately deferred. The Go port matches: the `sync.Mutex` map is the single-node equivalent; the constraint is a Phase-5+ option (or never). The consumer's doc comment names this gap.
- A durable outbox / write-ahead-log for events. Same known gap Phase 3 documented; Phase 4 inherits it.
- Distributed tracing of saga steps. The slog structured fields the consumer emits per step (`book_id`, `reservation_id`, `member_id`, `step`) are the entire observability story for Phase 4. Phase 5+ MAY revisit with an OpenTelemetry exporter.
- Background scheduling of `POST /fines/batch/process`. The TS source ships the endpoint but does not auto-trigger it on a schedule; an operator (or a cron) hits it. The Go port matches — no goroutine timer, no `time.Tick`, no `cron` library. Phase 5+ option.
- `chat` module — deferred to Phase 5 per discovery.
- HTTP-layer authentication for fines/categories. Both modules' handlers follow the same demo-auth shortcut pattern Phase 2/3 established: handlers extract memberId from URL params or body and operate without a real auth middleware. Documented in each module's `handlers.go` doc comment.
- `accesscontrol.Authorize` calls inside the fines + categories facades. The TS source does NOT authorize on fines/categories at the facade layer (they're admin endpoints in a real deployment; the demo doesn't model the admin role). The Go port matches — no `accessControl.Authorize` calls in `fines.Facade` or `categories.Facade`. **Recorded as Open Question 4**; recommended default: match TS source (no authorization at the facade layer).
- Fine-paid event (`FinePaid`). The TS source does NOT publish an event when a fine is paid — `payFine` is a state mutation that returns the updated DTO. The Go port matches; no `FinePaid` event.
- `MemberAutoSuspended` consumers. The event is published but no Phase-4 module subscribes to it (no notifications, no audit log handler). The bus carries the event and any downstream wiring is Phase-5+ work.
- Auto-suspend reversal. The TS source has no "auto-unsuspend on fine paid" flow; once `MemberAutoSuspended` fires, the suspension persists until a manual `PATCH /members/{id}/reinstate` (Phase-2 endpoint) clears it. The Go port matches.

## Slices

Phase 4 ships in **six slices**, ordered to land the saga substrate first (consumer + atomicity tests), then the two business modules (fines + categories), then the cross-module integration tests last. Within the saga slices, the outside-in order is: consumer code → consumer tests → atomicity tests (consumer code Slice 1; consumer behavioural tests in the same slice; consumer atomicity tests Slice 2 split out for emphasis since they're the architectural payoff). The two business modules ship vertical (types → repo → facade → HTTP → tests → wiring) inside their own slices (Slices 3 + 4 for fines; Slice 5 for categories). Slice 6 is the cross-module integration test that proves the whole chain.

Each slice ends green: `go build`, unit tests, integration tests where the slice ships an integration test.

---

### Slice 1: `AutoLoanOnReturnConsumer` — code + behavioural tests

Brings the repository to "`LoanReturned` triggers the auto-loan flow: claim the head-of-queue eligible reservation in a tx, call `lending.Borrow` outside the tx, publish `AutoLoanOpened` on success or un-fulfil + `AutoLoanFailed` on failure." Slice 1 ships the consumer code, the new events, the new reservation-repo listing method, the two new lending-facade listing methods, AND the behavioural test scenarios (happy path, eligibility cascade, lifecycle, failure policy, AutoLoanOpened payload). Slice 2 carves out the atomicity tests — they share scaffolding but warrant their own block.

#### Acceptance Criteria — new event types

- [ ] `internal/lending/types.go` declares `type ReservationFulfilled struct { ReservationId ReservationId; MemberId membership.MemberId; BookId catalog.BookId; FulfilledAt time.Time }` implementing `events.DomainEvent` via `func (e ReservationFulfilled) Type() string { return "ReservationFulfilled" }`. Value receiver, matches Phase-3 convention.
- [ ] `types.go` declares `type ReservationUnfulfilled struct { ReservationId ReservationId; MemberId membership.MemberId; BookId catalog.BookId; UnfulfilledAt time.Time }` implementing `Type() string { return "ReservationUnfulfilled" }`.
- [ ] `types.go` declares `type AutoLoanOpened struct { BookId catalog.BookId; LoanId LoanId; MemberId membership.MemberId; ReservationId ReservationId; OpenedAt time.Time }` implementing `Type() string { return "AutoLoanOpened" }`. Field order matches TS source 1:1 (`bookId, loanId, memberId, reservationId, openedAt`).
- [ ] `types.go` declares `type AutoLoanFailed struct { BookId catalog.BookId; ReservationId ReservationId; MemberId membership.MemberId; Reason string; FailedAt time.Time }` implementing `Type() string { return "AutoLoanFailed" }`. Field order matches TS source 1:1.
- [ ] The four new event types are exported from `internal/lending` (matching `LoanOpened`/`LoanReturned`/`ReservationQueued` from Phase 3).

#### Acceptance Criteria — new reservation-repository listing method

- [ ] `internal/lending/reservation_repository.go` extends `ReservationRepository` with `ListPendingReservationsForBook(ctx context.Context, bookId catalog.BookId) ([]ReservationDto, error)`.
- [ ] Returns reservations where `BookId == bookId AND FulfilledAt == nil`, **ordered by `ReservedAt` ascending** (queue order — the head-of-queue is the earliest reservation).
- [ ] `internal/lending/in_memory_reservation_repository.go` adds the method: filter the map's values, defensive-copy each, sort by `ReservedAt`, return.
- [ ] `internal/lending/bun_reservation_repository.go` adds the method: `db.NewSelect().Model(&rows).Where("book_id = ? AND fulfilled_at IS NULL", bookId).Order("reserved_at ASC").Scan(ctx)`. The read goes through the base `*bun.DB`, not the tx (reads bypass the tx substrate per Phase 3 convention).
- [ ] `internal/lending/in_memory_reservation_repository_test.go` extends with at least three scenarios: (a) empty book has empty list; (b) a book with one pending + one fulfilled reservation returns only the pending; (c) queue order is preserved — three pending reservations with `ReservedAt` `t1 < t2 < t3` return in order `[r1, r2, r3]`.
- [ ] `internal/lending/bun_reservation_repository_test.go` extends with the same three scenarios against testcontainers Postgres.

#### Acceptance Criteria — new lending-facade listing methods

- [ ] `internal/lending/facade.go` exports `Facade.ListLoansFor(ctx context.Context, memberId membership.MemberId) ([]LoanDto, error)`. Delegates to `f.loans.ListLoansForMember(ctx, memberId)`. No tx; no events; pure delegation.
- [ ] `Facade.ListOverdueLoans(ctx context.Context, now time.Time) ([]LoanDto, error)`. Calls `f.loans.ListLoans(ctx)`, filters in Go to `returnedAt == nil AND dueDate.Before(now)`, returns the filtered slice.
- [ ] Both methods' doc comments name them as read-only listings consumed by Phase-4's fines module + auto-loan consumer.
- [ ] `Facade.ListLoansFor` and `Facade.ListOverdueLoans` are tested in `internal/lending/facade_test.go` via two new scenarios: (a) `ListLoansFor` returns only the named member's loans; (b) `ListOverdueLoans(now)` returns only loans with `ReturnedAt == nil AND DueDate < now` — seed three loans (one returned, one not-yet-due, one overdue) and assert only the overdue one comes back.

#### Acceptance Criteria — consumer struct + lifecycle

- [ ] `internal/lending/auto_loan_on_return.go` exports `AutoLoanOnReturnConsumer struct` with unexported fields: `bus events.EventBus`, `membership *membership.Facade`, `reservations ReservationRepository`, `lending *Facade`, `txFactory tx.TransactionalContextFactory`, `clock func() time.Time`, `logger *slog.Logger`, `bookLocks map[catalog.BookId]*sync.Mutex`, `bookLocksMu sync.Mutex` (the mutex guarding the map itself), `unsubscribe events.Unsubscribe`, `started bool`.
- [ ] `type AutoLoanOnReturnConsumerDeps struct { Bus events.EventBus; Membership *membership.Facade; Reservations ReservationRepository; Lending *Facade; TxFactory tx.TransactionalContextFactory; Clock func() time.Time; Logger *slog.Logger }` exported. `Clock` and `Logger` MAY be nil — the constructor substitutes `time.Now` and `slog.New(slog.DiscardHandler)` respectively.
- [ ] `NewAutoLoanOnReturnConsumer(deps AutoLoanOnReturnConsumerDeps) *AutoLoanOnReturnConsumer` returns a fresh consumer with empty `bookLocks` map, `unsubscribe = nil`, `started = false`. Does NOT subscribe — `Start` does.
- [ ] `(*AutoLoanOnReturnConsumer).Start(ctx context.Context) error`: if `started`, return nil (idempotent). Otherwise `c.unsubscribe = c.bus.Subscribe("LoanReturned", c.handleLoanReturned)`; set `started = true`; return nil. The `ctx` parameter is unused but matches the convention.
- [ ] `(*AutoLoanOnReturnConsumer).Stop(ctx context.Context) error`: if `!started`, return nil. Otherwise call `c.unsubscribe()`, set `c.unsubscribe = nil`, `started = false`, return nil. Idempotent — a second `Stop` after a first is a no-op.
- [ ] `handleLoanReturned(ctx context.Context, evt events.DomainEvent) error` is the bus handler. Type-asserts `evt.(LoanReturned)`; if the assertion fails, log a `warn` and return nil. Acquires the per-book lock for `evt.BookId`, calls `processReturn(ctx, evt)`, releases the lock. Returns nil unconditionally — saga consumers swallow their own errors and never propagate them to the bus (matches the TS source's `try/catch` around the borrow flow).

#### Acceptance Criteria — per-book mutex serialisation

- [ ] The consumer maintains `bookLocks map[catalog.BookId]*sync.Mutex` guarded by `bookLocksMu sync.Mutex`. Lazy allocation: on first request for a `BookId`, allocate a new `*sync.Mutex`, store it in the map. Subsequent requests for the same `BookId` reuse the stored mutex.
- [ ] `acquireBookLock(bookId catalog.BookId) *sync.Mutex`: takes `bookLocksMu`; looks up `bookId`; if absent, creates a new `*sync.Mutex` and stores; returns the mutex; releases `bookLocksMu`. The caller `Lock()`s the returned mutex.
- [ ] After the handler returns, the mutex is `Unlock()`ed via `defer`. The mutex itself stays in the map — Phase 4 does NOT prune unused mutexes from the map (the memory cost is one `sync.Mutex` per ever-seen book, which is bounded by the catalog size; Phase 5+ MAY revisit).
- [ ] Two concurrent `handleLoanReturned` calls for the SAME `BookId` serialise (the second blocks on the first's mutex). Two concurrent calls for DIFFERENT `BookId`s run in parallel (different mutexes).
- [ ] The mutex doc comment names the rule and the gap: "Per-book serialisation prevents two concurrent returns of copies of the SAME book from both claim-writing the same head-of-queue reservation in the JS-event-loop-equivalent race the TS source documents. The real fix is a DB unique constraint on (book_id, member_id) WHERE fulfilled_at IS NULL — deliberately deferred."

#### Acceptance Criteria — `processReturn` + `attemptAutoLoan` flow

- [ ] `processReturn(ctx context.Context, evt LoanReturned) error`: calls `c.reservations.ListPendingReservationsForBook(ctx, evt.BookId)`; on error, log and return nil (saga swallows). For each reservation in queue order: call `c.membership.CheckEligibility(ctx, reservation.MemberId)`; if not eligible, continue to the next reservation. On the first eligible reservation, call `c.attemptAutoLoan(ctx, reservation, evt.CopyId)` and RETURN (do not iterate past the first attempt — matches TS source's `return` after one attempt).
- [ ] `attemptAutoLoan(ctx, reservation, copyId)`: call `c.claimReservation(ctx, reservation)`; on claim-tx error, log at `error` level with structured fields (`book_id`, `reservation_id`, `member_id`, `error`) and return — no further work, no `AutoLoanFailed` (the claim itself failed; nothing to un-fulfil). On success, call `c.lending.Borrow(ctx, authUser, copyId)` where `authUser = accesscontrol.AuthUser{MemberID: string(reservation.MemberId), Role: accesscontrol.RoleMember}`. If `Borrow` returns an error, call `c.tryUnfulfilClaim(ctx, claimed)`, then `c.publishAutoLoanFailed(ctx, claimed, err.Error())`, then return. If `Borrow` succeeds, call `c.publishAutoLoanOpened(ctx, loan, claimed)`.
- [ ] `claimReservation(ctx, reservation) (ReservationDto, error)`: build the claimed DTO `claimed := reservation; claimed.FulfilledAt = ptr(c.clock())`. Construct a fresh `txc := c.txFactory()`. Inside `txc.Run(ctx, func(ctx) error { ... })`: call `c.reservations.SaveReservation(ctx, claimed, txc)`; `txc.StageEvent(ReservationFulfilled{ReservationId: claimed.ReservationId, MemberId: claimed.MemberId, BookId: claimed.BookId, FulfilledAt: *claimed.FulfilledAt})`. On `Run` returning nil, return `(claimed, nil)`. On `Run` error, return `(ReservationDto{}, err)` — the staged event was suppressed by the tx rollback, no `ReservationFulfilled` is on the bus.
- [ ] `tryUnfulfilClaim(ctx, claimed)`: build the unfulfilled DTO `unfulfilled := claimed; unfulfilled.FulfilledAt = nil`. Construct a fresh `txc := c.txFactory()`. Inside `txc.Run(ctx, func(ctx) error { ... })`: call `c.reservations.SaveReservation(ctx, unfulfilled, txc)`; `txc.StageEvent(ReservationUnfulfilled{ReservationId: unfulfilled.ReservationId, MemberId: unfulfilled.MemberId, BookId: unfulfilled.BookId, UnfulfilledAt: c.clock()})`. On `Run` error, log at `error` level and continue — DO NOT return early. `tryUnfulfilClaim` returns no error; its failure is recoverable in the sense that `AutoLoanFailed` still fires regardless. The reservation is left in an inconsistent state (fulfilled-but-no-loan) and the operator can investigate via the logs.
- [ ] `publishAutoLoanOpened(ctx, loan, reservation)`: build `AutoLoanOpened{BookId: loan.BookId, LoanId: loan.LoanId, MemberId: loan.MemberId, ReservationId: reservation.ReservationId, OpenedAt: c.clock()}`; call `c.bus.Publish(ctx, evt)`; log bus errors at `error` level but do NOT surface them. **Note**: `AutoLoanOpened` is published via direct `bus.Publish`, NOT via `txc.StageEvent`, because it fires AFTER `lending.Borrow` returns (which is OUTSIDE any saga tx).
- [ ] `publishAutoLoanFailed(ctx, reservation, reason)`: build `AutoLoanFailed{BookId: reservation.BookId, ReservationId: reservation.ReservationId, MemberId: reservation.MemberId, Reason: reason, FailedAt: c.clock()}`; call `c.bus.Publish(ctx, evt)`; log bus errors and do NOT surface. **`AutoLoanFailed` is published OUTSIDE any tx** — this is the canonical invariant: even when the un-fulfil tx rolls back, `AutoLoanFailed` is still observable on the bus.

#### Acceptance Criteria — consumer behavioural tests (port `auto-loan-on-return.consumer.spec.ts`)

- [ ] `internal/lending/auto_loan_on_return_test.go` lives in package `lending`. Uses stdlib `testing` only.
- [ ] A `buildConsumerScene(t *testing.T, opts ...func(*consumerSceneOpts)) consumerScene` helper constructs: a real `InMemoryEventBus`; real `*catalog.Facade` and `*membership.Facade` via their `NewFacadeWithOverrides`; a shared `*InMemoryReservationRepository` instance both the lending facade AND the consumer hold; a `txFactory` that returns `tx.NewInMemoryTransactionalContext(bus, logger)`; a real `*lending.Facade` via `NewFacadeWithOverrides` with sequential ID generators + a fixed clock (`time.Date(2030, 1, 15, 0, 0, 0, 0, time.UTC)`); a real `AutoLoanOnReturnConsumer` constructed with the same bus + reservations + txFactory + a fixed clock; `consumer.Start(ctx)` called before returning. The helper also exposes `seedAvailableCopy()` and `seedMember(name)` helpers that mirror the TS source's scene-builder.
- [ ] A `consumerSceneOpts struct { LoanRepository LoanRepository; ReservationRepository ReservationRepository }` lets individual tests inject throwing decorators.
- [ ] An `eventTypes(bus *InMemoryEventBus) []string` test helper that returns the captured events' Type() strings in order. The captured-events handler is subscribed in `buildConsumerScene` against `LoanOpened`, `LoanReturned`, `ReservationQueued`, `ReservationFulfilled`, `ReservationUnfulfilled`, `AutoLoanOpened`, `AutoLoanFailed`.
- [ ] AC (happy path — head-of-queue reserver): seed copy of book B, register Alice + Bob, Alice borrows the copy, Bob reserves book B, clear the captured-events slice, Alice returns the loan. After return: Bob has exactly one loan on the same `copyId` with `ReturnedAt == nil`; the captured-events slice equals `["LoanReturned", "ReservationFulfilled", "LoanOpened", "AutoLoanOpened"]` in order; the (single) reservation has `FulfilledAt == FIXED_NOW`.
- [ ] AC (catalog state after auto-loan): same setup, after Alice's return the copy ends UNAVAILABLE (the consumer's `lending.Borrow` drove `catalog.MarkCopyUnavailable`).
- [ ] AC (empty queue): Alice borrows + returns the copy with no reservations queued; the captured-events slice equals `["LoanReturned"]` exactly; the copy ends AVAILABLE.
- [ ] AC (eligibility cascade — skip suspended, stop at first eligible): seed copy, register Alice + Suspended + Eligible (in that order), Alice borrows, Suspended reserves, Eligible reserves, suspend Suspended via `membership.Suspend`, clear events, Alice returns. After return: Eligible has one loan on the copy; Suspended has zero loans; Suspended's reservation has `FulfilledAt == nil` (untouched); Eligible's reservation has `FulfilledAt == FIXED_NOW`; events equal `["LoanReturned", "ReservationFulfilled", "LoanOpened", "AutoLoanOpened"]`.
- [ ] AC (multiple ineligible reservations skipped): seed copy, register Alice + SuspendedOne + SuspendedTwo + FirstEligible + SecondEligible (in that order), Alice borrows, all four reserve, suspend SuspendedOne + SuspendedTwo, Alice returns. After return: FirstEligible has one loan; the other three have zero loans (SecondEligible included — the consumer stops at the first eligible attempt); events equal `["LoanReturned", "ReservationFulfilled", "LoanOpened", "AutoLoanOpened"]`.
- [ ] AC (all ineligible — no-op): seed copy, register Alice + SuspendedOne + SuspendedTwo, Alice borrows, both suspended reserve, suspend both, Alice returns. After return: no new loans for either suspended member; both reservations stay pending (`FulfilledAt == nil`); events equal `["LoanReturned"]`; copy ends AVAILABLE.
- [ ] AC (single ineligible — no-op): same shape with one suspended reservation; same outcome.
- [ ] AC (start twice is idempotent): call `consumer.Start(ctx)` twice — only one subscription is active; a single `LoanReturned` event triggers the handler once (not twice). Verified by counting `AutoLoanOpened` events after a successful happy-path return — equals 1, not 2.
- [ ] AC (stop detaches the handler): seed copy + Alice + Bob, Alice borrows, Bob reserves, call `consumer.Stop(ctx)`, Alice returns. After return: Bob has zero loans; the captured-events slice contains `LoanReturned` only (no `ReservationFulfilled`, no `LoanOpened`, no `AutoLoanOpened`). Documents the explicit-start/stop convention.
- [ ] AC (start after stop re-subscribes): call `Start` → `Stop` → `Start`; a `LoanReturned` after the second `Start` triggers the handler exactly once.
- [ ] AC (`AutoLoanOpened` payload fidelity): after a happy-path auto-loan, the captured `AutoLoanOpened` event has `BookId == copy.BookId`, `LoanId == bob's new loan id`, `MemberId == bob.MemberId`, `ReservationId == bob's reservation id`, `OpenedAt == FIXED_NOW`.
- [ ] AC (`AutoLoanFailed` on borrow rejection — suspended-mid-flight): seed copy + Alice + Bob, Alice borrows, Bob reserves, suspend Bob (via `membership.Suspend`) AFTER his reservation was queued and BEFORE Alice returns. Alice returns. After return: Bob has zero loans; Bob's reservation has `FulfilledAt == nil` (the un-fulfil tx rolled back the claim); events equal `["LoanReturned", "ReservationFulfilled", "ReservationUnfulfilled", "AutoLoanFailed"]`. **Wait** — re-examine: the eligibility check happens BEFORE `attemptAutoLoan` in the source (`if (!eligibility.eligible) continue`); so a suspended member at the eligibility-check time gets skipped and never claims. To trigger the failure-after-claim path we need the borrow itself to throw, NOT the eligibility check. **Revised AC**: arm a `throwingOnceLendingFacade` (unexported decorator in `auto_loan_on_return_test.go` that wraps `*lending.Facade` and forwards every method except `Borrow`, which returns the armed error on next call), inject via `consumerSceneOpts.Lending`. Seed copy + Alice + Bob, Alice borrows, Bob reserves, arm the next `Borrow` to fail with `errors.New("simulated borrow failure")`, Alice returns. After return: Bob has zero loans; Bob's reservation has `FulfilledAt == nil` (un-fulfil tx ran successfully); events equal `["LoanReturned", "ReservationFulfilled", "ReservationUnfulfilled", "AutoLoanFailed"]`; the `AutoLoanFailed.Reason == "simulated borrow failure"`.
- [ ] AC (`AutoLoanFailed` even when un-fulfil ALSO fails): combine a `throwingOnceLendingFacade` armed on `Borrow` with a `throwingOnceReservationRepository` (from Phase 3) armed on the SECOND `SaveReservation` (the first one is the claim; the second is the un-fulfil). After Alice returns: events equal `["LoanReturned", "ReservationFulfilled", "AutoLoanFailed"]` — note `ReservationUnfulfilled` is ABSENT (un-fulfil tx rolled back); `AutoLoanFailed.Reason` contains the ORIGINAL borrow error message (not the un-fulfil error); Bob's reservation still has `FulfilledAt == FIXED_NOW` (claim committed, un-fulfil didn't). Documents the "AutoLoanFailed fires regardless of un-fulfil outcome" rule.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go build ./...` passes.
- [ ] `go vet ./...` is clean.
- [ ] `go test ./internal/lending/...` is green and runs in under 400 ms (the consumer scene adds ~15 scenarios; concurrency tests added in Slice 2).
- [ ] No `init()` introduced.
- [ ] `internal/lending/auto_loan_on_return.go` imports stdlib + `internal/shared/events` + `internal/shared/tx` + `internal/accesscontrol` + `internal/catalog` + `internal/membership` (all already on `lending`'s allowed-imports list).
- [ ] `internal/lending/auto_loan_on_return_test.go`'s `throwingOnceLendingFacade` is unexported, lives only in the test file, never imported elsewhere.

---

### Slice 2: Consumer atomicity tests (the architectural payoff)

Brings the repository to "the four canonical saga atomicity invariants are locked under test." Slice 2 ships ONLY tests — no new production code. The atomicity tests share scaffolding with Slice 1 but are split into their own slice so the architectural payoff has its own commit, its own AC block, and its own narrative weight.

#### Acceptance Criteria — claim-tx atomicity

- [ ] AC (claim tx rolls back staged `ReservationFulfilled` on save error): seed copy + Alice + Bob, Alice borrows, Bob reserves, inject a `throwingOnceReservationRepository` armed on the NEXT `SaveReservation` (which is the claim — the reservation save inside `Borrow` already happened during `lending.reserve`, so the next save the consumer triggers is the claim). Alice returns. After return: Bob's reservation has `FulfilledAt == nil` (claim tx rolled back); the captured-events slice does NOT contain `ReservationFulfilled` (the staged event was suppressed by the rollback); the slice DOES contain `LoanReturned`; the slice does NOT contain `LoanOpened` or `AutoLoanOpened` (no borrow attempted); Bob has zero loans. **This is the first canonical atomicity AC: claim tx rollback suppresses `ReservationFulfilled`.**
- [ ] AC (claim tx error logged at `error` level): same setup, the saga consumer's slog handler (a test-double slog handler installed via the scene helper) records exactly one record at `error` level with attribute `book_id == copy.BookId`, `reservation_id == bob's reservation id`, `member_id == bob.MemberId`, `error == "armed failure"`.
- [ ] AC (claim tx error does NOT panic): the test does not need a `defer recover()` — the consumer must swallow the error and return cleanly from the bus handler. Verified by the test executing to completion past the `Alice returns` line.

#### Acceptance Criteria — un-fulfil-tx atomicity

- [ ] AC (un-fulfil tx rolls back staged `ReservationUnfulfilled` on save error): seed copy + Alice + Bob, Alice borrows, Bob reserves, inject BOTH a `throwingOnceLendingFacade` armed on `Borrow` (to force the un-fulfil path) AND a `throwingOnceReservationRepository` armed on the SECOND save it sees (the first save is the claim, which must succeed; the second is the un-fulfil, which must fail). Alice returns. After return: Bob's reservation has `FulfilledAt == FIXED_NOW` (claim committed, un-fulfil rolled back); the captured-events slice contains `ReservationFulfilled` (from the claim) but does NOT contain `ReservationUnfulfilled` (suppressed by un-fulfil rollback). **This is the second canonical atomicity AC: un-fulfil tx rollback suppresses `ReservationUnfulfilled`.**

#### Acceptance Criteria — `AutoLoanFailed` outside any tx

- [ ] AC (`AutoLoanFailed` fires even when un-fulfil tx rolled back): same setup as the previous AC. The captured-events slice equals `["LoanReturned", "ReservationFulfilled", "AutoLoanFailed"]` — `AutoLoanFailed` IS present despite the un-fulfil tx having rolled back. **This is the third canonical atomicity AC: `AutoLoanFailed` is published OUTSIDE any tx, so the saga's atomicity boundaries do not silence the failure signal.**
- [ ] AC (`AutoLoanFailed` payload reflects ORIGINAL borrow error, not un-fulfil error): same setup, the `AutoLoanFailed.Reason` equals the borrow error's message (`"simulated borrow failure"`), NOT the un-fulfil error's message. Documents the source-fidelity rule.

#### Acceptance Criteria — per-book serialisation

- [ ] AC (per-book mutex prevents double-fulfilment under concurrent returns of the same book): seed copy A of book B, copy B of book B (two copies of the same book), register Alice + Carol (Alice has loan on copyA, Carol has loan on copyB), register Bob (the reserver). Bob reserves book B. **Both** Alice and Carol return their loans CONCURRENTLY (two goroutines call `scene.Lending.ReturnLoan(aliceLoan.LoanId)` and `scene.Lending.ReturnLoan(carolLoan.LoanId)` in parallel; the test uses `sync.WaitGroup` to launch both and wait for both to complete). After both returns finish: Bob has EXACTLY ONE loan (not zero, not two); exactly one of Alice's or Carol's copies is now UNAVAILABLE (the one Bob got auto-loaned); the captured-events slice contains exactly TWO `LoanReturned` events, exactly ONE `ReservationFulfilled` event (the first consumer run claimed it; the second consumer run observed `FulfilledAt != nil` and saw no pending reservations to process), exactly ONE `LoanOpened` event (Bob's auto-loan), exactly ONE `AutoLoanOpened` event. **This is the fourth canonical atomicity AC: per-book serialisation under the `sync.Mutex` map prevents the double-fulfilment race the TS source documents.** Run the test under `go test -race ./internal/lending/...` to catch any race-detector violation.
- [ ] AC (different books run in parallel without contention): seed copy of book B1 + copy of book B2; register Alice + Bob + Carol + Dan; Alice borrows B1's copy, Carol borrows B2's copy; Bob reserves B1, Dan reserves B2; both Alice and Carol return concurrently; both reservers (Bob + Dan) end with one loan each on the respective copies; the captured-events slice contains exactly two `AutoLoanOpened` events (one per reserver). The test does NOT need to assert order between the two parallel chains — the bus's per-handler dispatch order is not deterministic across different events.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go test ./internal/lending/...` stays green and now totals under 500 ms (Slice 2 adds 5–8 scenarios; the concurrency test adds ~50 ms).
- [ ] `go test -race ./internal/lending/...` is green.
- [ ] No new production code in this slice — all changes live in `auto_loan_on_return_test.go`. The slice's commit message says "Slice 2: saga atomicity tests (no production-code changes)".

---

### Slice 3: `internal/fines` module — types + schema + repos + facade + tests (in-memory only)

Brings the repository to "the fines facade is callable in-memory with `assessFinesFor`, `processOverdueLoans`, `listFinesFor`, `findFine`, `payFine` — backed by an in-memory repo, with auto-suspend over a configurable threshold." Slice 3 ships the module skeleton + business logic + in-memory tests. Slice 4 layers in the bun repo + HTTP surface + wiring + integration test.

#### Acceptance Criteria — types + events

- [ ] `internal/fines/types.go` declares `type FineId string`, `type AmountCents int64` (named integer type to match TS source's `AmountCents`).
- [ ] `type FinesConfig struct { DailyRateCents AmountCents; SuspensionThresholdCents AmountCents }`. Doc comment names the defaults (25 / 1000) and the env-var names (`FINES_DAILY_RATE_CENTS`, `FINES_SUSPENSION_THRESHOLD_CENTS`).
- [ ] `type FineDto struct { FineId FineId; MemberId membership.MemberId; LoanId lending.LoanId; AmountCents AmountCents; AssessedAt time.Time; PaidAt *time.Time }`. `PaidAt` is a pointer for the "unpaid" sentinel.
- [ ] `type FineAssessed struct { FineId FineId; MemberId membership.MemberId; LoanId lending.LoanId; AmountCents AmountCents; AssessedAt time.Time }` implementing `events.DomainEvent` via `func (e FineAssessed) Type() string { return "FineAssessed" }`.
- [ ] `type MemberAutoSuspended struct { MemberId membership.MemberId; TotalUnpaidCents AmountCents; ThresholdCents AmountCents; SuspendedAt time.Time }` implementing `Type() string { return "MemberAutoSuspended" }`.
- [ ] `types.go` declares domain errors matching TS source 1:1:
  - `FineNotFoundError{ FineId FineId }` → message `"Fine not found: <fineId>"`.
  - `FineAlreadyPaidError{ FineId FineId }` → message `"Fine already paid: <fineId>"`.
- [ ] Each error implements `Error() string` and is matchable via `errors.As`. Pointer-receiver errors per Phase-2/3 convention.

#### Acceptance Criteria — schema (path-param parsing only)

- [ ] `internal/fines/schema.go` exports `ParseFineId(raw string) (FineId, error)` — trims; rejects blank with `*InvalidFineError{Reason: "fineId is required"}`. Same shape as Phase 3's `ParseReturnLoanRequest`.
- [ ] `type InvalidFineError struct { Reason string }` implementing `Error() string` returning `"Invalid fine request: <reason>"`. Maps to HTTP 400 in the registry.

#### Acceptance Criteria — repository port

- [ ] `internal/fines/repository.go` declares `type FineRepository interface` with methods:
  - `SaveFine(ctx context.Context, fine FineDto) error` — write; NOT staged through a tx (fines does not yet integrate with `TransactionalContext`; see Open Question 2).
  - `FindFineById(ctx context.Context, fineId FineId) (*FineDto, error)` — read; `(nil, nil)` on miss.
  - `FindFineByLoanId(ctx context.Context, loanId lending.LoanId) (*FineDto, error)` — used by `assessFinesFor` to short-circuit "already fined" loans. `(nil, nil)` on miss.
  - `ListFinesForMember(ctx context.Context, memberId membership.MemberId) ([]FineDto, error)`.

#### Acceptance Criteria — in-memory repository impl

- [ ] `internal/fines/in_memory_repository.go` exports `InMemoryFineRepository struct` + `NewInMemoryFineRepository() *InMemoryFineRepository`. Backed by `map[FineId]FineDto` + `sync.RWMutex`. Defensive copies on read.
- [ ] `SaveFine` upserts (overwrites the existing row by `fineId`); this is how `payFine` updates `PaidAt`.
- [ ] `FindFineByLoanId` iterates the map; returns the first matching `LoanId` (no ordering guarantee since at most one fine per loan in practice — enforced by `assessFinesFor`'s short-circuit).
- [ ] `ListFinesForMember` returns a snapshot ordered by `FineId` ascending (matches the catalog/lending in-memory ordering convention).
- [ ] `internal/fines/in_memory_repository_test.go` covers each method: happy path, not-found-returns-nil.

#### Acceptance Criteria — sample data (functional options)

- [ ] `internal/fines/sample_data.go` exports `SampleNewFine(opts ...FineOption) FineDto` returning a default `FineDto{FineId: "fine-placeholder", MemberId: "member-placeholder", LoanId: "loan-placeholder", AmountCents: 100, AssessedAt: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), PaidAt: nil}`.
- [ ] Options: `WithFineId(FineId)`, `WithFineMemberId(membership.MemberId)`, `WithFineLoanId(lending.LoanId)`, `WithAmountCents(AmountCents)`, `WithAssessedAt(time.Time)`, `WithPaidAt(time.Time)` (sets the pointer).
- [ ] `type FineOption func(*FineDto)`. Last-option-wins ordering.

#### Acceptance Criteria — configuration

- [ ] `internal/fines/configuration.go` exports `type Overrides struct { Lending *lending.Facade; Membership *membership.Facade; Repository FineRepository; Bus events.EventBus; Config *FinesConfig; NewID func() string; Clock func() time.Time; Logger *slog.Logger }`. Every field optional.
- [ ] `NewFacadeWithOverrides(o Overrides) *Facade` substitutes defaults:
  - `Lending → lending.NewFacadeWithOverrides(lending.Overrides{})` (fresh in-memory lending facade — used in tests; in production the wired facade is passed in).
  - `Membership → membership.NewFacadeWithOverrides(membership.Overrides{})`.
  - `Repository → NewInMemoryFineRepository()`.
  - `Bus → events.NewInMemoryEventBus(discardLogger)`.
  - `Config → &FinesConfig{DailyRateCents: 25, SuspensionThresholdCents: 1000}`.
  - `NewID → uuid.NewString`.
  - `Clock → time.Now`.
  - `Logger → slog.New(slog.DiscardHandler)`.

#### Acceptance Criteria — `Facade` methods

- [ ] `internal/fines/facade.go` exports `type Facade struct` with unexported fields: `lending *lending.Facade`, `membership *membership.Facade`, `repository FineRepository`, `bus events.EventBus`, `config FinesConfig`, `newID func() string`, `clock func() time.Time`, `logger *slog.Logger`.
- [ ] `NewFacade(lending, membership, repository, bus, config, newID, clock, logger)` constructor.
- [ ] `Facade.AssessFinesFor(ctx context.Context, memberId membership.MemberId, now time.Time) ([]FineDto, error)`: call `f.membership.FindMember(ctx, memberId)` (bubbles `*MemberNotFoundError` on miss; this is the cross-module precondition); call `f.lending.ListLoansFor(ctx, memberId)`; filter to overdue (`returnedAt == nil AND dueDate.Before(now)`); for each overdue loan: call `f.repository.FindFineByLoanId(ctx, loan.LoanId)` — if non-nil, continue (already fined); else build a fine via `buildFine(loan, now)`, save via `f.repository.SaveFine`, publish `FineAssessed` via `f.bus.Publish`, append to the result slice. Return the slice.
- [ ] `Facade.ProcessOverdueLoans(ctx context.Context, now time.Time) error`: call `f.lending.ListOverdueLoans(ctx, now)`; compute distinct memberIds; for each memberId call `f.AssessFinesFor(ctx, memberId, now)` then `f.maybeAutoSuspend(ctx, memberId, now)`. Return on the first error from either call (matches TS source's loop semantics).
- [ ] `Facade.ListFinesFor(ctx context.Context, memberId membership.MemberId) ([]FineDto, error)`: delegates to `f.repository.ListFinesForMember`.
- [ ] `Facade.FindFine(ctx context.Context, fineId FineId) (FineDto, error)`: `f.repository.FindFineById`; on nil return `*FineNotFoundError{FineId}`.
- [ ] `Facade.PayFine(ctx context.Context, fineId FineId) (FineDto, error)`: load via `FindFineById`; on nil return `*FineNotFoundError`; if `fine.PaidAt != nil` return `*FineAlreadyPaidError{FineId}`; build `paid := fine; paid.PaidAt = ptr(f.clock())`; save; return. NO event published on pay (matches TS source).
- [ ] `(f *Facade) maybeAutoSuspend(ctx context.Context, memberId membership.MemberId, now time.Time) error`: compute total unpaid cents via `f.repository.ListFinesForMember` + filter `PaidAt == nil` + sum `AmountCents`; if total < `f.config.SuspensionThresholdCents`, return nil. Else: call `f.membership.FindMember(ctx, memberId)`; if `member.Status == membership.MembershipStatusSuspended`, return nil (already suspended). Else: call `f.membership.Suspend(ctx, memberId)`; publish `MemberAutoSuspended{MemberId, TotalUnpaidCents: total, ThresholdCents: f.config.SuspensionThresholdCents, SuspendedAt: now}` via `f.bus.Publish`. Return nil.
- [ ] `(f *Facade) buildFine(loan lending.LoanDto, now time.Time) FineDto`: compute `daysOverdue := int64(math.Ceil(now.Sub(loan.DueDate).Hours() / 24))` (ceil-of-days-between — matches TS source's `Math.ceil((later - earlier) / MS_PER_DAY)`); return `FineDto{FineId: FineId(f.newID()), MemberId: loan.MemberId, LoanId: loan.LoanId, AmountCents: AmountCents(daysOverdue) * f.config.DailyRateCents, AssessedAt: now, PaidAt: nil}`.

#### Acceptance Criteria — facade tests (`facade_test.go`)

- [ ] `internal/fines/facade_test.go` lives in package `fines`. Uses real `*lending.Facade` + `*membership.Facade` + `*InMemoryFineRepository` constructed via the scene helper. Sequential ID generators per facade. Fixed clock.
- [ ] A `buildScene(t *testing.T, opts ...func(*sceneOpts)) sceneT` test helper constructs the dependency graph and exposes: the fines facade, the underlying lending facade (so tests can seed loans), the underlying membership facade (so tests can register/suspend members), the underlying catalog facade (so tests can seed books + copies — needed by `lending.Borrow`), the underlying bus, and a captured-events slice. Same shape as Phase 3's `buildScene`.
- [ ] AC (`AssessFinesFor` happy path — one overdue loan): seed member + book + copy, borrow with `borrowedAt = clock()` and `dueDate = clock() + 14 days`; advance `clock` (via the scene helper's mutable-clock; or pass a custom `now` to `AssessFinesFor`) by 30 days; call `AssessFinesFor(memberId, now)`. Expected: returns slice of length 1 with `AmountCents == 16 * 25 == 400` (16 days overdue × 25 cents/day; ceil(30-14) == 16); the repo contains the fine; the bus contains one `FineAssessed` event with matching fields.
- [ ] AC (`AssessFinesFor` skips already-fined loans): repeat `AssessFinesFor` for the same memberId — second call returns an empty slice; the repo still contains exactly one fine; no second `FineAssessed` event published.
- [ ] AC (`AssessFinesFor` skips not-yet-due loans): seed loan with `dueDate > now`; assess; result is empty.
- [ ] AC (`AssessFinesFor` skips returned loans): seed loan + return it within the lending facade; assess; result is empty.
- [ ] AC (`AssessFinesFor` unknown member): pass an unknown memberId; returns `*membership.MemberNotFoundError` directly (cross-module read surfaces); repo unchanged; no event.
- [ ] AC (`ProcessOverdueLoans` iterates distinct memberIds): seed loans for Alice (one overdue) + Bob (one overdue, one not-yet-due) + Carol (one returned + one overdue); call `ProcessOverdueLoans(now)`. Expected: Alice has 1 fine, Bob has 1 fine, Carol has 1 fine; three `FineAssessed` events fired.
- [ ] AC (auto-suspend at threshold): set `SuspensionThresholdCents = 100`; assess fines that cumulatively reach 100 cents unpaid; the named member is suspended via `membership.Suspend`; one `MemberAutoSuspended` event is published with `TotalUnpaidCents`, `ThresholdCents`, `SuspendedAt` populated.
- [ ] AC (auto-suspend skipped under threshold): same setup with `SuspensionThresholdCents = 10000` (so the test fines don't reach it); no suspension; no `MemberAutoSuspended` event.
- [ ] AC (auto-suspend skipped when already suspended): pre-suspend the member; call `ProcessOverdueLoans`; the member stays suspended (no re-suspend call); NO `MemberAutoSuspended` event published this time (matches TS source's idempotency).
- [ ] AC (`FindFine` happy path): assess a fine; call `FindFine`; returns the fine.
- [ ] AC (`FindFine` not found): unknown id → `*FineNotFoundError`.
- [ ] AC (`PayFine` happy path): assess; pay; returns DTO with `PaidAt == clock()`; subsequent `FindFine` reflects the same `PaidAt`.
- [ ] AC (`PayFine` already paid): assess; pay; pay again → `*FineAlreadyPaidError`.
- [ ] AC (`PayFine` not found): unknown id → `*FineNotFoundError`.
- [ ] AC (`ListFinesFor` returns only the named member's fines): seed fines for Alice + Bob; `ListFinesFor(Alice)` returns only Alice's.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go build ./...` is green.
- [ ] `go vet ./...` clean.
- [ ] `go test ./internal/fines/...` is green and under 250 ms.
- [ ] No `init()` introduced.
- [ ] `internal/fines/` imports per BOUNDARIES.md: `internal/lending`, `internal/membership`, `internal/shared/events`, plus stdlib + `github.com/google/uuid` + `math`. NOT `internal/catalog` directly (fines goes through `lending`'s loan DTOs which already carry `BookId`), NOT `internal/accesscontrol`, NOT `internal/categories`.

---

### Slice 4: fines bun repo + HTTP + composition root + integration test

Brings the repository to "every fines facade method is reachable over HTTP, persists to Postgres via bun, and is exercised end-to-end against testcontainers." Same shape as Phase 3's Slice 7 but applied to fines.

#### Acceptance Criteria — migration

- [ ] `migrations/0004_fines.sql` creates `fines`: `fine_id UUID PK`, `member_id UUID NOT NULL`, `loan_id UUID NOT NULL`, `amount_cents BIGINT NOT NULL`, `assessed_at TIMESTAMPTZ NOT NULL`, `paid_at TIMESTAMPTZ NULL`. No FK references.
- [ ] Index on `member_id` (for `ListFinesForMember`).
- [ ] Index on `loan_id` (for `FindFineByLoanId` short-circuit).
- [ ] `migrations/atlas.sum` regenerated and committed.
- [ ] `task migrate:apply` against a fresh dev Postgres creates the table and is a no-op on second run.

#### Acceptance Criteria — bun repository

- [ ] `internal/fines/bun_repository.go` exports `BunFineRepository struct { db *bun.DB }` + `NewBunFineRepository(db *bun.DB) *BunFineRepository`. Implements `FineRepository`.
- [ ] `var _ FineRepository = (*BunFineRepository)(nil)` guard.
- [ ] `FineRow struct { FineId FineId; MemberId membership.MemberId; LoanId lending.LoanId; AmountCents AmountCents; AssessedAt time.Time; PaidAt *time.Time }` with bun struct tags mapped to `fines` table.
- [ ] `SaveFine` upserts via `ON CONFLICT (fine_id) DO UPDATE SET ...` — handles both initial assessment and pay-fine state mutation.
- [ ] `FindFineById` / `FindFineByLoanId`: `(nil, nil)` on `sql.ErrNoRows`.
- [ ] `ListFinesForMember`: ordered by `fine_id ASC` for in-memory/bun parity.
- [ ] `internal/fines/bun_repository_test.go` (`//go:build integration`): runs the in-memory scenarios against testcontainers Postgres. `t.Cleanup` truncates `fines` between tests.

#### Acceptance Criteria — HTTP DTOs + mapping

- [ ] `internal/fines/http/dto.go` exports:
  - `FineResponse struct { FineId string ` `json:"fineId"` `; MemberId string ` `json:"memberId"` `; LoanId string ` `json:"loanId"` `; AmountCents int64 ` `json:"amountCents"` `; AssessedAt time.Time ` `json:"assessedAt"` `; PaidAt *time.Time ` `json:"paidAt,omitempty"` ` }`.
- [ ] `internal/fines/http/mapping.go` exports unexported `toFineResponse(FineDto) FineResponse` and `toFineResponseSlice([]FineDto) []FineResponse`.

#### Acceptance Criteria — handlers

- [ ] `internal/fines/http/handlers.go` exports `Handlers struct { facade *fines.Facade; logger *slog.Logger; clock func() time.Time }` + `NewHandlers(facade, logger, clock)`. The `clock` is injected so the HTTP layer can pass `now` to `AssessFinesFor`/`ProcessOverdueLoans` deterministically in tests.
- [ ] All handlers go through `sharedhttp.Handle` so domain errors are middleware-mapped.
- [ ] `Handlers.AssessFinesFor(w, r) error`: read `:memberId` URL param; call `facade.AssessFinesFor(ctx, memberId, h.clock())`; respond 200 + `[]FineResponse` (200 not 201 — matches TS source's `@HttpCode(200)` on a POST).
- [ ] `Handlers.ProcessOverdueLoans(w, r) error`: call `facade.ProcessOverdueLoans(ctx, h.clock())`; respond 204 (no body).
- [ ] `Handlers.ListFinesFor(w, r) error`: read `:memberId`; call `facade.ListFinesFor(ctx, memberId)`; respond 200 + `[]FineResponse`.
- [ ] `Handlers.FindFine(w, r) error`: read `:fineId`; parse via `ParseFineId`; call `facade.FindFine`; respond 200 + `FineResponse`.
- [ ] `Handlers.PayFine(w, r) error`: read `:fineId`; parse via `ParseFineId`; call `facade.PayFine`; respond 200 + `FineResponse`.

#### Acceptance Criteria — `module.go`

- [ ] `internal/fines/module.go` exports `type Deps struct { Facade *fines.Facade; Logger *slog.Logger; Clock func() time.Time }` and `func Wire(r chi.Router, deps Deps)`.
- [ ] Mounts:
  - `POST /members/{memberId}/fines/assessments` → `AssessFinesFor` (200 on success)
  - `POST /fines/batch/process` → `ProcessOverdueLoans` (204 on success)
  - `GET /members/{memberId}/fines` → `ListFinesFor`
  - `GET /fines/{fineId}` → `FindFine`
  - `PATCH /fines/{fineId}/paid` → `PayFine`

#### Acceptance Criteria — composition root

- [ ] `internal/app/wiring.go` constructs `finesRepo := fines.NewBunFineRepository(bunDB)`; loads `FinesConfig` from viper (env vars `FINES_DAILY_RATE_CENTS`, `FINES_SUSPENSION_THRESHOLD_CENTS` with defaults 25 / 1000); constructs the fines facade via `fines.NewFacadeWithOverrides`; registers error codes `fine_not_found → 404`, `fine_already_paid → 409`, `invalid_fine → 400`; mounts `finesModule.Wire(router, finesModule.Deps{Facade: finesFacade, Logger: logger, Clock: time.Now})`; extends `app.Wired` with `FinesFacade *fines.Facade`.

#### Acceptance Criteria — handler tests

- [ ] `internal/fines/http/handlers_test.go` (unit) constructs a real `*fines.Facade` (with real in-memory repos + the underlying real lending + membership + catalog facades), wraps in `NewHandlers`, exercises each handler via `httptest.NewRecorder` + a chi router so URL params resolve.
- [ ] AC: `POST /members/{memberId}/fines/assessments` returns 200 + `[]FineResponse` (possibly empty).
- [ ] AC: `POST /fines/batch/process` returns 204 (no body).
- [ ] AC: `GET /members/{memberId}/fines` returns 200 + `[]FineResponse`.
- [ ] AC: `GET /fines/{fineId}` returns 200 + `FineResponse`.
- [ ] AC: `GET /fines/{fineId}` for an unknown id returns 404 + `{"error":"fine_not_found"}`.
- [ ] AC: `PATCH /fines/{fineId}/paid` for an unpaid fine returns 200 + `FineResponse` with `paidAt` set.
- [ ] AC: `PATCH /fines/{fineId}/paid` for an already-paid fine returns 409 + `{"error":"fine_already_paid"}`.
- [ ] AC: `PATCH /fines/{fineId}/paid` for an unknown id returns 404.
- [ ] AC: `GET /fines/` with a blank fineId — actually `chi` rejects empty path params at the routing layer, so this AC reduces to "the route doesn't match" (404 from chi).

#### Acceptance Criteria — crucial-path integration test

- [ ] `test/crucial_path/fines_integration_test.go` (`//go:build integration`) boots the full app via `test/support.BootApp` against testcontainers Postgres + Redis.
- [ ] AC: Seed sequence — `POST /members`, `POST /books`, `POST /books/{bookId}/copies`, `POST /loans` (with a `dueDate` in the past — **wait**, the borrow handler doesn't accept a custom `dueDate`; the facade computes it as `borrowedAt + 14 days`). **Revised approach**: seed a loan via direct facade call inside the test (`wired.LendingFacade.Borrow(...)`) AND then directly UPDATE the `due_date` column in `loans` to a past date via `wired.DB.Exec(ctx, "UPDATE loans SET due_date = $1 WHERE loan_id = $2", pastDate, loanId)`. This is a test-only mechanism to fabricate an overdue loan; the handler-level path is exercised via the assessment endpoint.
- [ ] AC: `POST /members/{memberId}/fines/assessments` for the member with the (now-overdue) loan returns 200 + `[FineResponse{...}]`. Direct row count: `SELECT COUNT(*) FROM fines WHERE member_id = $1` returns 1.
- [ ] AC: Calling `POST /members/{memberId}/fines/assessments` again returns 200 + `[]` (already-fined short-circuit); the row count stays at 1.
- [ ] AC: `GET /members/{memberId}/fines` returns the fine in the response body.
- [ ] AC: `PATCH /fines/{fineId}/paid` returns 200 + `FineResponse` with non-nil `paidAt`. Direct row read confirms `paid_at IS NOT NULL`.
- [ ] AC: Auto-suspend — seed enough overdue loans (multiple separate copy/borrow/update-due-date cycles for the same member) so that the cumulative `amountCents` of unpaid fines exceeds 1000 (with the default daily rate × overdue days, two overdue loans 22 days past due each is ~1100 cents). Call `POST /fines/batch/process`. After: the member's status (`GET /members/{memberId}` from Phase 2) is SUSPENDED; a `MemberAutoSuspended` event was observed on the bus (via `wired.Bus.Subscribe(...)` registered before the call).
- [ ] AC: `t.Cleanup` truncates `fines`, `loans`, `reservations`, `copies`, `books`, `members` between tests.

#### Acceptance Criteria — `.http` activation

- [ ] `.http/fines.http` exists with active requests using `{{baseUrl}}`: `POST {{baseUrl}}/members/<id>/fines/assessments`, `POST {{baseUrl}}/fines/batch/process`, `GET {{baseUrl}}/members/<id>/fines`, `GET {{baseUrl}}/fines/<id>`, `PATCH {{baseUrl}}/fines/<id>/paid`.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go build ./...` green.
- [ ] `go vet ./...` clean.
- [ ] `go test ./internal/fines/...` green; total unit time under 250 ms.
- [ ] `go test -tags=integration ./internal/fines/... ./test/crucial_path/fines_integration_test.go` green; under 30 s including testcontainers cold start.
- [ ] No `init()` introduced.

---

### Slice 5: `internal/categories` module — types + repos + facade + HTTP + integration test

Brings the repository to "the categories module is shipped vertical: CRUD via `POST /categories`, `GET /categories?startsWith=...`, `GET /categories/{id}`, backed by Postgres with a UNIQUE constraint on `name`." Categories is the simplest module in the codebase — no cross-module dependencies, no events, no tx integration. Shipping it vertically in one slice (not split across multiple) is intentional; the work is small.

#### Acceptance Criteria — types + schema

- [ ] `internal/categories/types.go` declares `type CategoryId string`.
- [ ] `type CategoryDto struct { CategoryId CategoryId; Name string; CreatedAt time.Time }`. **Note**: TS source names the field `id` (not `categoryId`); Go port uses `CategoryId` for consistency with the `<Module>Id` newtype pattern. The HTTP DTO maps to `"id"` to preserve wire compatibility.
- [ ] Domain errors matching TS source:
  - `CategoryNotFoundError{ Identifier string }` → message `"Category not found: <identifier>"`. (TS source uses a single `identifier` parameter rather than a typed `CategoryId` so that "find by name" lookups can also report cleanly; the Go port matches.)
  - `DuplicateCategoryError{ Name string }` → message `"A category with name <name> already exists"`.
  - `InvalidCategoryError{ Reason string }` → message `"Invalid category: <reason>"`.
  - `InvalidCategoriesQueryError{ Reason string }` → message `"Invalid categories query: <reason>"`.
- [ ] All errors are pointer-receiver.
- [ ] `internal/categories/schema.go` exports `ParseNewCategory(name string) (string, error)` — trims `name`; rejects blank → `*InvalidCategoryError{Reason: "name is required"}`; rejects names longer than 100 chars → `*InvalidCategoryError{Reason: "name too long"}`; returns the trimmed name.
- [ ] `ParseStartsWith(raw string) (string, error)` — trims; rejects blank → `*InvalidCategoriesQueryError{Reason: "startsWith is required"}`; returns the trimmed prefix.

#### Acceptance Criteria — repository port + in-memory impl

- [ ] `internal/categories/repository.go` declares `type CategoryRepository interface`:
  - `Save(ctx context.Context, category CategoryDto) error` — translates DB unique-violation on `name` to `*DuplicateCategoryError{Name}`.
  - `FindById(ctx context.Context, id CategoryId) (*CategoryDto, error)` — `(nil, nil)` on miss.
  - `FindByNamePrefix(ctx context.Context, prefix string) ([]CategoryDto, error)` — case-insensitive prefix match (matches TS source's `name.toLowerCase().startsWith(prefix.toLowerCase())`).
- [ ] `internal/categories/in_memory_repository.go`: in-memory map keyed by `CategoryId` + a `nameIndex map[string]CategoryId` for fast duplicate detection (or a linear scan — Phase 4 picks linear scan for simplicity since categories is a small curated set). On `Save`, scan for an existing category with the same `Name` (case-insensitive); if found and id differs, return `*DuplicateCategoryError`.
- [ ] `FindByNamePrefix` filters + sorts by `Name` ascending (case-insensitive).

#### Acceptance Criteria — bun repository + migration

- [ ] `migrations/0005_categories.sql` creates `categories`: `category_id UUID PK`, `name TEXT NOT NULL`, `created_at TIMESTAMPTZ NOT NULL`. UNIQUE INDEX `categories_name_unique` ON `lower(name)` (case-insensitive uniqueness, matches the in-memory rule).
- [ ] `migrations/atlas.sum` regenerated.
- [ ] `internal/categories/bun_repository.go` exports `BunCategoryRepository struct { db *bun.DB }` + `NewBunCategoryRepository(db *bun.DB) *BunCategoryRepository`. Implements `CategoryRepository`. `var _ CategoryRepository = ...` guard.
- [ ] `Save` issues `INSERT INTO categories ...`; on `pgErr.Code == "23505"` (Postgres unique violation) returns `*DuplicateCategoryError{Name}`. Other errors wrap and return.
- [ ] `FindByNamePrefix` queries `WHERE lower(name) LIKE lower(?) || '%' ORDER BY lower(name) ASC`.
- [ ] `internal/categories/bun_repository_test.go` (`//go:build integration`) covers the in-memory scenarios against testcontainers Postgres. Additionally asserts the unique constraint produces `*DuplicateCategoryError`.

#### Acceptance Criteria — sample data + configuration

- [ ] `internal/categories/sample_data.go` exports `SampleNewCategory(opts ...CategoryOption) CategoryDto` returning a default `CategoryDto{CategoryId: "cat-placeholder", Name: "Fiction", CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}`. Options: `WithCategoryId`, `WithCategoryName`, `WithCategoryCreatedAt`.
- [ ] `internal/categories/configuration.go` exports `type Overrides struct { Repository CategoryRepository; NewID func() string; Clock func() time.Time; Logger *slog.Logger }`. Every field optional. `NewFacadeWithOverrides` substitutes defaults.

#### Acceptance Criteria — `Facade` methods + tests

- [ ] `internal/categories/facade.go` exports `type Facade struct` with `repository CategoryRepository`, `newID func() string`, `clock func() time.Time`, `logger *slog.Logger`.
- [ ] `Facade.CreateCategory(ctx context.Context, name string) (CategoryDto, error)`: parse name via `ParseNewCategory`; build `CategoryDto{CategoryId: CategoryId(f.newID()), Name: parsed, CreatedAt: f.clock()}`; save via repo (which may return `*DuplicateCategoryError`); return.
- [ ] `Facade.FindCategoryById(ctx context.Context, id CategoryId) (CategoryDto, error)`: load via repo; on nil return `*CategoryNotFoundError{Identifier: string(id)}`.
- [ ] `Facade.ListByPrefix(ctx context.Context, prefix string) ([]CategoryDto, error)`: parse via `ParseStartsWith`; delegate to `repo.FindByNamePrefix`.
- [ ] `internal/categories/facade_test.go` (unit) covers:
  - AC: Create happy path — `Name` trimmed, `CategoryId` populated, `CreatedAt == clock()`.
  - AC: Create with blank name → `*InvalidCategoryError{Reason: "name is required"}`.
  - AC: Create with name > 100 chars → `*InvalidCategoryError{Reason: "name too long"}`.
  - AC: Create duplicate name (case-insensitive) → `*DuplicateCategoryError{Name}`.
  - AC: `FindCategoryById` happy path.
  - AC: `FindCategoryById` unknown id → `*CategoryNotFoundError`.
  - AC: `ListByPrefix` returns matching categories sorted by name ascending.
  - AC: `ListByPrefix` with blank prefix → `*InvalidCategoriesQueryError`.
- [ ] `internal/categories/in_memory_repository_test.go` covers repo-level scenarios separately (duplicate detection, case-insensitive prefix match).

#### Acceptance Criteria — HTTP DTOs + handlers + module

- [ ] `internal/categories/http/dto.go` exports:
  - `CreateCategoryRequest struct { Name string ` `json:"name"` ` }`.
  - `CategoryResponse struct { Id string ` `json:"id"` `; Name string ` `json:"name"` `; CreatedAt time.Time ` `json:"createdAt"` ` }` — **note the JSON key `id` not `categoryId`** to preserve wire compatibility with the TS source.
- [ ] `internal/categories/http/mapping.go`: `toCategoryResponse(CategoryDto) CategoryResponse`.
- [ ] `internal/categories/http/handlers.go` exports `Handlers struct { facade *categories.Facade; logger *slog.Logger }` + `NewHandlers`.
- [ ] `Handlers.CreateCategory(w, r) error`: decode `CreateCategoryRequest` with `DisallowUnknownFields`; call `facade.CreateCategory`; respond 201 + `CategoryResponse`.
- [ ] `Handlers.ListByPrefix(w, r) error`: read `startsWith` query param; call `facade.ListByPrefix`; respond 200 + `[]CategoryResponse`.
- [ ] `Handlers.FindCategoryById(w, r) error`: read `:id` URL param; call `facade.FindCategoryById`; respond 200 + `CategoryResponse`.
- [ ] `internal/categories/module.go` exports `Wire(r chi.Router, deps Deps)`:
  - `POST /categories` → `CreateCategory`
  - `GET /categories` (with `?startsWith=` query) → `ListByPrefix`
  - `GET /categories/{id}` → `FindCategoryById`
- [ ] `internal/categories/http/handlers_test.go` covers each handler's happy path + error paths (400 invalid, 404 not found, 409 duplicate).

#### Acceptance Criteria — composition root

- [ ] `internal/app/wiring.go` constructs `categoriesRepo := categories.NewBunCategoryRepository(bunDB)`; constructs the facade; registers error codes `category_not_found → 404`, `duplicate_category → 409`, `invalid_category → 400`, `invalid_categories_query → 400`; mounts `categoriesModule.Wire(router, ...)`; extends `app.Wired` with `CategoriesFacade *categories.Facade`.

#### Acceptance Criteria — integration test

- [ ] `test/crucial_path/categories_integration_test.go` (`//go:build integration`) boots the full app and exercises:
  - AC: `POST /categories` with `{"name":"Fiction"}` returns 201 + `CategoryResponse`. Row in `categories`.
  - AC: `POST /categories` with the same name (case-insensitive: `"FICTION"`) returns 409 + `{"error":"duplicate_category"}`.
  - AC: `POST /categories` with blank name returns 400.
  - AC: `POST /categories` with unknown JSON field returns 400 (driven by `DisallowUnknownFields`).
  - AC: `GET /categories?startsWith=fi` returns 200 + `[CategoryResponse{Name:"Fiction"}]`.
  - AC: `GET /categories?startsWith=` returns 400 + `invalid_categories_query`.
  - AC: `GET /categories/{id}` returns 200 + `CategoryResponse`.
  - AC: `GET /categories/{id}` for unknown id returns 404 + `category_not_found`.

#### Acceptance Criteria — `.http` activation

- [ ] `.http/categories.http` with sample bodies for each endpoint.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go build ./...`, `go vet ./...`, `go test ./internal/categories/...` green; under 200 ms.
- [ ] `go test -tags=integration ./internal/categories/... ./test/crucial_path/categories_integration_test.go` green.
- [ ] No `init()` introduced.
- [ ] `internal/categories/` imports only stdlib + `internal/shared/*` + the bun package. NOT `internal/lending`, NOT `internal/membership`, NOT `internal/catalog`, NOT `internal/fines`.

---

### Slice 6: Saga crucial-path integration test + composition-root start/stop wiring

Brings the repository to "the full auto-loan chain runs end-to-end through the HTTP surface against real Postgres + Redis; the saga consumer is started by the composition root and stopped on graceful shutdown." This slice is the integration test that proves Phase 4's Done criteria, plus the composition-root wiring that makes the consumer go in production.

#### Acceptance Criteria — composition-root start/stop

- [ ] `internal/app/wiring.go` extends `Wire` so the `AutoLoanOnReturnConsumer` is constructed after all facades + wiring + route mounting:
  - `autoLoanConsumer := lending.NewAutoLoanOnReturnConsumer(lending.AutoLoanOnReturnConsumerDeps{Bus: bus, Membership: membershipFacade, Reservations: reservationsRepo, Lending: lendingFacade, TxFactory: txFactory, Clock: time.Now, Logger: logger.With("component", "auto-loan-consumer")})`.
  - `if err := autoLoanConsumer.Start(ctx); err != nil { return Wired{}, err }`.
- [ ] `app.Wired` extends with `AutoLoanConsumer *lending.AutoLoanOnReturnConsumer`.
- [ ] `app.Wired.Close` extends to call `autoLoanConsumer.Stop(ctx)` BEFORE closing the DB (so the consumer stops handling events before the DB connection drops).
- [ ] The order of operations in `Wire` is documented in the function's doc comment: "1. build deps → 2. build facades → 3. wire HTTP routes → 4. construct + Start consumers → 5. return Wired with Close that Stops consumers, then closes DB".
- [ ] `cmd/library/main.go`'s graceful-shutdown handler ensures `Wired.Close(ctx)` is called on SIGINT/SIGTERM — verify the existing Phase-1/2 wiring already handles this; the Phase-4 change is purely additive (no new lifecycle hook required at the `main.go` level since `Close` is already called).

#### Acceptance Criteria — saga crucial-path test

- [ ] `test/crucial_path/saga_integration_test.go` (`//go:build integration`) boots the full app via `test/support.BootApp` against testcontainers Postgres + Redis.
- [ ] AC (happy chain): seed two members (Alice + Bob) and one available copy of one book. `POST /loans {memberId: alice, copyId}` for Alice (returns 201). `POST /reservations {memberId: bob, bookId}` for Bob (returns 201). Subscribe to `AutoLoanOpened` via `wired.Bus.Subscribe(...)` BEFORE the return. `PATCH /loans/{aliceLoanId}/return` (returns 200). Wait synchronously for the auto-loan to propagate — since the consumer runs in the same goroutine as the bus dispatch in the in-memory bus AND `bus.Publish` is synchronous, the auto-loan is complete by the time `PATCH` returns. **However**, the per-book lock acquires *during* the handler, which runs *during* `bus.Publish`, which runs *during* the post-commit publish step of `ReturnLoan` — so by the time the PATCH response returns, the entire auto-loan chain has already happened. Assert: (a) `wired.DB` row count in `loans WHERE member_id = bob.memberId` is 1; (b) the row's `copy_id == copyId`, `returned_at IS NULL`; (c) `wired.DB` row in `reservations WHERE member_id = bob.memberId` has `fulfilled_at IS NOT NULL` and equals approximately `now()`; (d) `wired.CatalogFacade.FindCopy(ctx, copyId)` returns `Status == "UNAVAILABLE"`; (e) the subscribed `AutoLoanOpened` channel/slice has exactly one event with `BookId == bookId`, `MemberId == bob.memberId`, `ReservationId == bob's reservation id`.
- [ ] AC (eligibility cascade through HTTP): seed Alice + SuspendedBob + EligibleCarol; Alice borrows; SuspendedBob reserves; EligibleCarol reserves; `PATCH /members/{suspendedBobId}/suspend` (Phase 2 endpoint); subscribe to `AutoLoanOpened`; Alice returns. After: only EligibleCarol has a loan; SuspendedBob's reservation has `fulfilled_at IS NULL`; EligibleCarol's reservation has `fulfilled_at IS NOT NULL`; one `AutoLoanOpened` event observed with `MemberId == carol.memberId`.
- [ ] AC (no reservations — vanilla return): seed Alice + book + copy; Alice borrows + returns. After: no auto-loan opened; copy is AVAILABLE; the bus shows only `LoanReturned` (subscribed for the test).
- [ ] AC (auto-loan failure — borrow rejects because reserver is no longer eligible): seed Alice + Bob, Alice borrows, Bob reserves, suspend Bob via `PATCH /members/{bobId}/suspend` AFTER his reservation is queued, subscribe to both `AutoLoanFailed` and `AutoLoanOpened`, Alice returns. After: Bob has zero loans; Bob's reservation has `fulfilled_at IS NULL` (the consumer skips the suspended member at the eligibility check and never claims). The captured events show `LoanReturned` but NO `AutoLoanFailed` (the consumer doesn't fail — it just skips). **Refinement**: this AC actually proves the eligibility-cascade path, NOT the borrow-rejection path. To prove the borrow-rejection path through HTTP requires a way to make `lending.Borrow` reject after the reservation passes eligibility — e.g. the copy becomes unavailable mid-flight. Inject this race by directly UPDATEing `copies SET status = 'UNAVAILABLE' WHERE copy_id = $1` via `wired.DB.Exec` BEFORE Alice's `PATCH /return` returns. The sequence: Alice borrows, Bob reserves, manually mark the copy unavailable in the DB, Alice returns. The return's `catalog.MarkCopyAvailable` will flip it back to AVAILABLE, but then a race window opens before the consumer's `lending.Borrow` runs — actually, since everything runs synchronously in the in-memory bus, this is not racy; the manual unavail is overwritten by `MarkCopyAvailable` before the consumer attempts the borrow. **Revised AC**: drop this through-HTTP test of the borrow-rejection path; the facade-level atomicity AC in Slice 2 (with `throwingOnceLendingFacade`) already locks the invariant. This crucial-path test focuses on the chains observable through HTTP without injecting failures.
- [ ] AC (the saga survives a multi-step chain): seed Alice + Bob + Carol; Alice borrows copy1, Bob reserves bookId, Alice returns (Bob auto-gets the loan), Bob returns (no further reserver) — verify Bob's loan is returned-state, copy ends AVAILABLE, no second auto-loan event. Then Carol reserves bookId; subscribe; manually borrow copy1 by anyone — wait, the test is getting convoluted. Reduce to: "Bob's auto-loan can be returned through `PATCH /loans/{bobLoanId}/return`, the copy ends AVAILABLE, no further auto-loan opens (no pending reservations)."
- [ ] AC (consumer lifecycle on shutdown): the test triggers `wired.Close(ctx)` and verifies that subsequent `PATCH /loans/{id}/return` calls (issued before close completes — wait, the server is shut down) — skip this AC; the lifecycle test is covered at the consumer-unit level in Slice 1.
- [ ] AC (`t.Cleanup` truncates `loans`, `reservations`, `fines`, `categories`, `copies`, `books`, `members` between tests).

#### Acceptance Criteria — `.http` activation

- [ ] `.http/saga.http` with a sample chain of requests: `POST /members` (×2), `POST /books`, `POST /books/{bookId}/copies`, `POST /loans` (Alice borrows), `POST /reservations` (Bob reserves), `PATCH /loans/{aliceLoanId}/return`, then `GET /members/{bobId}/loans` (— wait, lending doesn't expose a list-loans-for-member HTTP endpoint in Phase 3; it does expose `lending.Facade.ListLoansFor` now in Phase 4 Slice 1 but not yet through HTTP). **Refinement**: add `GET /members/{memberId}/loans` to `internal/lending/http/handlers.go` + `internal/lending/module.go` in Slice 1 since `ListLoansFor` is added there. **Recorded as a minor extension of Slice 1**; the AC text in Slice 1's "new lending-facade listing methods" block already covers the facade method but does NOT cover the HTTP route. **Update**: add a sub-AC in Slice 1 below saying "Slice 6 will reference this method via HTTP; Slice 1 adds `GET /members/{memberId}/loans` to lending's HTTP routes returning `[]LoanResponse`." Or alternatively the saga.http file uses direct DB inspection notes ("after the PATCH, run `psql -c 'SELECT * FROM loans WHERE member_id = ...'`"). Recommended: **add the HTTP route in Slice 1**; it's three lines of code.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go test -tags=integration ./test/crucial_path/saga_integration_test.go` green and completes in under 30 s.
- [ ] `go test -tags=integration ./...` totals under 120 s (Phase 1 healthz + Phase 2 catalog + membership + Phase 3 lending + Phase 4 tx-bun + fines + categories + saga).
- [ ] No `init()` introduced.
- [ ] `app.Wired` exposes `AutoLoanConsumer` for test introspection (so the saga integration test can verify `consumer.IsStarted()` if such a helper exists — actually, the started state is internal; the test doesn't need to assert it directly because the behavioural ACs cover correctness).

---

## File Map

| Slice | Files created (or significantly modified) |
| --- | --- |
| 1 | `internal/lending/types.go` (extended: 4 new event types), `internal/lending/reservation_repository.go` (extended: `ListPendingReservationsForBook`), `internal/lending/in_memory_reservation_repository.go` (extended), `internal/lending/in_memory_reservation_repository_test.go` (extended), `internal/lending/bun_reservation_repository.go` (extended), `internal/lending/bun_reservation_repository_test.go` (extended — `//go:build integration`), `internal/lending/facade.go` (extended: `ListLoansFor`, `ListOverdueLoans`), `internal/lending/facade_test.go` (extended: 2 listing scenarios), `internal/lending/http/handlers.go` (extended: `ListLoansFor` handler returning `[]LoanResponse`), `internal/lending/http/handlers_test.go` (extended: list-loans-for-member AC), `internal/lending/module.go` (extended: `GET /members/{memberId}/loans` route), `internal/lending/auto_loan_on_return.go` (NEW: consumer struct + lifecycle + saga flow), `internal/lending/auto_loan_on_return_test.go` (NEW: behavioural scenarios + scene helper + `throwingOnceLendingFacade` decorator) |
| 2 | `internal/lending/auto_loan_on_return_test.go` (extended: 4 canonical atomicity scenarios + per-book concurrency test under `-race`) — no production-code changes |
| 3 | `internal/fines/types.go`, `internal/fines/schema.go`, `internal/fines/repository.go`, `internal/fines/in_memory_repository.go`, `internal/fines/in_memory_repository_test.go`, `internal/fines/sample_data.go`, `internal/fines/configuration.go`, `internal/fines/facade.go`, `internal/fines/facade_test.go` |
| 4 | `migrations/0004_fines.sql`, `migrations/atlas.sum` (regenerated), `internal/fines/bun_repository.go`, `internal/fines/bun_repository_test.go` (`//go:build integration`), `internal/fines/http/dto.go`, `internal/fines/http/mapping.go`, `internal/fines/http/handlers.go`, `internal/fines/http/handlers_test.go`, `internal/fines/module.go`, `internal/app/wiring.go` (modified: fines facade construction + viper config load + error registry + `Wire` call + `app.Wired.FinesFacade`), `test/crucial_path/fines_integration_test.go` (`//go:build integration`), `.http/fines.http` |
| 5 | `migrations/0005_categories.sql`, `migrations/atlas.sum` (regenerated), `internal/categories/types.go`, `internal/categories/schema.go`, `internal/categories/repository.go`, `internal/categories/in_memory_repository.go`, `internal/categories/in_memory_repository_test.go`, `internal/categories/bun_repository.go`, `internal/categories/bun_repository_test.go` (`//go:build integration`), `internal/categories/sample_data.go`, `internal/categories/configuration.go`, `internal/categories/facade.go`, `internal/categories/facade_test.go`, `internal/categories/http/dto.go`, `internal/categories/http/mapping.go`, `internal/categories/http/handlers.go`, `internal/categories/http/handlers_test.go`, `internal/categories/module.go`, `internal/app/wiring.go` (modified: categories facade construction + error registry + `Wire` call + `app.Wired.CategoriesFacade`), `test/crucial_path/categories_integration_test.go` (`//go:build integration`), `.http/categories.http` |
| 6 | `internal/app/wiring.go` (modified: `AutoLoanConsumer` construction + `Start(ctx)` + `app.Wired.AutoLoanConsumer` + `Close` extension to call `Stop(ctx)`), `test/crucial_path/saga_integration_test.go` (`//go:build integration`), `.http/saga.http` |

No file is created in more than one slice. Slice 1 extends three Phase-3 lending files (`types.go`, `reservation_repository.go`, `facade.go`, `facade_test.go`, plus the bun + in-memory repo files and HTTP handlers); each extension is additive. Slice 2 extends only one file (`auto_loan_on_return_test.go`). Slices 3 + 4 + 5 each ship a fresh module's files. Slice 6 is the only slice that touches `wiring.go` for the consumer lifecycle.

**Slice-ordering note**: Slices 1 → 2 run in declared order (Slice 2 layers on Slice 1's scaffolding). Slices 3 → 4 must be sequential (Slice 4 depends on Slice 3's facade + types). Slice 5 is independent of Slices 1–4 (categories has no cross-module dependencies); it CAN run in parallel with Slice 4 if a team chooses, but the declared order is sequential. Slice 6 depends on Slices 1 (consumer code exists) + 4 (fines wiring exists in the composition root) + 5 (categories wiring exists). The fastest path through Phase 4 in calendar time is Slice 1 → Slice 2 → Slice 5 (parallel) + Slice 3 → Slice 4 → Slice 6; the slowest is strict sequential 1 → 6.

## Idiom Enforcement (every slice must follow)

Every slice in Phase 4 follows these conventions. Carried forward from Phase 1/2/3; new Phase-4 additions called out at the end.

- **Manual constructor wiring.** No `wire`, no `fx`. `internal/app/wiring.go` constructs the fines facade, categories facade, and auto-loan consumer explicitly with named deps.
- **HTTP DTOs live in `<module>/http/dto.go`** and never escape that sub-package. Phase 4 introduces `internal/fines/http/` and `internal/categories/http/` following the rule.
- **Stdlib testing only.** No `testify`. All Phase-4 tests use `t.Run`, `t.Errorf`, `t.Fatalf`, `errors.As`, `errors.Is`. Test slog handlers for log-record assertions are inline test helpers (~10 lines) in the test files that need them.
- **Hand-written validation.** No `go-playground/validator`. Phase 4's `ParseFineId`, `ParseNewCategory`, `ParseStartsWith` are 3-5 lines each.
- **testcontainers-go reaches podman via `DOCKER_HOST`.** Already established.
- **No mocks in tests.** Spec-local decorator wrappers (unexported, declared in the test file) for fault injection. Phase 4 introduces `throwingOnceLendingFacade` (in `auto_loan_on_return_test.go`); reuses `throwingOnceReservationRepository` from Phase 3 (already declared in `lending/facade_test.go`, accessible to `auto_loan_on_return_test.go` because both files share the `lending` package).
- **`log/slog` everywhere.** Every Phase-4 component takes a `*slog.Logger` constructor parameter. The saga consumer logs structured records per step (`step=claim_reservation`, `step=borrow`, `step=unfulfil`, `step=publish_auto_loan_opened`, `step=publish_auto_loan_failed`) with `book_id`, `reservation_id`, `member_id`, `error` attributes when relevant.
- **Functional options for sample data.** Phase 4 adds `SampleNewFine` and `SampleNewCategory`.
- **No `init()` for module wiring.** Verified per slice by absence of any `init()` in `internal/lending/auto_loan_on_return.go`, `internal/fines/`, `internal/categories/`.
- **Pointer-receiver errors.** `*FineNotFoundError`, `*FineAlreadyPaidError`, `*InvalidFineError`, `*CategoryNotFoundError`, `*DuplicateCategoryError`, `*InvalidCategoryError`, `*InvalidCategoriesQueryError` — all match Phase 1/2/3 convention.
- **Source-fidelity names.** Match TS source 1:1: `AutoLoanOpened` (not `AutoLoanOpened` lol; actually `AutoLoanOpened` IS the TS name — confirmed); `AutoLoanFailed` (TS: `AutoLoanFailed`); `ReservationFulfilled` (TS: `ReservationFulfilled`); `ReservationUnfulfilled` (TS: `ReservationUnfulfilled`); `FineAssessed` (TS: `FineAssessed`); `MemberAutoSuspended` (TS: `MemberAutoSuspended`); `FineNotFoundError` (TS: `FineNotFoundError`); `FineAlreadyPaidError` (TS: `FineAlreadyPaidError`); `CategoryNotFoundError` (TS: `CategoryNotFoundError`); `DuplicateCategoryError` (TS: `DuplicateCategoryError`). Field-order on event types matches TS source 1:1 — `AutoLoanOpened{BookId, LoanId, MemberId, ReservationId, OpenedAt}`; `AutoLoanFailed{BookId, ReservationId, MemberId, Reason, FailedAt}`; `FineAssessed{FineId, MemberId, LoanId, AmountCents, AssessedAt}`. Per `.claude/MEMORY.md` source-fidelity rule.
- **Defensive slice/struct copies in in-memory repos.** Phase 4's `InMemoryFineRepository` and `InMemoryCategoryRepository` follow.
- **Bun repository: `var _ Repository = ...` guard + `(nil, nil)` on `sql.ErrNoRows`.** Phase 4's bun fine + category repos follow.
- **`DisallowUnknownFields` on JSON decoders.** Phase 4's `CreateCategoryRequest` decoder uses it.
- **Handlers return `error` → `sharedhttp.Handle`.** Phase 4's fines + categories handlers follow.
- **TransactionalContext is the single entry point for any business operation that writes to >1 aggregate atomically.** Phase 3 established; Phase 4's saga consumer is the canonical demonstration — claim and un-fulfil each open their own `TransactionalContext` via the injected factory. Fines does NOT integrate with `TransactionalContext` (its writes are single-aggregate per fine, no event-with-write atomicity needed); recorded as Open Question 2.
- **Cross-module facade reads happen BEFORE the tx opens.** Fines reads from `lending` and `membership` before issuing its repo write.
- **Post-commit side effects (cross-module mutations called from a facade method) go OUTSIDE `tx.Run`.** Fines' auto-suspend calls `membership.Suspend` AFTER its own per-fine writes — though since fines does NOT use `tx.Run`, the rule reduces to "auto-suspend happens after the per-fine save completes."

**New Phase-4 conventions** (carry forward to Phase 5+):

- **Saga consumers subscribe to events via `bus.Subscribe(eventType, handler)` and run their own tx via the injected tx factory.** The consumer owns a `TransactionalContextFactory` constructor-injected; each saga step (`claimReservation`, `tryUnfulfilClaim`) constructs a fresh context per call. No `TransactionalContext` is shared across saga steps within the same consumer run — the un-fulfil opens its own fresh context independent of the claim's context.
- **Per-aggregate serialisation via a `sync.Mutex` map.** The saga consumer keyed by `BookId` (in the auto-loan-on-return case). Lazy allocation, no pruning, bounded by the catalog size. Phase 5+ consumers MAY use the same pattern keyed by other aggregate ids.
- **Saga consumers are started/stopped EXPLICITLY by the composition root.** No goroutines started from constructors. `Start(ctx)` subscribes to the bus; `Stop(ctx)` unsubscribes. `Wire` calls `Start` after route mounting; `Close` calls `Stop` before closing the DB.
- **Saga consumers swallow their own errors — they don't propagate to the bus.** The bus handler always returns nil. Errors are logged at `error` or `warn` level with structured fields. The downstream publisher (e.g. `lending.ReturnLoan` whose `bus.Publish(LoanReturned)` triggered the consumer) sees success regardless of the consumer's internal outcome.
- **`AutoLoanFailed` (and equivalent failure-signal events in future sagas) is published OUTSIDE any tx.** Even when the un-fulfil tx rolls back, the failure remains observable on the bus. This is the canonical invariant the four atomicity tests in Slice 2 lock in.
- **Cross-module facade READS in a non-saga module follow the same rule as Phase 3.** Fines reads from `lending.ListLoansFor`, `lending.ListOverdueLoans`, `membership.FindMember` BEFORE issuing its repo write or bus publish.

## Definition of Done — Phase 4

Phase 4 is done when **all** of the following are true. Each item is verified manually (developer laptop) or by `task test` / `task test:integration`.

### Functional

- [ ] `task up && task migrate:apply && task run` boots the server on port 3000 with all five migrations applied. `task migrate:status` reports `0001_catalog.sql`, `0002_membership.sql`, `0003_lending.sql`, `0004_fines.sql`, `0005_categories.sql` as applied.
- [ ] All Phase 1/2/3 endpoints still work (regression).
- [ ] `curl -X POST localhost:3000/loans -d '{"memberId":"<aliceId>","copyId":"<copyId>"}'` returns 201 + `LoanResponse` (Phase 3 carry-over).
- [ ] `curl -X POST localhost:3000/reservations -d '{"memberId":"<bobId>","bookId":"<bookId>"}'` for the same book returns 201 + `ReservationResponse`.
- [ ] `curl -X PATCH localhost:3000/loans/<aliceLoanId>/return` returns 200 + `LoanResponse`. **Then**: `curl localhost:3000/members/<bobId>/loans` returns `[LoanResponse{copyId: <copyId>, returnedAt: null}]` — Bob got the auto-loan; the copy is back on his loan. `curl localhost:3000/copies/<copyId>` (— wait, no such endpoint; verify via DB: `SELECT status FROM copies WHERE copy_id = $1` returns `UNAVAILABLE`).
- [ ] Subscribing to `wired.Bus` for `AutoLoanOpened` BEFORE the PATCH and reading after the PATCH returns one event with `BookId`, `MemberId == bob.memberId`, `ReservationId == bob's reservation id`.
- [ ] `curl -X POST localhost:3000/members/<memberId>/fines/assessments` returns 200 + `[]FineResponse` (possibly empty for a member with no overdue loans).
- [ ] After fabricating an overdue loan (direct DB UPDATE on `loans.due_date`), `POST /members/<memberId>/fines/assessments` returns a non-empty `[]FineResponse` with one fine entry per overdue loan.
- [ ] `curl -X POST localhost:3000/fines/batch/process` returns 204.
- [ ] `curl -X GET localhost:3000/members/<memberId>/fines` returns 200 + `[]FineResponse`.
- [ ] `curl -X GET localhost:3000/fines/<fineId>` returns 200 + `FineResponse`.
- [ ] `curl -X PATCH localhost:3000/fines/<fineId>/paid` returns 200 + `FineResponse` with `paidAt` populated.
- [ ] `curl -X POST localhost:3000/categories -d '{"name":"Fiction"}'` returns 201 + `CategoryResponse{id, name:"Fiction", createdAt}`.
- [ ] `curl -X POST localhost:3000/categories -d '{"name":"FICTION"}'` returns 409 + `{"error":"duplicate_category"}` (case-insensitive uniqueness).
- [ ] `curl "localhost:3000/categories?startsWith=fi"` returns 200 + `[CategoryResponse{name:"Fiction"}]`.
- [ ] `curl localhost:3000/categories/<id>` returns 200 + `CategoryResponse`.
- [ ] An unknown `fineId` in `GET /fines/<fineId>` returns 404 + `{"error":"fine_not_found"}`.
- [ ] An unknown `categoryId` in `GET /categories/<id>` returns 404 + `{"error":"category_not_found"}`.
- [ ] Paying an already-paid fine returns 409 + `{"error":"fine_already_paid"}`.

### Saga atomicity invariants (the four canonical claims)

- [ ] **Invariant 1 — Claim tx rolls back staged `ReservationFulfilled`.** When the claim's reservation save errors, the staged `ReservationFulfilled` is never published, the reservation row in the underlying state is unchanged, and the consumer logs the error at `error` level.
- [ ] **Invariant 2 — Un-fulfil tx rolls back staged `ReservationUnfulfilled`.** When the un-fulfil's reservation save errors, the staged `ReservationUnfulfilled` is never published, the reservation row stays `FulfilledAt != nil` (claim committed, un-fulfil rolled back), and the consumer logs the error.
- [ ] **Invariant 3 — `AutoLoanFailed` fires OUTSIDE any tx.** When the un-fulfil tx rolls back (Invariant 2 setup), `AutoLoanFailed` is STILL published on the bus, carrying the ORIGINAL borrow error message (not the un-fulfil error).
- [ ] **Invariant 4 — Per-`BookId` serialisation prevents double-fulfilment.** Under two concurrent returns of two copies of the same book (with one pending reservation), exactly ONE loan opens for the reserver, exactly ONE `ReservationFulfilled` event fires, exactly ONE `AutoLoanOpened` event fires. The test runs under `-race` with no race-detector violation.

### Lifecycle invariants

- [ ] `Start(ctx)` is idempotent — calling twice is a no-op (one subscription).
- [ ] `Stop(ctx)` is idempotent — calling twice is a no-op.
- [ ] `Start` after `Stop` re-subscribes — subsequent `LoanReturned` events trigger the handler.
- [ ] The composition root calls `consumer.Start(ctx)` after `Wire` and `consumer.Stop(ctx)` before closing the DB in `Close`.

### Saga event chain (happy path)

- [ ] After a `PATCH /loans/{id}/return` for a loan whose book has one eligible pending reservation, the bus emits in order: `LoanReturned` (from `ReturnLoan`'s direct publish) → `ReservationFulfilled` (from the claim tx commit) → `LoanOpened` (from the consumer's `lending.Borrow`'s staged event publish) → `AutoLoanOpened` (from the consumer's direct publish after `lending.Borrow` returns).
- [ ] After a `PATCH /loans/{id}/return` for a loan whose book has no pending reservations, the bus emits only `LoanReturned`.
- [ ] After a `PATCH /loans/{id}/return` for a loan whose book has only ineligible reservations queued, the bus emits only `LoanReturned`.

### Fines invariants

- [ ] `AssessFinesFor(memberId, now)` is idempotent — calling twice for the same memberId at the same `now` returns one assessed slice the first time and an empty slice the second time. The repo row count for that member's fines is the same after both calls.
- [ ] `ProcessOverdueLoans(now)` iterates distinct memberIds and assesses each — verified by a multi-member overdue scenario where every member gets fines.
- [ ] Auto-suspend fires only when `total unpaid >= threshold` AND the member is not already suspended.
- [ ] `MemberAutoSuspended` event payload carries `TotalUnpaidCents`, `ThresholdCents`, `SuspendedAt`.
- [ ] `PayFine` is idempotent in shape but not in fact — paying an already-paid fine returns `*FineAlreadyPaidError`, not a duplicate update.
- [ ] `FineAssessed` event publishes per assessed fine.

### Categories invariants

- [ ] `CreateCategory` rejects blank names and names > 100 chars at the schema layer.
- [ ] Duplicate names (case-insensitive) return `*DuplicateCategoryError` — enforced at both the in-memory and Postgres layers.
- [ ] `ListByPrefix` is case-insensitive.
- [ ] `FindCategoryById` for an unknown id returns `*CategoryNotFoundError`.

### Test suite

- [ ] `task test` (unit, no build tags) is green and completes in well under 1.5 seconds (target: under 1 s). Includes: Phase 1/2/3 unit tests + auto-loan consumer unit tests (~25 scenarios) + fines facade tests (~15 scenarios) + categories facade tests (~10 scenarios) + handler tests for both modules.
- [ ] `task test -race` (unit with race detector) is green. The auto-loan per-book serialisation test (Slice 2) MUST run under `-race`.
- [ ] `task test:integration` is green and completes in under 150 seconds on a developer laptop including testcontainers cold start. Includes: Phase 1/2/3 integration tests + fines bun-repo + fines crucial-path + categories bun-repo + categories crucial-path + saga crucial-path + the new lending bun-reservation listing tests.

### Quality + hygiene

- [ ] `task fmt` and `task lint` pass with zero output.
- [ ] No new third-party direct dep beyond what Phase 3 already pulled in. Phase 4 introduces no new library dependency — bun, slog, events, uuid, viper are already there.
- [ ] No `init()` function in any Phase-4 file.
- [ ] No file under `internal/lending/` imports a forbidden module per `.claude/BOUNDARIES.md` (forbidden: `internal/fines`, `internal/categories`, `internal/chat`).
- [ ] No file under `internal/fines/` imports `internal/categories` or `internal/chat`.
- [ ] No file under `internal/categories/` imports `internal/lending`, `internal/membership`, `internal/catalog`, `internal/fines`, `internal/chat`, `internal/accesscontrol`, or `internal/shared/tx`. Categories is a leaf module.
- [ ] Every TS scenario in `auto-loan-on-return.consumer.spec.ts` that covers happy path, eligibility cascade, lifecycle, AutoLoanOpened payload, failure policy, claim-tx atomicity, un-fulfil-tx atomicity, and per-book concurrency has a Go counterpart in `internal/lending/auto_loan_on_return_test.go`. Verified by reading the two files side by side.
- [ ] Every TS scenario in `fines.facade.spec.ts` that covers `assessFinesFor`, `processOverdueLoans`, `listFinesFor`, `findFine`, `payFine` has a Go counterpart in `internal/fines/facade_test.go`.
- [ ] Every TS scenario in `categories.facade.spec.ts` that covers `createCategory`, `findCategoryById`, `listByPrefix` has a Go counterpart in `internal/categories/facade_test.go`.
- [ ] `.claude/BOUNDARIES.md` reflects Phase 4's new modules: `lending → accesscontrol, catalog, membership, shared/events, shared/tx, shared/db, shared/http`; `fines → lending, membership, shared/events, shared/db, shared/http`; `categories → shared/db, shared/http`.

## Open Questions

These are decisions a developer should make before Slice 1 begins, OR record explicitly as accepted defaults. All have a recommended default aligned with discovery + source fidelity; flag any disagreement before slicing.

1. **Does `fines` subscribe to `LoanReturned`?** The discovery doc line 99 says fines "Subscribes to `LoanReturned` to assess overdue fines (port the source's behaviour)". The TS source does NOT subscribe to `LoanReturned` from fines — its assessment is explicit/batched via `POST /members/{memberId}/fines/assessments` and `POST /fines/batch/process`. The discovery doc's phrasing appears to be inaccurate about the source behaviour. **Recommended default: match TS source — NO `LoanReturned` subscriber in fines.** The fines facade exposes the two assessment endpoints; an operator (or, in Phase 5+, a cron) triggers them. **Flag if you want fines to also auto-assess on `LoanReturned`** — that would be a deviation from TS source and would couple fines to the lending event bus in a way the TS source deliberately avoids. (The discovery doc's Slice 4 says "fines saga: when a loan returns past due-date, fines facade stages an AssessFine in its own tx" — this is more of a design pivot than a port. Recommended default: drop the saga-style coupling; ship the two explicit endpoints; revisit in Phase 5+ if a real auto-assessment trigger is needed.)
2. **Does `fines` use `TransactionalContext`?** Fines writes single-aggregate (one `fines` row per assessment) and publishes one event per write. The TS source does NOT use a tx for fines — it does `await this.repository.saveFine(fine); await this.bus.publish(...)` in sequence. There is no event-with-write atomicity (a crash between save and publish drops the event — same outbox-shaped gap Phase 3 documented). **Recommended default: match TS source — NO `TransactionalContext` integration in fines.** The save-then-publish pattern is sufficient for the demo's invariants; the known gap is identical to Phase 3's accepted gap. **Flag if you want fines wrapped in `TransactionalContext`** for the additional atomicity guarantee; it would require extending `FineRepository.SaveFine` to take a `txc` parameter and would diverge from TS source by ~20 lines.
3. **Does `MemberAutoSuspended` belong in the `fines` package or the `membership` package?** The TS source declares it in `fines.types.ts` because fines triggers it. The Go port matches — declared in `internal/fines/types.go`, imported by `internal/fines/facade.go` to construct + publish. The membership module is the WRITE target (`membership.Suspend`) but not the event owner. **Recommended default: declare in `internal/fines/types.go`.** This matches TS source and keeps the event close to its producer.
4. **Should fines + categories handlers call `accesscontrol.Authorize`?** TS source does NOT authorize at the facade layer for either module (they're admin endpoints in the demo's model). **Recommended default: match TS source — no `accesscontrol.Authorize` calls in `fines.Facade` or `categories.Facade`.** The handlers don't pass an `AuthUser` at all — they operate on memberId path params directly. **Flag if you want admin-role enforcement**; it would require extending `accesscontrol.policy.go` with `fines.assess`, `fines.process`, `categories.create` actions + `RoleAdmin` (currently not declared) and would require the handlers to extract an admin token. This is real auth work, deferred to a Phase 5+ "real auth" milestone.
5. **Should `app.Wired` expose `AutoLoanConsumer`?** Phase 4 needs it in `Close` to call `Stop`. **Recommended default: yes** — extend `app.Wired` with `AutoLoanConsumer *lending.AutoLoanOnReturnConsumer` (the same pattern as Phase 3's `LendingFacade`, `Bus`, `DB` exposure). Tests don't need to introspect it directly (the behavioural assertions cover correctness), but the composition root needs the handle to call `Stop` in graceful shutdown. **Locked.**
6. **Add `GET /members/{memberId}/loans` HTTP route in Phase 4 Slice 1?** Phase 3 deferred all listing endpoints. Slice 1 adds `Facade.ListLoansFor`; the saga integration test (Slice 6) needs an HTTP-level way to assert "Bob got the auto-loan." Two options: (a) add the HTTP route in Slice 1; (b) assert via direct facade call in the test (`wired.LendingFacade.ListLoansFor(ctx, bobId)`). **Recommended default: option (a) — add the HTTP route.** It's three lines of code and unlocks `GET /members/{memberId}/loans` for `.http/saga.http` and any future external client. **Locked as the AC in Slice 1 says.**
7. **Add `GET /members/{memberId}/reservations` HTTP route?** Same question for reservations. The saga integration test could benefit from asserting "Bob's reservation is fulfilled" through HTTP. **Recommended default: NO — defer** to a hypothetical Phase 5+ "completeness pass". Direct DB inspection or direct facade call is sufficient in the integration test. The TS source ships such endpoints; the Go port is allowed to be less complete on read endpoints since the architectural points are made on the write paths. **Flag if you want it.**
8. **Per-book mutex map pruning.** The `bookLocks` map grows monotonically (one `*sync.Mutex` per ever-seen book). For a small demo this is bounded by the catalog size; in a real deployment with millions of books it would be unbounded memory growth. **Recommended default: do NOT prune; document the gap.** Phase 5+ can swap to a `sync.Map`-with-LRU or a sharded approach. **Locked.**
9. **`FinesConfig` env var names.** The defaults `FINES_DAILY_RATE_CENTS=25` and `FINES_SUSPENSION_THRESHOLD_CENTS=1000` match TS source values. The TS source's env vars are named `FINES_DAILY_RATE_CENTS` and `FINES_SUSPENSION_THRESHOLD_CENTS` already. **Recommended default: same names.** **Locked.**
10. **Categories `name` length limit.** The TS source's `parseNewCategory` rejects blank but does NOT limit length. The Go port spec proposes a 100-char limit as a defensive default. **Recommended default: 100-char limit** (defensive vs the TS source's open-ended). **Flag if you want to match TS source exactly** (no length cap) — the deviation is benign.
11. **`POST /fines/batch/process` HTTP code.** TS source returns 204 (no body). Go port matches. The handler's `error` return goes through `sharedhttp.Handle` so success is signalled by `w.WriteHeader(204)` + no body. **Locked.**
12. **Should the saga integration test (Slice 6) use a synchronous bus assertion or a polling assertion?** The in-memory bus is synchronous — `bus.Publish` returns only after all handlers complete. So by the time `PATCH /loans/{id}/return` returns 200, the consumer's auto-loan chain is fully complete. The test can assert state immediately without polling. **Recommended default: synchronous assertion.** **Flag if you suspect any async path** — there isn't one in the in-memory bus, and we don't ship an async bus implementation. **Locked.**

## Phase 4 → Phase 5 Handoff

When Phase 5 starts, the spec-builder for that phase can assume:

1. The dev loop works — `task up`, `task migrate:apply`, `task run`, `task test`, `task test:integration`, `task test -race` are stable. Five business migrations are in `migrations/`.
2. `internal/lending.AutoLoanOnReturnConsumer` is the canonical saga consumer reference. Its shape — owned-tx-factory + per-aggregate-mutex-map + explicit Start/Stop + claim-first / un-fulfil / publish-outside-tx pattern — is the template any future saga consumer follows. Phase 5's chat module does NOT introduce a saga consumer (chat is a leaf module with no event subscriptions), but if Phase 5+ adds further sagas, the template is locked.
3. `internal/fines.Facade` exposes `AssessFinesFor`, `ProcessOverdueLoans`, `ListFinesFor`, `FindFine`, `PayFine`. Phase 5 does not extend fines.
4. `internal/categories.Facade` exposes `CreateCategory`, `FindCategoryById`, `ListByPrefix`. Phase 5 does not extend categories.
5. Events `LoanOpened`, `LoanReturned`, `ReservationQueued`, `ReservationFulfilled`, `ReservationUnfulfilled`, `AutoLoanOpened`, `AutoLoanFailed` are declared in `internal/lending/types.go`. Events `FineAssessed`, `MemberAutoSuspended` are declared in `internal/fines/types.go`. No new events are introduced in Phase 5 unless chat needs them (it doesn't — chat is a synchronous request/stream pattern, no domain events).
6. `internal/app.Wired` carries `Router`, `DB`, `Bus`, `CatalogFacade`, `MembershipFacade`, `LendingFacade`, `FinesFacade`, `CategoriesFacade`, `AutoLoanConsumer`, `Close`. Phase 5 extends with `ChatFacade`. `Close` already orchestrates consumer-stop then DB-close; Phase 5 inserts chat's own lifecycle hooks if needed (chat may have no lifecycle hooks at all — depends on whether the OpenAI client needs explicit close).
7. `internal/app/wiring.go`'s `buildDomainErrorRegistry` registers all Phase 1/2/3 domain errors plus Phase 4's `fine_not_found`, `fine_already_paid`, `invalid_fine`, `category_not_found`, `duplicate_category`, `invalid_category`, `invalid_categories_query`. Phase 5 extends with chat-specific errors (TBD by the chat spec).
8. The per-module file convention is now battle-tested across five modules (catalog + membership + lending + fines + categories). The chat module in Phase 5 follows the same template; the only deviation is that chat exposes an SSE handler rather than a JSON-response handler — handled in `internal/chat/http/handlers.go` via `http.Flusher` assertion.
9. The "no-mocks, in-memory doubles + spec-local Throwing-Once decorator wrappers" discipline is locked. Phase 5's chat tests declare unexported test decorators inside the chat test file as needed.
10. `accesscontrol.Authorize` is called from `lending.Borrow` only. Phase 5 MAY add `accesscontrol.Authorize` calls to chat if `chat.send` needs role-gating; the policy table in `accesscontrol/policy.go` already supports adding new module/action rows.
11. The post-commit publish pattern and the saga atomicity invariants are documented in the `TransactionalContext` doc comment, in each of the lending facade methods' doc comments, AND in `internal/lending/auto_loan_on_return.go`'s package + struct doc comments. Phase 5's SAGA.md port (Phase 5 Slice 6) references this canonical doc tree.

No Phase 4 file or AC needs to change to enable Phase 5.

[ ] Reviewed
