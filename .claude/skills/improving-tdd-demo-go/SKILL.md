---
name: improving-tdd-demo-go
description: This skill should be used when adding a new module to the test-n-design-go Go port of the improving-tdd-demo. Contains the per-module file template, the facade pattern, the wiring step, the boundaries step, and the testing conventions. Use when the user says "add a new module", "port the X module from TS", "follow the Go module template", or asks how to extend the codebase.
---

# improving-tdd-demo-go

## When this skill applies

Load this skill when the developer is adding a new business module and needs to conform to the per-module convention this codebase locks in. The skill encodes the file layout, the facade pattern, the `TransactionalContext` rules, the post-commit publish ordering, the saga consumer shape, the boundaries policy, and the testing conventions.

If the developer is adding a shared port (under `internal/shared/<port>/`) rather than a business module, the same conventions apply with three differences: no `accesscontrol` dependency, no `module.go`/`http/` subdir, no domain-error registration.

## The file layout

A business module lives at `internal/<module>/`. The template is a menu — include only the files the module needs. Use `internal/catalog/`, `internal/membership/`, or `internal/fines/` as the canonical reference.

```
internal/<module>/
  types.go                  # Domain DTOs + domain errors (pointer-receiver Error())
  schema.go                 # ParseX(raw) (X, error) — hand-written validation
  repository.go             # Port interface (only if module owns state)
  in_memory_repository.go   # In-mem impl — default for unit tests
  bun_repository.go         # Postgres impl via bun (only if module persists)
  sample_data.go            # SampleX(opts ...XOption) — functional options
  configuration.go          # NewFacadeWithOverrides(Overrides) — test substitution
  facade.go                 # *Facade + NewFacade(deps...) + methods
  facade_test.go            # In-package spec, in-mem substrates, stdlib testing
  http/
    dto.go                  # JSON wire shapes — never escape this package
    mapping.go              # HTTP DTO <-> facade DTO
    handlers.go             # Per-endpoint http.HandlerFunc; return error
    module.go               # Wire(r chi.Router, deps Deps) — composition glue
    handlers_test.go        # External package (http_test); httptest.NewServer
```

Optional: `<module>/<aggregate>_repository.go` when one module owns multiple aggregates (see `internal/lending/loan_repository.go` + `internal/lending/reservation_repository.go`).

## The facade pattern

The facade is the only public surface. Unexported fields keep collaborators encapsulated; the composition root wires them via `NewFacade`; tests substitute them via `NewFacadeWithOverrides`. Every method takes domain DTOs (never HTTP DTOs) and returns domain DTOs + `error`.

Reference: `internal/catalog/facade.go`.

Rules:
- Constructor argument order: cross-module facades first, own-module repositories second, shared substrate (bus, txFactory) third, cross-cutting helpers (newID, clock, logger) last.
- Every facade method takes `ctx context.Context` first.
- Every facade method returns `(T, error)` or just `error` — never panics for business failures.
- `accessControl.Authorize(authUser, "<module>", "<action>")` is the first call in any mutating method when the policy lists the action.

## Hand-written validation

No `go-playground/validator`. Each module declares `ParseX(raw) (X, error)` in `schema.go` that trims, normalises, validates, and returns the typed value plus an `*Invalid<X>Error` on failure. Pointer-receiver `Error()` so `errors.As(err, &target)` matches wrapped errors.

`json.Decoder` always uses `DisallowUnknownFields()` in HTTP handlers.

## The repository port

Two-file pattern: interface in `repository.go`, in-memory impl in `in_memory_repository.go`. The bun adapter (`bun_repository.go`) lands only when the module persists.

Rules:
- `Find*` returns `(nil, nil)` on "no rows". The facade translates that to `*XxxNotFoundError`.
- Non-nil errors indicate infrastructure failure and propagate unchanged.
- In-memory impl is `sync.RWMutex`-guarded and preserves insertion order via a parallel slice.
- Repository methods receive the live tx through `context.Context` (set by `BunTransactionalContext`). Read it with `tx.TxFromContext(ctx)`.
- For bun repos with UUID PKs, catch Postgres SQLSTATE 22P02 and collapse to `(nil, nil)` so a garbage id returns 404 instead of 500. See `internal/fines/bun_repository.go` `isInvalidUUIDSyntax`.

## TransactionalContext

`internal/shared/tx/transactional_context.go` defines the contract every business operation uses to wrap a single atomic write + cross-module event publish. A `TransactionalContext` instance is single-shot.

Rules:
- `txc.Run(ctx, work)` commits if `work` returns nil; rolls back on error and discards staged events.
- `txc.StageEvent(evt)` publishes AFTER commit in stage order. Bus failures log at error level; commit stands.
- Cross-module post-commit mutations (e.g. `catalog.MarkCopyUnavailable` from `lending.Borrow`) run OUTSIDE `Run` — NOT via `Stage`. `Stage` is for own-module writes only.
- In-memory factory wraps `*tx.InMemoryTransactionalContext`; bun factory wraps `*tx.BunTransactionalContext` closing over `*bun.DB`.

## The post-commit publish pattern

`lending.Borrow` (`internal/lending/facade.go`): `LoanOpened` is staged via `txc.StageEvent`, so it publishes DURING commit, BEFORE the post-commit `catalog.MarkCopyUnavailable` call.

`lending.ReturnLoan` (same file): `LoanReturned` is published via `bus.Publish` AFTER `catalog.MarkCopyAvailable` succeeds, NOT via `StageEvent`. The auto-loan saga relies on this: by the time it sees `LoanReturned`, the copy is already AVAILABLE and the downstream `Borrow` succeeds.

Rule: if a saga's downstream action requires the post-commit cross-module mutation to have already happened, publish AFTER that mutation via `bus.Publish`. Otherwise stage it.

## Saga consumer pattern

A saga consumer subscribes to a domain event, owns its own `TransactionalContext` factory, serialises per-aggregate via a `sync.Mutex` map, and has explicit `Start(ctx)`/`Stop(ctx)` lifecycle.

Reference: `internal/lending/auto_loan_on_return.go`.

Rules:
- No `init()` for subscription. Wiring is explicit in `internal/app/wiring.go`, which calls `consumer.Start(ctx)` after route mounting and `consumer.Stop(stopCtx)` from `Close` BEFORE the DB closes.
- `Start` and `Stop` are idempotent.
- Per-aggregate serialisation via `map[<Key>]*sync.Mutex` lazy-allocated under a top-level mutex.
- The bus handler returns `nil` unconditionally. Saga errors log with structured fields and are swallowed so the fanout continues.
- The failure-signal event (`AutoLoanFailed`) publishes OUTSIDE any tx so it's observable even after a rolled-back un-fulfil.

## Manual constructor wiring

No `wire`, no `fx`. All wiring is explicit in `internal/app/wiring.go`. A new module adds four things:

1. Facade construction — call `NewFacadeWithOverrides(Overrides{...})`.
2. Route mounting — call `<module>http.Wire(router, <module>http.Deps{Facade: facade, Logger: logger})`.
3. `Wired` field — add `<Module>Facade *<module>.Facade` so tests can hold a reference.
4. Close path — if the module owns a goroutine, `Start` it after route mounting and add its `Stop` to `buildCloser` BEFORE the DB closes.

## Domain error registration

Every exported domain error registers with the `*sharedhttp.DomainErrorRegistry` in `internal/app/wiring.go`'s `buildDomainErrorRegistry()`:

```go
registry.Register(&fines.FineNotFoundError{}, http.StatusNotFound, "fine_not_found")
registry.Register(&fines.FineAlreadyPaidError{}, http.StatusConflict, "fine_already_paid")
registry.Register(&fines.InvalidFineError{}, http.StatusBadRequest, "invalid_fine")
```

Status conventions: 404 not-found; 409 conflicts (duplicates, ineligibility, state-conflicts); 400 validation; 403 authorisation; 502 gateway failures. The middleware uses `errors.As`, so wrapped errors match.

## Boundaries (`.claude/BOUNDARIES.md`)

- Modules import only declared dependencies.
- Cross-module deps go facade-to-facade. Never reach into another module's repository, types, or HTTP layer.
- No cross-module DB joins. Cross-module reads use the other module's facade (batch methods to avoid N+1).
- No cross-module transactions.
- No shared internal types between modules. The canonical type lives in its owning module; consumers import it.
- HTTP DTOs never escape `<module>/http/`.
- Shared infrastructure NEVER imports business modules. Direction: `cmd -> business modules -> shared`.
- No `init()` for module wiring.

## Testing conventions

Stdlib `testing.T` + `errors.Is`/`errors.As`. No `testify`. No mock library.

Substitution via in-memory implementations through `NewFacadeWithOverrides`. Fault injection uses spec-local decorator wrappers — unexported, declared inside the test file, named like `throwingOnceCatalogRepository` or `throwingChatGateway`.

Test tiers:
- Unit (`*_test.go`, no build tag): in-memory substrates only, target under 1s total. Default `task test`.
- Integration (`*_integration_test.go` with `//go:build integration`): testcontainers Postgres + Redis. Run via `task test:integration`.

HTTP handler tests use `httptest.NewServer` in an external test package. For SSE / streaming handlers, `httptest.NewRecorder` does NOT correctly satisfy `http.Flusher` — use the real server.

## Sample data — functional options

```go
type NewBookOption func(*NewBookDto)

func WithIsbn(isbn Isbn) NewBookOption {
    return func(dto *NewBookDto) { dto.Isbn = isbn }
}

func SampleNewBook(opts ...NewBookOption) NewBookDto {
    dto := NewBookDto{
        Title:   "The Pragmatic Programmer",
        Authors: []string{"Andrew Hunt", "David Thomas"},
        Isbn:    "978-0135957059",
    }
    for _, opt := range opts {
        opt(&dto)
    }
    return dto
}
```

Last-option-wins. Defaults match the TS source exactly.

## Source fidelity

Type, field, and error names match the TypeScript source 1:1 unless Go idiom demands otherwise. Cross-check when porting:

- Domain type names (`UnknownActionError`, not `UnauthorizedPermissionError`).
- Field order in DTOs (matches the TS interface order).
- Constants and enum values (`AVAILABLE`/`UNAVAILABLE`, not `Available`/`Unavailable`).
- Error messages where the user-visible text matters.

Document any justified deviation in a doc comment.

## The test-and-ship checklist

- [ ] `task build` is green.
- [ ] `task lint` is clean.
- [ ] `task test` is green and new module tests run in under ~300 ms.
- [ ] `task test:race` is green (requires CGO/gcc).
- [ ] If a Postgres adapter shipped: `task migrate:apply` succeeds and `task test:integration` is green.
- [ ] No `init()` introduced.
- [ ] No mocks added. Substitution is in-memory implementations or spec-local decorators.
- [ ] No `testify`, no `go-playground/validator`, no `wire`/`fx`.
- [ ] `.claude/BOUNDARIES.md` updated with the new module's row.
- [ ] Domain errors registered in `internal/app/wiring.go`.
- [ ] HTTP DTOs live in `internal/<module>/http/` and never escape.
- [ ] If the module owns a goroutine: `Start`/`Stop` wired through `internal/app/wiring.go`; `Close` stops it BEFORE the DB closes.

## Taskfile commands

- `task up` — start Postgres + Redis via podman compose; wait until healthy.
- `task down` — stop containers; preserve data volume.
- `task run` — `go run ./cmd/library`.
- `task build` — compile the binary into `./bin/`.
- `task test` — unit suite, no build tags.
- `task test:race` — unit suite with the race detector (requires CGO).
- `task test:integration` — integration suite (testcontainers). Requires `task up` first.
- `task migrate:apply` — apply atlas migrations.
- `task migrate:status` — atlas migration status.
- `task fmt` — `gofmt -w .` plus `go mod tidy`.
- `task lint` — `go vet ./...`.
- `task ci` — lint + race tests + build.
