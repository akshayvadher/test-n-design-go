# test-n-design-go

A Go port of Jakub Nabrdalik's [improving-tdd-demo](https://github.com/jakubn/improving-test) — a modular-monolith library service that demonstrates the architectural patterns underneath sustainable TDD: bounded contexts behind facades, one transaction per module, domain events as the only cross-module write path, and in-memory test doubles in place of mocks.

This is a teaching repository. Every design decision is deliberate, every shortcut is documented, and every architectural rule is enforced through file layout rather than tooling.

## Quick start

The service ships as a single binary backed by Postgres and Redis. Both run locally via `podman compose`; no docker required.

```sh
task up                # start postgres + redis, wait until healthy
task migrate:apply     # apply atlas migrations
task run               # run the library server on :3000 (Ctrl-C to stop)
```

Verify the server is up:

```sh
curl http://localhost:3000/healthz
# {"status":"ok"}
```

Tear down when done:

```sh
task down              # stops containers; data volume preserved
task down:clean        # stops containers AND deletes the data volume
```

## Testing

Two tiers:

```sh
task test              # unit tests, no containers, sub-second
task test:race         # unit tests + race detector
task test:integration  # postgres + redis via testcontainers-go
```

Unit tests use in-memory substrates everywhere — repositories, the event bus, and the transactional context. Integration tests run against real Postgres (spun up per-suite via testcontainers) and a real Redis client. There is no third tier and no mocks.

See [GUIDE.md](GUIDE.md) for the per-slice test-and-ship checklist.

## Project layout

```
test-n-design-go/
├── cmd/library/                 composition root for the production binary
├── internal/
│   ├── accesscontrol/           access-control facade (role checks)
│   ├── catalog/                 books + copies
│   ├── membership/              members + eligibility
│   ├── lending/                 loans + reservations + auto-loan saga
│   ├── fines/                   fine assessment + payment
│   ├── categories/              curated category list
│   ├── chat/                    SSE chat module (Phase 5)
│   ├── app/                     shared wiring used by main + tests
│   └── shared/                  cross-module substrates (db, events, tx, http, gateways)
├── migrations/                  atlas versioned SQL migrations
├── test/
│   ├── crucial_path/            cross-module integration tests
│   ├── support/                 testcontainers helpers, app factory
│   └── integration/             slice-level integration tests
├── docs/specs/                  phase specs (the source of truth)
├── .claude/                     BOUNDARIES.md + bee workflow state
├── compose.yaml                 podman compose for postgres + redis
├── Taskfile.yml                 task runner
├── atlas.hcl                    atlas config
└── go.mod
```

## Modules

- **accesscontrol** — `Authorize(user, resource, action)` returns the access decision against a hardcoded role policy. No state, no HTTP routes. Every business module that performs a mutation calls into it.
- **catalog** — owns books and physical copies. `POST /books`, `POST /books/{id}/copies`, copy availability transitions. Reads go through a Redis-backed `BookCacheGateway`; lookups fall back to the bun repository on cache miss.
- **membership** — owns members and the eligibility decision lending consumes. `POST /members`, `GET /members/{id}`.
- **lending** — owns loans and reservations. `POST /loans`, `POST /loans/{id}/return`, `POST /reservations`. Hosts the [auto-loan saga](SAGA.md): when a loan returns, the saga walks the reservation queue and either opens a fresh loan for the next eligible reserver or publishes an `AutoLoanFailed` event.
- **fines** — assesses overdue fines, suspends members past a configurable threshold, and accepts payments. `POST /fines/assess`, `POST /fines/{id}/pay`.
- **categories** — the simplest module: no cross-module deps, no events, no transactions. `POST /categories`, `GET /categories?startsWith=...`, `GET /categories/{id}`.
- **chat** — SSE streaming chat. `POST /chat` returns a Server-Sent Events stream framed as `delta` / `done` events. Backed by a pluggable `ChatGateway` port — the binary defaults to a deterministic in-memory gateway that splits the last message into whitespace tokens; an `OpenAIChatGateway` exists alongside it and can be swapped in at the composition root.

## Architectural principles

The five rules every module respects:

- **Modular monolith.** One Go package per bounded context. The facade is the only exported surface; repositories, HTTP DTOs, and parsers stay internal to the module.
- **Facade pattern.** Each module exports `NewFacade(deps...) *Facade`. Tests substitute dependencies via a module-local `Overrides` struct with in-memory defaults. No global state, no `init()` for wiring.
- **One transaction per module.** A `TransactionalContext` instance lives inside one module. Cross-module mutations happen sequentially: own-tx commits first, then the cross-module facade call runs. See [ARCHITECTURE.md § One transaction per module](ARCHITECTURE.md#one-transaction-per-module).
- **Events as the only cross-module write path.** When a domain decision needs to ripple, it stages a `DomainEvent` during the local tx. Subscribers receive the event AFTER the publisher's tx commits. See [SAGA.md](SAGA.md) for the auto-loan walk-through.
- **In-memory test doubles, no mocks.** Test substitution is an in-memory implementation of the production port; fault injection is a spec-local decorator (`throwingOnceCatalogRepository`, `throwingChatGateway`) declared in the test file. No `gomock`, no `mockery`, no `testify/mock`.

These rules are enforced by file layout, package boundaries, and [`.claude/BOUNDARIES.md`](.claude/BOUNDARIES.md) — not by linters. The architecture review in [ARCHITECTURE.md § Boundaries enforcement](ARCHITECTURE.md#boundaries-enforcement) explains why.

## What to read next

- [ARCHITECTURE.md](ARCHITECTURE.md) — module boundaries, the facade pattern, the post-commit publish rule, and how the substrates fit together.
- [SAGA.md](SAGA.md) — the auto-loan saga walk-through: the four atomicity invariants, per-aggregate serialisation, and the durability gap.
- [GUIDE.md](GUIDE.md) — how to add a new module. Templated on the categories module (no cross-module deps) so the diff stays small.
- [`.claude/BOUNDARIES.md`](.claude/BOUNDARIES.md) — the per-module import allowlist that every new module updates.

## Tech stack

| Concern             | Choice |
| ---                 | --- |
| Language            | Go 1.26+ |
| Router              | `github.com/go-chi/chi/v5` |
| SQL builder         | `github.com/uptrace/bun` |
| Postgres driver     | `bun/driver/pgdriver` |
| Migrations          | `ariga.io/atlas` (versioned SQL, zero-padded) |
| Logger              | `log/slog` (stdlib) |
| Env config          | `github.com/spf13/viper` |
| UUID                | `github.com/google/uuid` |
| Redis client        | `github.com/redis/go-redis/v9` |
| OpenAI SDK          | `github.com/sashabaranov/go-openai` |
| Test containers     | `github.com/testcontainers/testcontainers-go` |
| Container engine    | `podman compose` (NOT docker) |
| Task runner         | `taskfile.dev` |
| Assertions          | stdlib `testing.T` + `errors.Is/As` (no testify) |
| Validation          | hand-written `Parse<X>` functions (no `validator`) |
| DI                  | manual constructor wiring (no `wire`, no `fx`) |

## License

MIT. See `LICENSE` for the full text.
