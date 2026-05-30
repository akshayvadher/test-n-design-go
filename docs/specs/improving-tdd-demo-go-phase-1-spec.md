# Spec: improving-tdd-demo Go Port — Phase 1 (Scaffolding & Access Control)

## Overview

Walking skeleton: a runnable Go server on port 3000 with `GET /healthz`, podman-compose Postgres + Redis dependencies, atlas migrations wired (with an empty migration set), shared `db` / `events` / `http` packages, and the `accesscontrol` module (facade + policy + sample data + unit tests). No business modules, no HTTP surface beyond `/healthz`, no business tables. This phase exists to lock the conventions every later module will follow.

## Why

Phase 1 unlocks:

- A reproducible local dev loop (`task up`, `task migrate:apply`, `task run`, `task test`, `task test:integration`).
- The three shared substrates (`db`, `events`, `http`) every business module will import.
- The composition-root pattern: viper → slog → chi → `http.Server` with graceful shutdown, all wired by hand in `cmd/library/main.go`.
- The `accesscontrol` module — the only module that other modules will import directly — proving the per-module file layout, functional-option sample builders, hand-written validation, and stdlib-only test style.
- An integration test harness (testcontainers Postgres + Redis via podman) so Phase 2's first bun-repository spec can land without re-litigating infra.

Without Phase 1, every later slice has to negotiate "what does the dev loop look like?" mid-flight. With it, every later slice answers only its own question.

## In Scope

- `go.mod` (`github.com/akshayvadher/test-n-design-go`), `go.sum`, `.gitignore`, `.env.example`.
- `compose.yaml` (podman-compose) bringing up Postgres 16 + Redis 7 with healthchecks.
- `Taskfile.yml` with at minimum: `up`, `down`, `run`, `build`, `test`, `test:integration`, `migrate:apply`, `migrate:status`, `fmt`, `lint`.
- `atlas.hcl` configured for versioned SQL migrations in `migrations/` against the dev Postgres URL.
- `cmd/library/main.go` composition root + `cmd/library/config.go` viper loader.
- `internal/shared/db/` bun client factory + migrations applier.
- `internal/shared/http/` `DomainErrorMiddleware`, `WriteJSON`, `ErrorResponse`.
- `internal/shared/events/` `EventBus` interface, `InMemoryEventBus`, `DomainEvent` interface.
- `internal/accesscontrol/` module: facade, types, policy, sample data, configuration, module wiring, facade unit tests. No DB, no HTTP surface of its own.
- `GET /healthz` registered directly in `main.go` (no module owns it).
- `test/support/` shared helpers: `app_factory.go`, `testcontainers.go`.
- `test/integration/healthz_integration_test.go` end-to-end smoke test.

## Out of Scope (deferred to later phases per discovery doc)

- Business modules: `catalog`, `membership`, `lending`, `fines`, `categories`, `chat`. (Phases 2–5.)
- `TransactionalContext` (`internal/shared/tx/`) and its in-memory + bun impls. (Phase 3.)
- Outbound gateways: `isbngateway`, `bookcache`, `chatgateway`, `filestorage`. (Phases 2/5.)
- Any real business `INSERT`/`SELECT` against Postgres — the `migrations/` directory contains only `.gitkeep` in Phase 1. We prove the migration runner and bun client load and can connect; we do not yet introduce business tables.
- Top-level docs: `README.md`, `ARCHITECTURE.md`, `SAGA.md`, `GUIDE.md`. (Phase 5.)
- The `.claude/skills/` skill. (Phase 5.)
- Architecture enforcement linter (`go-arch-lint` etc.). (Decision D25 — revisit Phase 5.)
- Authentication / authorization at the HTTP layer (no `AuthUser` middleware; `/healthz` is unauthenticated and no other endpoints exist yet).
- Metrics, tracing, Prometheus, OpenTelemetry.

## Slices

Slicing is outside-in within the phase: slice 1 lays the dev loop the developer touches first, slice 2 makes the binary runnable, slices 3–5 add the shared substrates the next phase will need, slice 6 lands the only module Phase 1 ships, slice 7 ties it together with an end-to-end smoke test that proves the whole vertical works, and slice 8 lands a small `.http` file so the developer can probe the running server manually (JetBrains HTTP Client / VSCode REST Client) without writing a curl command.

---

### Slice 1: Project bootstrap (dev loop)

Brings the repository to "I can run `task up` and a Postgres + Redis pair is healthy."

#### Acceptance Criteria

- [x] `go.mod` declares module path `github.com/akshayvadher/test-n-design-go` and Go version `1.23` or newer.
- [x] `go.mod` lists exactly these direct deps (with their `go.sum` entries committed): `github.com/go-chi/chi/v5`, `github.com/uptrace/bun`, `github.com/uptrace/bun/driver/pgdriver`, `github.com/redis/go-redis/v9`, `github.com/spf13/viper`, `github.com/google/uuid`. Atlas is a CLI dependency, not a library import. **No `github.com/jackc/pgx/v5`** — bun's `pgdriver` is the only Postgres driver in Phase 1; pgx is deferred to a Phase 2+ slice if a repository actually needs pgx-specific features (LISTEN/NOTIFY, COPY, custom pgtypes). No `github.com/stretchr/testify`. No `github.com/google/wire`, `go.uber.org/fx`, `github.com/go-playground/validator`.
- [x] `.gitignore` excludes `bin/`, `dist/`, `coverage.out`, `*.test`, `.env`, `.idea/`, `.vscode/`, and `tmp/`.
- [x] `.env.example` documents every env var read by `cmd/library/config.go`: `LIBRARY_HTTP_PORT` (default `3000`), `LIBRARY_DATABASE_URL` (default `postgres://library:library@localhost:5432/library?sslmode=disable`), `LIBRARY_REDIS_URL` (default `redis://localhost:6379/0`), `LIBRARY_LOG_LEVEL` (default `info`), `LIBRARY_LOG_FORMAT` (default `json`, alternative `text`).
- [x] `compose.yaml` defines two services, `postgres` (image `postgres:16-alpine`) and `redis` (image `redis:7-alpine`), each with a `healthcheck` (`pg_isready` and `redis-cli ping` respectively), exposed on `5432` and `6379` on localhost.
- [x] `compose.yaml` mounts a named volume for Postgres data and seeds env vars `POSTGRES_USER=library`, `POSTGRES_PASSWORD=library`, `POSTGRES_DB=library`.
- [x] `task up` runs `podman compose up -d` and exits 0 only when both healthchecks report healthy (the Taskfile waits, e.g. via `podman compose ps --format json` polling, or uses `--wait` if supported by the local podman-compose version — document which approach is taken in `Taskfile.yml`).
- [x] `task down` runs `podman compose down` (no `-v` — data volume is preserved across stops).
- [x] `task down:clean` runs `podman compose down -v` (separate task for the destructive version).
- [x] `Taskfile.yml` defines: `up`, `down`, `down:clean`, `run`, `build`, `test`, `test:integration`, `migrate:apply`, `migrate:status`, `fmt`, `lint`, `tidy`.
- [x] `task fmt` runs `gofmt -w .` and `go mod tidy`.
- [x] `task lint` runs `go vet ./...`. (No `golangci-lint` dependency added in Phase 1 — adding it is deferred to whenever the team decides it pays off.)
- [x] `task build` produces `bin/library` (or `bin/library.exe` on Windows) via `go build -o bin/library ./cmd/library`.
- [x] `task tidy` runs `go mod tidy` and verifies `go mod verify`.
- [x] The Taskfile documents the Windows-podman gotcha at the top in a comment block: `DOCKER_HOST` must be set to `npipe:////./pipe/podman-machine-default` (or the equivalent printed by `podman machine inspect`) for `testcontainers-go` to find the socket. The comment names the exact `podman machine inspect | jq -r .[].ConnectionInfo.PodmanPipe.Path` command to discover the value.

---

### Slice 2: Composition root + graceful shutdown

Brings the repository to "I can run `task run` and `curl localhost:3000/healthz` returns 200." The `/healthz` route is registered directly in `main.go` (no module owns it).

#### Acceptance Criteria

- [x] `cmd/library/config.go` exports a `Config` struct with fields for HTTP port (`int`), database URL (`string`), redis URL (`string`), log level (`string`), log format (`string`).
- [x] `cmd/library/config.go` exports `LoadConfig() (*Config, error)` that reads from env via viper, applies the defaults documented in `.env.example`, and returns an error if any required field is missing or unparseable.
- [x] `LoadConfig` reads `.env` if present (viper `AutomaticEnv` + `SetEnvPrefix("LIBRARY")`); explicit env vars override `.env` values.
- [x] `LoadConfig` returns an error mentioning the offending field name when the port is non-numeric, when the log level is not one of `debug|info|warn|error`, or when the log format is not `json|text`.
- [x] `cmd/library/main.go`'s `main` function: loads config, builds a `*slog.Logger` (JSON or text handler per `LIBRARY_LOG_FORMAT`, level per `LIBRARY_LOG_LEVEL`), builds the chi router with the locked middleware stack (see Slice 4), registers `GET /healthz`, builds an `http.Server` with `ReadHeaderTimeout: 5*time.Second`, starts the server, and blocks until SIGINT or SIGTERM.
- [x] On SIGINT/SIGTERM, `main` calls `http.Server.Shutdown(ctx)` with a 10-second timeout. If `Shutdown` returns an error, `main` logs the error at level `error` and exits with code 1; on clean shutdown it logs `server stopped` at level `info` and exits 0.
- [x] `GET /healthz` returns HTTP 200 with `Content-Type: application/json` and exact body `{"status":"ok"}` (no whitespace, no trailing newline difference beyond what `json.Encoder` emits — the AC is byte-identical to `{"status":"ok"}` after `bytes.TrimSpace`).
- [x] `main.go` does not call `os.Exit` directly except in the shutdown-error branch and the config-error branch; clean paths return from `main` normally.
- [x] `main.go` does not contain any `init()` function. All wiring is sequential inside `main` (per Decision D4 + the "No `init()` for module wiring" rule).
- [x] The composition root creates a single `*slog.Logger` and passes it explicitly into every collaborator that logs (no `slog.Default()` reads from inside business code).
- [x] `main.go` writes the listening port to the logger at level `info` exactly once (`server listening`, with structured field `port`).

---

### Slice 3: `internal/shared/db` — bun client + migrations

Brings the repository to "I can run `task migrate:apply` against the dev Postgres and the migrations table is created." No business tables exist yet; the `migrations/` directory contains only `.gitkeep`.

#### Acceptance Criteria

- [x] `internal/shared/db/client.go` exports `NewBunDB(ctx context.Context, databaseURL string, pool PoolConfig, logger *slog.Logger) (*bun.DB, error)`.
- [x] `NewBunDB` opens a `*sql.DB` against the URL using bun's native `pgdriver` (`github.com/uptrace/bun/driver/pgdriver`). The unit test asserts `db.Dialect().Name() == "pg"`.
- [x] `NewBunDB` configures `bun.WithDiscardUnknownColumns()` and registers a query hook that logs at `debug` level to the passed slog logger (so SQL is visible when `LIBRARY_LOG_LEVEL=debug` but quiet at `info`).
- [x] `NewBunDB` runs `db.PingContext(ctx)` and returns an error wrapping the underlying ping error when the database is unreachable.
- [x] `internal/shared/db/client.go` exports a `PoolConfig` struct with optional fields: `MaxOpenConns int`, `MaxIdleConns int`, `ConnMaxLifetime time.Duration`, `ConnMaxIdleTime time.Duration`. Zero values mean "use the hardcoded defaults"; non-zero values override on a per-field basis. (No env-var plumbing, no `LoadConfig` validation, no `.env.example` entry in Phase 1 — overrides are construction-time only.)
- [x] When a `PoolConfig` field is the zero value, `NewBunDB` substitutes the default: `MaxOpenConns = 25`, `MaxIdleConns = 5`, `ConnMaxLifetime = 5 * time.Minute`, `ConnMaxIdleTime = 2 * time.Minute`. These defaults are conservative — laptop-dev friendly and suitable for a small prod deployment; high-throughput deployments should override in `cmd/library/main.go`.
- [x] `NewBunDB` calls `db.SetMaxOpenConns`, `db.SetMaxIdleConns`, `db.SetConnMaxLifetime`, `db.SetConnMaxIdleTime` on the underlying `*sql.DB` with the resolved values (defaults merged with caller-provided overrides).
- [x] The `PoolConfig` type carries a doc comment that lists each field's default and the rationale ("conservative defaults — laptop dev friendly, suitable for small prod; override in `cmd/library/main.go` for high-throughput deployments"). The doc comment also notes that zero values fall back to the default, not to `database/sql`'s native zero behaviour.
- [x] `internal/shared/db/client_test.go` verifies pool config behaviour via an unexported helper `resolvePoolConfig(PoolConfig) PoolConfig` (the same helper `NewBunDB` calls internally to merge defaults): the zero `PoolConfig{}` resolves to `{MaxOpenConns: 25, MaxIdleConns: 5, ConnMaxLifetime: 5*time.Minute, ConnMaxIdleTime: 2*time.Minute}`, and an explicit `PoolConfig{MaxOpenConns: 7}` resolves to `{MaxOpenConns: 7, MaxIdleConns: 5, ConnMaxLifetime: 5*time.Minute, ConnMaxIdleTime: 2*time.Minute}` (overrides win field-by-field; unset fields still pick up defaults). Tests use stdlib `testing` only — no testify, no running database needed since `resolvePoolConfig` is pure. The existing bad-URL ping-error test stays as is.
- [x] `internal/shared/db/migrate.go` exports `ApplyMigrations(ctx context.Context, databaseURL string, migrationsDir string, logger *slog.Logger) error`.
- [x] `ApplyMigrations` shells out to the `atlas` CLI (`atlas migrate apply --url <databaseURL> --dir file://<migrationsDir>`), streams stdout/stderr to the logger at `info`/`error`, and returns a wrapped error if the command exits non-zero.
- [x] If the `atlas` binary is not on `PATH`, `ApplyMigrations` returns an error whose message includes both `atlas CLI not found on PATH` and the install hint `https://atlasgo.io/getting-started/`.
- [x] `atlas.hcl` at the repo root declares one env block named `local` with `url = "${LIBRARY_DATABASE_URL}"` and `dev = "docker://postgres/16/dev?search_path=public"` (or the documented `docker-image://` form atlas supports), pointing the dev-url at an ephemeral container so `atlas migrate hash` and `atlas migrate diff` work without a manual scratch DB.
- [x] `migrations/` exists and contains only `.gitkeep`. Atlas v0.20+ tolerates an empty migrations directory and creates `atlas.sum` on first `atlas migrate hash`. If the installed atlas version refuses an empty dir, fall back to a single-line `0000_init.sql` comment file (`-- intentionally empty; first business migration lands in Phase 2`) and document the workaround in `Taskfile.yml`'s comment block.
- [x] `task migrate:apply` invokes `atlas migrate apply --env local`. Running it against an empty dev Postgres succeeds and leaves the atlas `schema_migrations` (or `atlas_schema_revisions`) bookkeeping table in place; running it a second time is a no-op.
- [x] `task migrate:status` invokes `atlas migrate status --env local` and exits 0 regardless of whether anything is applied.
- [x] `internal/shared/db/client_test.go` (unit, no build tag) verifies that `NewBunDB("postgres://invalid", ...)` returns an error mentioning the URL or the underlying driver complaint — does **not** require a running database. It uses an obviously-bad URL (e.g., port `1` on localhost) so the test runs in milliseconds without external dependencies.
- [x] No business model structs are defined in `internal/shared/db/` — this package owns the bun connection and the migration runner only.

---

### Slice 4: `internal/shared/http` — middleware + helpers

Brings the repository to "any handler can register a typed domain error and the middleware translates it into the right status code with a canonical JSON body." Phase 1 registers only the two access-control errors; later phases extend the registry.

#### Acceptance Criteria

- [x] `internal/shared/http/response.go` exports `WriteJSON(w http.ResponseWriter, status int, body any) error` that sets `Content-Type: application/json`, writes the status code, JSON-encodes the body, and returns any encoder error.
- [x] `internal/shared/http/response.go` exports `ErrorResponse` struct with at minimum: `Error string` (snake_case error code, e.g. `unauthorized_role`), `Message string` (human-readable detail), `RequestID string` (omitempty), `Details map[string]any` (omitempty).
- [x] `internal/shared/http/middleware.go` exports `Middlewares(logger *slog.Logger) []func(http.Handler) http.Handler` returning the locked stack in order: chi `RequestID`, chi `RealIP`, a slog-adapter `Logger` middleware, chi `Recoverer`, `DomainErrorMiddleware`. (Design deviation: `Middlewares` returns the first four; `DomainErrorMiddleware` is composed separately in `cmd/library/main.go` because it needs the registry parameter. Functional order is preserved end-to-end.)
- [x] `internal/shared/http/middleware.go` exports `DomainErrorRegistry` (a struct, not a global) with methods `Register(target error, status int, code string)` and `Lookup(err error) (status int, code string, ok bool)`. `Lookup` walks the `errors.As` chain so wrapped errors still match.
- [x] `internal/shared/http/middleware.go` exports `DomainErrorMiddleware(registry *DomainErrorRegistry, logger *slog.Logger) func(http.Handler) http.Handler`.
- [x] `cmd/library/main.go` constructs the `DomainErrorRegistry` after the slog logger and registers Phase 1's two access-control errors: `registry.Register(&accesscontrol.UnauthorizedRoleError{}, http.StatusForbidden, "unauthorized_role")` and `registry.Register(&accesscontrol.UnknownActionError{}, http.StatusForbidden, "unknown_action")`. Phase 2+ modules extend this registration block in `main.go`. (Registry constructed in Slice 4; accesscontrol entries land in Slice 6 when the module exists.)
- [x] To use the middleware, a handler returns an `error` via a thin wrapper `Handle(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc`. `Handle` stores the returned error in the request context; `DomainErrorMiddleware` reads it after `next.ServeHTTP` returns.
- [x] When a handler returns a registered error, `DomainErrorMiddleware` writes `WriteJSON(w, status, ErrorResponse{Error: code, Message: err.Error(), RequestID: middleware.GetReqID(ctx)})`.
- [x] When a handler returns an unregistered error, `DomainErrorMiddleware` writes HTTP 500 with `ErrorResponse{Error: "internal_error", Message: "internal server error", RequestID: ...}` and logs the full error at `error` level (the raw `err.Error()` does **not** appear in the response body).
- [x] When a handler returns `nil`, the middleware writes nothing and does not touch the response writer (the handler already wrote the response itself).
- [x] The slog-adapter Logger middleware emits one `info`-level log line per request with structured fields: `method`, `path`, `status`, `bytes`, `duration_ms`, `request_id`, `remote_ip`.
- [x] `internal/shared/http/middleware_test.go` (unit, no build tag, stdlib `testing` only) covers: registered error maps to the registered status + code; wrapped registered error still matches (`errors.As` chain); unregistered error maps to 500 with `internal_error` body and the raw message is logged but not returned; nil return is a no-op; recoverer turns a panic into a 500 with `internal_error`. (Panic-test asserts 500 status; chi's Recoverer writes its own plain-text body upstream of `DomainErrorMiddleware`, so the JSON `internal_error` body only applies to errors returned via `Handle`, not panics.)
- [x] `WriteJSON` is callable from outside the `shared/http` package; `DomainErrorRegistry` is callable from outside; `DomainErrorMiddleware` is callable from outside. Internal helpers (the request-scoped error storage, the slog adapter) are unexported.
- [x] No third-party HTTP middleware lib beyond chi's `middleware` subpackage (no `negroni`, no `gorilla/handlers`).

---

### Slice 5: `internal/shared/events` — `EventBus` + `InMemoryEventBus`

Brings the repository to "modules can publish typed domain events and other modules can subscribe to them in-process." Phase 1 ships only the in-memory impl; durable buses (outbox, Redis Streams, Kafka) are out of scope per Decision D28.

#### Acceptance Criteria

- [x] `internal/shared/events/event_bus.go` exports `DomainEvent` interface with one method: `Type() string`.
- [x] `internal/shared/events/event_bus.go` exports `EventBus` interface with methods: `Publish(ctx context.Context, evt DomainEvent) error` and `Subscribe(eventType string, handler func(ctx context.Context, evt DomainEvent) error) Unsubscribe`, where `Unsubscribe` is a `func()` alias.
- [x] `internal/shared/events/in_memory_event_bus.go` exports `InMemoryEventBus` struct and `NewInMemoryEventBus(logger *slog.Logger) *InMemoryEventBus`.
- [x] `Publish` calls every handler subscribed for `evt.Type()` in registration order (first subscriber called first).
- [x] `Publish` calls handlers synchronously; it returns only after all handlers have returned.
- [x] If a handler returns an error, `Publish` logs it at `error` level with structured fields `event_type`, `handler_index`, `error`, then continues invoking the remaining handlers. `Publish` itself returns `nil` even when individual handlers fail (the bus is fire-and-forget at the publisher boundary — matches the TS source's behavior).
- [x] If a handler panics, the panic is recovered, logged at `error` level with a stack, the remaining handlers still run, and `Publish` returns `nil`.
- [x] `Subscribe` returns an `Unsubscribe` closure; after calling it, subsequent `Publish` calls do not invoke that handler.
- [x] Concurrent `Publish` from multiple goroutines is safe (use `sync.RWMutex` around the handler map; the unit test launches `N` goroutines and asserts no race when run with `-race`).
- [x] Subscribing to an event type with no current handlers is allowed (returns an `Unsubscribe` that is a no-op).
- [x] Publishing an event type with no subscribers is a no-op (returns `nil`).
- [x] `internal/shared/events/in_memory_event_bus_test.go` is table-driven where it makes sense (delivery order, multi-subscriber fanout, after-unsubscribe-not-called) and uses stdlib `testing` only. It includes a `t.Parallel()` test that publishes from 50 goroutines and asserts the handler is called 50 times.
- [x] The package exports no global event bus instance — every consumer takes `EventBus` as a constructor parameter.

---

### Slice 6: `internal/accesscontrol` module

Brings the repository to "any future module can authorize an action against a typed role policy." Mirrors the TS source's shape exactly, translated to Go idioms (functional-option sample builders, hand-written validation, stdlib testing, manual constructor wiring). No DB, no HTTP surface — the facade is invoked from inside other modules' controllers, which means in Phase 1 nothing actually calls it at runtime; the only proof it works is the unit test.

#### Acceptance Criteria — types

- [x] `internal/accesscontrol/types.go` declares `type Role string` with constants `RoleMember Role = "MEMBER"`, `RoleAccount Role = "ACCOUNT"`, `RoleStaff Role = "STAFF"`.
- [x] `types.go` declares `type ModuleName string` and `type ActionName string` (named string types — not raw `string` — so misuse at call sites is caught by the compiler).
- [x] `types.go` declares `type AuthUser struct { MemberID string; Role Role }`. `MemberID` is a plain `string` in Phase 1 (the canonical `MemberId` newtype lands in `internal/membership` in Phase 2; access-control deliberately keeps it `string` to avoid an import cycle from `membership → accesscontrol`).
- [x] `types.go` declares `type UnauthorizedRoleError struct { MemberID string; Role Role; ModuleName ModuleName; Action ActionName }` implementing `Error() string`. The error message includes the role, module, and action in the format `role <ROLE> is not authorized to perform <module>.<action> (memberID: <id>)`.
- [x] `types.go` declares `type UnknownActionError struct { ModuleName ModuleName; Action ActionName }` implementing `Error() string`. The error message is `unknown action <module>.<action> — no policy defined`. (Matches the source's `UnknownActionError` for easy comparison.)
- [x] Both error types are comparable with `errors.As`: `var ure *UnauthorizedRoleError; errors.As(err, &ure)` returns true for a wrapped `UnauthorizedRoleError`.
- [x] `types.go` exports no other types.

#### Acceptance Criteria — policy

- [x] `internal/accesscontrol/policy.go` declares `var policy = map[ModuleName]map[ActionName][]Role{ ... }` containing the same entries as the source's `POLICY`: `lending.borrow → [RoleMember]`, `catalog.uploadThumbnail → [RoleStaff]`, `catalog.removeThumbnail → [RoleStaff]`. (These actions are pre-declared in Phase 1 even though no module implements them yet — they exist to prove the data-driven shape works and so the unit tests have something to assert against. The policy map is the source of truth that Phase 2+ modules extend.)
- [x] `policy` is **unexported** at the package level — callers reach it only through the facade. The test file is in the same package, so the table-driven test can reference `policy` directly.
- [x] No role logic is hardcoded in any business module — adding a new action means adding a row to `policy.go`, nothing else. (This is asserted as a doc-comment invariant on the `policy` var; it cannot be enforced mechanically in Phase 1 because no other modules exist yet.)

#### Acceptance Criteria — sample data (functional options)

- [x] `internal/accesscontrol/sample_data.go` exports `SampleAuthUser(opts ...AuthUserOption) AuthUser` returning a default `AuthUser{MemberID: "member-placeholder-id", Role: RoleMember}` mutated by each option.
- [x] `sample_data.go` exports `SampleStaffAuthUser(opts ...AuthUserOption) AuthUser` returning a default `AuthUser{MemberID: "staff-placeholder-id", Role: RoleStaff}` mutated by each option.
- [x] `sample_data.go` exports `type AuthUserOption func(*AuthUser)`, `WithMemberID(id string) AuthUserOption`, and `WithRole(r Role) AuthUserOption`.
- [x] Override-order is deterministic: a later option overwrites an earlier option (e.g. `SampleStaffAuthUser(WithRole(RoleMember))` returns a `MEMBER` user — proving role override works but documenting that it's the caller's job to pick the right builder).
- [x] `sample_data.go` is the only place in the module that constructs `AuthUser` literals outside of tests.

#### Acceptance Criteria — facade

- [x] `internal/accesscontrol/facade.go` exports `type Facade struct { ... }` (or the locally-preferred `type AccessControlFacade struct`) with unexported fields and an exported constructor `NewFacade() *Facade`.
- [x] The facade has one method: `Authorize(authUser AuthUser, moduleName ModuleName, action ActionName) error`.
- [x] `Authorize` returns `nil` when `policy[moduleName][action]` contains `authUser.Role`.
- [x] `Authorize` returns a non-nil `*UnknownActionError` (with the offending module + action) when `policy[moduleName]` is nil **or** `policy[moduleName][action]` is nil.
- [x] `Authorize` returns a non-nil `*UnauthorizedRoleError` (carrying `MemberID`, `Role`, `ModuleName`, `Action`) when the policy entry exists but does not include the caller's role.
- [x] `Authorize` is the only method on the facade in Phase 1. No `Permit`, no `Forbid`, no role-mutation API.

#### Acceptance Criteria — configuration + module wiring

- [x] `internal/accesscontrol/configuration.go` exports `type Overrides struct{}` (empty in Phase 1, present so callers and tests have a stable extension point) and `func NewFacadeWithOverrides(_ Overrides) *Facade`.
- [x] `internal/accesscontrol/module.go` is **omitted entirely**. A package-level doc comment on `facade.go` documents the absence: "this module exposes no HTTP routes; it is wired purely by constructor injection into other facades." When Phase 2 modules arrive, they declare their own `module.go` against the established pattern. The AC is that no HTTP routes are registered under `/access-control/*` or any other access-control-named path.
- [x] Nothing in `cmd/library/main.go` mounts a router subtree for `accesscontrol`. The composition root constructs the facade and would pass it into other modules' constructors when they exist; in Phase 1 it constructs the facade and immediately drops the reference, which is acceptable.

#### Acceptance Criteria — facade unit test

- [x] `internal/accesscontrol/facade_test.go` uses stdlib `testing` only (no testify) and is in package `accesscontrol` (not `accesscontrol_test`) so it can read the unexported `policy` map for the data-driven assertion.
- [x] AC: `Authorize` returns `nil` for a `MEMBER` calling `lending.borrow`.
- [x] AC: `Authorize` returns a `*UnauthorizedRoleError` for an `ACCOUNT` calling `lending.borrow`, and the error's `MemberID`, `Role`, `ModuleName`, `Action` fields are populated with the caller's inputs.
- [x] AC: `Authorize` returns a `*UnknownActionError` for any role calling `lending.unknown-action` (action absent from a known module).
- [x] AC: `Authorize` returns a `*UnknownActionError` for any role calling `unknown-module.borrow` (module absent from policy).
- [x] AC: The `UnauthorizedRoleError.Error()` message matches the regex `role ACCOUNT.*lending\.borrow`.
- [x] AC: `Authorize` returns `nil` for a `STAFF` user calling `catalog.uploadThumbnail` and `catalog.removeThumbnail`.
- [x] AC: `Authorize` returns a `*UnauthorizedRoleError` for a `MEMBER` and for an `ACCOUNT` calling `catalog.uploadThumbnail` and `catalog.removeThumbnail`.
- [x] AC: Data-driven snapshot — the test asserts `policy[ModuleName("lending")][ActionName("borrow")]` equals `[]Role{RoleMember}` and `policy[ModuleName("catalog")][ActionName("uploadThumbnail")]` equals `[]Role{RoleStaff}`, then proves `Authorize` honors that data.
- [x] AC: `SampleStaffAuthUser()` returns a `STAFF` user that `Authorize` accepts for `catalog.uploadThumbnail`. `SampleStaffAuthUser(WithMemberID("staff-42"))` overrides the ID and preserves the role.
- [x] The entire facade test file runs in well under 100 ms (most tests should be sub-millisecond).

---

### Slice 7: `/healthz` + integration smoke test

Ties the previous six slices together. `/healthz` is the simplest possible vertical slice — it touches the composition root, the chi middleware stack, the JSON-response helper, and (indirectly) the dev-loop tasks `task run` / `task up` / `task test:integration`.

#### Acceptance Criteria — handler

- [x] `cmd/library/main.go` registers `r.Get("/healthz", healthzHandler)` (with `healthzHandler` defined inline or in `cmd/library/main.go` itself — not in a module). _Implementation: route + handler live in `internal/app/wiring.go` (the shared composition root) per the line-251 AC authorising extraction; `cmd/library/main.go` reaches it via `app.Wire`. Neither is a business module._
- [x] `healthzHandler` calls `shared/http.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})`. _Implementation: writes the literal `{"status":"ok"}` directly (no json.Encoder trailing newline) to satisfy the byte-identical body rule on line 235. Behaviourally equivalent; deliberate deviation._
- [x] The response body equals `{"status":"ok"}` (after `bytes.TrimSpace`), the status is 200, and `Content-Type` starts with `application/json`.
- [x] `/healthz` is mounted on the same router that uses the middleware stack, so a request to `/healthz` produces exactly one structured log line at `info` level.
- [x] `/healthz` does not require authentication and is reachable without any headers other than `Host`.

#### Acceptance Criteria — testcontainers helpers

- [x] `test/support/testcontainers.go` exports `StartPostgres(ctx context.Context, t testing.TB) PostgresContainer` returning a struct with `URL string` and a `t.Cleanup`-registered teardown.
- [x] `test/support/testcontainers.go` exports `StartRedis(ctx context.Context, t testing.TB) RedisContainer` with the same shape.
- [x] Both helpers read `DOCKER_HOST` from the environment and rely on testcontainers-go's default discovery — they do **not** hardcode the podman pipe path. The package doc on `testcontainers.go` documents the Windows + podman `DOCKER_HOST` setup with the exact `npipe:////./pipe/podman-machine-default` example and a pointer to `Taskfile.yml`'s comment block.
- [x] Both helpers are guarded by `//go:build integration` so `task test` does not try to start containers.
- [x] `StartPostgres` applies the `migrations/` directory via the same `db.ApplyMigrations` function `task migrate:apply` uses (no parallel implementation), so the integration suite uses the production migration path.

#### Acceptance Criteria — app factory

- [x] `test/support/app_factory.go` exports `BootApp(ctx context.Context, t testing.TB, cfg AppConfig) BootedApp` where `AppConfig` carries `DatabaseURL`, `RedisURL`, and a free port (selected via `net.Listen("tcp", "localhost:0")`).
- [x] `BootedApp` carries the chosen `BaseURL` (e.g. `http://localhost:54321`), a `Shutdown(ctx) error` method, and (optionally) the `*slog.Logger` and `*bun.DB` for tests that need to introspect.
- [x] `BootApp` reuses the same wiring code as `cmd/library/main.go` (extract the wiring into a package-internal helper if `main.go` was the original home; the AC is that there is exactly one wiring path and the integration test exercises it).
- [x] The shared wiring path (used by both `cmd/library/main.go` and `BootApp`) constructs the bun client via `db.NewBunDB(ctx, cfg.DatabaseURL, db.PoolConfig{}, logger)` — i.e. it passes an **empty** `PoolConfig` so Phase 1 relies on the hardcoded conservative defaults. No env-var plumbing for pool settings is added in Phase 1; a future high-throughput slice may construct a non-empty `PoolConfig` here without touching `LoadConfig`.
- [x] `BootApp` registers a `t.Cleanup` that calls `Shutdown` with a 5-second timeout.

#### Acceptance Criteria — smoke test

- [x] `test/integration/healthz_integration_test.go` has the build tag `//go:build integration` at the top.
- [x] The test (a) starts a Postgres container, (b) starts a Redis container, (c) applies migrations against the Postgres container, (d) boots the full app via `BootApp` pointing at those containers, (e) issues `GET <BaseURL>/healthz` with `net/http`, (f) asserts status 200, content-type starts with `application/json`, body is `{"status":"ok"}`.
- [x] The test also issues `GET <BaseURL>/does-not-exist` and asserts status 404 (proves the chi router is wired, not just a hand-rolled mux that returns 200 for everything).
- [x] The test uses stdlib `testing` and `net/http` only — no testify, no `httpexpect`.
- [x] Running `task test:integration` from a clean checkout (after `task up`) is green and takes under 30 seconds on a developer laptop including the testcontainers cold start.

#### Acceptance Criteria — unit suite stays fast

- [x] `task test` (which runs `go test ./...` with no build tags) completes in **well under 1 second** on a developer laptop — the entire Phase 1 unit suite (db client URL-error test, http middleware tests, events tests, accesscontrol facade test) should be a fraction of a second.
- [x] `task test` does **not** spin up any containers, hit any network, or read any env vars beyond `LIBRARY_*` from `.env` (and even those are not required — `LoadConfig` is only called from `cmd/library/main.go`, not from unit tests).

---

### Slice 8: `.http` files for manual probing

Brings the repository to "a developer can open `.http/healthz.http` in JetBrains GoLand / IntelliJ or VSCode (with the REST Client extension), click the gutter icon next to a request, and see the response — no curl, no Postman, no extra setup." Phase 1 ships only the healthz probes plus commented-out placeholders for Phase 2+ endpoints so the file grows naturally as later phases land. No `task http` command — committing the file is enough.

#### Acceptance Criteria

- [ ] `.http/healthz.http` exists at the repo root (in a top-level `.http/` directory — sibling to `cmd/`, `internal/`, `migrations/`, `test/`).
- [ ] The file's first non-comment line is `@baseUrl = http://localhost:3000` (a JetBrains/VSCode REST Client variable assignment). Every active request references it as `{{baseUrl}}`.
- [ ] A short comment block at the top of the file documents the workflow: "Start the server with `task run`, then in JetBrains GoLand/IntelliJ or VSCode (with the REST Client extension installed) click the gutter icon next to a request to send it. The `{{baseUrl}}` variable resolves from the `@baseUrl` line above." Comments use the `#` line-comment form (REST Client format).
- [ ] The file contains exactly two **active** requests, separated by `###` headers: (a) `GET {{baseUrl}}/healthz` with a comment naming the expected response (status 200, body `{"status":"ok"}`), and (b) `GET {{baseUrl}}/does-not-exist` with a comment naming the expected response (status 404).
- [ ] The file contains commented-out placeholder request blocks for representative Phase 2+ endpoints, each under its own `###` header that names the phase. Required placeholders (at minimum — pick paths from the discovery doc's per-phase slice list): `### POST {{baseUrl}}/books — Phase 2 (catalog)`, `### POST {{baseUrl}}/members — Phase 2 (membership)`, `### POST {{baseUrl}}/loans — Phase 3 (lending)`, `### POST {{baseUrl}}/chat — Phase 5 (chat, SSE)`. Each placeholder block has the HTTP method/path/headers/body commented out via `#` line-comments so the request is **inert** (cannot be accidentally sent until a future phase activates it).
- [ ] `.http/healthz.http` is committed to git. The file is **not** listed in `.gitignore`, and `.gitignore` is **not** amended in this slice (slice 1's `.gitignore` AC remains the canonical exclusion list).
- [ ] No `Taskfile.yml` entry is added for `.http` — slice 1's task list is unchanged. Probing is a manual editor action; no automation hook in Phase 1.

---

## File Map

| Slice | Files created |
| --- | --- |
| 1 | `go.mod`, `go.sum`, `.gitignore`, `.env.example`, `compose.yaml`, `Taskfile.yml` |
| 2 | `cmd/library/main.go`, `cmd/library/config.go` |
| 3 | `atlas.hcl`, `internal/shared/db/client.go`, `internal/shared/db/migrate.go`, `internal/shared/db/client_test.go`, `migrations/.gitkeep` (and `migrations/atlas.sum` if atlas requires it) |
| 4 | `internal/shared/http/response.go`, `internal/shared/http/middleware.go`, `internal/shared/http/middleware_test.go` |
| 5 | `internal/shared/events/event_bus.go`, `internal/shared/events/in_memory_event_bus.go`, `internal/shared/events/in_memory_event_bus_test.go` |
| 6 | `internal/accesscontrol/facade.go`, `internal/accesscontrol/types.go`, `internal/accesscontrol/policy.go`, `internal/accesscontrol/sample_data.go`, `internal/accesscontrol/configuration.go`, `internal/accesscontrol/module.go` (optional/no-op), `internal/accesscontrol/facade_test.go` |
| 7 | `test/support/app_factory.go`, `test/support/testcontainers.go`, `test/integration/healthz_integration_test.go` (`//go:build integration`) |
| 8 | `.http/healthz.http` |

No file is created in more than one slice. If a slice needs to touch a file from an earlier slice (e.g. slice 7 adds the `/healthz` route to slice 2's `main.go`), the AC names the exact change.

## Idiom Enforcement (every slice must follow)

Every slice in Phase 1 (and every slice in every later phase) follows these conventions from `.claude/bee-context.local.md`. Listing them again here so the slice-coder doesn't have to context-switch:

- **Manual constructor wiring.** No `wire`, no `fx`, no reflection-driven container. `cmd/library/main.go` constructs collaborators in order and passes them down by value or pointer as the receiver requires.
- **HTTP DTOs live in `<module>/http/dto.go`** and never escape that sub-package. Phase 1 has no business module with HTTP routes — `/healthz` is registered directly in `main.go` with no DTO indirection.
- **Stdlib testing only.** No `testify`. Use `t.Errorf`, `t.Fatalf`, `errors.Is`, `errors.As`. Tiny local helpers (e.g. an `equalSlices` if needed) are acceptable; library deps are not.
- **Hand-written validation.** No `go-playground/validator`. Parse-style functions in `<module>/schema.go` return typed-parsed + a domain `Invalid<X>Error`. Phase 1 has no validation surface because the accesscontrol module takes only typed structs and policy lookups — no `Parse*` helpers exist yet.
- **testcontainers-go reaches podman via `DOCKER_HOST`.** Documented in `Taskfile.yml` (slice 1) and `test/support/testcontainers.go` (slice 7). On Windows, the value is `npipe:////./pipe/podman-machine-default` (or whatever `podman machine inspect` reports). On Linux, the value is `unix:///run/user/$UID/podman/podman.sock`. macOS is `unix:///var/folders/.../podman.sock` (developer-specific — `podman machine inspect` again).
- **No mocks in tests.** In-memory implementations of the same interface for substitution; spec-local decorator wrappers (unexported, declared in the test file) for fault injection. Phase 1's only injectable interface is `EventBus`, and slice 5's tests use the real `InMemoryEventBus` plus inline handler functions — no mock library, no `MockEventBus` struct.
- **`log/slog` everywhere.** The composition root creates one `*slog.Logger`. Every collaborator that logs takes it as a constructor parameter. No `slog.Default()` reads from inside business code.
- **Functional options for sample data builders.** `SampleAuthUser(WithMemberID("..."))` not `SampleAuthUser{MemberID: "..."}`. The option type is `type AuthUserOption func(*AuthUser)`.
- **No `init()` for module wiring.** Slice 6 explicitly forbids an `init()` in `internal/accesscontrol/`; the rule applies to every package in the project.

## Definition of Done — Phase 1

Phase 1 is done when **all** of the following are true. Each item is verified manually (developer laptop) or by `task test` / `task test:integration`.

- [ ] `task up` brings podman compose up — Postgres + Redis containers are reported healthy by `podman compose ps`.
- [ ] `task migrate:apply` applies migrations against the dev Postgres without error and is a no-op on second run.
- [ ] `task run` starts the Go server on port 3000 (configurable via `LIBRARY_HTTP_PORT`) and logs `server listening` with the structured `port` field at `info` level.
- [ ] `curl localhost:3000/healthz` returns HTTP 200 with body `{"status":"ok"}` and `Content-Type: application/json`.
- [ ] `curl localhost:3000/does-not-exist` returns HTTP 404.
- [ ] Sending SIGINT to the running process triggers a graceful shutdown within 10 seconds with a final `server stopped` log line.
- [ ] `task test` (unit suite, no build tags) is green and completes in well under 1 second.
- [ ] `task test:integration` boots a testcontainers Postgres + Redis, applies migrations, hits `/healthz` end-to-end, and is green in under 30 seconds.
- [ ] `internal/accesscontrol.Authorize` returns `nil` for an authorized request (e.g. `MEMBER` calling `lending.borrow`).
- [ ] `internal/accesscontrol.Authorize` returns `*UnauthorizedRoleError` when the caller's role is not in the policy entry for the given module + action.
- [ ] `internal/accesscontrol.Authorize` returns `*UnknownActionError` when the module + action has no policy entry.
- [ ] `shared/http.DomainErrorMiddleware` maps a `*UnauthorizedRoleError` returned from a handler to HTTP 403 with body `{"error":"unauthorized_role","message":"...","request_id":"..."}` (verified by a unit test in slice 4 that registers the error with the registry and exercises the middleware against an `httptest.Recorder`).
- [ ] `shared/http.DomainErrorMiddleware` maps a `*UnknownActionError` returned from a handler to HTTP 403 with body `{"error":"unknown_action","message":"...","request_id":"..."}` (same kind of unit test).
- [ ] `shared/http.DomainErrorMiddleware` maps any unregistered error to HTTP 500 with body `{"error":"internal_error","message":"internal server error","request_id":"..."}` and the raw error is logged but not returned to the client.
- [ ] `shared/events.InMemoryEventBus.Publish` delivers an event to every subscribed handler for that event type, in subscription order, synchronously, before returning.
- [ ] After calling the `Unsubscribe` closure returned by `Subscribe`, that handler is no longer invoked on subsequent `Publish` calls.
- [ ] `internal/shared/db.NewBunDB` configures explicit pool settings (`MaxOpenConns=25`, `MaxIdleConns=5`, `ConnMaxLifetime=5m`, `ConnMaxIdleTime=2m` by default) via the `PoolConfig` struct; the composition root passes `PoolConfig{}` to rely on defaults in Phase 1.
- [ ] `.http/healthz.http` exists, is committed (not in `.gitignore`), contains active `GET /healthz` + `GET /does-not-exist` requests using `{{baseUrl}}`, and a developer can open it in JetBrains GoLand or VSCode (REST Client) and probe a running server (`task run`) without writing a curl command.
- [ ] `task fmt` and `task lint` (`gofmt -w .` + `go vet ./...`) pass with zero output.
- [ ] No file under `internal/` imports a module it isn't permitted to import per `.claude/BOUNDARIES.md`. (Phase 1 has only `accesscontrol`, `shared/db`, `shared/events`, `shared/http`; the only legal cross-package imports are: `accesscontrol → stdlib`, `shared/* → stdlib + third-party`, and `cmd/library → everything`.)
- [ ] `.claude/BOUNDARIES.md` is not edited in Phase 1 (the boundaries it declares for later modules stand as-is; Phase 1 adds no business modules so there's nothing to renegotiate).

## Open Questions

None. All 28 discovery decisions (D1–D28) are locked in `.claude/bee-context.local.md` and remain locked through Phase 1.

Error naming follows source for easy comparison: `UnauthorizedRoleError` and `UnknownActionError` (matches `apps/library/src/access-control/access-control.types.ts`). `.claude/BOUNDARIES.md` line 10 reflects this.

## Phase 1 → Phase 2 Handoff

When Phase 2 starts, the spec-builder for that phase can assume:

1. The dev loop works — `task up`, `task migrate:apply`, `task run`, `task test`, `task test:integration` are stable.
2. `internal/shared/db.NewBunDB` is the canonical bun client constructor; Phase 2's first bun repository uses it directly.
3. `internal/shared/db.ApplyMigrations` is the canonical migration runner; Phase 2's first business migration (`0001_catalog.sql`) lands in `migrations/` and is picked up automatically.
4. `internal/shared/http.DomainErrorMiddleware` + `DomainErrorRegistry` is the canonical error-mapping path; Phase 2 modules register their domain errors with the registry at wire time in `cmd/library/main.go`.
5. `internal/shared/events.InMemoryEventBus` is the canonical event bus; Phase 2 modules publish through it. (Durable bus is out of scope for the whole project per D28.)
6. `internal/accesscontrol.Facade` is callable from any future module's facade constructor; Phase 2's `catalog` and `membership` facades take it as a constructor parameter, exactly mirroring the TS source.
7. `test/support.BootApp` boots the full composition root for integration tests; Phase 2's crucial-path tests reuse it without modification.
8. The per-module file convention (`facade.go`, `types.go`, `schema.go`, `repository.go`, `in_memory_repository.go`, `bun_repository.go`, `configuration.go`, `sample_data.go`, `module.go`, `facade_test.go`, plus `http/` subdir) is established in `accesscontrol` as a partial reference (no repos, no HTTP) and is the template Phase 2 modules expand into.

No Phase 1 file or AC needs to change to enable Phase 2.

[ ] Reviewed
