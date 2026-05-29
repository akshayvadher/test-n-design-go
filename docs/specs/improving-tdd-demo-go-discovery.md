# Discovery: Improving-TDD-Demo — Go Port

## One-line Goal
Port the TypeScript/NestJS modular-monolith TDD reference app at `D:/test/tdd/improving-test/` to idiomatic Go at `D:/test/tdd/test-n-design-go/`, preserving the ideology (facade-per-module, one-tx-per-module, sagas via events, in-memory test doubles, no mocks, ~1s unit suite, modular monolith) while letting Go conventions shape the *form*.

## Why
The TS demo is a teaching artifact for Jakub Nabrdalik's "Improving your TDD" principles. A Go port serves three audiences:
- Go-first developers who want to learn the same TDD/architecture principles in their native language.
- The author, validating that the principles translate cleanly to a language without classes, decorators, or async iterators.
- Future Bee / Claude Code skill demos where Go is the substrate.

The principles are the product; the framework is incidental. If the port is faithful, the same `GUIDE.md`/`ARCHITECTURE.md`/`SAGA.md` story should read as naturally in Go as in TS.

## Who
- **Primary**: Go developers learning TDD-driven modular monoliths (the same audience as the TS demo's Go-curious readers).
- **Secondary**: The author, using the port as a reference implementation for `/bee:sdd` workflows on greenfield Go projects.
- **Tertiary**: Bee skill authors who want a Go test-bed (LSP analysis, boundaries, browser tests via SSE).

## Success Criteria
- Every facade-level test from the TS source has an equivalent Go test, and the Go unit suite still finishes in ~1s (subset that doesn't need a DB container).
- A reader who knows the TS source can navigate the Go source without a map — module boundaries, file names, and the facade pattern are recognisable.
- `task up && task test` brings the entire stack online (Postgres + Redis via podman compose) and runs all tiers green.
- The architectural invariants (no cross-module joins, one tx per module, no mocks, in-memory doubles default) hold in the Go code and are enforced by tests, not just by docs.
- The five-phase plan ships value at every step: after Phase 1 you can `curl /healthz`, after Phase 2 you can add a book and a member end-to-end, etc.

## Problem Statement
The TS source proves a set of architectural principles work; we need to demonstrate the same principles port cleanly to a language with very different idioms (no decorators, no classes-as-DI-tokens, no async iterators, channels instead of `EventEmitter`, build tags instead of test workspaces). The port is faithful when (a) the *shape* of every module is recognisable from the TS original and (b) the *Go reading* is idiomatic — no apologetic comments saying "this would be a class in TypeScript."

## Hypotheses
- **H1**: NestJS `@Module` + `Symbol()` provider tokens map cleanly to a per-package `module.go` exposing a `New<Module>(deps...) *Facade` factory + a `Wire(app *App)` registration function. No DI container needed.
- **H2**: Drizzle's `db.transaction(tx => ...)` callback maps 1:1 to bun's `db.RunInTx(ctx, opts, func(ctx, tx bun.Tx) error)`. The `TransactionalContext` abstraction survives unchanged in shape; only the underlying handle type differs.
- **H3**: The TS `InMemoryEventBus` (typed `subscribe('LoanReturned', handler)`) maps to a Go `EventBus` interface with `Publish(ctx, evt DomainEvent)` + `Subscribe(eventType string, handler func(ctx, DomainEvent) error) Unsubscribe`. `DomainEvent` is an interface with a `Type() string` method. Type-discrimination at the consumer is a type-switch or type-assertion — slightly more verbose than TS's discriminated unions but mechanical.
- **H4**: NestJS SSE (`@Sse()` + `Observable<MessageEvent>`) maps to a chi handler that sets SSE headers, asserts `http.Flusher`, and ranges over a `<-chan ChatFrame`. The chat facade returns a channel (or an iterator function in Go 1.23+); the handler writes `data:` lines and flushes.
- **H5**: There is no in-process Postgres for Go (no PGlite equivalent). The "middle tier" becomes one shared testcontainers Postgres + transaction-rollback per test, which is fast enough (~hundreds of ms per spec) without a third tier.
- **H6**: The HTTP handler / facade type split (user's strong preference) is enforced by directory layout: each module has `http/` for wire types + handlers, the module root for facade + domain types. Cross-module callers see only domain types; HTTP shapes never leak across module boundaries.
- **H7**: `OnModuleInit` lifecycle (used in lending to `start()` the auto-loan consumer) maps to an explicit `Start(ctx) error` / `Stop(ctx) error` on the module struct, called by the composition root in `cmd/library/main.go`. No magic lifecycle hooks.
- **H8**: testcontainers-go works with the podman socket if `DOCKER_HOST` is set to the podman socket path (Windows: `npipe:////./pipe/podman-machine-default`). If that proves flaky, ory/dockertest is the fallback. Both are documented; the spec phase will pick one after a 30-minute spike.

## Out of Scope
- Frontend. The TS source is server-only; the Go port stays server-only.
- A new feature surface. Anything the TS source doesn't have, the Go port doesn't get (file thumbnails *are* in the TS source so they're in scope; categories *are* in scope; a hypothetical "notifications" module is not).
- Distributed-system upgrades. No outbox table, no Redis-backed event bus, no Kafka — same in-process bus as the TS source.
- Generic frameworks. No "Go DI container" library, no codegen for handlers, no reflection-driven router. Plain factory wiring per the locked stack decision.
- `wire` / `fx`. Manual constructor wiring is the locked decision.
- Production hardening. No graceful-shutdown contortions beyond `http.Server.Shutdown`, no metrics/Prometheus exporter, no auth beyond the demo `AuthUser`.

## Phase Milestone Map

The user's sketch is sound. I've refined it for size balance — Phase 3 in the original is the heaviest (lending + reservations + saga is 3 modules' worth of concepts) so I split the saga out where natural. Five phases, each independently shippable, each ends with a runnable green test suite.

### Phase 1 — Scaffolding & Access Control (Walking Skeleton)
*Ships a runnable, health-checked server with one trivial module.*

Slices:
1. `go.mod` (`github.com/akshayvadher/test-n-design-go`), `Taskfile.yml`, `compose.yaml` (Postgres + Redis via podman), `.env.example`, `.gitignore`.
2. `cmd/library/main.go` composition root: viper config → `log/slog` logger → chi router with `RequestID`, `RealIP`, `Recoverer`, `Logger` middleware → `http.Server` + graceful shutdown.
3. `internal/shared/db` package: bun client factory, atlas versioned migrations directory (`migrations/`), `task migrate:apply` Taskfile entry.
4. `internal/shared/http` package: `DomainErrorMiddleware` that maps registered error types to HTTP status codes (the chi equivalent of `DomainErrorFilter`); `WriteJSON` helper; canonical error body shape.
5. `internal/shared/events` package: `EventBus` interface + `InMemoryEventBus` impl + `DomainEvent` interface + table-driven tests.
6. `internal/accesscontrol` module: facade + types + policy data table + `sample-access-control-data.go` + facade spec. No DB; no controller in this phase (it's used by other modules' controllers — no public HTTP surface of its own).
7. `GET /healthz` returning `{"status":"ok"}` + smoke integration test that spins the server and hits it.

**Done when**: `task up && task test && task run` boots the server on port 3000, `curl localhost:3000/healthz` returns 200, and `go test ./... ` is green in ~500ms.

### Phase 2 — Catalog + Membership (Foundational Modules)
*Ships two independent modules with full HTTP surface, in-memory + bun repositories, and crucial-path integration tests.*

Slices:
1. `internal/catalog` module structure: `facade.go`, `types.go`, `schema.go` (parse/validate), `repository.go` (port), `in_memory_repository.go`, `bun_repository.go`, `sample_data.go`, `configuration.go`, `module.go`. HTTP under `internal/catalog/http/`.
2. Catalog facade-level tests (port from `catalog.facade.spec.ts`) — same scenarios, same names, in-memory substrates.
3. Catalog HTTP handlers + DTOs + mapping functions (HTTP DTO ↔ facade DTO). Wire into chi via `module.go`.
4. Catalog `bun_repository.go` + `0001_catalog.sql` migration. Crucial-path integration test boots the real composition root against a testcontainers Postgres.
5. `internal/membership` module: same shape, same slice sequence (facade → tests → handlers → bun repo → crucial-path).
6. Shared `internal/shared/isbngateway` port + in-memory impl + a stub external impl (the TS source has an `InMemoryIsbnLookupGateway` only — port that and leave a placeholder for an OpenLibrary impl).
7. Shared `internal/shared/bookcache` port + in-memory impl + Redis impl using `github.com/redis/go-redis/v9`.

**Done when**: `POST /books`, `GET /books`, `PATCH /books/:bookId`, `DELETE /books/:bookId`, `POST /books/:bookId/copies`, `PATCH /copies/:copyId/(un)available`, `POST /members`, etc. all work end-to-end against Postgres. Both modules' facade-level + crucial-path tests pass.

### Phase 3 — Lending Module (No Saga Yet)
*Ships the lending facade + reservations, with the `TransactionalContext`, cross-module facade calls, and the post-commit publish pattern — but without the auto-loan saga.*

Slices:
1. `internal/shared/tx` package: `TransactionalContext` interface + `InMemoryTransactionalContext` + `BunTransactionalContext`. Same shape as TS source: `Run(ctx, fn)`, `Stage(apply)`, `StageEvent(evt)`. Events publish *after* commit; staged writes execute inside the tx callback.
2. Atomicity tests for both `TransactionalContext` impls (in-memory + bun) proving: write rolls back on error, staged event suppressed on rollback, events publish in stage-order after commit.
3. `internal/lending` module skeleton: types, schemas, repositories (loan + reservation, in-memory + bun), facade, configuration, sample data.
4. Lending facade `Borrow` flow — cross-module reads via `CatalogFacade` + `MembershipFacade` + `AccessControlFacade`; own-tx wraps loan write + `LoanOpened` event; `MarkCopyUnavailable` called *after* commit (the post-commit side-effect rule).
5. `Reserve` flow — own tx, `ReservationQueued` event, no cross-module write.
6. `ReturnLoan` flow — own tx for loan update, `MarkCopyAvailable` *after* commit, `LoanReturned` published after the catalog call (saga handler not yet listening; verify event is on the bus).
7. Lending HTTP handlers + crucial-path integration test.

**Done when**: A member can borrow, return, and reserve; loans persist; reservations queue; `LoanReturned` is observed on the in-memory bus but does not yet trigger an auto-loan. Atomicity tests prove tx semantics in both substrates.

### Phase 4 — Auto-Loan Saga + Fines + Categories
*Ships the saga (the architectural punchline) plus the two remaining business modules.*

Slices:
1. `internal/lending/auto_loan_on_return.go` consumer: claim-first, per-book lock (Go `sync.Mutex` map), `attemptAutoLoan` → `lending.Borrow` → on failure un-fulfill + `AutoLoanFailed`, on success `AutoLoanOpened`. Started/stopped explicitly by the composition root.
2. Consumer atomicity tests (port `auto-loan-on-return.consumer.spec.ts`): claim tx rolls back the staged `ReservationFulfilled`; un-fulfill tx rolls back the staged `ReservationUnfulfilled`; `AutoLoanFailed` still fires outside the tx; per-book serialisation prevents double-fulfilment.
3. `internal/fines` module: facade + repo + bun + HTTP + crucial-path. Subscribes to `LoanReturned` to assess overdue fines (port the source's behaviour).
4. `internal/fines` saga: when a loan returns past due-date, fines facade stages an `AssessFine` in its own tx + emits `FineAssessed`.
5. `internal/categories` module: facade + repo + bun + HTTP. Independent of other modules in business logic (it's a curated taxonomy in the TS source).
6. Cross-module integration tests in `test/crucial_path/` that boot the full app and exercise lending → returns → auto-loan-of-reserver chains end-to-end.

**Done when**: A reservation queued before a return triggers an auto-loan after the return commits; on failure the claim is rolled back; fines are assessed for overdue returns; all four atomicity tests pass; categories module green.

### Phase 5 — Chat + Docs + Polish
*Ships the SSE streaming surface, the OpenAI gateway, and the user-facing docs that make this a learnable artifact.*

Slices:
1. `internal/shared/chatgateway` port: `Stream(ctx, messages []ChatMessage) (<-chan ChatDelta, error)` + `InMemoryChatGateway` (deterministic delta generator for tests).
2. `OpenAIChatGateway` using `github.com/sashabaranov/go-openai` or `github.com/openai/openai-go`. (Pick one in spec phase; both are mainstream.)
3. `internal/chat` module: facade returning a `<-chan ChatFrame`, parsing the request via the schema package, error-frame on gateway failure.
4. `internal/chat/http` SSE handler: assert `http.Flusher`, set `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`; write `event:` / `data:` lines per frame; close on `<-ctx.Done()` or channel close.
5. SSE handler tests using `httptest.NewServer` + a real HTTP client that reads the streaming body line by line.
6. Top-level docs: `README.md`, `ARCHITECTURE.md`, `SAGA.md`, `GUIDE.md` — *port the TS originals*, adjusting only the code samples to Go. The principles are unchanged; the examples become Go examples.
7. `.claude/skills/` skill that teaches the Go port's conventions (mirror of the TS demo's skill — adapt the language).

**Done when**: `POST /chat` streams deltas over SSE end-to-end; all four docs read as natural Go documentation (no "this is how it works in TS"); the Claude skill loads and offers correct guidance for adding a new module.

## Module Structure

The structure is for the `boundary-generation` skill. Each Go package is a module; the dependency direction is enforced by import paths and the linter (gocyclo-style boundaries check or `go-arch-lint` if needed — TBD per Open Question 4).

### Layout

```
test-n-design-go/
├── cmd/
│   └── library/
│       └── main.go              # composition root
├── internal/
│   ├── accesscontrol/           # access-control module
│   ├── catalog/                 # catalog module
│   ├── membership/              # membership module
│   ├── lending/                 # lending module (+ auto-loan saga)
│   ├── fines/                   # fines module
│   ├── categories/              # categories module
│   ├── chat/                    # chat module
│   └── shared/
│       ├── db/                  # bun client, migrations runner
│       ├── events/              # EventBus interface + in-memory impl
│       ├── http/                # domain-error middleware, response helpers
│       ├── tx/                  # TransactionalContext interface + impls
│       ├── isbngateway/         # outbound port + impls
│       ├── bookcache/           # outbound port + impls (in-mem + Redis)
│       ├── chatgateway/         # outbound port + impls (in-mem + OpenAI)
│       └── filestorage/         # outbound port + impls (in-mem + ...)
├── migrations/                  # atlas versioned SQL migrations
├── test/
│   ├── crucial_path/            # cross-module integration tests
│   └── support/                 # testcontainers helpers, app factory
├── docs/
│   ├── specs/
│   ├── adrs/                    # (created on demand)
│   └── ...
├── compose.yaml                 # podman compose
├── Taskfile.yml
├── atlas.hcl                    # atlas config
├── go.mod
└── go.sum
```

### Per-Module Public Surface

Each business module package exports **only**:
- The facade type (e.g. `CatalogFacade`) and its constructor `NewCatalogFacade(...) *CatalogFacade`.
- Domain DTOs used as facade method inputs/outputs (e.g. `BookDto`, `NewBookDto`).
- Domain error types (e.g. `BookNotFoundError`, `DuplicateIsbnError`).
- The module registration function `Wire(r chi.Router, deps Deps)` (the Go equivalent of `@Module`).
- Test helpers (sample builders, configuration overrides) — only used internally and by tests in the same module's tree. **Not** imported by other modules.

Each business module package keeps **unexported**:
- Repository interfaces and impls (unexported except where another file in the same package needs them).
- HTTP DTOs (live in the `http` sub-package; never crossed module boundaries).
- Internal helpers, builders, validators.

### Allowed Dependency Edges

| Module | May import |
| --- | --- |
| `accesscontrol` | stdlib only |
| `catalog` | `accesscontrol`, `shared/isbngateway`, `shared/bookcache`, `shared/filestorage`, `shared/tx`, `shared/events`, `shared/db`, `shared/http` |
| `membership` | `accesscontrol`, `shared/tx`, `shared/events`, `shared/db`, `shared/http` |
| `lending` | `accesscontrol`, `catalog`, `membership`, `shared/events`, `shared/tx`, `shared/db`, `shared/http` |
| `fines` | `accesscontrol`, `lending` (via facade), `shared/events`, `shared/tx`, `shared/db`, `shared/http` |
| `categories` | `accesscontrol`, `shared/tx`, `shared/db`, `shared/http` |
| `chat` | `shared/chatgateway`, `shared/http` |
| `shared/*` | stdlib + third-party only, **never** business modules |

### Forbidden Patterns
- **No cross-module DB joins.** Bun queries inside `lending` may only touch `loans` and `reservations` tables. Cross-module reads go through the other module's facade (with a batch method when N+1 would bite — see `Catalog.GetBooks([]BookId)` for the pattern).
- **No cross-module transactions.** A `TransactionalContext` instance is created and used inside one module. No passing a tx handle across a facade boundary.
- **No shared internal types between modules.** If `lending` and `fines` both reference a `LoanId`, the canonical type lives in `lending` and `fines` imports it. Never invent a `shared/types` dumping ground.
- **No HTTP DTOs leaking out of `http` sub-packages.** Facade methods take and return domain DTOs only. Handlers map HTTP DTO ↔ facade DTO at the edge.
- **No mocks (`gomock`, `mockery`, `testify/mock`, hand-rolled `MockXxx` structs).** Test substitution uses in-memory implementations of the same interface; fault injection uses spec-local "ThrowingOnce" decorator wrappers (unexported).
- **No exporting repositories or internal helpers from the package barrel.** The module's public surface is fixed: facade + DTOs + errors + Wire.
- **No `init()` for module wiring.** All wiring is explicit in `cmd/library/main.go`. `init()` is reserved for things genuinely tied to package load (rare).

## Idiom-Translation Table (NestJS → Go)

| NestJS / TS pattern | Go equivalent |
| --- | --- |
| `@Module` + `Symbol()` DI provider tokens | Factory function `NewCatalogFacade(...)` in `module.go` returning `*CatalogFacade`. Composition root in `cmd/library/main.go` wires concrete deps. |
| `@Injectable` class | Exported struct with unexported fields + constructor + exported methods. No annotation. |
| `@Controller` + `@Get/@Post/...` | Plain functions in `<module>/http/handler.go`. Registered on a chi router via `RegisterRoutes(r chi.Router, facade *Facade)`. |
| Drizzle ORM (`db.select().from(books).where(eq(...))`) | uptrace/bun query builder (`db.NewSelect().Model(&books).Where(...)`). Mapping functions `toBookRow` / `toBookDto` stay; bun struct tags replace Drizzle column helpers. |
| Drizzle `db.transaction(tx => work)` | `db.RunInTx(ctx, &sql.TxOptions{}, func(ctx, tx bun.Tx) error)`. The `TransactionalContext` abstraction wraps this. |
| Zod schemas + `parseX` helpers | `<module>/schema.go` with `Parse<X>(input <Input>) (<Parsed>, error)`. Validation written by hand (or `go-playground/validator` if it pays off — Open Question 5). Returns `Invalid<X>Error` on failure. |
| Vitest workspaces (`unit` / `integration` / `pglite`) | Build tags: no tag = unit (`<x>_test.go`); `//go:build integration` for testcontainers tests; no third tier. |
| testcontainers-js | testcontainers-go (preferred; works with podman via `DOCKER_HOST`). Fallback: ory/dockertest. |
| `@electric-sql/pglite` (in-process Postgres) | **No equivalent.** Shared testcontainers Postgres + transaction-rollback-per-test is the replacement. See Hypothesis H5 + Open Question 6. |
| Sample data builders `sampleNewBook({overrides})` | Functional-option builders: `SampleNewBook(WithIsbn("..."), WithTitle("..."))`. Each option is a `func(*NewBookDto)`. |
| `class FooError extends Error` | `type FooError struct { ... }` implementing `Error() string`. Domain errors live in `<module>/types.go` next to the DTOs. Wrapped/checked via `errors.Is` / `errors.As`. |
| NestJS `ExceptionFilter` (`DomainErrorFilter`) | chi middleware `DomainErrorMiddleware(next http.Handler) http.Handler` that captures errors from handlers via a thin `handle(http.ResponseWriter, *http.Request) error` wrapper, type-switches on registered error types, writes the right status. |
| In-process `EventBus` with typed `subscribe<T>` | `EventBus` interface: `Publish(ctx, evt DomainEvent) error` + `Subscribe(eventType string, handler func(ctx, DomainEvent) error) Unsubscribe`. `DomainEvent` is `interface { Type() string }`. Consumers type-assert/type-switch on the concrete event. |
| `OnModuleInit` / `OnModuleDestroy` for consumers | Explicit `Start(ctx) error` / `Stop(ctx) error` on the module struct. Called by `cmd/library/main.go` in order. |
| SSE via `@Sse()` + `Observable<MessageEvent>` | chi handler: assert `http.Flusher`, set SSE headers, range over `<-chan ChatFrame` from the facade, write `event: <type>\ndata: <json>\n\n` and `Flush()` on each. Close on `<-ctx.Done()` or channel close. |
| `randomUUID()` from `node:crypto` | `github.com/google/uuid` `uuid.NewString()`. Injected as `newId func() string` for deterministic tests. |
| Deterministic `clock = () => Date` | `clock func() time.Time` injected; default `time.Now`. |
| `pnpm test:unit` / `:integration` / `:pglite` | `task test` (unit only) / `task test:integration` (with `-tags=integration`) / no third tier. |
| `pnpm-workspace.yaml` monorepo | Single Go module rooted at `go.mod`. The TS workspace structure is collapsed; `cmd/library/` is the only binary. |
| `index.ts` barrel | Go package itself is the barrel — only exported identifiers are visible. No separate `index.go` needed. |
| `Spread overrides` in sample data | Functional options (see above). |
| `vitest.workspace.ts` running multiple roots | A single `go test ./...` for unit; `go test -tags=integration ./...` for integration. Build tags do the workspace split. |

## Testing Tier Decisions

- **Unit tier (default)**: `<x>_test.go`, no build tag, co-located with source. Runs against in-memory repositories and in-memory gateways. Target: full suite < 1s. Includes all facade specs and the `TransactionalContext` in-memory atomicity tests.
- **Integration tier**: `<x>_integration_test.go` with `//go:build integration`. Runs against a real Postgres (testcontainers-go) + real Redis. Includes:
  - Per-module crucial-path test (boots composition root, hits HTTP, asserts DB state).
  - bun repository contract test (same scenarios as in-memory repository spec, run against real Postgres).
  - `BunTransactionalContext` atomicity tests (the real-DB equivalent of the in-memory tx atomicity test).
- **No third tier.** PGlite has no Go equivalent. The middle-tier "Postgres semantics without Docker" need is satisfied by sharing one testcontainers Postgres across the integration suite and using transaction-rollback-per-test where SQL semantics matter. This is fast enough (~hundreds of ms per spec after the first cold start) and avoids a third substrate.
- **Mocking philosophy**: **no mocks.** Substitution via in-memory implementations of the same interface (the TS source pattern). Fault injection via spec-local decorator wrappers (`throwingOnceCatalogRepository`) that wrap the real in-memory impl. **Unexported** — never visible outside the test file.
- **Assertion library**: **stdlib `testing.T` + `errors.Is/As`.** No testify. Reasoning: (a) the TS source uses `expect()` because Vitest provides it, not because the team chose an assertion lib; (b) `t.Errorf` + `errors.Is` is sufficient for the assertions we actually need (equality, error-kind matching, slice contents); (c) every Go dependency added is a forever decision in a teaching repo. If a place genuinely needs `require.ElementsMatch`, we'll cross that bridge with a tiny helper, not a library. *Open to override — see Open Question 1.*

## HTTP Handler / Facade Split (Architectural Conviction)

This is the single most important convention. Every module follows it.

```
internal/<module>/
├── facade.go              # *Facade struct + exported methods; uses *Dto types
├── types.go               # Domain DTOs (BookDto, NewBookDto, ...) + domain errors
├── schema.go              # ParseX functions returning typed-Parsed + error
├── repository.go          # Port interface (unexported or exported as needed)
├── in_memory_repository.go
├── bun_repository.go
├── configuration.go       # NewXxxFacade overrides (test substitution)
├── sample_data.go         # SampleX(opts ...) builders for tests
├── module.go              # Wire(r, deps) — composition glue
├── facade_test.go         # Facade-level spec (in-memory substrates)
└── http/
    ├── handler.go         # http.HandlerFunc per endpoint; decodes request -> calls facade -> encodes response
    ├── dto.go             # Wire types: AddBookRequest, BookResponse, ErrorResponse (JSON shapes only)
    └── mapping.go         # toBookResponse(BookDto) / fromAddBookRequest(AddBookRequest) NewBookDto
```

### Rules
- **Handler functions live in `http/handler.go`.** They take `*Facade` (via closure or struct), decode JSON into HTTP DTOs, map to domain DTOs, call the facade, map the result back, write JSON.
- **HTTP DTOs live in `http/dto.go`** with `json:` tags. They are **never** exported beyond the `http` package. They exist only for wire-format stability.
- **Facade methods take and return domain DTOs.** No `*http.Request`, no JSON tags, no HTTP status concerns.
- **Mapping is a function, not a method.** `toBookResponse(BookDto) BookResponse` and `fromAddBookRequest(AddBookRequest) NewBookDto`. These live in `http/mapping.go`. Mapping is explicit.
- **The facade test files never import `http/`.** A facade spec is a unit test of the facade — no HTTP layer involved.
- **The HTTP handler test files use `httptest.NewRecorder` + a real `Facade` wired with in-memory deps.** No double substitution at both layers.

This split is the user's strong preference and is non-negotiable. All slices in all phases follow it.

## Open Questions

These are decisions the developer should make before Phase 1 begins. None are blockers — defaults are noted — but the developer's call locks the convention for the whole port.

1. **Assertion library**. Default recommendation: **stdlib only** (`testing.T` + `errors.Is/As` + tiny local helpers). Alternative: add `testify/assert` for `assert.ElementsMatch` and friends. The teaching value of a no-dependency repo is real; the cost of writing a five-line `elementsMatch` helper is trivial. Recommend stdlib. **Decision needed**.

2. **Logger**. Default recommendation: **`log/slog`** (stdlib, structured, JSON handler, since Go 1.21). The composition root creates one slog `*Logger`, passes it down to modules as a constructor dep, and emits structured logs from facades and handlers. **Recommended.** Confirm.

3. **HTTP middleware stack**. Recommendation: chi's `middleware.RequestID`, `middleware.RealIP`, `middleware.Recoverer`, `middleware.Logger` (with slog adapter), plus the custom `DomainErrorMiddleware`. Order: RequestID → RealIP → Logger → Recoverer → DomainErrorMiddleware → routes. **Confirm or revise**.

4. **Architecture enforcement**. The forbidden-pattern rules (no cross-module joins, no shared internal types) are stated in docs. Should we also enforce them with `go-arch-lint` or similar? Recommendation: **document-only in Phase 1**; revisit in Phase 5 if violations appear in code review. Adds a dep; benefits a teaching repo less than a real codebase. **Confirm**.

5. **Validation library**. Default recommendation: **hand-written `Parse<X>` functions** that return typed-parsed + `Invalid<X>Error`. Matches the TS source's `parseX` helpers exactly. Alternative: `go-playground/validator` with struct tags. The hand-written version is 5-15 lines per schema, fully type-safe, no reflection, and reads naturally. Recommend hand-written. **Confirm**.

6. **Test container provider on Windows + podman**. Recommendation: **testcontainers-go with `DOCKER_HOST` set to the podman pipe**. Fallback: ory/dockertest. Decision deferrable to the Phase 2 first-bun-repo-test spike. **Spike during Phase 2 Slice 4**; lock the choice in `bee-context.local.md` after the spike.

7. **Atlas migrations: declarative HCL vs versioned SQL**. Source uses zero-padded versioned SQL. Recommendation: **versioned SQL** (`0001_initial.sql`, `0002_fines.sql`, ...) — matches the source's mental model and is simpler for a teaching repo. Atlas `dev-url` points at a scratch testcontainers Postgres. **Recommended.** Confirm.

8. **Single-app layout: `apps/library/` mirror vs `cmd/library/` Go-idiomatic**. Recommendation: **`cmd/library/`** + `internal/...` (Go-idiomatic, no `apps/` directory). The TS source uses pnpm workspaces to support future apps; the Go port has no such need. Collapse. **Recommended.** Confirm.

9. **Dev port**. Recommendation: **3000** (matches source; less cognitive load when comparing TS and Go logs side-by-side). **Recommended.** Confirm.

10. **OpenAI client library**. Recommendation: **`github.com/sashabaranov/go-openai`** (mainstream, supports streaming over SSE cleanly with `client.CreateChatCompletionStream`). Alternative: `github.com/openai/openai-go` (official, newer, less battle-tested). Decision deferrable to Phase 5. **Spike in Phase 5 Slice 2**.

11. **Iterator vs channel for chat streaming**. Recommendation: **channel** (`<-chan ChatFrame`). Go 1.23's `iter.Seq` is elegant but channels are still the lingua franca for streaming over network boundaries and they integrate with `context.Context` cancellation idiomatically. **Recommended.** Confirm.

12. **Per-module saga consumer state on restart**. The TS source's auto-loan consumer is in-process and re-subscribes on every boot — there's no durable consumer offset. The Go port follows the same convention (no outbox, no replay). **Confirm this is intentional** — it matches the source but is worth a one-line callout for honesty.

## Decisions Log

All decisions are locked. Captured during pre-discovery interview + follow-up question round.

| # | Decision | Rationale |
| --- | --- | --- |
| D1 | Go module path: `github.com/akshayvadher/test-n-design-go` | Locked. |
| D2 | Multi-phase delivery (each phase commits + ships separately) | Locked. |
| D3 | Full-fidelity chat (OpenAI gateway + SSE streaming) | Locked. |
| D4 | Manual constructor wiring (no `wire`/`fx`) | Locked. Matches teaching-repo simplicity. |
| D5 | Router: `go-chi/chi` | Locked. |
| D6 | Migrations: `ariga.io/atlas` with versioned SQL (zero-padded `0001_*.sql`) | Locked. Mirrors source's mental model. |
| D7 | ORM / query builder: `uptrace/bun` | Locked. |
| D8 | Config: `spf13/viper` | Locked. |
| D9 | Task runner: `Taskfile` | Locked. |
| D10 | Container engine: `podman compose` (NOT docker) | Locked. testcontainers-go reaches podman via `DOCKER_HOST` env. |
| D11 | External systems: Postgres + Redis (same as source) | Locked. |
| D12 | HTTP DTOs separate from facade DTOs (strict separation) | User's strong preference. Non-negotiable. See "HTTP Handler / Facade Split" section. |
| D13 | No mocks; in-memory doubles + spec-local decorator wrappers | Carried from source ideology. Non-negotiable. |
| D14 | One transaction per module; cross-module via events | Carried from source ideology. Non-negotiable. |
| D15 | Five phases (Scaffolding → Catalog+Membership → Lending → Saga+Fines+Categories → Chat+Docs) | Refined from user sketch — lending separated from the saga because lending alone is already three concepts (loans, reservations, return). |
| D16 | Single `cmd/library/` binary, `internal/...` packages (Go-idiomatic) | Locked. |
| D17 | Test tiers: unit (no tag) + integration (`-tags=integration`) only; no third tier | No PGlite equivalent in Go; rollback-per-test against shared testcontainers suffices. |
| D18 | `log/slog` structured logger | Locked. |
| D19 | Stdlib testing assertions only — no testify | Locked. Helper functions where needed. |
| D20 | Hand-written `Parse<X>` validators — no `validator` library | Locked. |
| D21 | Assertion library: stdlib `testing.T` + `errors.Is/As` only | Locked. |
| D22 | Container library: `testcontainers-go` via `DOCKER_HOST` → podman socket | Locked. ory/dockertest is fallback only if podman integration proves flaky. |
| D23 | OpenAI client: `github.com/openai/openai-go` (official SDK) | Locked. |
| D24 | HTTP middleware order: `RequestID → RealIP → Logger(slog) → Recoverer → DomainErrorMiddleware → routes` | Locked. |
| D25 | Architecture enforcement: document-only in Phase 1. Revisit `go-arch-lint` in Phase 5 if violations appear | Locked. |
| D26 | Dev port: 3000 (matches source) | Locked. |
| D27 | Chat streaming: `<-chan ChatFrame` (not `iter.Seq`) | Locked. Plays well with `context.Context` cancellation. |
| D28 | No durable consumer offsets in the event bus (in-process only, matches source) | Locked. Will be called out in `SAGA.md`. |

## Revised Assessment

Size: **EPIC** (5 phases, ~30 slices total, full multi-module port)
Greenfield: **yes** (empty target dir; full freedom to set conventions)

Greenfield + EPIC means the `boundary-generation` skill should run after discovery completes — the Module Structure section above is its input.
