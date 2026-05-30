# Spec: improving-tdd-demo Go Port — Phase 2 (Catalog + Membership)

## Overview

Foundational business modules: ports `internal/catalog` and `internal/membership` end-to-end. Each ships with facade + types + schema (hand-written parse-style validators) + in-memory repository + bun repository + sample-data functional-option builders + configuration constructor + `module.go` HTTP wiring + an `http/` subpackage carrying DTOs, handlers and HTTP↔domain mapping. Shared outbound ports land alongside: `internal/shared/isbngateway` (in-memory + placeholder external impl) and `internal/shared/bookcache` (in-memory + Redis impl using `github.com/redis/go-redis/v9`). Facade-level tests port the TS source's `catalog.facade.spec.ts` and `membership.facade.spec.ts` scenario-for-scenario; crucial-path integration tests boot the full composition root against testcontainers Postgres and exercise the HTTP surface end-to-end. By the end of Phase 2 a developer can `curl -X POST localhost:3000/books`, `POST /members`, `POST /books/:bookId/copies`, `PATCH /copies/:copyId/(un)available`, `PATCH /members/:id/(suspend|reactivate|tier)`, `GET /members/:id/eligibility`, etc. — and the persistence layer is real Postgres.

## Why

Phase 2 unlocks:

- The first two business modules — proof that the per-module template (`facade.go` / `types.go` / `schema.go` / `repository.go` / `in_memory_repository.go` / `bun_repository.go` / `sample_data.go` / `configuration.go` / `module.go` + `http/` subdir) ports cleanly from the TS source's NestJS shape to a Go package.
- The hand-written `Parse<X>` validation pattern (no `go-playground/validator`) on real inputs — ISBN format checks, email format checks, thumbnail mime sniffing, trim-and-reject-blank rules.
- The bun-repository + atlas migration pattern on real tables (`books`, `copies`, `members`) with the conservative `PoolConfig{}` defaults the composition root already passes. The first business migrations (`0001_catalog.sql` and `0002_membership.sql`) test that atlas's "empty migrations dir" tolerance survives the transition to "directory with content."
- The shared outbound port pattern: a `port.go` declaring the interface, an `in_memory_<port>.go` in the same package, and (for `bookcache`) a `redis_<port>.go` adapter using `github.com/redis/go-redis/v9` (the dep enters `go.mod` in this phase).
- The "no-mocks, in-memory doubles + spec-local Throwing-Once decorator wrappers" discipline on real cache and gateway code — every fault-injection test in `catalog.facade.spec.ts` (cache GET fails / cache SET fails / cache EVICT fails / ISBN gateway throws once / file storage put throws once) has a Go counterpart.
- The first crucial-path integration test that boots the real composition root via `test/support.BootApp`, applies real migrations against a testcontainers Postgres, and hits the wired HTTP surface end-to-end. Phase 3+ crucial-path tests reuse the same harness without modification.

Without Phase 2, every later module has to negotiate "what does the per-module file layout look like for a stateful module with HTTP routes?" mid-flight. With it, Phase 3's lending module — three concepts thick (loans + reservations + return), the first module to need a `TransactionalContext` — answers only its tx-substrate question, not the module-shape question.

## In Scope

- `internal/shared/isbngateway/` package: `port.go` (`IsbnLookupGateway`, `BookMetadata`), `in_memory.go` (`InMemoryIsbnLookupGateway` with `Seed(isbn, BookMetadata)` + `FindByIsbn(ctx, isbn) (*BookMetadata, error)`), `external.go` (placeholder stub `OpenLibraryIsbnLookupGateway` with the same interface; body returns `nil, errors.New("not implemented")` so the Phase 5+ implementation slot is reserved without committing to a shape). Unit tests for the in-memory impl using stdlib `testing`.
- `internal/shared/bookcache/` package: `port.go` (`BookCacheGateway`), `in_memory.go` (`InMemoryBookCacheGateway`), `redis.go` (`RedisBookCacheGateway` using `github.com/redis/go-redis/v9`; key format `catalog:book:isbn:<isbn>`; JSON-encoded `BookDto`). Unit tests for the in-memory impl. Integration test for the Redis impl gated by `//go:build integration` using the `StartRedis` testcontainers helper Phase 1 shipped. `github.com/redis/go-redis/v9` enters `go.mod` direct deps in this phase.
- `internal/catalog/` module: every file the per-module template lists — `facade.go`, `types.go`, `schema.go`, `repository.go`, `in_memory_repository.go`, `bun_repository.go`, `sample_data.go`, `configuration.go`, `module.go`, `facade_test.go`, `in_memory_repository_test.go`, `bun_repository_test.go` (build-tag `integration`), plus `http/` subpackage with `dto.go`, `handlers.go`, `mapping.go`, `handlers_test.go`.
- `internal/membership/` module: same shape, no `bookcache`/`isbngateway` dependencies — just `accesscontrol` (constructor-injected) and `shared/db` (via the bun repository).
- `migrations/0001_catalog.sql` creating `books` (`book_id UUID PK`, `title TEXT NOT NULL`, `authors TEXT[] NOT NULL`, `isbn TEXT NOT NULL UNIQUE`) and `copies` (`copy_id UUID PK`, `book_id UUID NOT NULL REFERENCES books(book_id)`, `condition TEXT NOT NULL`, `status TEXT NOT NULL`). `migrations/0002_membership.sql` creating `members` (`member_id UUID PK`, `name TEXT NOT NULL`, `email TEXT NOT NULL UNIQUE`, `tier TEXT NOT NULL`, `status TEXT NOT NULL`).
- `internal/app/wiring.go` extended so `Wire` constructs the catalog + membership facades, registers Phase-2 domain errors with the `DomainErrorRegistry`, and mounts both modules' routes via their `module.go` `Wire(r, deps)` functions.
- `test/crucial_path/catalog_integration_test.go` and `test/crucial_path/membership_integration_test.go` (both `//go:build integration`) — boot the real composition root via `test/support.BootApp`, hit the HTTP surface, assert HTTP status + body, then (where relevant) introspect via `*bun.DB` to assert row-level state. (`test/crucial_path/` did not exist in Phase 1; Phase 2 Slice 4 creates it.)
- `.http/catalog.http` and `.http/membership.http` files activating the Phase-2 endpoints the Phase 1 spec stubbed (`POST {{baseUrl}}/books`, `POST {{baseUrl}}/members`, etc.) for manual probing.

## Out of Scope (deferred to later phases per discovery doc)

- `internal/lending/`, `internal/fines/`, `internal/categories/`, `internal/chat/`. (Phases 3–5.)
- `internal/shared/tx/` (`TransactionalContext` + in-memory/bun impls). Phase 2 catalog and membership facades are single-table-per-write — neither needs a transactional context yet. Phase 3 introduces it for lending's `Borrow`/`Return` flows.
- `internal/shared/filestorage/`. Phase 2's catalog facade ships *without* `attachThumbnail` / `readThumbnail` / `removeThumbnail`. Thumbnails introduce a second outbound port (`FileStorageGateway`) and three more handlers; deferring them keeps Phase 2 focused on the canonical "stateful CRUD + cache + ISBN enrichment" pattern. The Phase 2 catalog facade ports every TS method EXCEPT the three thumbnail methods. Phase 5 ships the `filestorage` port + impl + the three thumbnail handlers, and the `attachThumbnail` / `readThumbnail` / `removeThumbnail` facade spec scenarios from the TS source. **Recorded as Open Question 1** with the recommended default to defer.
- `internal/shared/chatgateway/`. (Phase 5.)
- The OpenLibrary-backed `IsbnLookupGateway`. Phase 2 ships only the in-memory impl plus an `external.go` stub whose body returns `nil, errors.New("not implemented")`. The TS source also ships only the in-memory impl (`InMemoryIsbnLookupGateway`); the placeholder file in Go exists so the implementation slot is visible and the shape is locked.
- Cross-module event subscriptions. Catalog and membership publish nothing in Phase 2 — there are no Phase-2 domain events. (Phase 3 introduces `LoanOpened` / `LoanReturned` / `ReservationQueued` / etc.)
- `go-arch-lint` or other architecture-enforcement tooling. (D25; revisit Phase 5.)
- Authentication middleware. The catalog thumbnail handlers (deferred to Phase 5) are the only Phase-2 surface that would have needed auth; without them, `accesscontrol.Authorize` is still wired into the catalog facade constructor but goes unexercised on the HTTP path in Phase 2. The facade-level tests still cover the authorize call indirectly via the deferred thumbnail methods (when Phase 5 enables them).
- Top-level docs (`README.md`, `ARCHITECTURE.md`, etc.). (Phase 5.)

## Slices

Slicing is outside-in: catalog's facade is built (Slice 1) → driven by facade-level tests (Slice 2) → wrapped in HTTP handlers (Slice 3) → persisted to Postgres via bun + crucial-path integration test (Slice 4) → membership repeats the same five-step sequence in one slice (Slice 5) → shared outbound ports land last because catalog Slice 1 builds against them but they don't need their own slice ceremony in the TS source either (Slices 6–7). The dev-loop `.http` placeholders Phase 1 stubbed are activated at the end of each module's slice block.

---

### Slice 1: `internal/catalog` module skeleton (facade-level shape, in-memory only)

Brings the repository to "the catalog facade exists with its full method surface, with in-memory substrates wired, and `go build ./...` is green — but no HTTP routes, no migrations, no DB." This slice is pure code architecture: the facade signature, the domain DTOs, the schema parsers, the repository port, the in-memory impl, the sample-data builders, and the configuration constructor. Tests come in Slice 2.

#### Acceptance Criteria — types

- [ ] `internal/catalog/types.go` declares `type BookId string`, `type CopyId string`, `type Isbn string` (named string types, not raw `string` — misuse at call sites caught by the compiler).
- [ ] `types.go` declares `type CopyStatus string` with constants `CopyStatusAvailable CopyStatus = "AVAILABLE"`, `CopyStatusUnavailable CopyStatus = "UNAVAILABLE"` (matches TS source `CopyStatus.AVAILABLE` / `UNAVAILABLE`).
- [ ] `types.go` declares `type CopyCondition string` with constants `CopyConditionNew = "NEW"`, `CopyConditionGood = "GOOD"`, `CopyConditionFair = "FAIR"`, `CopyConditionPoor = "POOR"`.
- [ ] `types.go` declares `type NewBookDto struct { Title string; Authors []string; Isbn Isbn }` — `Title` and `Authors` are optional in the TS source (gateway can fill them) but always carry zero-values in the struct; the schema parser handles "missing vs blank" via the gateway-merge step in the facade.
- [ ] `types.go` declares `type UpdateBookDto struct { Title *string; Authors *[]string }` — pointer fields so "field absent" vs "field present but empty/blank" are distinguishable. The schema parser rejects "both nil" with `InvalidBookError("at least one of title or authors must be provided")`.
- [ ] `types.go` declares `type BookDto struct { BookId BookId; Title string; Authors []string; Isbn Isbn }`. (`Thumbnail` field intentionally omitted in Phase 2 per the deferred-thumbnails scope decision; Phase 5 adds an optional `Thumbnail *BookThumbnailDto` field without breaking JSON consumers because the field will be JSON-tagged `omitempty`.)
- [ ] `types.go` declares `type NewCopyDto struct { BookId BookId; Condition CopyCondition }`.
- [ ] `types.go` declares `type CopyDto struct { CopyId CopyId; BookId BookId; Condition CopyCondition; Status CopyStatus }`.
- [ ] `types.go` declares domain errors: `BookNotFoundError{ Identifier string }`, `CopyNotFoundError{ CopyId CopyId }`, `DuplicateIsbnError{ Isbn Isbn }`, `InvalidBookError{ Reason string }`, `InvalidCopyError{ Reason string }`. Each implements `Error() string` matching the TS source's message format (`"Book not found: <identifier>"`, `"A book with ISBN <isbn> already exists"`, `"Invalid book: <reason>"`, etc.). Names match TS source 1:1.
- [ ] Each error type is matchable via `errors.As`: `var bnfe *BookNotFoundError; errors.As(err, &bnfe)` returns true for a wrapped `BookNotFoundError`.
- [ ] `types.go` exports no other types in Phase 2. (`BookThumbnailDto`, `ThumbnailNotFoundError`, `InvalidThumbnailError` land in Phase 5.)

#### Acceptance Criteria — schema (hand-written parsers)

- [ ] `internal/catalog/schema.go` exports `ParseIsbn(raw string) (Isbn, error)` matching the TS source's `parseIsbn` behaviour: trim → reject empty → reject if the string with hyphens/spaces stripped does NOT match `^\d{9}[\dX]$` (ISBN-10) OR `^\d{13}$` (ISBN-13). On failure return `*InvalidBookError{Reason: "isbn format is invalid: " + raw}` (or `"isbn is required"` for blank). Trimmed value is the returned `Isbn`.
- [ ] `schema.go` exports `ParseNewBook(dto NewBookDto) (NewBookDto, error)` matching `parseNewBook`: trim title and each author; filter blank authors out; reject if title is blank with `*InvalidBookError{Reason: "title is required"}`; reject if authors slice is empty after filtering with `*InvalidBookError{Reason: "at least one author is required"}`; delegate ISBN validation to `ParseIsbn`. Returns the trimmed/filtered `NewBookDto`.
- [ ] `schema.go` exports `ParseUpdateBook(dto UpdateBookDto) (UpdateBookDto, error)` matching `parseUpdateBook`: reject if both `Title` and `Authors` are nil with `*InvalidBookError{Reason: "at least one of title or authors must be provided"}`; when `Title` is non-nil, trim it and reject if the trimmed value is blank; when `Authors` is non-nil, trim each entry, filter blanks, and reject if the result is empty. ISBN is NOT a field on `UpdateBookDto` so the TS "isbn cannot be updated" check is enforced at the HTTP DTO mapping layer, not the schema (call out the AC: the HTTP mapping rejects an unknown JSON field by mapping it to `*InvalidBookError{Reason: "isbn cannot be updated"}` — see Slice 3).
- [ ] `schema.go` exports `ParseNewCopy(dto NewCopyDto) (NewCopyDto, error)` matching `parseNewCopy`: reject if `Condition` is not one of `NEW|GOOD|FAIR|POOR` with `*InvalidCopyError{Reason: "condition must be one of NEW, GOOD, FAIR, POOR"}`.
- [ ] All parsers use only stdlib + the package's own `types.go` — no `go-playground/validator`, no third-party regex helpers beyond `regexp` from stdlib. Hand-written matches the locked convention D20.

#### Acceptance Criteria — repository port + in-memory impl

- [ ] `internal/catalog/repository.go` declares `type Repository interface` with methods: `SaveBook(ctx context.Context, book BookDto) error`, `FindBookById(ctx context.Context, bookId BookId) (*BookDto, error)`, `FindBookByIsbn(ctx context.Context, isbn Isbn) (*BookDto, error)`, `ListBooks(ctx context.Context) ([]BookDto, error)`, `ListBooksByIds(ctx context.Context, bookIds []BookId) ([]BookDto, error)`, `DeleteBook(ctx context.Context, bookId BookId) error`, `SaveCopy(ctx context.Context, copy CopyDto) error`, `FindCopyById(ctx context.Context, copyId CopyId) (*BookDto, error)` — wait, that's `(*CopyDto, error)`. `FindCopyById(ctx context.Context, copyId CopyId) (*CopyDto, error)`.
- [ ] `Find*` methods return `(nil, nil)` on "not found" (no rows). They return a wrapped error only on infrastructure failure. The facade is responsible for translating `nil` into `*BookNotFoundError` / `*CopyNotFoundError`. (This mirrors the TS source's `Promise<BookDto | undefined>` shape.)
- [ ] `internal/catalog/in_memory_repository.go` exports `InMemoryRepository` struct + `NewInMemoryRepository() *InMemoryRepository`. Implements every method of `Repository`. Backed by two `map`s keyed by id (with insertion-order tracking for `ListBooks` — use a slice of book-ids alongside the map, or an ordered map shim; the TS source returns insertion order via `Array.from(Map.values())` which JavaScript guarantees, and the Go in-memory impl must match).
- [ ] `ListBooks` returns books in insertion order. Asserted by Slice 2's facade-level test "lists all books in the order they were added".
- [ ] `ListBooksByIds` returns one row per matching book regardless of duplicates in the input slice (asserted by Slice 2 test "returns one row per matching book when the caller passes duplicate ids").
- [ ] `ListBooksByIds` with an empty `bookIds` slice returns `([]BookDto{}, nil)` (not nil slice — assert with `len(books) == 0` and `books != nil` if a downstream caller cares; the facade's `GetBooks` short-circuits before calling the repo on empty input per TS source line 181).
- [ ] `internal/catalog/in_memory_repository_test.go` (unit, no build tag) covers each method's happy path + the "not found returns nil" path. Stdlib `testing` only.

#### Acceptance Criteria — facade

- [ ] `internal/catalog/facade.go` exports `type Facade struct` with unexported fields and a constructor `NewFacade(repo Repository, newID func() string, isbnGateway isbngateway.IsbnLookupGateway, cache bookcache.BookCacheGateway, accessControl *accesscontrol.Facade, logger *slog.Logger) *Facade`. (No `fileStorage` parameter in Phase 2 — thumbnails are deferred. Phase 5 adds it.)
- [ ] `Facade.AddBook(ctx, dto NewBookDto) (BookDto, error)` matches the TS `addBook` semantics: parse ISBN → call gateway `FindByIsbn(ctx, isbn)` → merge gateway-supplied metadata into the dto under the rule "client-supplied wins; gateway fills only missing/blank fields" (matches TS `merged.title = dto.title ?? enrichment?.title` and `merged.authors = dto.authors?.length ? dto.authors : enrichment?.authors`) → parse the merged dto with `ParseNewBook` → check `FindBookByIsbn` for an existing book and return `*DuplicateIsbnError{Isbn}` if found → mint a new `BookId` via `newID()` → `SaveBook` → return the saved `BookDto`. The duplicate check happens AFTER gateway enrichment so the merged ISBN is what gets compared (matches TS test "enriches before the duplicate-ISBN check and compares on the merged ISBN").
- [ ] `AddBook` does NOT write to the cache. (Matches TS test AC-2.4: `addBook does NOT populate the cache`.) Only `FindBook`, `UpdateBook`, and `DeleteBook` touch the cache.
- [ ] `Facade.FindBook(ctx, isbn Isbn) (BookDto, error)`: call `cache.Get(ctx, isbn)` — if non-nil, return it (cache hit). On cache miss, call `repo.FindBookByIsbn`; if also nil, return `*BookNotFoundError{Identifier: string(isbn)}`. On repo hit, populate the cache via `cache.Set(ctx, isbn, book)` and return the book.
- [ ] `FindBook` does NOT negative-cache. On cache MISS + repo MISS, the cache stays empty for that isbn. (Matches TS AC-2.3.)
- [ ] `Facade.UpdateBook(ctx, bookId BookId, dto UpdateBookDto) (BookDto, error)`: parse the dto via `ParseUpdateBook` → call `repo.FindBookById`; return `*BookNotFoundError{Identifier: string(bookId)}` if nil → build the updated `BookDto` by applying non-nil patch fields onto the existing record → `repo.SaveBook(updated)` → `cache.Set(ctx, existing.Isbn, updated)` (write-through) → return updated.
- [ ] `Facade.DeleteBook(ctx, bookId BookId) error`: `repo.FindBookById`; return `*BookNotFoundError{Identifier: string(bookId)}` if nil → `repo.DeleteBook(bookId)` → `cache.Evict(ctx, existing.Isbn)` → return nil.
- [ ] `Facade.ListBooks(ctx) ([]BookDto, error)`: returns `repo.ListBooks` directly.
- [ ] `Facade.GetBooks(ctx, bookIds []BookId) ([]BookDto, error)`: if `len(bookIds) == 0` return `([]BookDto{}, nil)` WITHOUT calling the repo (asserted in Slice 2 — "returns [] for an empty bookIds array without throwing"). Otherwise delegate to `repo.ListBooksByIds`.
- [ ] `Facade.RegisterCopy(ctx, bookId BookId, dto NewCopyDto) (CopyDto, error)`: parse via `ParseNewCopy` → `repo.FindBookById`; return `*BookNotFoundError{Identifier: string(bookId)}` if nil → mint a `CopyId` via `newID()` → build `CopyDto{Status: CopyStatusAvailable}` (new copies default to AVAILABLE — matches TS AC) → `repo.SaveCopy` → return.
- [ ] `Facade.FindCopy(ctx, copyId CopyId) (CopyDto, error)`: `repo.FindCopyById`; return `*CopyNotFoundError{CopyId}` if nil; else return the copy.
- [ ] `Facade.MarkCopyAvailable(ctx, copyId CopyId) (CopyDto, error)` and `Facade.MarkCopyUnavailable(ctx, copyId CopyId) (CopyDto, error)` share an unexported `updateCopyStatus(ctx, copyId, status)` helper: load copy or return `*CopyNotFoundError`; flip status; save; return.
- [ ] `Facade` exposes NO `AttachThumbnail` / `ReadThumbnail` / `RemoveThumbnail` methods in Phase 2 (deferred to Phase 5 per the Out-of-Scope decision).
- [ ] On every cache write path (`FindBook` populate-after-miss, `UpdateBook` write-through, `DeleteBook` evict), a cache error is propagated to the caller — the facade does NOT swallow cache errors. The repository write is the source of truth; the cache failure surfaces but the repo write remains durable (matches TS AC-5.3 / AC-5.4 fault-injection tests).

#### Acceptance Criteria — sample data (functional options)

- [ ] `internal/catalog/sample_data.go` exports `SampleNewBook(opts ...NewBookOption) NewBookDto` returning default `NewBookDto{Title: "The Pragmatic Programmer", Authors: []string{"Andrew Hunt", "David Thomas"}, Isbn: "978-0135957059"}` mutated by each option. Matches TS `sampleNewBook` defaults exactly.
- [ ] `sample_data.go` exports `SampleNewBookWithIsbn(isbn Isbn) NewBookDto` as a shorthand for `SampleNewBook(WithIsbn(isbn))`.
- [ ] `sample_data.go` exports `SampleUpdateBook(opts ...UpdateBookOption) UpdateBookDto` returning a default with `Title: ptr("Updated Title")` and `Authors: ptr([]string{"Updated Author"})`.
- [ ] `sample_data.go` exports `SampleNewCopy(opts ...NewCopyOption) NewCopyDto` returning default `NewCopyDto{BookId: "book-placeholder-id", Condition: CopyConditionGood}` mutated by each option.
- [ ] Option types: `type NewBookOption func(*NewBookDto)`, `type UpdateBookOption func(*UpdateBookDto)`, `type NewCopyOption func(*NewCopyDto)`. Options exported: `WithTitle(string) NewBookOption`, `WithAuthors([]string) NewBookOption`, `WithIsbn(Isbn) NewBookOption`, `WithCondition(CopyCondition) NewCopyOption`, `WithBookId(BookId) NewCopyOption`. For `UpdateBookDto`: `WithUpdateTitle(string) UpdateBookOption` (sets the pointer), `WithUpdateAuthors([]string) UpdateBookOption`, plus `WithUpdateTitleNil() UpdateBookOption` / `WithUpdateAuthorsNil() UpdateBookOption` for explicitly testing the "field absent" path.
- [ ] Override-order is deterministic: a later option overwrites an earlier option.

#### Acceptance Criteria — configuration

- [ ] `internal/catalog/configuration.go` exports `type Overrides struct { Repository Repository; NewID func() string; IsbnLookupGateway isbngateway.IsbnLookupGateway; BookCacheGateway bookcache.BookCacheGateway; AccessControl *accesscontrol.Facade; Logger *slog.Logger }` — every field optional (zero value means "use the default").
- [ ] `configuration.go` exports `NewFacadeWithOverrides(o Overrides) *Facade` that substitutes defaults for missing fields: `Repository → NewInMemoryRepository()`, `NewID → uuid.NewString` (`github.com/google/uuid`, already in `go.mod` from Phase 1), `IsbnLookupGateway → isbngateway.NewInMemoryIsbnLookupGateway()`, `BookCacheGateway → bookcache.NewInMemoryBookCacheGateway()`, `AccessControl → accesscontrol.NewFacade()`, `Logger → slog.New(slog.DiscardHandler)` (Go 1.24+ has `slog.DiscardHandler`; if the project is still on 1.23, fall back to a no-op `slog.NewTextHandler(io.Discard, nil)`). Then call `NewFacade(...)` with the resolved deps and return.
- [ ] `NewFacadeWithOverrides` is what Slice 2's facade tests use (`buildFacade()` → `catalog.NewFacadeWithOverrides(catalog.Overrides{NewID: sequentialIds()})`). The TS source's `createCatalogFacade({newId: sequentialIds()})` maps 1:1.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go build ./...` passes — every type, parser, repository method, facade method, sample-data builder, and the configuration constructor compile.
- [ ] `go vet ./...` passes with no findings.
- [ ] No `init()` function appears anywhere in `internal/catalog/`.
- [ ] No file in `internal/catalog/` imports `internal/catalog/http/` (the http subdir doesn't exist yet; the compiler enforces this trivially).
- [ ] The slice does NOT touch `internal/app/wiring.go` — wiring lands in Slice 3. Slice 1's catalog facade is constructible but unwired.

---

### Slice 2: Catalog facade-level tests (port from `catalog.facade.spec.ts`)

Brings the repository to "every behaviour Slice 1 promised is asserted by a test that runs in under 100 ms with in-memory substrates." The TS source's `catalog.facade.spec.ts` has roughly 60 scenarios across 9 `describe` blocks (basic CRUD, `getBooks`, ISBN enrichment, gateway failures, cache read-through, `updateBook`, `deleteBook`, cache gateway failures, copy lifecycle). Phase 2 ports every scenario EXCEPT those touching thumbnails or file storage (deferred). Slice 2's test count target is **~45 scenarios** at the facade level. Slice 4 adds the `bun_repository_test.go` contract tests against testcontainers Postgres.

#### Acceptance Criteria — test scaffolding

- [ ] `internal/catalog/facade_test.go` lives in package `catalog` (not `catalog_test`) so it can reference unexported helpers without a barrel re-export.
- [ ] A `sequentialIds(prefix string) func() string` test helper (closure over a counter) is declared at the top of `facade_test.go` and reused — matches the TS source's `sequentialIds`. Default prefix `"id"`.
- [ ] A `buildFacade(opts ...catalog.OverrideOption) *catalog.Facade` test helper (or equivalent inline call to `NewFacadeWithOverrides`) constructs a facade with deterministic ids and in-memory substrates. Reused across every test.
- [ ] Tests use stdlib `testing` only — `t.Run`, `t.Errorf`, `t.Fatalf`, `errors.As`, `errors.Is`. No testify.
- [ ] The full `internal/catalog/facade_test.go` runs in well under 1 second (target: under 200 ms).

#### Acceptance Criteria — basic CRUD scenarios (port of describe block 1)

- [ ] AC: `AddBook` then `FindBook(isbn)` returns the same book.
- [ ] AC: `ListBooks` returns books in insertion order.
- [ ] AC: `RegisterCopy` then `FindCopy(copyId)` returns the same copy.
- [ ] AC: A newly registered copy has status `CopyStatusAvailable` by default.
- [ ] AC: `MarkCopyUnavailable` then `MarkCopyAvailable` toggles status to AVAILABLE and a subsequent `FindCopy` reflects it.
- [ ] AC: `MarkCopyUnavailable` on a newly-registered (AVAILABLE) copy flips status to UNAVAILABLE.
- [ ] AC: `FindBook` on an unknown isbn returns a `*BookNotFoundError` (`errors.As` match).
- [ ] AC: `RegisterCopy` against an unknown bookId returns a `*BookNotFoundError`.
- [ ] AC: `MarkCopyAvailable` on an unknown copyId returns a `*CopyNotFoundError`.
- [ ] AC: `MarkCopyUnavailable` on an unknown copyId returns a `*CopyNotFoundError`.
- [ ] AC: `AddBook` with a blank or whitespace-only title returns `*InvalidBookError`.
- [ ] AC: `AddBook` with no authors (empty slice or all-blank entries) returns `*InvalidBookError`.
- [ ] AC: `AddBook` with a malformed isbn (empty / "123" / "not-an-isbn") returns `*InvalidBookError`.
- [ ] AC: `AddBook` accepts well-formed ISBN-13 hyphenated, ISBN-13 plain, ISBN-10 hyphenated. All three stored and findable by their exact ISBN string.
- [ ] AC: `AddBook` trims surrounding whitespace from title, each author, and isbn (assert via `book.Title == "The Pragmatic Programmer"` etc.).
- [ ] AC: `RegisterCopy` with `Condition: "BROKEN"` returns `*InvalidCopyError`.
- [ ] AC: `AddBook` with an isbn that already exists returns `*DuplicateIsbnError`.

#### Acceptance Criteria — `GetBooks` scenarios (port of describe block 2)

- [ ] AC: `GetBooks([])` returns `[]BookDto{}` without calling the repo (seed two books to prove the short-circuit doesn't depend on emptiness).
- [ ] AC: `GetBooks([bookA.BookId, bookB.BookId])` returns both books (order unspecified).
- [ ] AC: `GetBooks([known, unknown])` returns only the matching book; unknown ids silently dropped.
- [ ] AC: `GetBooks([id, id, otherId])` returns one row per matching book (dedup is caller's responsibility but the repo filter doesn't duplicate output).
- [ ] AC: `GetBooks([unknown1, unknown2])` returns `[]BookDto{}`.

#### Acceptance Criteria — ISBN enrichment scenarios (port of describe block 3)

- [ ] Test helper `buildFacadeWithGateway(seed map[Isbn]isbngateway.BookMetadata) (*catalog.Facade, *isbngateway.InMemoryIsbnLookupGateway)` constructs a facade wired to a seeded in-memory gateway.
- [ ] AC: Missing title + gateway has title → saved title comes from gateway, client authors win.
- [ ] AC: Missing authors + gateway has authors → saved authors come from gateway, client title wins.
- [ ] AC: Client title supplied + gateway has a different title → client title wins.
- [ ] AC: Client authors supplied + gateway has different authors → client authors win.
- [ ] AC: Unseeded gateway + full client data → save succeeds with client data.
- [ ] AC: Missing title on both sides → `*InvalidBookError`.
- [ ] AC: Missing title AND authors on both sides → `*InvalidBookError`.
- [ ] AC: Enrichment happens BEFORE duplicate-ISBN check (seed gateway + add book with isbn X; second `AddBook({Isbn: X})` triggers enrichment then duplicate check → returns `*DuplicateIsbnError`).
- [ ] AC: A facade built with `NewFacadeWithOverrides(catalog.Overrides{IsbnLookupGateway: seededGateway})` uses the override, not a fresh default.
- [ ] AC: A facade built with no `IsbnLookupGateway` override falls back to a fresh empty in-memory gateway (verified by `AddBook` with only an ISBN returning `*InvalidBookError` because there's nothing to enrich from).
- [ ] AC: The returned `BookDto` from `AddBook` reflects the merged persisted shape (title from gateway, authors from client) — and the same shape round-trips through `FindBook`.

#### Acceptance Criteria — gateway-failure scenarios (port of describe block 4 — spec-local `ThrowingOnceIsbnLookupGateway`)

- [ ] `facade_test.go` declares an **unexported** `throwingOnceIsbnLookupGateway` struct in the same file (NOT in a shared package, NOT exported). Wraps a real `isbngateway.IsbnLookupGateway`. Method `armFailureOnNextLookup(err error)`. `FindByIsbn` checks the armed error, clears it, returns it; otherwise delegates. (Mirrors TS `ThrowingOnceIsbnLookupGateway`.)
- [ ] AC: `FindByIsbn` armed with `errors.New("isbn service is down")` → `AddBook` surfaces that exact error to the caller (assert via `err.Error() == "isbn service is down"` or `errors.Is(err, armed)`).
- [ ] AC: After a gateway failure, the repo has no record for that ISBN (`FindBook(isbn)` returns `*BookNotFoundError`; `ListBooks()` returns `[]BookDto{}`).
- [ ] AC: After a single-shot armed failure, the next `AddBook` call succeeds (state clears).

#### Acceptance Criteria — cache read-through scenarios (port of describe block 5)

- [ ] Test helper `buildScene()` returns `(*bookcache.InMemoryBookCacheGateway, *catalog.Facade)` with a fresh in-memory cache.
- [ ] AC: Cache HIT → `FindBook` returns cached `BookDto` without consulting the repo (seed cache with a `BookDto` for an ISBN that is NOT in the repo; `FindBook(isbn)` returns the seeded entry).
- [ ] AC: Cache MISS + repo HIT → returns repo book AND populates the cache (verified by direct `cache.Get(ctx, isbn)`).
- [ ] AC: Cache MISS + repo MISS → returns `*BookNotFoundError` AND does NOT negative-cache (verified by `cache.Get(ctx, isbn)` still returning `nil, nil`).
- [ ] AC: `AddBook` does NOT populate the cache (`cache.Get(ctx, isbn) == nil, nil` after `AddBook`).
- [ ] AC: Two consecutive `FindBook` calls after a fresh add — first populates the cache, second reads from the cache.
- [ ] AC: A facade built with `NewFacadeWithOverrides(BookCacheGateway: overrideCache)` uses the override; default-built facade falls back to a fresh in-memory cache.

#### Acceptance Criteria — `UpdateBook` scenarios (port of describe block 6)

- [ ] AC: Title-only patch — returned DTO has new title, original authors/bookId/isbn.
- [ ] AC: Authors-only patch — returned DTO has new authors, original title/bookId/isbn.
- [ ] AC: Title + authors patch — both updated atomically.
- [ ] AC: Write-through cache — `FindBook(isbn)` after update returns updated DTO.
- [ ] AC: Write-through cache — direct `cache.Get(ctx, isbn)` returns the updated DTO immediately after `UpdateBook` (no intermediate `FindBook` needed).
- [ ] AC: Unknown bookId → `*BookNotFoundError`. Cache for any unrelated ISBN is NOT modified.
- [ ] AC: Empty patch (both `Title` and `Authors` nil) → `*InvalidBookError` matching the regex `at least one of title or authors must be provided`.
- [ ] AC: Whitespace-only title (`Title: ptr("   ")`) → `*InvalidBookError`.
- [ ] AC: Empty authors slice (`Authors: ptr([]string{})`) → `*InvalidBookError`. All-blank authors (`Authors: ptr([]string{"", "   "})`) → `*InvalidBookError`.
- [ ] AC: `UpdateBookDto` cannot carry an `Isbn` field (compile-time — `UpdateBookDto` has no `Isbn` field). The "isbn cannot be updated" enforcement lives at the HTTP DTO mapping layer; Slice 3 asserts that an inbound JSON body with an `isbn` key returns 400.

#### Acceptance Criteria — `DeleteBook` scenarios (port of describe block 7)

- [ ] AC: `DeleteBook` on an existing book returns nil.
- [ ] AC: After delete, `FindBook(isbn)` returns `*BookNotFoundError`.
- [ ] AC: Cache state — deleting a never-cached book leaves the cache empty for that isbn.
- [ ] AC: Cache state — deleting a previously-cached book evicts the cache entry.
- [ ] AC: Unknown bookId → `*BookNotFoundError`; an unrelated cache entry is NOT modified.
- [ ] AC: Two-book scenario — deleting one only evicts that one; the other survives.
- [ ] AC: Second delete of the same bookId → `*BookNotFoundError`.
- [ ] AC: `AddBook` with the same ISBN AFTER a delete succeeds with a fresh bookId.

#### Acceptance Criteria — cache-failure scenarios (port of describe block 8 — spec-local `throwingOnceBookCacheGateway`)

- [ ] `facade_test.go` declares an **unexported** `throwingOnceBookCacheGateway` struct in the same file. Wraps a real `bookcache.BookCacheGateway`. Methods: `armFailureOnNextSet(err)`, `armFailureOnNextGet(err)`, `armFailureOnNextEvict(err)`. Each method checks-clears-throws-or-delegates. (Mirrors TS `ThrowingOnceBookCacheGateway`.)
- [ ] AC: Armed `cache.Set` failure during `FindBook` miss-then-populate — exact armed error surfaces to caller; subsequent `FindBook` succeeds (arming was single-shot).
- [ ] AC: Armed `cache.Get` failure during `FindBook` — exact armed error surfaces; subsequent `FindBook` succeeds.
- [ ] AC: Armed `cache.Set` failure during `UpdateBook` write-through — exact armed error surfaces; the repo write is durable (next `FindBook` returns the new title).
- [ ] AC: Armed `cache.Evict` failure during `DeleteBook` — exact armed error surfaces; the repo delete is durable (next `FindBook` returns `*BookNotFoundError`).

#### Acceptance Criteria — `getBooks` short-circuit (sanity)

- [ ] AC: The unit test asserts `GetBooks([])` does NOT touch the repository. Use an unexported `recordingRepository` decorator (same spec-local pattern) wrapping `NewInMemoryRepository()` and counting calls; assert `recordingRepo.ListBooksByIdsCallCount == 0` after `GetBooks([])`.

---

### Slice 3: Catalog HTTP handlers + DTOs + mapping + wire into chi

Brings the repository to "every catalog facade method is reachable over HTTP at its canonical URL, with HTTP-DTO ↔ domain-DTO mapping at the edge and an exhaustive handler test suite." Slice 3 does NOT introduce real Postgres — handlers run against in-memory substrates in tests. Slice 4 adds the bun repository and the crucial-path integration test against testcontainers Postgres.

#### Acceptance Criteria — HTTP DTOs

- [ ] `internal/catalog/http/dto.go` exports `AddBookRequest struct { Title string; Authors []string; Isbn string }` with `json:` tags (`json:"title"`, `json:"authors"`, `json:"isbn"`). `Title` and `Authors` are present as plain (not pointer) types because the gateway-enrichment merge step handles "field absent" via blank-string check.
- [ ] `dto.go` exports `UpdateBookRequest struct { Title *string ` `json:"title,omitempty"` `; Authors *[]string ` `json:"authors,omitempty"` ` }` — pointer fields so JSON decoder distinguishes "field absent" vs "field present with empty value." (Use `*string` because Go's `encoding/json` unmarshals an absent field to the zero value otherwise, indistinguishable from an explicit empty string.)
- [ ] `dto.go` exports `BookResponse struct { BookId string; Title string; Authors []string; Isbn string }` with snake_case `json:` tags matching the TS API contract — `json:"bookId"`, `json:"title"`, `json:"authors"`, `json:"isbn"`. (The TS source uses camelCase JSON; the Go port matches to keep the API contract byte-identical across the port.)
- [ ] `dto.go` exports `NewCopyRequest struct { Condition string ` `json:"condition"` ` }`. (`BookId` is NOT in the body — it's a URL parameter; the TS controller takes it from `:bookId`.)
- [ ] `dto.go` exports `CopyResponse struct { CopyId string ` `json:"copyId"` `; BookId string ` `json:"bookId"` `; Condition string ` `json:"condition"` `; Status string ` `json:"status"` ` }`.
- [ ] `dto.go` types are never imported outside `internal/catalog/http/`. Verified by `Grep` in CI or by code review — there is no enforcement linter in Phase 2 (D25).

#### Acceptance Criteria — mapping

- [ ] `internal/catalog/http/mapping.go` exports `fromAddBookRequest(req AddBookRequest) catalog.NewBookDto` translating field-for-field (`Isbn` string → `catalog.Isbn`).
- [ ] `mapping.go` exports `fromUpdateBookRequest(req UpdateBookRequest) catalog.UpdateBookDto` translating the pointer fields directly.
- [ ] `mapping.go` exports `toBookResponse(book catalog.BookDto) BookResponse` and `toCopyResponse(copy catalog.CopyDto) CopyResponse`.
- [ ] `mapping.go` enforces "isbn cannot be updated" at the JSON-decode step: the handler decodes into a `struct{ Isbn json.RawMessage ` `json:"isbn,omitempty"` ` UpdateBookRequest }` (or a similar disambiguation pattern); if `Isbn` is present (non-empty `RawMessage`), the handler returns `*catalog.InvalidBookError{Reason: "isbn cannot be updated"}`. **Implementation hint** (not an AC, just the recommended approach): use `json.Decoder.DisallowUnknownFields()` to make `UpdateBookRequest` reject `isbn` — that's mechanical and matches the TS source's `.strict('isbn cannot be updated')`.
- [ ] Mapping functions are unexported (lowercase) — they live inside the `http` subpackage and never leak out.

#### Acceptance Criteria — handlers

- [ ] `internal/catalog/http/handlers.go` exports `Handlers struct` carrying `*catalog.Facade` + `*slog.Logger`. Constructor `NewHandlers(facade *catalog.Facade, logger *slog.Logger) *Handlers`.
- [ ] Handlers go through the `sharedhttp.Handle(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc` wrapper (already shipped in Phase 1) so domain errors returned by handlers are mapped via `DomainErrorMiddleware`.
- [ ] `Handlers.AddBook(w, r) error`: decode `AddBookRequest` (with `DisallowUnknownFields` rejecting extras → `*catalog.InvalidBookError`); call `facade.AddBook(ctx, fromAddBookRequest(req))`; on success `sharedhttp.WriteJSON(w, http.StatusCreated, toBookResponse(book))`; on error return the error (middleware maps it).
- [ ] `Handlers.ListBooks(w, r) error`: call `facade.ListBooks(ctx)`; respond `200` with `[]BookResponse` (empty list serializes as `[]`, not `null` — assert by initializing `responses := make([]BookResponse, 0, len(books))`).
- [ ] `Handlers.FindBook(w, r) error`: read `:isbn` URL param via `chi.URLParam(r, "isbn")`; call `facade.FindBook(ctx, catalog.Isbn(isbn))`; respond `200` with `BookResponse`.
- [ ] `Handlers.UpdateBook(w, r) error`: read `:bookId` URL param; decode `UpdateBookRequest` (with `DisallowUnknownFields`); call `facade.UpdateBook(ctx, catalog.BookId(bookId), fromUpdateBookRequest(req))`; respond `200`.
- [ ] `Handlers.DeleteBook(w, r) error`: read `:bookId`; call `facade.DeleteBook(ctx, catalog.BookId(bookId))`; respond `204` with empty body.
- [ ] `Handlers.RegisterCopy(w, r) error`: read `:bookId`; decode `NewCopyRequest`; call `facade.RegisterCopy(ctx, catalog.BookId(bookId), catalog.NewCopyDto{BookId: catalog.BookId(bookId), Condition: catalog.CopyCondition(req.Condition)})`; respond `201` with `CopyResponse`.
- [ ] `Handlers.MarkCopyAvailable(w, r) error`: read `:copyId`; call `facade.MarkCopyAvailable(ctx, catalog.CopyId(copyId))`; respond `200` with `CopyResponse`.
- [ ] `Handlers.MarkCopyUnavailable(w, r) error`: same shape as MarkCopyAvailable.

#### Acceptance Criteria — `module.go` (wire into chi)

- [ ] `internal/catalog/module.go` exports `type Deps struct { Facade *catalog.Facade; Logger *slog.Logger }` and `func Wire(r chi.Router, deps Deps)`.
- [ ] `Wire` mounts the routes:
  - `POST /books` → `AddBook`
  - `GET /books` → `ListBooks`
  - `GET /books/{isbn}` → `FindBook`
  - `PATCH /books/{bookId}` → `UpdateBook`
  - `DELETE /books/{bookId}` → `DeleteBook`
  - `POST /books/{bookId}/copies` → `RegisterCopy`
  - `PATCH /copies/{copyId}/available` → `MarkCopyAvailable`
  - `PATCH /copies/{copyId}/unavailable` → `MarkCopyUnavailable`
- [ ] `Wire` is the ONLY public surface that registers routes; it does NOT construct the facade (the composition root does). The Deps struct is the seam.

#### Acceptance Criteria — composition-root integration (`internal/app/wiring.go`)

- [ ] `internal/app/wiring.go` Slice-3 changes: `Wire` constructs the catalog facade via `catalog.NewFacadeWithOverrides(catalog.Overrides{...})` (passing the bun-backed repository will land in Slice 4; in Slice 3 the catalog facade is constructed with the in-memory repo as a temporary stand-in OR Slice 3 wires it directly via `NewFacade` with explicit deps — either is acceptable as long as Slice 4 swaps in the bun repo). **Recommended**: Slice 3 wires with the in-memory repo so the slice can ship end-to-end and Slice 4's diff is just "swap in the bun repo." Recorded as Open Question 2 with default to use the in-memory repo in Slice 3.
- [ ] `wiring.go` extends `buildDomainErrorRegistry` to register: `*catalog.BookNotFoundError → 404 "book_not_found"`, `*catalog.CopyNotFoundError → 404 "copy_not_found"`, `*catalog.DuplicateIsbnError → 409 "duplicate_isbn"`, `*catalog.InvalidBookError → 400 "invalid_book"`, `*catalog.InvalidCopyError → 400 "invalid_copy"`.
- [ ] `wiring.go` calls `catalogModule.Wire(router, catalogModule.Deps{Facade: catalogFacade, Logger: logger})` after constructing the facade.
- [ ] `wiring.go`'s `Wired` struct gains a `CatalogFacade *catalog.Facade` field so integration tests can introspect (e.g. assert that the bun repo really saved the row).

#### Acceptance Criteria — handler tests

- [ ] `internal/catalog/http/handlers_test.go` (unit, no build tag) constructs a real `*catalog.Facade` via `catalog.NewFacadeWithOverrides(catalog.Overrides{NewID: sequentialIds()})` (no double-substitution — wire real facade with in-memory deps), wraps it in `NewHandlers(facade, logger)`, and exercises each handler via `httptest.NewRecorder` + a hand-built `*http.Request` (using `chi.NewRouter()` to get URL-param resolution working).
- [ ] AC: `POST /books` with a valid body returns 201 + `BookResponse` JSON.
- [ ] AC: `POST /books` with an empty title returns 400 + `ErrorResponse{Error: "invalid_book"}`.
- [ ] AC: `POST /books` with a malformed isbn returns 400 + `ErrorResponse{Error: "invalid_book"}`.
- [ ] AC: `POST /books` with a duplicate isbn returns 409 + `ErrorResponse{Error: "duplicate_isbn"}`.
- [ ] AC: `POST /books` with an unknown JSON field returns 400 (driven by `DisallowUnknownFields`).
- [ ] AC: `GET /books` returns 200 + `[]` when empty.
- [ ] AC: `GET /books/{isbn}` on a known isbn returns 200 + `BookResponse`.
- [ ] AC: `GET /books/{isbn}` on an unknown isbn returns 404 + `ErrorResponse{Error: "book_not_found"}`.
- [ ] AC: `PATCH /books/{bookId}` with `{"title": "X"}` returns 200 + updated `BookResponse`.
- [ ] AC: `PATCH /books/{bookId}` with `{"isbn": "X"}` returns 400 + `ErrorResponse{Error: "invalid_book", Message: contains "isbn cannot be updated"}`.
- [ ] AC: `PATCH /books/{bookId}` with `{}` returns 400.
- [ ] AC: `DELETE /books/{bookId}` returns 204 with empty body.
- [ ] AC: `DELETE /books/{bookId}` on an unknown id returns 404.
- [ ] AC: `POST /books/{bookId}/copies` with a valid condition returns 201 + `CopyResponse`.
- [ ] AC: `POST /books/{bookId}/copies` with an invalid condition returns 400 + `ErrorResponse{Error: "invalid_copy"}`.
- [ ] AC: `POST /books/{bookId}/copies` on an unknown bookId returns 404.
- [ ] AC: `PATCH /copies/{copyId}/available` returns 200 + `CopyResponse{Status: "AVAILABLE"}`.
- [ ] AC: `PATCH /copies/{copyId}/unavailable` returns 200 + `CopyResponse{Status: "UNAVAILABLE"}`.
- [ ] AC: `PATCH /copies/{copyId}/available` on unknown id returns 404 + `ErrorResponse{Error: "copy_not_found"}`.
- [ ] Tests assert response bodies via `json.NewDecoder(rec.Body).Decode(&resp)` + field-by-field equality. No string-matching against raw JSON bytes (whitespace tolerance).

#### Acceptance Criteria — `.http` file activation

- [ ] `.http/catalog.http` exists. Active requests using `{{baseUrl}}`: `POST {{baseUrl}}/books` (sample body with title + authors + isbn), `GET {{baseUrl}}/books`, `GET {{baseUrl}}/books/978-0135957059`, `PATCH {{baseUrl}}/books/<bookId>` (sample title-only body), `DELETE {{baseUrl}}/books/<bookId>`, `POST {{baseUrl}}/books/<bookId>/copies`, `PATCH {{baseUrl}}/copies/<copyId>/available`, `PATCH {{baseUrl}}/copies/<copyId>/unavailable`.
- [ ] The Phase-2 placeholder lines in `.http/healthz.http` (`### POST {{baseUrl}}/books — Phase 2 (catalog)`) are deleted or moved into `catalog.http`. (Choose one; recommend leaving `healthz.http` as the smoke probe and moving Phase-2 endpoints into `catalog.http`.)

---

### Slice 4: Catalog `bun_repository.go` + `0001_catalog.sql` + crucial-path integration test

Brings the repository to "every HTTP endpoint actually persists to Postgres, the `books` and `copies` tables exist, and a crucial-path integration test boots the real composition root via testcontainers and exercises the HTTP surface end-to-end."

#### Acceptance Criteria — migration

- [ ] `migrations/0001_catalog.sql` exists. Creates two tables:
  - `books`: `book_id UUID PRIMARY KEY`, `title TEXT NOT NULL`, `authors TEXT[] NOT NULL`, `isbn TEXT NOT NULL UNIQUE`.
  - `copies`: `copy_id UUID PRIMARY KEY`, `book_id UUID NOT NULL REFERENCES books(book_id)`, `condition TEXT NOT NULL`, `status TEXT NOT NULL`.
- [ ] `migrations/atlas.sum` is regenerated via `atlas migrate hash` and committed.
- [ ] `task migrate:apply` against a fresh dev Postgres creates both tables and is a no-op on second run.
- [ ] `task migrate:status` after apply reports both migrations applied (the Phase-1 `.gitkeep` migration if it exists, plus `0001_catalog.sql`).

#### Acceptance Criteria — bun repository

- [ ] `internal/catalog/bun_repository.go` exports `BunRepository struct { db *bun.DB }` and `NewBunRepository(db *bun.DB) *BunRepository`. Implements every method of `Repository`.
- [ ] Bun struct tags: `BookRow struct { BookId BookId ` `bun:"book_id,pk"` `; Title string ` `bun:"title,notnull"` `; Authors []string ` `bun:"authors,array,notnull"` `; Isbn Isbn ` `bun:"isbn,notnull,unique"` ` }` mapped to table `books` via `bun:"table:books"`. Similar `CopyRow` mapped to `copies`.
- [ ] `SaveBook` uses `db.NewInsert().Model(&row).On("CONFLICT (book_id) DO UPDATE SET title = EXCLUDED.title, authors = EXCLUDED.authors, isbn = EXCLUDED.isbn").Exec(ctx)` — matches the TS source's `onConflictDoUpdate` (upsert by primary key). Same pattern for `SaveCopy`.
- [ ] `FindBookById` / `FindBookByIsbn`: `db.NewSelect().Model(&row).Where("book_id = ?", id).Scan(ctx)` (or `where isbn = ?`); on `sql.ErrNoRows` return `(nil, nil)`; on other errors wrap and return.
- [ ] `ListBooks` returns rows ordered by insertion (use an implicit `book_id` ASC order via `OrderExpr("book_id")` OR — recommended — add a generated `created_at` column to the migration). **Open Question 3**: does the bun repo need explicit ordering to match the in-memory repo's insertion order? Recommended default: order by `book_id` ASC (UUIDs are not insertion-monotonic, but the order is at least deterministic; the in-memory repo will need to use the same ordering rule for the bun contract test to match — adjust the in-memory impl in Slice 4 if needed). Alternative: add a `created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()` column to `books` + `copies` and order by it. Decide in Slice 4; the AC is "the in-memory repo and bun repo return books in the same order for the same insertion sequence." Pick one approach and apply it consistently.
- [ ] `ListBooksByIds` uses `db.NewSelect().Model(&rows).Where("book_id IN (?)", bun.In(bookIds)).Scan(ctx)`; on empty input returns `([]BookDto{}, nil)` without hitting the DB.
- [ ] `DeleteBook` uses `db.NewDelete().Model((*BookRow)(nil)).Where("book_id = ?", id).Exec(ctx)` — note the foreign key constraint on `copies.book_id`: deleting a book with copies will fail. The facade does NOT pre-delete copies in Phase 2 (the TS source doesn't either; cascade behaviour is not in `catalog.facade.spec.ts`). **Open Question 4**: should `DeleteBook` cascade copies? Recommended default: **no cascade** — match TS source 1:1. If a Phase 3+ test trips the FK, adjust then.
- [ ] Row ↔ DTO mapping via `toBookRow(BookDto) BookRow` and `toBookDto(BookRow) BookDto` (and same for copies). Defensive `Authors` copy on both directions (`append([]string(nil), src.Authors...)`).

#### Acceptance Criteria — bun repository contract test (build tag integration)

- [ ] `internal/catalog/bun_repository_test.go` has `//go:build integration` at the top.
- [ ] The test starts a Postgres container via `test/support.StartPostgres(ctx, t)`, applies migrations via the existing `db.ApplyMigrations` path, constructs the bun client via `db.NewBunDB`, instantiates `BunRepository`.
- [ ] AC: Every scenario from `in_memory_repository_test.go` (Slice 1) runs against the bun repo via the shared contract test (extract a `func runRepositoryContract(t *testing.T, repo Repository)` helper if convenient; OR duplicate the scenarios — the AC is "the bun repo behaves identically to the in-memory repo for every scenario").
- [ ] AC: A test-level `t.Cleanup` truncates `books` and `copies` between tests (or each test runs in a transaction that's rolled back) so tests don't see each other's state. Recommended approach: `TRUNCATE books, copies RESTART IDENTITY CASCADE` between tests. The container is shared across the suite via a package-level setup.

#### Acceptance Criteria — composition-root swap (Slice 4 only)

- [ ] `internal/app/wiring.go` swaps the catalog facade's repository from `NewInMemoryRepository()` (Slice 3) to `catalog.NewBunRepository(bunDB)`. After the swap, every HTTP request reads/writes Postgres.
- [ ] No other slice's tests break — facade-level tests still use in-memory because they go through `NewFacadeWithOverrides`, not `Wire`.

#### Acceptance Criteria — crucial-path integration test

- [ ] `test/crucial_path/` directory is created (Phase 1's `test/` only had `integration/` and `support/`).
- [ ] `test/crucial_path/catalog_integration_test.go` (`//go:build integration`) does the full vertical: `StartPostgres` + `StartRedis` + `BootApp` against those containers.
- [ ] AC: `POST /books` with `{"title":"The Pragmatic Programmer","authors":["Andrew Hunt","David Thomas"],"isbn":"978-0135957059"}` returns 201; the response body's `bookId` is a non-empty UUID; a follow-up `GET /books/978-0135957059` returns the same book.
- [ ] AC: `POST /books` with a duplicate isbn returns 409 + `ErrorResponse{Error: "duplicate_isbn"}`.
- [ ] AC: `PATCH /books/{bookId}` updates the title; a follow-up `GET /books/{isbn}` reflects the new title.
- [ ] AC: `POST /books/{bookId}/copies` returns 201 + `CopyResponse{Status: "AVAILABLE"}`.
- [ ] AC: `PATCH /copies/{copyId}/unavailable` flips status; `PATCH /copies/{copyId}/available` flips it back.
- [ ] AC: `DELETE /books/{bookId}` returns 204; a follow-up `GET /books/{isbn}` returns 404.
- [ ] AC: After the test completes, `t.Cleanup` truncates the tables so the next crucial-path test starts clean.
- [ ] The test exposes `wired.DB` (via the `Wired.DB *bun.DB` field already on the Phase 1 `app.Wired`) and asserts row counts directly for at least one scenario — e.g. `COUNT(*) FROM books WHERE isbn = '978-0135957059'` returns 1 after the POST.

---

### Slice 5: `internal/membership` module (all-in-one — facade → tests → handlers → bun repo → crucial-path)

Phase 2's second business module. The pattern is established by catalog Slices 1–4; membership ships in one slice because there's no new architectural ground to break — it's the same template applied to a thinner module (no cache, no ISBN gateway, no copy-status state machine).

#### Acceptance Criteria — types + schema

- [ ] `internal/membership/types.go` declares: `type MemberId string`; `type MembershipTier string` constants `MembershipTierStandard = "STANDARD"` / `MembershipTierPremium = "PREMIUM"`; `type MembershipStatus string` constants `MembershipStatusActive = "ACTIVE"` / `MembershipStatusSuspended = "SUSPENDED"`; `NewMemberDto struct { Name string; Email string }`; `MemberDto struct { MemberId MemberId; Name string; Email string; Tier MembershipTier; Status MembershipStatus }`; `EligibilityDto struct { MemberId MemberId; Eligible bool; Reason string ` `json:"reason,omitempty"` ` }`.
- [ ] Domain errors: `MemberNotFoundError{Identifier string}`, `DuplicateEmailError{Email string}`, `InvalidMemberError{Reason string}`. Each implements `Error() string` matching TS message format.
- [ ] `internal/membership/schema.go` exports `ParseNewMember(dto NewMemberDto) (NewMemberDto, error)`: trim name + email; reject blank name with `*InvalidMemberError{Reason: "name is required"}`; reject blank email with `*InvalidMemberError{Reason: "email is required"}`; reject email that doesn't match `^[^\s@]+@[^\s@]+\.[^\s@]+$` with `*InvalidMemberError{Reason: "email format is invalid: " + email}`. Returns the trimmed dto.

#### Acceptance Criteria — repository (port + in-memory + bun)

- [ ] `internal/membership/repository.go` declares `type Repository interface { SaveMember(ctx, MemberDto) error; FindMemberById(ctx, MemberId) (*MemberDto, error); FindMemberByEmail(ctx, string) (*MemberDto, error) }`.
- [ ] `internal/membership/in_memory_repository.go` exports `InMemoryRepository` backed by a `map[MemberId]MemberDto`. Implements the port. Stdlib-`testing` unit tests in `in_memory_repository_test.go`.
- [ ] `internal/membership/bun_repository.go` exports `BunRepository` backed by `*bun.DB`. Table `members` per the migration. Upsert on `member_id` for `SaveMember`. `sql.ErrNoRows → (nil, nil)` for finds.
- [ ] `internal/membership/bun_repository_test.go` (`//go:build integration`) runs the same scenarios against testcontainers Postgres.
- [ ] `migrations/0002_membership.sql` creates `members(member_id UUID PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL UNIQUE, tier TEXT NOT NULL, status TEXT NOT NULL)`. `atlas.sum` regenerated.

#### Acceptance Criteria — facade

- [ ] `internal/membership/facade.go` exports `Facade struct` + `NewFacade(repo Repository, newID func() string, logger *slog.Logger) *Facade`. (No `accesscontrol` parameter in the TS source; the membership facade doesn't authorize — it's used by other modules' authorized flows. Phase 2 keeps it identical.)
- [ ] `Facade.RegisterMember(ctx, dto) (MemberDto, error)`: parse via `ParseNewMember`; `repo.FindMemberByEmail(email)`; if found return `*DuplicateEmailError{email}`; else mint `MemberId` via `newID()`, build `MemberDto{Tier: MembershipTierStandard, Status: MembershipStatusActive}`, save, return.
- [ ] `Facade.FindMember(ctx, memberId) (MemberDto, error)`: `repo.FindMemberById`; nil → `*MemberNotFoundError{Identifier: string(memberId)}`; else return.
- [ ] `Facade.Suspend(ctx, memberId) (MemberDto, error)` and `Facade.Reactivate(ctx, memberId) (MemberDto, error)` share an unexported `updateMemberStatus(ctx, memberId, status)` helper.
- [ ] `Facade.UpgradeTier(ctx, memberId MemberId, tier MembershipTier) (MemberDto, error)`: load member or `*MemberNotFoundError`; set `Tier = tier`; save; return.
- [ ] `Facade.CheckEligibility(ctx, memberId) (EligibilityDto, error)`: load member or `*MemberNotFoundError`; if `Status == MembershipStatusSuspended` return `EligibilityDto{MemberId, Eligible: false, Reason: "SUSPENDED"}`; else `EligibilityDto{MemberId, Eligible: true}`.

#### Acceptance Criteria — sample data + configuration

- [ ] `internal/membership/sample_data.go` exports `SampleNewMember(opts ...NewMemberOption) NewMemberDto` (defaults `Name: "Ada Lovelace", Email: "ada.lovelace@example.com"`) and `SampleNewMemberWithEmail(email string) NewMemberDto`. Option type + `WithName(string)`, `WithEmail(string)`.
- [ ] `internal/membership/configuration.go` exports `Overrides{Repository, NewID, Logger}` and `NewFacadeWithOverrides(Overrides) *Facade`. Defaults: in-memory repo, `uuid.NewString`, discard-handler slog.

#### Acceptance Criteria — facade unit tests

- [ ] `internal/membership/facade_test.go` (stdlib `testing`, package `membership`) ports every scenario from `membership.facade.spec.ts`:
  - AC: `RegisterMember` returns a member with non-empty id, `Tier: Standard`, `Status: Active`.
  - AC: `FindMember(memberId)` returns the registered member.
  - AC: `Suspend` flips status to SUSPENDED; subsequent `FindMember` reflects it.
  - AC: `Reactivate` flips status back to ACTIVE.
  - AC: `UpgradeTier(memberId, Premium)` returns member with `Tier: Premium`; subsequent `FindMember` reflects it.
  - AC: `CheckEligibility` for an ACTIVE member returns `Eligible: true` with the right `MemberId`.
  - AC: `CheckEligibility` for a SUSPENDED member returns `Eligible: false`, `Reason: "SUSPENDED"`.
  - AC: `RegisterMember` with blank name (empty / whitespace-only) returns `*InvalidMemberError`.
  - AC: `RegisterMember` with malformed email (empty / "not-an-email" / "missing@domain" / "two@@at.com") returns `*InvalidMemberError`.
  - AC: `RegisterMember` trims surrounding whitespace from name and email.
  - AC: `RegisterMember` with an email that already exists returns `*DuplicateEmailError`.
  - AC: `Suspend` / `Reactivate` / `UpgradeTier` / `CheckEligibility` on an unknown memberId each return `*MemberNotFoundError`.

#### Acceptance Criteria — HTTP layer

- [ ] `internal/membership/http/dto.go` exports `RegisterMemberRequest{Name string ` `json:"name"` `; Email string ` `json:"email"` `}`, `MemberResponse{MemberId string ` `json:"memberId"` `; Name string ` `json:"name"` `; Email string ` `json:"email"` `; Tier string ` `json:"tier"` `; Status string ` `json:"status"` `}`, `UpgradeTierRequest{Tier string ` `json:"tier"` `}`, `EligibilityResponse{MemberId string ` `json:"memberId"` `; Eligible bool ` `json:"eligible"` `; Reason string ` `json:"reason,omitempty"` `}`.
- [ ] `internal/membership/http/mapping.go` exports `fromRegisterMemberRequest(req) NewMemberDto`, `toMemberResponse(MemberDto) MemberResponse`, `toEligibilityResponse(EligibilityDto) EligibilityResponse`.
- [ ] `internal/membership/http/handlers.go` registers:
  - `POST /members` → `RegisterMember` → 201 + `MemberResponse`
  - `GET /members/{id}` → `FindMember` → 200 + `MemberResponse`
  - `PATCH /members/{id}/suspend` → `Suspend` → 200 + `MemberResponse`
  - `PATCH /members/{id}/reactivate` → `Reactivate` → 200 + `MemberResponse`
  - `PATCH /members/{id}/tier` → `UpgradeTier` → 200 + `MemberResponse` (body `{"tier": "PREMIUM"}`)
  - `GET /members/{id}/eligibility` → `CheckEligibility` → 200 + `EligibilityResponse`
- [ ] `internal/membership/module.go` exports `Deps{Facade *Facade; Logger *slog.Logger}` and `Wire(r chi.Router, deps Deps)` mounting the routes above.
- [ ] `internal/membership/http/handlers_test.go` covers every endpoint × happy path + at least one error path: invalid email → 400 + `invalid_member`; unknown id → 404 + `member_not_found`; duplicate email → 409 + `duplicate_email`; unknown JSON field on `POST /members` → 400.

#### Acceptance Criteria — composition root + crucial path

- [ ] `internal/app/wiring.go` constructs `membership.NewFacadeWithOverrides` with the bun-backed repo, registers `*MemberNotFoundError → 404 "member_not_found"`, `*DuplicateEmailError → 409 "duplicate_email"`, `*InvalidMemberError → 400 "invalid_member"`, and calls `membershipModule.Wire(router, ...)`.
- [ ] `app.Wired` gains a `MembershipFacade *membership.Facade` field.
- [ ] `test/crucial_path/membership_integration_test.go` (`//go:build integration`):
  - AC: `POST /members` with `{"name":"Ada","email":"ada@example.com"}` returns 201 + `MemberResponse{Tier:"STANDARD", Status:"ACTIVE", MemberId: <uuid>}`.
  - AC: `POST /members` with duplicate email returns 409 + `duplicate_email`.
  - AC: `GET /members/{id}` returns 200 + the member.
  - AC: `PATCH /members/{id}/suspend` then `GET /members/{id}/eligibility` returns 200 + `{Eligible: false, Reason: "SUSPENDED"}`.
  - AC: `PATCH /members/{id}/reactivate` then `GET /members/{id}/eligibility` returns 200 + `{Eligible: true}` (no `Reason` field in the JSON output — `omitempty` strips it).
  - AC: `PATCH /members/{id}/tier` with `{"tier":"PREMIUM"}` returns 200 + `MemberResponse{Tier:"PREMIUM"}`.
  - AC: `t.Cleanup` truncates `members` after each test.

#### Acceptance Criteria — `.http` file activation

- [ ] `.http/membership.http` exists with active `{{baseUrl}}` requests for every endpoint above.

---

### Slice 6: `internal/shared/isbngateway` package

Brings the repository to "any module needing ISBN lookup has a stable in-process port plus an in-memory test double; an external (OpenLibrary) impl slot is reserved without committing to a request/response shape." This slice is small because the TS source's `InMemoryIsbnLookupGateway` is itself 14 lines — the Go port is similar.

#### Acceptance Criteria

- [ ] `internal/shared/isbngateway/port.go` declares `type BookMetadata struct { Title string; Authors []string }` (matches `BookMetadata` in TS source 1:1).
- [ ] `port.go` declares `type IsbnLookupGateway interface { FindByIsbn(ctx context.Context, isbn string) (*BookMetadata, error) }`. The return is `(*BookMetadata, error)` — `nil, nil` means "no record."
- [ ] `internal/shared/isbngateway/in_memory.go` exports `InMemoryIsbnLookupGateway struct` + `NewInMemoryIsbnLookupGateway() *InMemoryIsbnLookupGateway`. Backed by a `map[string]BookMetadata`. Method `Seed(isbn string, metadata BookMetadata)` mutates the map. `FindByIsbn(ctx, isbn)` returns `(&metadata, nil)` on hit, `(nil, nil)` on miss. Concurrency: a `sync.RWMutex` protects the map (the in-memory event bus already follows this pattern in Phase 1).
- [ ] `internal/shared/isbngateway/external.go` exports `OpenLibraryIsbnLookupGateway struct` with stub fields (e.g. `httpClient *http.Client`, `baseURL string`) and `NewOpenLibraryIsbnLookupGateway(client *http.Client, baseURL string) *OpenLibraryIsbnLookupGateway`. `FindByIsbn` body is `return nil, errors.New("OpenLibraryIsbnLookupGateway not implemented")`. A `// TODO(phase-5): implement against OpenLibrary's /isbn/<isbn>.json endpoint` comment marks the slot.
- [ ] `internal/shared/isbngateway/in_memory_test.go` (unit, no build tag) covers: `Seed` + `FindByIsbn` returns the seeded metadata; unseeded isbn returns `(nil, nil)`; concurrent reads under `-race` are safe (table-driven 10-goroutine test).
- [ ] No mocks anywhere — fault injection is done via spec-local `throwingOnceIsbnLookupGateway` declared INSIDE `internal/catalog/facade_test.go` (per Slice 2's AC). The shared package exposes only the real in-memory impl.
- [ ] The package's exported surface is exactly: `BookMetadata` (struct), `IsbnLookupGateway` (interface), `InMemoryIsbnLookupGateway` (struct + `New*` + `Seed` + `FindByIsbn`), `OpenLibraryIsbnLookupGateway` (struct + `New*` + `FindByIsbn`). Nothing else.

---

### Slice 7: `internal/shared/bookcache` package

Brings the repository to "the catalog facade's cache dependency has a stable port, a real in-memory impl, and a Redis-backed impl. `github.com/redis/go-redis/v9` enters `go.mod`."

#### Acceptance Criteria — port + in-memory

- [ ] `internal/shared/bookcache/port.go` declares `type BookCacheGateway interface { Get(ctx context.Context, isbn catalog.Isbn) (*catalog.BookDto, error); Set(ctx context.Context, isbn catalog.Isbn, book catalog.BookDto) error; Evict(ctx context.Context, isbn catalog.Isbn) error }`. **Import direction**: `shared/bookcache` imports `internal/catalog`. This is the one place where a `shared/*` package imports a business module. The TS source has the same direction (`shared/book-cache-gateway/book-cache-gateway.ts` imports `BookDto` from `catalog/catalog.types.ts`). **Open Question 5**: confirm this is acceptable. Recommended default: **accept it** — match TS source 1:1, document the exception in `.claude/BOUNDARIES.md` Slice 7 of this phase (one-line note: "shared/bookcache imports catalog.BookDto by design — see Phase 2 spec line for rationale").
- [ ] `internal/shared/bookcache/in_memory.go` exports `InMemoryBookCacheGateway` backed by `map[catalog.Isbn]catalog.BookDto` + `sync.RWMutex`. `Get` returns `(*BookDto, nil)` on hit, `(nil, nil)` on miss. `Set` overwrites. `Evict` deletes the key (no error on missing).
- [ ] `internal/shared/bookcache/in_memory_test.go` (unit) covers get/set/evict happy paths + miss returns `(nil, nil)`.

#### Acceptance Criteria — Redis impl

- [ ] `internal/shared/bookcache/redis.go` exports `RedisBookCacheGateway struct { client *redis.Client; logger *slog.Logger }` + `NewRedisBookCacheGateway(client *redis.Client, logger *slog.Logger) *RedisBookCacheGateway`.
- [ ] Key format: `catalog:book:isbn:<isbn>` (matches TS source line 32 of `redis-book-cache-gateway.ts` exactly).
- [ ] `Set` calls `client.Set(ctx, key(isbn), json.Marshal(book), 0)`. `Get` calls `client.Get(ctx, key(isbn))`; on `redis.Nil` returns `(nil, nil)`; else `json.Unmarshal` into a `BookDto` and return. `Evict` calls `client.Del(ctx, key(isbn))`.
- [ ] No `OnModuleDestroy` equivalent — Redis client lifecycle is owned by the composition root (`internal/app/wiring.go` calls `client.Close()` from the `Wired.Close` closure). The Phase 1 `app.Wired.Close` already takes a `func() error`; Slice 7 extends it to also close the Redis client.
- [ ] `go.mod` direct deps gain `github.com/redis/go-redis/v9` in this slice. (Phase 1's `bee-context.local.md` already lists this dep as the locked Redis client choice, but Phase 1 never `go get`-ed it because nothing imported it; Slice 7 imports it and the dep lands.)

#### Acceptance Criteria — Redis impl integration test

- [ ] `internal/shared/bookcache/redis_test.go` has `//go:build integration` at the top.
- [ ] The test calls `test/support.StartRedis(ctx, t)` (already shipped Phase 1), constructs a `*redis.Client` against the container's URL, instantiates `RedisBookCacheGateway`.
- [ ] AC: `Set` + `Get` round-trips a `BookDto`; field equality holds (`json.Marshal`+`Unmarshal` preserves all fields).
- [ ] AC: `Get` on a key never `Set` returns `(nil, nil)` — does NOT return an error for "not found."
- [ ] AC: `Evict` removes the key; subsequent `Get` returns `(nil, nil)`.
- [ ] AC: Multiple `Set` calls for the same key overwrite (last wins).

#### Acceptance Criteria — composition root opts into Redis

- [ ] `internal/app/wiring.go` constructs the bookcache: if `LIBRARY_REDIS_URL` is set, build a `*redis.Client` against it and wrap with `bookcache.NewRedisBookCacheGateway`; otherwise fall back to `bookcache.NewInMemoryBookCacheGateway()`. Same conditional shape as TS `catalog.module.ts` lines 39–43.
- [ ] `app.Wired.Close` closes the Redis client when constructed (chain it with `bunDB.Close`: return the first error, or join via `errors.Join`).
- [ ] `cmd/library/config.go` already loads `LIBRARY_REDIS_URL` (Phase 1 Slice 2 AC). Phase 2 surfaces the value into `app.Deps` so `Wire` can decide between in-memory and Redis. **Recommended**: extend `app.Deps` with a `RedisURL string` field; empty means "use in-memory."
- [ ] AC: A unit-test-friendly default — when `RedisURL == ""`, `Wire` uses the in-memory cache without touching Redis at all. `task test` (unit) does not need Redis.

---

## File Map

| Slice | Files created (or significantly modified) |
| --- | --- |
| 1 | `internal/catalog/facade.go`, `internal/catalog/types.go`, `internal/catalog/schema.go`, `internal/catalog/repository.go`, `internal/catalog/in_memory_repository.go`, `internal/catalog/in_memory_repository_test.go`, `internal/catalog/sample_data.go`, `internal/catalog/configuration.go` |
| 2 | `internal/catalog/facade_test.go` (declares unexported `sequentialIds`, `throwingOnceIsbnLookupGateway`, `throwingOnceBookCacheGateway`, `recordingRepository` decorators) |
| 3 | `internal/catalog/http/dto.go`, `internal/catalog/http/mapping.go`, `internal/catalog/http/handlers.go`, `internal/catalog/http/handlers_test.go`, `internal/catalog/module.go`, `internal/app/wiring.go` (extended: catalog facade construction + error registry + `Wire` call), `.http/catalog.http`, `.http/healthz.http` (modified: Phase-2 catalog placeholders removed) |
| 4 | `internal/catalog/bun_repository.go`, `internal/catalog/bun_repository_test.go` (`//go:build integration`), `migrations/0001_catalog.sql`, `migrations/atlas.sum` (regenerated), `internal/app/wiring.go` (modified: swap in-memory → bun repo), `test/crucial_path/` (new dir), `test/crucial_path/catalog_integration_test.go` (`//go:build integration`) |
| 5 | `internal/membership/facade.go`, `internal/membership/types.go`, `internal/membership/schema.go`, `internal/membership/repository.go`, `internal/membership/in_memory_repository.go`, `internal/membership/in_memory_repository_test.go`, `internal/membership/bun_repository.go`, `internal/membership/bun_repository_test.go` (`//go:build integration`), `internal/membership/sample_data.go`, `internal/membership/configuration.go`, `internal/membership/facade_test.go`, `internal/membership/http/dto.go`, `internal/membership/http/mapping.go`, `internal/membership/http/handlers.go`, `internal/membership/http/handlers_test.go`, `internal/membership/module.go`, `migrations/0002_membership.sql`, `migrations/atlas.sum` (regenerated), `internal/app/wiring.go` (modified: membership facade + error registry + `Wire` call), `test/crucial_path/membership_integration_test.go` (`//go:build integration`), `.http/membership.http` |
| 6 | `internal/shared/isbngateway/port.go`, `internal/shared/isbngateway/in_memory.go`, `internal/shared/isbngateway/in_memory_test.go`, `internal/shared/isbngateway/external.go` |
| 7 | `internal/shared/bookcache/port.go`, `internal/shared/bookcache/in_memory.go`, `internal/shared/bookcache/in_memory_test.go`, `internal/shared/bookcache/redis.go`, `internal/shared/bookcache/redis_test.go` (`//go:build integration`), `internal/app/wiring.go` (modified: Redis client construction + close-chaining), `go.mod` + `go.sum` (`github.com/redis/go-redis/v9` lands) |

**Slice-ordering note**: Slices 1 and 2 logically depend on Slices 6 and 7 (the catalog facade imports `isbngateway.IsbnLookupGateway` and `bookcache.BookCacheGateway`). The recommended ordering is to land Slices 6 + 7 FIRST (small, mechanical, no Postgres), then Slices 1–5 in declared order. **Open Question 6** records this and the recommended default to flip the slice numbers at coding time: write Slices 6 and 7 first, then 1, 2, 3, 4, 5. The spec presents them in outside-in narrative order (the catalog module is what the user cares about; the ports are implementation details), but the slice-coder's execution order is 6 → 7 → 1 → 2 → 3 → 4 → 5.

## Idiom Enforcement (every slice must follow)

Every slice in Phase 2 (and every slice in every later phase) follows these conventions. Carried forward from Phase 1 verbatim; new Phase-2 additions called out at the end.

- **Manual constructor wiring.** No `wire`, no `fx`, no reflection-driven container. `internal/app/wiring.go` constructs collaborators in order.
- **HTTP DTOs live in `<module>/http/dto.go`** and never escape that sub-package. Phase 2 introduces the catalog and membership `http/` subdirs — both follow the rule.
- **Stdlib testing only.** No `testify`. Tiny local helpers (e.g. an `equalSlices` if needed) are acceptable; library deps are not.
- **Hand-written validation.** No `go-playground/validator`. Parse-style functions in `<module>/schema.go` return typed-parsed + a domain `Invalid<X>Error`. Phase 2 introduces `ParseIsbn` / `ParseNewBook` / `ParseUpdateBook` / `ParseNewCopy` / `ParseNewMember` — all hand-written using `strings.TrimSpace`, `regexp.MustCompile`, and per-field branching.
- **testcontainers-go reaches podman via `DOCKER_HOST`.** Already documented in `Taskfile.yml` and `test/support/testcontainers.go` from Phase 1.
- **No mocks in tests.** In-memory implementations of the same interface for substitution; spec-local decorator wrappers (unexported, declared in the test file) for fault injection. Phase 2 declares **three** spec-local decorators inside `internal/catalog/facade_test.go`: `throwingOnceIsbnLookupGateway`, `throwingOnceBookCacheGateway`, `recordingRepository`. Each is unexported, each lives only in the test file, none is imported by any other package.
- **`log/slog` everywhere.** Every facade constructor takes a `*slog.Logger`. The default in `NewFacadeWithOverrides` is a discard handler. Production code path is `cmd/library/main.go` → `app.Wire(ctx, app.Deps{Logger: logger})` → facade constructors.
- **Functional options for sample data builders.** Phase 2 introduces `SampleNewBook` / `SampleNewCopy` / `SampleUpdateBook` (catalog) and `SampleNewMember` (membership), each via `func(*<Dto>)` options.
- **No `init()` for module wiring.** Verified per slice by absence of any `init()` function in catalog, membership, isbngateway, bookcache.
- **Source-fidelity names.** Match TS source 1:1: `IsbnLookupGateway` (not `IsbnGateway`), `BookCacheGateway` (not `BookCache`), `NewBookDto` (not `NewBookRequest` — that's the HTTP DTO), `MarkCopyUnavailable` (not `DisableCopy`), `RegisterMember` (not `CreateMember`), `UpgradeTier` (not `ChangeTier`), `CheckEligibility` (not `IsEligible`). Per `.claude/MEMORY.md` source-fidelity rule.

**New Phase-2 conventions** (carry forward to Phase 3+):

- **HTTP DTOs use pointer fields for optional patch semantics.** When a JSON body distinguishes "field absent" vs "field present with empty value" (e.g. `PATCH /books/{bookId}`), the DTO uses `*string` / `*[]string` so `encoding/json` preserves the distinction. The mapping function dereferences before handing to the facade.
- **`json.Decoder.DisallowUnknownFields()` enforces forbidden fields at the JSON boundary.** Used in Slice 3 for `UpdateBookRequest` to reject `isbn` per the TS source's `.strict('isbn cannot be updated')` rule.
- **Bun queries upsert by primary key.** Every `Save*` method in a bun repository uses `On("CONFLICT (<pk>) DO UPDATE SET ...")` to match the TS source's `onConflictDoUpdate` shape — the facade is responsible for distinguishing "insert" from "update" before calling `Save*`.
- **Empty-input short-circuit happens at the facade, not the repository.** `Facade.GetBooks([])` returns `[]BookDto{}` without consulting the repo. The same rule applies to any future batch read. Asserted in tests via a spec-local `recordingRepository`.
- **Cache failures surface; repo writes stay durable.** When the cache (Redis or in-memory) errors during a write-through or evict, the facade returns the cache error to the caller — but the repository write has already committed. Phase 2 asserts this via the `throwingOnceBookCacheGateway` decorator across `FindBook`, `UpdateBook`, and `DeleteBook`.

## Definition of Done — Phase 2

Phase 2 is done when **all** of the following are true. Each item is verified manually (developer laptop) or by `task test` / `task test:integration`.

- [ ] `task up && task migrate:apply && task run` boots the server on port 3000 with both new migrations applied. `task migrate:status` reports `0001_catalog.sql` and `0002_membership.sql` as applied.
- [ ] `curl -X POST localhost:3000/books -H 'content-type: application/json' -d '{"title":"X","authors":["A"],"isbn":"978-0135957059"}'` returns 201 with a `BookResponse` body whose `bookId` is a UUID.
- [ ] A follow-up `curl localhost:3000/books/978-0135957059` returns 200 + the same book.
- [ ] `curl localhost:3000/books` returns 200 + a JSON array including the added book.
- [ ] `curl -X POST localhost:3000/books` with a duplicate isbn returns 409 + `{"error":"duplicate_isbn",...}`.
- [ ] `curl -X POST localhost:3000/books` with a malformed isbn returns 400 + `{"error":"invalid_book",...}`.
- [ ] `curl -X PATCH localhost:3000/books/<bookId> -d '{"title":"Y"}'` returns 200 + updated book.
- [ ] `curl -X PATCH localhost:3000/books/<bookId> -d '{"isbn":"X"}'` returns 400 + `invalid_book` with message containing "isbn cannot be updated".
- [ ] `curl -X DELETE localhost:3000/books/<bookId>` returns 204.
- [ ] `curl -X POST localhost:3000/books/<bookId>/copies -d '{"condition":"GOOD"}'` returns 201 + `CopyResponse{Status:"AVAILABLE"}`.
- [ ] `curl -X PATCH localhost:3000/copies/<copyId>/unavailable` returns 200 + `CopyResponse{Status:"UNAVAILABLE"}`; the reverse PATCH flips it back.
- [ ] `curl -X POST localhost:3000/members -d '{"name":"Ada","email":"ada@example.com"}'` returns 201 + `MemberResponse{Tier:"STANDARD",Status:"ACTIVE"}`.
- [ ] `curl localhost:3000/members/<id>` returns 200; `PATCH /members/<id>/suspend` flips status to SUSPENDED; `GET /members/<id>/eligibility` returns `{"eligible":false,"reason":"SUSPENDED",...}`. Reactivate flips it back.
- [ ] `task test` (unit, no build tags) is green and completes in well under 1 second (target: under 500 ms). Includes catalog facade tests (~45 scenarios), catalog HTTP handler tests, catalog in-memory repository tests, membership facade tests (~13 scenarios), membership HTTP handler tests, membership in-memory repository tests, isbngateway in-memory tests, bookcache in-memory tests.
- [ ] `task test:integration` is green and completes in under 60 seconds on a developer laptop including testcontainers cold start. Includes: bun repository contract tests for catalog and membership; redis adapter contract test for bookcache; crucial-path integration tests for catalog and membership.
- [ ] `internal/catalog.Facade` is callable from any future module's facade constructor (Phase 3's lending facade will call `catalog.MarkCopyUnavailable` after committing a `Borrow`).
- [ ] `internal/membership.Facade` is callable from any future module's facade constructor (Phase 3's lending facade will call `membership.CheckEligibility` before authorizing a borrow).
- [ ] Every TS scenario in `catalog.facade.spec.ts` that does NOT touch thumbnails has a Go counterpart in `internal/catalog/facade_test.go`. Verified by reading the two files side by side. The thumbnail scenarios are deferred to Phase 5 with a `// TODO(phase-5): port attachThumbnail / readThumbnail / removeThumbnail scenarios` comment block.
- [ ] Every TS scenario in `membership.facade.spec.ts` has a Go counterpart in `internal/membership/facade_test.go`.
- [ ] `task fmt` (`gofmt -w .` + `go mod tidy`) and `task lint` (`go vet ./...`) pass with zero output.
- [ ] No file under `internal/` imports a module it isn't permitted to import per `.claude/BOUNDARIES.md`. **Note**: Slice 7 introduces the one accepted exception — `shared/bookcache` imports `internal/catalog` for the `BookDto` shape. `.claude/BOUNDARIES.md` is updated in Slice 7 with a one-line note explaining the exception.
- [ ] `.claude/BOUNDARIES.md` reflects Phase 2's deps direction (catalog → isbngateway + bookcache + accesscontrol; bookcache → catalog by exception; membership → accesscontrol).
- [ ] The `accesscontrol` facade is wired into the catalog facade constructor (per the TS source's signature) even though no Phase-2 endpoint exercises authorization. Phase 5's thumbnail endpoints will activate the path; Phase 2 only proves wiring works (the catalog facade constructor in `app.Wire` accepts a non-nil `*accesscontrol.Facade`).
- [ ] No `init()` function in any new package.
- [ ] No file in `go.mod` direct deps other than: `github.com/go-chi/chi/v5`, `github.com/uptrace/bun`, `github.com/uptrace/bun/driver/pgdriver`, `github.com/redis/go-redis/v9` (newly added in Slice 7), `github.com/spf13/viper`, `github.com/google/uuid`. No testify, no validator, no wire/fx, no pgx (still deferred), no mock library.

## Open Questions

1. **Thumbnail handlers in Phase 2 vs Phase 5.** Recommended default: **defer to Phase 5** (Out of Scope section above). Rationale: thumbnails introduce a third outbound port (`FileStorageGateway`), three handlers, ~25 facade-spec scenarios (attach/read/remove × happy + permission + validation + cache integration), and mime-sniffing schema work — all of which dilute Phase 2's "stateful CRUD + cache + ISBN enrichment" focus. The TS source ships them together because NestJS has no slice budget; the Go port can ship them independently. **Flag for confirmation**: if the developer wants thumbnails in Phase 2, add an 8th slice (estimated ~3 days of work) and pull `internal/shared/filestorage` forward.
2. **In-Slice-3 catalog facade wired with in-memory repo or bun repo.** Recommended default: **in-memory repo in Slice 3, swap to bun in Slice 4.** Rationale: Slice 3 ships a green end-to-end test loop (HTTP layer + handlers + middleware integration) without dragging in testcontainers. Slice 4's diff becomes a one-line swap in `internal/app/wiring.go` plus the bun repo file + migration. Alternative: skip the in-memory wiring and require Slice 3 to land bun immediately — adds testcontainers to Slice 3's CI loop and tangles the slices' verification stories. Stick with the default.
3. **Bun repository ordering for `ListBooks`.** Recommended default: **explicit `ORDER BY book_id ASC`** in the bun repo, and update `InMemoryRepository.ListBooks` to also sort by `BookId` ASC (instead of insertion order via a slice of ids). Rationale: matching insertion order across in-memory and bun would require adding a `created_at` column (extra migration noise) or maintaining a per-instance counter (test-only artifact); `ORDER BY book_id` is deterministic for both substrates and reads naturally. The TS source's Slice-2 test asserts insertion order, but it's exercised against `InMemoryCatalogRepository` only — the Drizzle repo's `ListBooks` is unordered in the TS source too. **Flag for confirmation**: if the developer wants strict insertion-order parity, add `created_at` to both tables and order by it.
4. **`DeleteBook` cascade vs error on copies.** Recommended default: **no cascade** — match TS source 1:1. If a book has copies, the FK constraint on `copies.book_id` will fail the DELETE. The Phase 2 facade does NOT pre-delete copies because the TS source doesn't either. Phase 3's lending module will load the copies of a book during the loan flow, so the bug surfaces there if it surfaces at all. Defer the decision to whenever a real test trips the FK.
5. **`shared/bookcache` importing `internal/catalog`.** Recommended default: **accept it** (the TS source has the same direction). The BOUNDARIES line for `shared/bookcache` reads "Depends on: stdlib + third-party only" — Slice 7 adds an explicit exception line: "EXCEPTION: imports `internal/catalog` for the `BookDto` shape, per TS source `shared/book-cache-gateway/book-cache-gateway.ts` line 1. Any new `shared/<port>` package importing a business module needs the same explicit allowlist." Alternative: invert the dependency by moving `BookDto` into a `shared/types` dumping ground (forbidden by `.claude/BOUNDARIES.md` Forbidden Patterns) or by defining a separate `BookSnapshot` type in `shared/bookcache` and mapping at the catalog boundary — extra mechanical mapping for negligible benefit in a teaching repo.
6. **Slice execution order vs spec narrative order.** Recommended default: **execute Slices 6 → 7 → 1 → 2 → 3 → 4 → 5** even though the spec presents them in narrative order 1 → 7. Rationale: catalog Slice 1 imports `isbngateway` and `bookcache`; those packages don't exist until Slices 6 and 7. The slice-coder should land 6 and 7 first (each is small — a port file, an in-memory impl, a unit test) so catalog Slice 1 compiles on first build.

## Phase 2 → Phase 3 Handoff

When Phase 3 starts, the spec-builder for that phase can assume:

1. The dev loop works — `task up`, `task migrate:apply`, `task run`, `task test`, `task test:integration` are stable. Two business migrations are in `migrations/`.
2. `internal/catalog.Facade` exposes `AddBook`, `FindBook`, `UpdateBook`, `DeleteBook`, `ListBooks`, `GetBooks`, `RegisterCopy`, `FindCopy`, `MarkCopyAvailable`, `MarkCopyUnavailable`. Phase 3's lending facade will call `FindCopy` (to check status), `MarkCopyUnavailable` (post-commit side effect after `Borrow`), `MarkCopyAvailable` (post-commit side effect after `ReturnLoan`), and `GetBooks` (batch read for cross-module fetches that avoid N+1).
3. `internal/membership.Facade` exposes `RegisterMember`, `FindMember`, `Suspend`, `Reactivate`, `UpgradeTier`, `CheckEligibility`. Phase 3's lending facade will call `CheckEligibility` before authorizing a borrow.
4. `internal/shared/isbngateway` ships only the in-memory impl (the external `OpenLibrary` impl is a placeholder stub returning `not implemented`). Phase 5 may activate it; Phase 3 does not touch it.
5. `internal/shared/bookcache` ships in-memory + Redis impls. The composition root picks Redis when `LIBRARY_REDIS_URL` is set, otherwise in-memory. Phase 3 does not touch `bookcache` (lending owns no cache surface).
6. `internal/app.Wire` returns a `Wired` carrying `Router`, `DB`, `CatalogFacade`, `MembershipFacade`, `Close`. Phase 3 extends it with `LendingFacade`.
7. `internal/app/wiring.go`'s `buildDomainErrorRegistry` registers six Phase-2 domain errors (`book_not_found`, `copy_not_found`, `duplicate_isbn`, `invalid_book`, `invalid_copy`, `member_not_found`, `duplicate_email`, `invalid_member`). Phase 3 extends with `loan_not_found`, `reservation_not_found`, `member_ineligible`, `copy_unavailable`, etc.
8. The per-module file convention is now battle-tested across two modules (catalog + membership). Phase 3's `lending` module follows the same template: `facade.go`, `types.go`, `schema.go`, `repository.go`, `in_memory_repository.go`, `bun_repository.go`, `sample_data.go`, `configuration.go`, `module.go`, `facade_test.go`, plus `http/` subdir. Phase 3 also introduces `internal/shared/tx/` (the `TransactionalContext` substrate) before the lending facade, because lending is the first module that needs cross-event-with-write atomicity.
9. The "no-mocks, in-memory doubles + spec-local Throwing-Once decorator wrappers" discipline is locked. Phase 3's lending facade tests will declare unexported `throwingOnceLoanRepository`, `throwingOnceReservationRepository`, `throwingOnceCatalogFacade` (the latter as a thin pass-through wrapping the real catalog facade) inside the lending facade test file.
10. The `accesscontrol` facade is constructor-injected into the catalog facade today (and goes unexercised at runtime in Phase 2 because thumbnail endpoints are deferred). Phase 3's `lending` facade also takes the `accesscontrol` facade — and Phase 3 IS where the authorization path becomes active at runtime: `Borrow` requires the caller's `Role == "MEMBER"`.

No Phase 2 file or AC needs to change to enable Phase 3.

[ ] Reviewed
