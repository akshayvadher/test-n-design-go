# Architecture

This document describes how the codebase is organised and why. It is the answer to "where does X live, and what is X allowed to depend on?"

The architecture is a **modular monolith with facades**: one Go package per bounded context, with the facade as the only exported surface. The pattern stays the same whether the module owns persistent state or not, whether it exposes HTTP routes or not, whether it integrates with an external service or not. Per-module variation is in *which files exist*, not in *what those files do*.

## Module = package = directory

Every business module lives under `internal/<module>/` and looks like this:

```
internal/<module>/
├── facade.go              *Facade struct + exported methods (domain DTOs only)
├── types.go               domain DTOs + domain errors
├── schema.go              ParseX functions (only if module accepts external input)
├── repository.go          port interface (only if module owns state)
├── in_memory_repository.go
├── bun_repository.go      only if Postgres adapter exists
├── configuration.go       NewXxxFacadeWithOverrides — the test-substitution seam
├── sample_data.go         SampleX(opts ...) functional-option builders
├── module.go              Wire(r, deps) — only if module exposes HTTP routes
├── facade_test.go         facade-level spec (in-memory substrates)
└── http/                  only if module exposes HTTP routes
    ├── dto.go             JSON wire types
    ├── mapping.go         HTTP DTO ↔ domain DTO mapping
    ├── handlers.go        http.HandlerFunc per endpoint
    └── handlers_test.go
```

This is a **menu, not a checklist**. A module includes only the files it needs. The convention exists so that *when* a module declares a file with a given name, it follows the established shape. Categories ships eleven of these files (no events, no transactions); lending ships every file plus `auto_loan_on_return.go` and `wiring.go`.

The full per-module dependency allowlist lives in [`.claude/BOUNDARIES.md`](.claude/BOUNDARIES.md). Violations are caught by reading the import block at the top of each file — the architecture does not lean on a linter.

## The facade pattern

The facade is the only public surface of a module. Every business operation goes through one of its exported methods. Repositories, schemas, sample data and HTTP DTOs are exported only so composition-root wiring and same-package tests can reference them — never so other modules can.

From `internal/categories/facade.go`:

```go
type Facade struct {
    repository CategoryRepository
    newID      func() string
    clock      func() time.Time
    logger     *slog.Logger
}

func NewFacade(repository CategoryRepository, newID func() string, clock func() time.Time, logger *slog.Logger) *Facade {
    return &Facade{
        repository: repository,
        newID:      newID,
        clock:      clock,
        logger:     logger,
    }
}

func (f *Facade) CreateCategory(ctx context.Context, name string) (CategoryDto, error) { ... }
func (f *Facade) FindCategoryById(ctx context.Context, id CategoryId) (CategoryDto, error) { ... }
func (f *Facade) ListByPrefix(ctx context.Context, prefix string) ([]CategoryDto, error) { ... }
```

Three properties hold for every facade:

- **Dependencies are explicit and minimal.** The constructor names every collaborator. There is no `slog.Default()`, no global clock, no package-level `db`. The composition root wires the real implementations; tests wire in-memory substitutes via `NewFacadeWithOverrides`.
- **Methods take and return domain DTOs.** Wire-format JSON shapes live in `<module>/http/dto.go` and never escape that sub-package. The facade's method signatures are a stable Go API that the HTTP layer happens to consume; tests bypass HTTP entirely.
- **Cross-module deps are facade-to-facade.** Lending depends on `*catalog.Facade` and `*membership.Facade` — never on `catalog.Repository` or `membership.MemberRow`. If a downstream module needs batch lookups, the upstream module exposes a batch method on its facade (e.g. `catalog.GetBooks(ids)`).

The same shape applies to gateways:

```go
// internal/chat/facade.go
type Facade struct {
    gateway chatgateway.ChatGateway
    logger  *slog.Logger
}
```

`chatgateway.ChatGateway` is an interface; `InMemoryChatGateway` is the default; `OpenAIChatGateway` is the production impl. The composition root picks one. The facade does not know which.

## Repositories: port and impls

Modules that own state declare a single `Repository` port and ship at least one in-memory implementation. Production modules also ship a `bun_repository.go`.

```go
// internal/categories/repository.go
type CategoryRepository interface {
    Save(ctx context.Context, category CategoryDto) error
    FindById(ctx context.Context, id CategoryId) (*CategoryDto, error)
    FindByNamePrefix(ctx context.Context, prefix string) ([]CategoryDto, error)
}
```

The contract is documented next to the interface: `FindById` returns `(nil, nil)` on miss (the facade is responsible for translating that into `*CategoryNotFoundError`); a non-nil error indicates infrastructure failure and is propagated unchanged. Save translates a name collision into `*DuplicateCategoryError`. The in-memory impl and the bun impl honour the same contract, which is what makes the in-memory impl a legitimate test substitute rather than a mock.

There is no third implementation. There is no abstract base class. The interface exists because two real implementations exist — not as a "future-proofing" gesture.

## HTTP DTO ↔ facade DTO boundary

HTTP wire shapes live in `<module>/http/dto.go`. They are tagged with `json:"..."` and match the API contract verbatim. Domain DTOs live in `<module>/types.go` and use Go-native field names.

The mapping is explicit:

```go
// internal/chat/http/mapping.go
func toFacadeRequest(req ChatRequest) chat.ChatRequest {
    out := chat.ChatRequest{Messages: make([]chat.ChatMessage, len(req.Messages))}
    for i, m := range req.Messages {
        out.Messages[i] = chat.ChatMessage{Role: m.Role, Content: m.Content}
    }
    return out
}
```

Two concrete properties of this rule:

- **HTTP DTOs never escape `<module>/http/`.** A grep for `cataloghttp.BookRequest` outside `internal/catalog/http/` should return nothing.
- **Renaming a JSON field changes one file.** The wire shape is a property of the HTTP package; the domain shape is a property of the module. Schema evolution at the API layer does not ripple into the facade or its tests.

## One transaction per module

Every module that mutates state does so through a `TransactionalContext`:

```go
// internal/shared/tx/transactional_context.go
type TransactionalContext interface {
    Run(ctx context.Context, work func(ctx context.Context) error) error
    Stage(apply func(ctx context.Context) error)
    StageEvent(evt events.DomainEvent)
}
```

Two implementations satisfy the interface. `InMemoryTransactionalContext` buffers staged closures and events during `Run`'s work callback, then applies the writes and publishes the events on successful completion. `BunTransactionalContext` wraps `bun.DB.RunInTx` and executes staged closures immediately inside the tx callback against the live tx handle.

The contract is single-shot: callers construct a fresh `TransactionalContext` per business operation via a `TransactionalContextFactory`, and never share an instance across operations or goroutines. The interface deliberately hides the underlying transaction handle — a `bun.Tx` or `*sql.Tx` must never leak through it.

**A `TransactionalContext` instance is created and used inside ONE module.** This is the architectural rule the directory layout enforces. There is no shared transaction that spans modules. Cross-module consistency happens through events, not through a shared tx.

## Cross-module reads happen before the tx opens

The lending facade's `Borrow` flow demonstrates the read-then-write sequencing:

```go
// internal/lending/facade.go
func (f *Facade) Borrow(ctx context.Context, authUser accesscontrol.AuthUser, copyId catalog.CopyId) (LoanDto, error) {
    if err := f.accessControl.Authorize(authUser, "lending", "borrow"); err != nil {
        return LoanDto{}, err
    }
    if err := f.requireEligible(ctx, membership.MemberId(authUser.MemberID)); err != nil {
        return LoanDto{}, err
    }
    copy, err := f.catalog.FindCopy(ctx, copyId)
    if err != nil {
        return LoanDto{}, err
    }
    if copy.Status != catalog.CopyStatusAvailable {
        return LoanDto{}, &CopyUnavailableError{CopyId: copyId}
    }

    loan := f.buildLoan(membership.MemberId(authUser.MemberID), copy.CopyId, copy.BookId)

    txc := f.txFactory()
    if err := txc.Run(ctx, func(ctx context.Context) error {
        if err := f.loans.SaveLoan(ctx, loan, txc); err != nil {
            return err
        }
        txc.StageEvent(loanOpenedEvent(loan))
        return nil
    }); err != nil {
        return LoanDto{}, err
    }
    ...
}
```

Three cross-module reads (`Authorize`, `CheckEligibility`, `FindCopy`) run **before** `txFactory()` opens the local tx. If any of them fails, no tx is opened and no rollback is needed. Inside the tx, only own-module writes happen (`f.loans.SaveLoan`) plus the staged `LoanOpened` event.

This rule keeps tx scopes narrow and prevents the easy mistake of "I'll just call the other module's facade from inside my tx" — which would either lock for the duration of the cross-module call or perform writes that the local tx can't roll back.

## Post-commit side effects go OUTSIDE Run

The continuation of `Borrow`:

```go
    if _, err := f.catalog.MarkCopyUnavailable(ctx, copyId); err != nil {
        return LoanDto{}, err
    }
    return loan, nil
}
```

`MarkCopyUnavailable` is a cross-module write. It runs **after** `txc.Run` returns nil — never via `txc.Stage`. The sequencing:

1. Own-tx commits, persisting the loan row and publishing `LoanOpened` during commit.
2. `catalog.MarkCopyUnavailable` runs against the catalog module's own tx infrastructure (a different `TransactionalContext` instance, owned by catalog).

A failure in step 2 leaves the loan persisted and `LoanOpened` already on the bus. The caller receives the catalog error. This inconsistency is documented; the teaching repo accepts it rather than introducing a saga for the Borrow flow. The Return flow has a similar shape; the Auto-Loan flow uses a saga to handle the more interesting case (see [SAGA.md](SAGA.md)).

The rule generalises: **`Stage` is for own-module writes that participate in the local tx; cross-module writes are sequenced by the caller after `Run` returns nil.**

## Events as the only cross-module write path

The `EventBus` interface lives in `internal/shared/events/event_bus.go`:

```go
type DomainEvent interface {
    Type() string
}

type EventBus interface {
    Publish(ctx context.Context, evt DomainEvent) error
    Subscribe(eventType string, handler func(ctx context.Context, evt DomainEvent) error) Unsubscribe
}
```

Phase 1 ships only `InMemoryEventBus`. Durable transports (outbox, Redis Streams, Kafka) are deferred — when they arrive they implement `EventBus` and consumers switch transports at the composition root with no module-level changes.

Domain events stage during the publisher's tx via `txc.StageEvent`. They publish AFTER the tx commits, in stage order, via the bus. Subscribers therefore observe events in a state where the publisher's own writes have settled.

The auto-loan saga is the canonical consumer. It subscribes to `LoanReturned` and walks the pending reservation queue for the returned book. See [SAGA.md](SAGA.md) for the full walk-through.

## In-memory test doubles, no mocks

Test substitution is an in-memory implementation of the production port. Fault injection is a spec-local decorator declared in the test file. From `internal/lending/auto_loan_on_return_test.go`:

```go
type throwingOnceReservationRepository struct {
    inner    ReservationRepository
    failOn   string
    failed   bool
}

func (r *throwingOnceReservationRepository) SaveReservation(ctx context.Context, res ReservationDto, txc tx.TransactionalContext) error {
    if !r.failed && string(res.ReservationId) == r.failOn {
        r.failed = true
        return errors.New("simulated save failure")
    }
    return r.inner.SaveReservation(ctx, res, txc)
}
```

Three properties of this pattern:

- **The decorator wraps the real in-memory impl.** It is not a mock — it is the production-shaped implementation with a single fault injected. After the injected failure, subsequent calls behave normally.
- **It is unexported and lives in the test file.** No test framework, no shared mock library. The decorator's lifetime is the single test that needs it.
- **It composes with `errors.As`.** The injected error is a plain `errors.New` so the test asserts via `if err.Error() == "simulated save failure"` — the production code is the thing under test, not a generated mock surface.

`testify` and `gomock` are not in `go.mod`. They will not be added.

## Manual constructor wiring

`internal/app/wiring.go` is the composition root. It owns the order in which dependencies are built, the lifecycle of long-lived resources, and the registration of every domain error type. The flow:

1. Construct shared substrates: bun client, redis client (if `REDIS_URL` is set), book cache, error registry, chi router, event bus.
2. Construct each module's facade in dependency order: catalog → membership → lending (depends on catalog + membership) → fines + categories → chat.
3. Wire each module's HTTP routes onto the chi router.
4. Construct and `Start` long-running consumers — currently just `AutoLoanOnReturnConsumer`.
5. Return a `Wired` struct exposing the router, the facades (so integration tests can introspect), the bus, and a `Close` function that stops consumers then releases substrates.

No DI container, no `wire`, no `fx`. The composition root is a Go function that returns a struct. When the binary boots, it calls `app.Wire(ctx, deps)`. When the integration test harness boots, it calls the same function.

The cost of this approach: extending the wiring requires touching one file. The benefit: every dependency edge is visible by reading top-to-bottom.

## Boundaries enforcement

Every module's allowed imports are listed in [`.claude/BOUNDARIES.md`](.claude/BOUNDARIES.md):

```
- `categories/` — owns: Category, CategoryId, CategoryNotFoundError. Depends on:
    accesscontrol, shared/tx, shared/db, shared/http
- `chat/` — owns: ChatFrame, ChatRequest, InvalidChatRequestError, ChatGatewayError.
    Depends on: shared/chatgateway, shared/http
- `lending/` — owns: Loan, LoanId, Reservation, LoanOpened, LoanReturned, ...
    Depends on: accesscontrol, catalog, membership, shared/events, shared/tx, shared/db, shared/http
```

The rules:

- Imports outside the allowlist are violations.
- Cross-module deps go facade-to-facade. Never reach into another module's repository, types, or HTTP layer.
- No cross-module DB joins. Bun queries inside a module touch only that module's tables.
- HTTP DTOs never escape `<module>/http/`.
- Shared infrastructure (`internal/shared/`) NEVER imports business modules. The dependency direction is strict: `cmd → business modules → shared`.
- No `init()` for module wiring. All wiring is explicit in `internal/app/wiring.go`.

There is no `go-arch-lint` job that enforces these rules. They are enforced by reading the import block at the top of each file during review. The rules are short enough that the cost of reading them exceeds the cost of automating them — and an explicit table beats a brittle linter when the architecture itself is the teaching subject.

## Domain error registry

Every domain error that should map to a non-500 HTTP response is registered once, in `internal/app/wiring.go`:

```go
registry.Register(&catalog.BookNotFoundError{}, http.StatusNotFound, "book_not_found")
registry.Register(&catalog.DuplicateIsbnError{}, http.StatusConflict, "duplicate_isbn")
registry.Register(&lending.CopyUnavailableError{}, http.StatusConflict, "copy_unavailable")
registry.Register(&lending.MemberIneligibleError{}, http.StatusConflict, "member_ineligible")
registry.Register(&chat.InvalidChatRequestError{}, http.StatusBadRequest, "invalid_chat_request")
registry.Register(&chat.ChatGatewayError{}, http.StatusBadGateway, "chat_gateway_error")
```

`DomainErrorMiddleware` walks the registry on every handler error using `errors.As`. Registered errors emit `{"error": "<code>", "message": "<err.Error()>"}` with the registered status; unregistered errors collapse to 500 + `internal_error` with the raw text logged but never reaching the client body.

Two properties of this pattern:

- **Handlers return `error`.** They don't write JSON, they don't set status codes. `sharedhttp.Handle(fn)` stores the returned error into a request-scoped holder; the middleware reads it after `ServeHTTP` returns.
- **Domain errors are pointer types.** `*BookNotFoundError`, `*CopyUnavailableError`, `*ChatGatewayError`. Pointer receivers + `errors.As` is what makes the registry's type-driven lookup work through wrapped errors.

## Streaming responses

The chat module is the one module that doesn't fit the request-response shape. Its handler asserts `http.Flusher`, opens an SSE response, and ranges over a channel:

```go
// internal/chat/http/handlers.go (sketch)
flusher, ok := w.(http.Flusher)
if !ok {
    return errors.New("streaming unsupported")
}
req, err := decodeChatRequest(r.Body)
if err != nil { return &chat.InvalidChatRequestError{Reason: err.Error()} }

frames, err := h.facade.Stream(r.Context(), toFacadeRequest(req))
if err != nil { return err }

w.Header().Set("Content-Type", "text/event-stream")
w.Header().Set("Cache-Control", "no-cache")
w.Header().Set("Connection", "keep-alive")
w.Header().Set("X-Accel-Buffering", "no")
flusher.Flush()

for frame := range frames {
    fmt.Fprintf(w, "event: %s\ndata: %s\n\n", frame.Type, frame.Data)
    flusher.Flush()
}
return nil
```

The pre-stream error path returns through `sharedhttp.Handle`; once the stream has started, errors are written into the stream as `event: error` frames and the handler returns nil. No third-party SSE library; stdlib `http.Flusher` is enough.

## Where to look next

- [SAGA.md](SAGA.md) — the auto-loan saga walk-through, atomicity invariants, and the durability gap.
- [GUIDE.md](GUIDE.md) — how to add a new module. File-by-file template, wiring diff, boundaries diff.
- [`.claude/BOUNDARIES.md`](.claude/BOUNDARIES.md) — the per-module import allowlist.
