# Guide: adding a new module

This guide walks through everything needed to add a new module to the codebase. It uses the [`categories`](internal/categories) module as the reference — it has zero cross-module dependencies and ships the full file template, so a fresh module looks structurally identical to it.

The template scales. A module with cross-module deps adds facade-to-facade calls (see lending). A module with events adds `StageEvent` calls and an event type (see fines). A module with no state or HTTP routes drops the repository and `http/` sub-package (see accesscontrol). The shape of each file stays the same.

## The module template

A new module under `internal/<module>/` ships these files. Tick each as you create it.

- [ ] `types.go` — domain DTOs, domain identifier newtypes, domain errors.
- [ ] `schema.go` — `Parse<X>` functions returning typed-parsed + `*Invalid<X>Error`.
- [ ] `repository.go` — port interface (only if the module owns state).
- [ ] `in_memory_repository.go` — in-memory implementation of the port.
- [ ] `bun_repository.go` — Postgres implementation (only if a bun adapter exists).
- [ ] `sample_data.go` — `Sample<X>(opts ...Option) Dto` functional-option builders.
- [ ] `configuration.go` — `Overrides` struct + `NewFacadeWithOverrides`.
- [ ] `facade.go` — `Facade` struct + `NewFacade(deps...)` + exported methods.
- [ ] `facade_test.go` — facade-level spec (in-memory substrates, no testify).
- [ ] `http/dto.go` — JSON wire types (only if module exposes HTTP).
- [ ] `http/mapping.go` — HTTP DTO ↔ domain DTO mapping helpers.
- [ ] `http/handlers.go` — `http.HandlerFunc` per endpoint.
- [ ] `http/module.go` — `Wire(r chi.Router, deps Deps)`.
- [ ] `http/handlers_test.go` — end-to-end handler tests (via `httptest`).

Walk-through follows.

### `types.go`

Declares the domain identifier newtype, the persisted DTO, and every domain error this module owns.

```go
type CategoryId string

type CategoryDto struct {
    CategoryId CategoryId
    Name       string
    CreatedAt  time.Time
}

type CategoryNotFoundError struct {
    Identifier string
}

func (e *CategoryNotFoundError) Error() string {
    return fmt.Sprintf("Category not found: %s", e.Identifier)
}
```

Rules:

- Identifier newtypes wrap `string` so call sites cannot accidentally swap `CategoryId` with `BookId`.
- Domain errors have **pointer receivers** so `errors.As` resolves through wrapping layers and the domain-error registry's type-driven lookup works.
- The package doc comment at the top of `types.go` lists the module's dependencies (per [`.claude/BOUNDARIES.md`](.claude/BOUNDARIES.md)) so the constraint is visible to anyone editing the file.

### `schema.go`

Hand-written `Parse<X>` functions. No `go-playground/validator`, no struct tags for validation.

```go
func ParseNewCategory(name string) (string, error) {
    trimmed := strings.TrimSpace(name)
    if trimmed == "" {
        return "", &InvalidCategoryError{Reason: "name is required"}
    }
    if len(trimmed) > maxCategoryNameLength {
        return "", &InvalidCategoryError{Reason: "name too long"}
    }
    return trimmed, nil
}
```

`Parse<X>` returns the parsed (often-trimmed) value plus a pointer error so the call site can `if err != nil { return ..., err }`. Failure types are pointer types that map into the domain-error registry.

### `repository.go`

The port interface, plus contract notes documenting the return-shape conventions.

```go
type CategoryRepository interface {
    Save(ctx context.Context, category CategoryDto) error
    FindById(ctx context.Context, id CategoryId) (*CategoryDto, error)
    FindByNamePrefix(ctx context.Context, prefix string) ([]CategoryDto, error)
}
```

Conventions:

- `FindById` returns `(nil, nil)` on miss. The facade is responsible for translating to `*<X>NotFoundError`.
- `Save` translates a uniqueness violation into a pointer domain error (`*DuplicateCategoryError`). Other infrastructure errors propagate unchanged.
- Methods that participate in a transaction take `txc tx.TransactionalContext` as the last positional parameter. See `lending.LoanRepository.SaveLoan` for the canonical shape.

### `in_memory_repository.go`

A pure-Go map-backed implementation of the port. It is **not a mock** — it implements the same contract as the bun repository, satisfies the same test expectations, and is the default substrate in `NewFacadeWithOverrides`.

```go
type InMemoryCategoryRepository struct {
    mu             sync.RWMutex
    categoriesById map[CategoryId]CategoryDto
}

func NewInMemoryCategoryRepository() *InMemoryCategoryRepository {
    return &InMemoryCategoryRepository{
        categoriesById: map[CategoryId]CategoryDto{},
    }
}
```

Always safe for concurrent use (`sync.RWMutex` or `sync.Map`) so tests that fan out goroutines do not need to wrap it.

### `bun_repository.go`

The Postgres implementation. Lives next to the in-memory impl, satisfies the same interface. Resolves the live transaction handle via `tx.TxFromContext(ctx)` when one is active:

```go
func (r *BunCategoryRepository) Save(ctx context.Context, category CategoryDto) error {
    db, _ := tx.TxFromContext(ctx)
    if db == nil {
        db = r.db
    }
    _, err := db.NewInsert().Model(toRow(category)).Exec(ctx)
    if err != nil {
        // translate pg unique-violation → *DuplicateCategoryError
        ...
    }
    return nil
}
```

The bun repo NEVER imports any other module. Cross-module joins are forbidden — if the consuming module needs related data, it calls the upstream module's facade.

### `sample_data.go`

Functional-option builders for test fixtures. Each option is a `func(*Dto)` and applies in order.

```go
type CategoryOption func(*CategoryDto)

func WithCategoryId(id CategoryId) CategoryOption {
    return func(dto *CategoryDto) { dto.CategoryId = id }
}

func WithCategoryName(name string) CategoryOption {
    return func(dto *CategoryDto) { dto.Name = name }
}

func SampleNewCategory(opts ...CategoryOption) CategoryDto {
    dto := CategoryDto{
        CategoryId: "00000000-0000-0000-0000-000000000001",
        Name:       "Fiction",
        CreatedAt:  defaultSampleCreatedAt,
    }
    for _, opt := range opts {
        opt(&dto)
    }
    return dto
}
```

This is the locked convention — no spread-args overrides struct, no fluent-builder pattern.

### `configuration.go`

`Overrides` is the test-substitution seam. Every field is optional; a zero value means "use the default." `NewFacadeWithOverrides` fills the defaults and calls `NewFacade`.

```go
type Overrides struct {
    Repository CategoryRepository
    NewID      func() string
    Clock      func() time.Time
    Logger     *slog.Logger
}

func NewFacadeWithOverrides(o Overrides) *Facade {
    repository := o.Repository
    if repository == nil {
        repository = NewInMemoryCategoryRepository()
    }
    newID := o.NewID
    if newID == nil {
        newID = uuid.NewString
    }
    clock := o.Clock
    if clock == nil {
        clock = time.Now
    }
    logger := o.Logger
    if logger == nil {
        logger = slog.New(slog.NewTextHandler(io.Discard, nil))
    }
    return NewFacade(repository, newID, clock, logger)
}
```

Tests construct a facade with `NewFacadeWithOverrides(Overrides{NewID: sequentialIds("cat"), Clock: frozen})` — substituting only what the test needs to control.

### `facade.go`

The module's only public surface. Constructor takes every dependency by name; each method is small and reads top-to-bottom.

```go
type Facade struct {
    repository CategoryRepository
    newID      func() string
    clock      func() time.Time
    logger     *slog.Logger
}

func NewFacade(repository CategoryRepository, newID func() string, clock func() time.Time, logger *slog.Logger) *Facade {
    return &Facade{repository: repository, newID: newID, clock: clock, logger: logger}
}

func (f *Facade) CreateCategory(ctx context.Context, name string) (CategoryDto, error) {
    parsedName, err := ParseNewCategory(name)
    if err != nil {
        return CategoryDto{}, err
    }
    category := CategoryDto{
        CategoryId: CategoryId(f.newID()),
        Name:       parsedName,
        CreatedAt:  f.clock(),
    }
    if err := f.repository.Save(ctx, category); err != nil {
        return CategoryDto{}, err
    }
    return category, nil
}
```

Rules:

- Methods take and return domain DTOs only. Never HTTP DTOs.
- For modules that mutate state through the tx substrate, the method opens `txc := f.txFactory()` then calls `txc.Run(ctx, func(ctx context.Context) error { ... })`. See `lending.Borrow` for the canonical shape.
- Cross-module reads happen BEFORE the tx opens. Post-commit cross-module mutations happen AFTER `Run` returns. Never via `Stage` (see [ARCHITECTURE.md § Post-commit side effects go OUTSIDE Run](ARCHITECTURE.md#post-commit-side-effects-go-outside-run)).

### `facade_test.go`

Stdlib testing only. No testify. No mock library. In-memory substrates everywhere; throwing-once decorators for fault injection (unexported, declared in the test file).

```go
package categories

import (
    "context"
    "errors"
    "testing"
)

func TestCreateCategory(t *testing.T) {
    facade := buildFacade()
    ctx := context.Background()

    cat, err := facade.CreateCategory(ctx, "Fiction")
    if err != nil {
        t.Fatalf("CreateCategory: %v", err)
    }
    if cat.Name != "Fiction" {
        t.Errorf("Name = %q, want %q", cat.Name, "Fiction")
    }
}

func TestCreateCategory_blankName(t *testing.T) {
    facade := buildFacade()
    _, err := facade.CreateCategory(context.Background(), "  ")

    var invalid *InvalidCategoryError
    if !errors.As(err, &invalid) {
        t.Fatalf("err = %T, want *InvalidCategoryError", err)
    }
}
```

`buildFacade()` is a helper at the top of the file that wires `NewFacadeWithOverrides` with deterministic ids + a frozen clock. The pattern: every test starts with `facade := buildFacade()` and does one thing.

### `http/dto.go`

JSON wire types. Tagged with `json:"..."` to match the API contract verbatim. Never leak out of the `http/` sub-package.

```go
type CreateCategoryRequest struct {
    Name string `json:"name"`
}

type CategoryResponse struct {
    Id        string    `json:"id"`
    Name      string    `json:"name"`
    CreatedAt time.Time `json:"createdAt"`
}
```

### `http/mapping.go`

Unexported helpers that translate HTTP DTOs ↔ domain DTOs. The indirection enforces the boundary.

```go
func toCategoryResponse(c categories.CategoryDto) CategoryResponse {
    return CategoryResponse{
        Id:        string(c.CategoryId),
        Name:      c.Name,
        CreatedAt: c.CreatedAt,
    }
}
```

### `http/handlers.go`

One method per endpoint. Each returns `error` for `sharedhttp.Handle` / `DomainErrorMiddleware` to translate.

```go
type Handlers struct {
    facade *categories.Facade
    logger *slog.Logger
}

func NewHandlers(facade *categories.Facade, logger *slog.Logger) *Handlers {
    return &Handlers{facade: facade, logger: logger}
}

func (h *Handlers) CreateCategory(w http.ResponseWriter, r *http.Request) error {
    var req CreateCategoryRequest
    if err := decodeStrict(r, &req); err != nil {
        return &categories.InvalidCategoryError{Reason: err.Error()}
    }
    category, err := h.facade.CreateCategory(r.Context(), req.Name)
    if err != nil {
        return err
    }
    return sharedhttp.WriteJSON(w, http.StatusCreated, toCategoryResponse(category))
}
```

`decodeStrict` uses `json.NewDecoder(r.Body).DisallowUnknownFields()`. Unknown fields are validation errors, not silent drops.

### `http/module.go`

The `Wire` seam the composition root calls. Mounts every route onto the chi router.

```go
type Deps struct {
    Facade *categories.Facade
    Logger *slog.Logger
}

func Wire(r chi.Router, deps Deps) {
    handlers := NewHandlers(deps.Facade, deps.Logger)
    r.Post("/categories", sharedhttp.Handle(handlers.CreateCategory))
    r.Get("/categories", sharedhttp.Handle(handlers.ListByPrefix))
    r.Get("/categories/{id}", sharedhttp.Handle(handlers.FindCategoryById))
}
```

Routes use `sharedhttp.Handle(fn)` — never the raw `http.HandlerFunc` shape — so handler errors go through the domain-error registry.

### `http/handlers_test.go`

End-to-end handler tests using `httptest.NewServer`. Asserts on HTTP status, JSON body, and registered domain-error codes.

```go
func TestCreateCategory_201(t *testing.T) {
    srv := buildServer(t)
    body := `{"name":"Fiction"}`
    resp := postJSON(t, srv, "/categories", body)
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusCreated {
        t.Fatalf("status = %d, want 201", resp.StatusCode)
    }
    var got CategoryResponse
    decode(t, resp, &got)
    if got.Name != "Fiction" {
        t.Errorf("Name = %q, want Fiction", got.Name)
    }
}
```

## The migration template

Database tables live in `migrations/`. Files are zero-padded sequence numbers + module name:

```
migrations/
├── 0001_catalog.sql
├── 0002_membership.sql
├── 0003_lending.sql
├── 0004_fines.sql
├── 0005_categories.sql
└── atlas.sum
```

To add a migration:

1. Create `migrations/000N_<module>.sql` with the next sequence number.
2. Write `CREATE TABLE` (and any indexes) inside. Only the new module's tables — never join across modules' tables in the migration.
3. Regenerate `atlas.sum`: `atlas migrate hash`.
4. Apply locally: `task migrate:apply`.
5. Verify with `task migrate:status` (every migration shows `applied`).

One module's migrations never reference another module's tables. Cross-module relationships are derived by application code, not by foreign-key constraints (see [ARCHITECTURE.md § No cross-module DB joins](ARCHITECTURE.md)).

## The wiring step

Every new module touches `internal/app/wiring.go` exactly once. The diff a new module introduces:

```go
// 1. Construct the facade (after every facade it depends on)
categoriesFacade := categories.NewFacadeWithOverrides(categories.Overrides{
    Repository: categories.NewBunCategoryRepository(bunDB),
    Logger:     deps.Logger,
})

// 2. Wire the HTTP routes
categorieshttp.Wire(router, categorieshttp.Deps{
    Facade: categoriesFacade,
    Logger: deps.Logger,
})

// 3. Register the domain errors (inside buildDomainErrorRegistry)
registry.Register(&categories.CategoryNotFoundError{}, http.StatusNotFound, "category_not_found")
registry.Register(&categories.DuplicateCategoryError{}, http.StatusConflict, "duplicate_category")
registry.Register(&categories.InvalidCategoryError{}, http.StatusBadRequest, "invalid_category")
registry.Register(&categories.InvalidCategoriesQueryError{}, http.StatusBadRequest, "invalid_categories_query")

// 4. Expose the facade on the Wired struct (if integration tests need it)
type Wired struct {
    ...
    CategoriesFacade *categories.Facade
}
```

If the module ships a long-running consumer (a saga), add Start/Stop to the wiring's `buildCloser` so the consumer detaches BEFORE the substrates are released.

## The boundaries step

Add the new module's row to [`.claude/BOUNDARIES.md`](.claude/BOUNDARIES.md):

```
- `categories/` — owns: Category, CategoryId, CategoryNotFoundError. Depends on:
    accesscontrol, shared/tx, shared/db, shared/http
```

If the module is in `internal/shared/`, add it to the shared-infrastructure list instead:

```
- `shared/chatgateway/` — owns: ChatGateway port + impls (in-memory, OpenAI).
    Depends on: stdlib + third-party only.
```

Adding the row is the architectural commitment. Other modules can now reference the new module via its facade and only its facade.

## The test-and-ship checklist

Before opening the PR, walk this checklist top to bottom:

- [ ] `go build ./...` passes.
- [ ] `go vet ./...` clean.
- [ ] `task test` (unit) is green and runs in under 1.5 seconds total.
- [ ] `task test:race` (unit + race detector) is green.
- [ ] `task test:integration` is green when the module ships an integration test.
- [ ] No `init()` introduced anywhere in the new files.
- [ ] No mocks (`gomock`, `mockery`, `testify/mock`). Fault injection is a spec-local decorator declared in the test file.
- [ ] No `slog.Default()` read from inside business code. Every collaborator that logs takes a `*slog.Logger` constructor parameter.
- [ ] HTTP DTOs live in `<module>/http/dto.go` and never escape that sub-package.
- [ ] Domain errors are pointer types (`*<X>Error`) implementing `Error() string` on a pointer receiver.
- [ ] The new module's row exists in [`.claude/BOUNDARIES.md`](.claude/BOUNDARIES.md).
- [ ] Every domain error that should map to a non-500 HTTP status is registered in `buildDomainErrorRegistry`.
- [ ] No cross-module DB joins. Cross-module reads go through the other module's facade.
- [ ] No cross-module transactions. The module's writes happen inside its own `TransactionalContext.Run`.
- [ ] Post-commit cross-module mutations are outside the local tx, sequenced by the caller after `Run` returns nil.
- [ ] If the module publishes events: events stage via `txc.StageEvent` (publishes after commit) UNLESS the event must observe post-commit cross-module state (then publish via `bus.Publish` after the cross-module call).
- [ ] If the module ships a saga: it follows the four atomicity invariants from [SAGA.md](SAGA.md).

If every box ticks, the module is ready to merge.
