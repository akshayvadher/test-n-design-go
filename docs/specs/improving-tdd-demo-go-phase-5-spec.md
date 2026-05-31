# Spec: improving-tdd-demo Go Port — Phase 5 (Chat + Docs + Polish)

## Overview

Phase 5 ships the last business module — `chat` — exposing `POST /chat` as a Server-Sent Events stream backed by a pluggable `ChatGateway` port (in-memory deterministic gateway for tests; OpenAI-backed gateway for production). It ports the four top-level docs (`README.md`, `ARCHITECTURE.md`, `SAGA.md`, `GUIDE.md`) and writes a `.claude/skills/` skill that teaches Claude Code how to add a new module the Go way.

The architectural conviction: **streaming HTTP goes through `http.Flusher` + `event:` / `data:` SSE framing — no library, no websocket fallback; chat is a leaf module with no event subscriptions and no `TransactionalContext` integration (no domain writes); the facade returns a `<-chan ChatFrame` so the handler loop is `for frame := range ch { write; flush }` with `<-ctx.Done()` as the cancellation signal**.

Phase 5 introduces zero new architectural substrate. It proves the per-module template scales to a streaming surface.

## Why

- **The third HTTP shape — streaming SSE — without breaking Phase 1–4 conventions.** Chi for routing, `sharedhttp.Handle` for the pre-stream error path, `http.Flusher` for the streaming body. The reader sees the same outer shape as Phase 2's catalog handlers — extract request, call facade, write response — with the response loop substituted for `WriteJSON`.
- **A pluggable `ChatGateway` port.** `InMemoryChatGateway` splits the last message into whitespace tokens and emits one `ChatDelta` per token (deterministic, no timing flakes). `OpenAIChatGateway` wraps the chosen SDK and forwards stream chunks unchanged.
- **The four top-level docs** turn the repository from "a Go port" into "a Go demo of Jakub Nabrdalik's TDD principles." All four read in native Go — no "this would be a class in TypeScript" comments.
- **The Claude skill** teaches the per-module template. Loaded by `/bee:sdd` when a developer asks "add a new module"; the resulting module looks exactly like fines or categories.

## In Scope

- `internal/shared/chatgateway/`: `ChatMessage`, `ChatDelta`, `ChatGateway` interface; `InMemoryChatGateway` (token-splitter, deterministic); `OpenAIChatGateway` (wraps the chosen SDK; per Open Question 1 the recommended default is `github.com/sashabaranov/go-openai`).
- `internal/chat/` module: `facade.go`, `types.go`, `schema.go`, `sample_data.go`, `configuration.go`, `module.go`, `facade_test.go`. NO repository (stateless), NO bun integration, NO `//go:build integration` test (no DB).
- `internal/chat/http/`: `dto.go`, `mapping.go`, `handlers.go` (SSE handler), `handlers_test.go` (uses `httptest.NewServer` + real HTTP client reading the streaming body).
- `internal/app/wiring.go` extended: chat facade construction (OpenAI gateway when `OPENAI_API_KEY` set, else in-memory); chat error registry; `chatModule.Wire(r)` after Phase-4 modules; `app.Wired.ChatFacade`.
- `cmd/library/main.go` extended: load `OPENAI_API_KEY` and `OPENAI_MODEL` via viper (defaults: empty + `gpt-4o-mini`); fall back to in-memory gateway when key unset (so `task run` boots without an API key).
- `README.md`, `ARCHITECTURE.md`, `SAGA.md`, `GUIDE.md` at repo root. Port the TS originals; adjust code samples + file paths + commands to Go. No "this would be a class in TypeScript" phrasing.
- `.claude/skills/improving-tdd-demo-go.md` — Claude Code skill teaching the per-module template (mirrors the TS demo's skill).
- `.http/chat.http` — sample request body for the chat endpoint.

## Out of Scope

- Chat persistence (no `chats` table; stateless per request — matches TS).
- Authentication on `POST /chat` (no `accesscontrol.Authorize` — matches TS).
- Rate limiting / token budgeting.
- WebSocket fallback. SSE-only.
- `internal/chat/bun_repository.go`. No DB.
- Reconnection / resumability via `Last-Event-ID`. Each request is a fresh stream.
- Chat UI / dashboard.
- Cross-module event integration for chat (no `FineAssessed`-triggered auto-reply, etc.).
- Third-party SSE library. Stdlib `http.Flusher` is enough.
- Function-calling / tool-use. Plain text streaming only.
- Features the TS source's `chat.module.ts` has that aren't enumerated here. Spec-builder MUST diff the TS chat module during Slice 3 and add or defer (Open Question 5).

## Slices

Phase 5 ships in **seven slices**, ordered outside-in within the chat module (gateway port + in-memory gateway → OpenAI gateway → facade → SSE handler → handler tests), then docs (one slice covering all four), then the Claude skill. Each slice ends green: `go build`, unit tests, integration tests where the slice ships one.

---

### Slice 1: `chatgateway` port + `InMemoryChatGateway`

Locks the gateway shape before the OpenAI impl piles on. No HTTP, no chat facade — just the port and the in-memory impl.

#### Acceptance Criteria — port + types

- [ ] `internal/shared/chatgateway/gateway.go` declares `type ChatMessage struct { Role string; Content string }`. Field order matches TS source 1:1 (`role` then `content`).
- [ ] `type ChatDelta struct { Content string }`. Single-field struct for forward-compatibility (a future `Role` field on streaming deltas can be added without changing call sites).
- [ ] `type ChatGateway interface { Stream(ctx context.Context, messages []ChatMessage) (<-chan ChatDelta, error) }`. The error return is for setup-time failures (invalid messages list, gateway misconfiguration). Per-token errors close the channel; see Open Question 2.
- [ ] Constants for roles: `RoleUser = "user"`, `RoleAssistant = "assistant"`, `RoleSystem = "system"` — match OpenAI's role vocabulary. Exported.
- [ ] Package doc comment names the contract: "ChatGateway streams response deltas for a multi-turn chat completion. Implementations MUST close the returned channel when the stream ends (normally or on error). Callers MUST honour ctx cancellation by ranging until the channel closes."

#### Acceptance Criteria — `InMemoryChatGateway`

- [ ] `internal/shared/chatgateway/in_memory_gateway.go` exports `InMemoryChatGateway struct { TokenInterval time.Duration }` (exported field for tests to twiddle).
- [ ] `NewInMemoryChatGateway() *InMemoryChatGateway` returns `&InMemoryChatGateway{TokenInterval: 0}`. Zero interval = synchronous emit (test-friendly).
- [ ] `(*InMemoryChatGateway).Stream(ctx context.Context, messages []ChatMessage) (<-chan ChatDelta, error)`:
  - If `len(messages) == 0`, return `(nil, &EmptyMessagesError{})`.
  - Take the LAST message's `Content`; split on whitespace via `strings.Fields`; if zero tokens, open a channel, immediately close it, return.
  - Open a buffered channel of size `len(tokens) + 1`; in a goroutine: for each token, sleep `TokenInterval` (no-op if 0), check `<-ctx.Done()` (return early on cancellation), write `ChatDelta{Content: token + " "}` to the channel; on loop exit close the channel.
  - Return `(channel, nil)`.
- [ ] `type EmptyMessagesError struct{}` implementing `Error() string` returning `"chat gateway: messages must be non-empty"`. Pointer-receiver. Maps to HTTP 400 via the registry.
- [ ] The trailing space after each token is intentional — concatenating all deltas reconstructs the original text (minus a trailing space). Doc comment names this.

#### Acceptance Criteria — in-memory gateway tests

- [ ] `internal/shared/chatgateway/in_memory_gateway_test.go` lives in package `chatgateway`. Stdlib testing only.
- [ ] AC (happy path): `Stream(ctx, [{user, "hello world go"}])`; read all deltas; assert sequence `["hello ", "world ", "go "]`; assert channel closes after the third delta.
- [ ] AC (single-message single-token): `Stream(ctx, [{user, "hi"}])`; assert single delta `"hi "`; channel closes.
- [ ] AC (empty messages slice): `Stream(ctx, [])` returns `(nil, *EmptyMessagesError)` matchable via `errors.As`.
- [ ] AC (whitespace-only message): `Stream(ctx, [{user, "   "}])`; channel opens and closes immediately with zero deltas.
- [ ] AC (context cancellation aborts stream): set `TokenInterval = 50 * time.Millisecond`; `Stream(ctx, [{user, "a b c d e"}])`; cancel `ctx` after the first delta is read; assert the channel closes within 100 ms without emitting the remaining four tokens. Use `time.After` as the safety net.
- [ ] AC (multi-message history uses only last): `Stream(ctx, [{user, "ignored"}, {assistant, "also ignored"}, {user, "echo me"}])`; assert deltas equal `["echo ", "me "]`.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go build ./...` passes.
- [ ] `go vet ./...` clean.
- [ ] `go test ./internal/shared/chatgateway/...` is green and runs in under 200 ms (the cancellation test adds ~60 ms).
- [ ] `go test -race ./internal/shared/chatgateway/...` is green.
- [ ] No `init()` introduced.
- [ ] `internal/shared/chatgateway` imports only stdlib + `internal/shared/events` is NOT needed here (no events emitted from the gateway).

---

### Slice 2: `OpenAIChatGateway`

Ships the production gateway. No unit tests (we don't mock the OpenAI SDK) — only a compile-time interface guard. Manual smoke verification in DoD.

#### Acceptance Criteria — dependency

- [ ] `go.mod` gains `github.com/sashabaranov/go-openai v1.x.x` (locked to the latest stable as of spec time). See Open Question 1 if the spec-builder prefers `github.com/openai/openai-go` instead.
- [ ] `go mod tidy` is clean.

#### Acceptance Criteria — gateway impl

- [ ] `internal/shared/chatgateway/openai_gateway.go` exports `OpenAIChatGateway struct` with unexported fields `client *openai.Client`, `model string`, `logger *slog.Logger`.
- [ ] `NewOpenAIChatGateway(apiKey string, model string, logger *slog.Logger) *OpenAIChatGateway`. If `logger == nil`, substitutes `slog.New(slog.DiscardHandler)`. If `model == ""`, substitutes `"gpt-4o-mini"`. Constructs the underlying client via `openai.NewClient(apiKey)`.
- [ ] `(*OpenAIChatGateway).Stream(ctx context.Context, messages []ChatMessage) (<-chan ChatDelta, error)`:
  - If `len(messages) == 0`, return `(nil, &EmptyMessagesError{})` (same setup-error type as the in-memory gateway).
  - Map `messages` to `[]openai.ChatCompletionMessage` (role + content).
  - Call `g.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{Model: g.model, Messages: <mapped>, Stream: true})`. On setup error, return `(nil, err)` directly.
  - Open an unbuffered channel; spawn a goroutine that loops `stream.Recv()`: on `io.EOF`, close the channel and return; on other error, log at `error` level with `error`, `model`, attribute, close the channel, return; on success, write `ChatDelta{Content: response.Choices[0].Delta.Content}` to the channel (skip empty deltas to avoid noise).
  - Return `(channel, nil)`.
- [ ] `var _ ChatGateway = (*OpenAIChatGateway)(nil)` compile-time guard at the file's top.
- [ ] Doc comment names the gateway-error policy: "Setup errors (invalid API key, network unreachable at request time) return through the error return. Per-token errors close the channel early — callers see fewer deltas than expected but no signal that the stream truncated."

#### Acceptance Criteria — slice-level hygiene

- [ ] `go build ./...` passes (proves the gateway compiles against the chosen SDK version).
- [ ] `go vet ./...` clean.
- [ ] No `init()` introduced.
- [ ] `internal/shared/chatgateway/openai_gateway.go` imports stdlib + `github.com/sashabaranov/go-openai` (or the chosen alternative). NOT `internal/shared/events`, NOT `internal/app`, NOT any module package.

---

### Slice 3: `internal/chat` module — facade + types + schema + tests

Ships the chat module skeleton + business logic + facade tests. `chat.Facade.Stream` returns a `<-chan ChatFrame` against the in-memory gateway. HTTP + wiring land in Slice 4.

#### Acceptance Criteria — types

- [ ] `internal/chat/types.go` declares `type ChatFrame struct { Type ChatFrameType; Data string }`. Single-struct DTO for all stream frames (delta + error + done).
- [ ] `type ChatFrameType string` + exported constants `FrameTypeDelta ChatFrameType = "delta"`, `FrameTypeError ChatFrameType = "error"`, `FrameTypeDone ChatFrameType = "done"`. The SSE handler uses `Type` as the `event:` line and `Data` as the `data:` line.
- [ ] `type ChatMessage struct { Role string; Content string }` re-exported as an alias of `chatgateway.ChatMessage` OR re-declared with the same shape. Recommended default: re-declare to keep the chat-facade callers from importing `chatgateway` (clean module boundary). The facade internally converts `chat.ChatMessage` → `chatgateway.ChatMessage` before calling `Stream`.
- [ ] `type InvalidChatRequestError struct { Reason string }` implementing `Error() string` returning `"Invalid chat request: <reason>"`. Pointer-receiver. Maps to HTTP 400 in the registry.
- [ ] `type ChatGatewayError struct { Cause error }` implementing `Error() string` returning `"Chat gateway error: <cause>"` and `Unwrap() error { return e.Cause }`. Pointer-receiver. Maps to HTTP 502 in the registry. This is the type the facade returns when the gateway's setup-error fires.

#### Acceptance Criteria — schema

- [ ] `internal/chat/schema.go` exports `type ChatRequestDto struct { Messages []ChatMessage }` and `ParseChatRequest(raw []byte) (ChatRequestDto, error)`.
- [ ] `ParseChatRequest` uses `json.Decoder` with `DisallowUnknownFields()`.
- [ ] Rejects `len(Messages) == 0` with `*InvalidChatRequestError{Reason: "messages must be non-empty"}`.
- [ ] Rejects any message with blank `Content` (after `strings.TrimSpace`) with `*InvalidChatRequestError{Reason: "message content must be non-blank"}`.
- [ ] Rejects any message with `Role` not in `{"user", "assistant", "system"}` with `*InvalidChatRequestError{Reason: "invalid role: <role>"}`.
- [ ] Rejects JSON decode errors (malformed JSON, unknown fields) by wrapping into `*InvalidChatRequestError{Reason: <err.Error()>}`.

#### Acceptance Criteria — sample data

- [ ] `internal/chat/sample_data.go` exports `SampleChatRequest(opts ...ChatRequestOption) ChatRequestDto` defaulting to `ChatRequestDto{Messages: []ChatMessage{{Role: "user", Content: "Tell me about a book."}}}`.
- [ ] Option `WithMessages(messages []ChatMessage) ChatRequestOption`. Last-option-wins. (Chat sample data is minimal — the facade tests construct messages inline; the sample is a convenience for HTTP handler tests.)

#### Acceptance Criteria — configuration

- [ ] `internal/chat/configuration.go` exports `type Overrides struct { Gateway chatgateway.ChatGateway; Logger *slog.Logger }`. Two fields only.
- [ ] `NewFacadeWithOverrides(o Overrides) *Facade` substitutes defaults: `Gateway → chatgateway.NewInMemoryChatGateway()`, `Logger → slog.New(slog.DiscardHandler)`.

#### Acceptance Criteria — `Facade.Stream`

- [ ] `internal/chat/facade.go` exports `type Facade struct` with unexported fields `gateway chatgateway.ChatGateway`, `logger *slog.Logger`.
- [ ] `NewFacade(gateway chatgateway.ChatGateway, logger *slog.Logger) *Facade` constructor.
- [ ] `(*Facade).Stream(ctx context.Context, req ChatRequestDto) (<-chan ChatFrame, error)`:
  - Map `req.Messages` ([]chat.ChatMessage) → []chatgateway.ChatMessage.
  - Call `f.gateway.Stream(ctx, mapped)`. On error, wrap into `*ChatGatewayError{Cause: err}` and return `(nil, wrapped)`.
  - Open a buffered channel of size 8 (small buffer to smooth small bursts without blocking the gateway goroutine; size locked at 8 — small enough to backpressure on a slow client, large enough that single-digit-token gusts don't block).
  - Spawn a goroutine: range over the gateway's channel; for each delta, write `ChatFrame{Type: FrameTypeDelta, Data: delta.Content}` to the outer channel (respecting `<-ctx.Done()` — on cancel, return without writing); on gateway-channel close, write `ChatFrame{Type: FrameTypeDone, Data: ""}` to the outer channel (best-effort; ignored if ctx is cancelled), then close the outer channel.
  - Return `(channel, nil)`.
- [ ] Doc comment names the contract: "Stream returns a channel of ChatFrame ending in a single Done frame (unless the context cancels first). Per-token gateway errors close the inner channel and the facade emits a Done frame regardless — the caller cannot distinguish a clean completion from a mid-stream gateway truncation. Setup errors return through the error return."

#### Acceptance Criteria — facade tests (`facade_test.go`)

- [ ] `internal/chat/facade_test.go` lives in package `chat`. Uses real `*chatgateway.InMemoryChatGateway`. Stdlib testing only.
- [ ] AC (happy path — token-by-token streaming): build a facade with the default in-memory gateway; call `Stream(ctx, SampleChatRequest(WithMessages([]ChatMessage{{Role: "user", Content: "hello world"}})))`; range the returned channel; assert the captured slice equals `[{delta, "hello "}, {delta, "world "}, {done, ""}]`.
- [ ] AC (gateway setup error → `*ChatGatewayError`): inject a `throwingChatGateway` (unexported decorator in `facade_test.go` whose `Stream` returns `nil, errors.New("simulated")`); call `Stream`; the second return is `*ChatGatewayError` matchable via `errors.As`; `errors.Unwrap` returns the original sentinel.
- [ ] AC (empty-messages setup error from gateway propagates): inject the real in-memory gateway; call `Stream(ctx, ChatRequestDto{Messages: nil})` (bypassing schema validation — the test calls the facade directly); the gateway returns `*EmptyMessagesError`; the facade wraps into `*ChatGatewayError`; `errors.Unwrap` returns the gateway's error.
- [ ] AC (context cancellation closes channel early): build a facade with `TokenInterval = 50 * time.Millisecond` and a multi-token message; cancel ctx after the first delta is read; assert the channel closes within 200 ms with fewer than `len(tokens) + 1` frames (no `Done` frame guaranteed).
- [ ] AC (channel is buffered at size 8): synthesise a no-op consumer that doesn't read; call `Stream` with a 4-token message; assert the goroutine completes (the buffered writes don't block) by waiting on a `done` signal the goroutine sets. (Indirect test of the buffer-size choice; documents the intent.)
- [ ] AC (schema parsing — happy path): `ParseChatRequest(json bytes)` returns the populated DTO; reflected via JSON round-trip.
- [ ] AC (schema parsing — empty messages): rejects with `*InvalidChatRequestError`.
- [ ] AC (schema parsing — invalid role): `{"messages":[{"role":"robot","content":"hi"}]}` → `*InvalidChatRequestError`.
- [ ] AC (schema parsing — blank content): `{"messages":[{"role":"user","content":"   "}]}` → `*InvalidChatRequestError`.
- [ ] AC (schema parsing — unknown field): `{"messages":[...], "system":"ignored"}` → `*InvalidChatRequestError` (via `DisallowUnknownFields`).

#### Acceptance Criteria — slice-level hygiene

- [ ] `go build ./...` is green.
- [ ] `go vet ./...` clean.
- [ ] `go test ./internal/chat/...` is green and under 350 ms.
- [ ] No `init()` introduced.
- [ ] `internal/chat/` imports per BOUNDARIES.md: `internal/shared/chatgateway` + stdlib. NOT any other module.

---

### Slice 4: `internal/chat/http` SSE handler + module wiring + composition root

Ships the SSE handler, module registration, and composition-root wiring. `POST /chat` streams SSE end-to-end against the in-memory gateway. Handler tests in Slice 5.

#### Acceptance Criteria — request DTO

- [ ] `internal/chat/http/dto.go` exports `type ChatRequest struct { Messages []ChatMessageDto ` `json:"messages"` ` }` and `type ChatMessageDto struct { Role string ` `json:"role"` `; Content string ` `json:"content"` ` }`. JSON tags match TS source (`role`, `content`, `messages`).
- [ ] `internal/chat/http/mapping.go` exports unexported `toFacadeRequest(req ChatRequest) chat.ChatRequestDto` — maps the HTTP shape to the facade shape (identical field structure; the indirection enforces the boundary).

#### Acceptance Criteria — SSE handler

- [ ] `internal/chat/http/handlers.go` exports `type Handlers struct { facade *chat.Facade; logger *slog.Logger }` + `NewHandlers(facade *chat.Facade, logger *slog.Logger) *Handlers`.
- [ ] `(*Handlers).StreamChat(w http.ResponseWriter, r *http.Request) error` — returns `error` for `sharedhttp.Handle` to map (per Phase-2 handler convention) ONLY for the pre-stream error path (DTO parse error, gateway setup error). Once the stream starts, errors are written into the stream as `event: error` frames and the handler returns `nil`.
- [ ] Pre-stream:
  - Type-assert `w.(http.Flusher)`; if it doesn't satisfy, return `errors.New("streaming unsupported")` (maps to 500 — though chi's default response writer DOES satisfy `http.Flusher`, so this branch should never fire in practice; it's defensive).
  - Decode the body into `ChatRequest` via `json.NewDecoder(r.Body).DisallowUnknownFields().Decode(&req)`; on error, return wrapped `*InvalidChatRequestError`.
  - Call `chat.ParseChatRequest`-equivalent validation (call the schema package on the HTTP-shaped struct: build raw JSON bytes via `json.Marshal(toFacadeRequest(req))`, then `chat.ParseChatRequest` — OR call validation inline on `toFacadeRequest(req)` directly via a helper. Recommended default: inline helper `validateRequest(req chat.ChatRequestDto) error` exported from `internal/chat/schema.go` that takes the parsed DTO and returns the same `*InvalidChatRequestError` set. This avoids the double-JSON-round-trip).
  - Call `f.facade.Stream(r.Context(), facadeReq)`. On `*ChatGatewayError`, return it (maps to 502 in the registry).
- [ ] Stream-start: set headers `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`, `X-Accel-Buffering: no` (defensive for nginx/reverse-proxy buffering). `w.WriteHeader(200)` is implicit on the first write. Call `flusher.Flush()` immediately to commit the headers.
- [ ] Stream loop: `for frame := range channel { ... }`:
  - Write `event: <frame.Type>\ndata: <frame.Data>\n\n` to `w` (note the literal `event:` and `data:` SSE field names; double-newline terminates the frame).
  - Call `flusher.Flush()` after each frame so the client sees deltas in real time.
  - On any write error (client disconnected), log at `info` level with `"client disconnected"` + `"remote_addr"` attribute and return `nil` (the channel will close naturally when the facade goroutine notices the closed context — but the handler doesn't need to wait; returning unwinds the request).
  - Check `<-r.Context().Done()` between frames; on cancellation, return `nil`.
- [ ] Return `nil` after the loop ends (channel closed normally — the `Done` frame was the last one written).
- [ ] Doc comment names the SSE framing rule: "Each frame is two lines + a blank line: `event: <type>\ndata: <payload>\n\n`. The blank line is the frame terminator per the SSE spec (RFC 6202 / WHATWG). Multi-line data fields are NOT used — each delta's content fits on one `data:` line because tokens have no newlines (or, if they do, the receiver must un-escape; deliberately deferred). For an OpenAI stream that includes newlines in deltas, the data is encoded as JSON before writing — see Open Question 6."

#### Acceptance Criteria — module + wiring

- [ ] `internal/chat/module.go` exports `Module struct { Facade *chat.Facade; Handlers *http.Handlers; Logger *slog.Logger }` + `NewModule(facade *chat.Facade, logger *slog.Logger) *Module`.
- [ ] `(*Module).Wire(r chi.Router)` mounts `r.Post("/chat", sharedhttp.Handle(m.Handlers.StreamChat))`.
- [ ] `internal/app/wiring.go` extended:
  - Load `OPENAI_API_KEY` and `OPENAI_MODEL` via viper (defaults: empty key + `gpt-4o-mini`).
  - If `apiKey != ""`, construct `gateway = chatgateway.NewOpenAIChatGateway(apiKey, model, logger)`. Else `gateway = chatgateway.NewInMemoryChatGateway()`.
  - Construct `chatFacade = chat.NewFacade(gateway, logger)`.
  - Construct `chatModule = chat.NewModule(chatFacade, logger)`.
  - `chatModule.Wire(r)` is called from `Wire` after the Phase-4 modules.
  - Register error types via the domain-error registry: `*chat.InvalidChatRequestError → 400 "invalid_chat_request"`, `*chat.ChatGatewayError → 502 "chat_gateway_error"`, `*chatgateway.EmptyMessagesError → 400 "empty_messages"`.
  - Extend `app.Wired` with `ChatFacade *chat.Facade` (so tests can hold a reference). No `Start`/`Stop` lifecycle (chat has no goroutine to manage at the module level — the per-request goroutines die with their requests).

#### Acceptance Criteria — slice-level hygiene

- [ ] `go build ./...` is green.
- [ ] `go vet ./...` clean.
- [ ] `task run` boots the server with the in-memory gateway when `OPENAI_API_KEY` is unset; `curl -N -X POST localhost:3000/chat -d '{"messages":[{"role":"user","content":"hello world"}]}'` returns a 200 SSE stream with three frames (`delta: "hello "`, `delta: "world "`, `done: ""`).
- [ ] No `init()` introduced.
- [ ] `internal/chat/http/` imports per BOUNDARIES.md: `internal/chat` + `internal/shared/http` + `github.com/go-chi/chi/v5` + stdlib.

---

### Slice 5: SSE handler tests (`httptest.NewServer` + real HTTP client)

Tests only — no new production code. Uses `httptest.NewServer` (NOT `httptest.NewRecorder`; the recorder doesn't satisfy `http.Flusher` correctly for streaming bodies).

#### Acceptance Criteria — test scaffolding

- [ ] `internal/chat/http/handlers_test.go` lives in package `http_test` (external test package — proves the handler is testable through its public surface).
- [ ] A `buildServer(t *testing.T, opts ...func(*serverOpts)) *httptest.Server` test helper constructs: the chat facade with the in-memory gateway; a chi router with the chat module wired; the registered domain errors mapped via `sharedhttp` middleware; an `httptest.NewServer(router)` returned to the caller. `t.Cleanup` calls `srv.Close()`.
- [ ] A `readSSEStream(t *testing.T, body io.Reader) []sseFrame` helper reads the streaming body line by line via `bufio.Scanner`, parses `event: <type>` / `data: <payload>` / blank-line-terminated frames, returns the parsed slice. `sseFrame struct { Event string; Data string }`.
- [ ] A `serverOpts struct { Gateway chatgateway.ChatGateway }` lets individual tests inject the in-memory gateway with custom `TokenInterval` or a `throwingChatGateway`.

#### Acceptance Criteria — happy path streaming

- [ ] AC (basic stream — three frames returned in order): build server with default in-memory gateway; `POST /chat` with body `{"messages":[{"role":"user","content":"hello world"}]}`; read the response body via `readSSEStream`; assert the slice equals `[{event: "delta", data: "hello "}, {event: "delta", data: "world "}, {event: "done", data: ""}]`.
- [ ] AC (SSE headers set correctly): inspect response headers; assert `Content-Type == "text/event-stream"`, `Cache-Control == "no-cache"`, `Connection == "keep-alive"`, `X-Accel-Buffering == "no"`.
- [ ] AC (HTTP status is 200): inspect response status code.
- [ ] AC (single-token message): body `{"messages":[{"role":"user","content":"hi"}]}`; frames equal `[{delta, "hi "}, {done, ""}]`.
- [ ] AC (assistant + user history): body `{"messages":[{"role":"user","content":"ignored"},{"role":"assistant","content":"also ignored"},{"role":"user","content":"echo me"}]}`; frames equal `[{delta, "echo "}, {delta, "me "}, {done, ""}]` (in-memory gateway uses only the last message).

#### Acceptance Criteria — error path framing

- [ ] AC (invalid JSON body): `POST /chat` with body `{not json`; assert HTTP 400 + JSON `{"error":"invalid_chat_request"}` (non-streaming response — the error happens before the stream opens).
- [ ] AC (empty messages array): body `{"messages":[]}`; HTTP 400 + `{"error":"invalid_chat_request"}`.
- [ ] AC (invalid role): body `{"messages":[{"role":"robot","content":"hi"}]}`; HTTP 400 + `{"error":"invalid_chat_request"}`.
- [ ] AC (gateway setup error → 502): inject a `throwingChatGateway` (unexported, in `handlers_test.go`) returning `nil, errors.New("simulated setup failure")`; `POST /chat` with valid body; HTTP 502 + `{"error":"chat_gateway_error"}`.
- [ ] AC (unknown JSON field rejected): body `{"messages":[...], "system_prompt":"ignored"}`; HTTP 400 + `{"error":"invalid_chat_request"}` (via `DisallowUnknownFields`).

#### Acceptance Criteria — concurrency + cancellation

- [ ] AC (client closes connection mid-stream): build server with `TokenInterval = 100 * time.Millisecond` and a 5-token message; start a `POST /chat` via a custom HTTP client with a deadline of 150 ms (so the deadline elapses after the first or second delta); send the request; assert the request fails with a deadline error AND the server-side handler completes (verified by `t.Cleanup` not hanging — implicit). Test runs in under 500 ms total.
- [ ] AC (concurrent requests stream independently): launch 5 goroutines each `POST /chat` with a distinct message; collect each goroutine's frames; assert each goroutine sees its OWN message's tokens (not another goroutine's). Sufficient evidence that the per-request goroutine isolation is correct. Run under `-race` with no race-detector violation.

#### Acceptance Criteria — slice-level hygiene

- [ ] `go test ./internal/chat/http/...` is green and under 1.0 second (the cancellation test adds ~200 ms; the concurrency test adds ~100 ms).
- [ ] `go test -race ./internal/chat/...` is green.
- [ ] No new production code in this slice — all changes live in `handlers_test.go`. Slice commit message: "Slice 5: SSE handler tests (no production-code changes)".

---

### Slice 6: Top-level docs — `README.md`, `ARCHITECTURE.md`, `SAGA.md`, `GUIDE.md`

Docs only — no Go code. Port the TS source's equivalents adjusting code samples + file paths + commands to Go.

#### Acceptance Criteria — `README.md`

- [ ] `README.md` at repo root (overwrites any Phase 1 placeholder).
- [ ] Sections in order: (1) one-line description and Why (port of Jakub Nabrdalik's improving-tdd-demo, principle-preserving); (2) Quick start (`task up`, `task migrate:apply`, `task run`, `task test`, `task test:integration`); (3) Project layout (one paragraph per top-level directory: `cmd/library/`, `internal/`, `migrations/`, `test/`, `docs/`, `.claude/`); (4) Module overview (one short paragraph each: access control, catalog, membership, lending, fines, categories, chat); (5) The architectural principles in one paragraph each (modular monolith / facade pattern / one tx per module / events as cross-module write path / in-memory test doubles); (6) What to read next (links to `ARCHITECTURE.md`, `SAGA.md`, `GUIDE.md`); (7) Tech stack table (Go 1.23+, chi, bun, atlas, slog, viper, testcontainers-go, podman-compose, go-openai); (8) License.
- [ ] All commands work as documented — verified by copy-pasting them in a clean clone.
- [ ] No "this is how it works in TS" phrasing. All file paths point to Go files.

#### Acceptance Criteria — `ARCHITECTURE.md`

- [ ] `ARCHITECTURE.md` at repo root.
- [ ] Sections in order: (1) Module = package = directory (with the layout from `.claude/BOUNDARIES.md`); (2) The facade pattern (Go style — `NewFacade(deps...) *Facade` + method-on-pointer-receiver API surface; explicit interface satisfaction guards); (3) The repository port + impl pattern (in-memory default, bun for production); (4) The HTTP DTO ↔ facade DTO boundary (HTTP shapes live in `<module>/http/dto.go` and never leak); (5) One transaction per module (anchored on `internal/shared/tx`); (6) Cross-module reads happen before tx opens (with a Phase-3 lending example snippet); (7) Post-commit side effects go OUTSIDE `tx.Run` (with the `MarkCopyUnavailable` example from `internal/lending/facade.go`); (8) Events as the only cross-module write path (with the `LoanReturned` → `AutoLoanOnReturnConsumer` example); (9) In-memory test doubles, no mocks (explanation of why + the spec-local Throwing-Once decorator pattern); (10) Manual constructor wiring (`internal/app/wiring.go` walk-through); (11) Boundaries enforcement (`.claude/BOUNDARIES.md` + the per-module import allowlist).
- [ ] Each code sample is real, runnable Go from the actual codebase (use file-path-and-line-range citations like `internal/lending/facade.go:42-67`).
- [ ] No "this would be a class in TypeScript" comments. All explanations stand on their own in Go terms.

#### Acceptance Criteria — `SAGA.md`

- [ ] `SAGA.md` at repo root.
- [ ] Sections in order: (1) What a saga is in this codebase (concise definition: a consumer that subscribes to an event and orchestrates a multi-step business workflow with its own tx boundaries); (2) The auto-loan-on-return saga (the canonical example — walk through `internal/lending/auto_loan_on_return.go` step by step: subscribe → claim → borrow → publish OR un-fulfil + fail); (3) The four canonical atomicity invariants (lifted from Phase 4 spec's "Saga atomicity invariants" section verbatim); (4) Per-aggregate serialisation (`sync.Mutex` map keyed by `BookId` — explain the race it prevents, the gap it doesn't close, and the DB-unique-constraint alternative); (5) Explicit `Start(ctx)` / `Stop(ctx)` lifecycle (the no-`init()` rule + composition-root orchestration); (6) Saga consumers swallow their own errors (rationale + the structured-slog logging convention); (7) The known gap — no durable outbox (Phase-3 carry-over); (8) How to add a new saga (file-level template pointing at `internal/lending/auto_loan_on_return.go` as the reference).
- [ ] The four atomicity invariants match the Phase-4 spec word-for-word (or close — minor editorial adjustments OK; semantic content is identical).
- [ ] Code samples cite real file paths and line ranges.

#### Acceptance Criteria — `GUIDE.md`

- [ ] `GUIDE.md` at repo root.
- [ ] Sections in order: (1) The module template — file-by-file walk-through with a checklist (`types.go`, `schema.go`, `repository.go`, `in_memory_repository.go`, `bun_repository.go`, `sample_data.go`, `configuration.go`, `facade.go`, `module.go`, `http/dto.go`, `http/mapping.go`, `http/handlers.go`, `facade_test.go`, `http/handlers_test.go`); (2) The migration template (file naming: `migrations/<NNNN>_<module>.sql`; the `atlas.sum` regeneration step); (3) The wiring step (`internal/app/wiring.go` — show the diff a new module introduces: facade construction, error registration, route mounting, `app.Wired` field); (4) The boundaries step (`.claude/BOUNDARIES.md` — add the new module's import allowlist row); (5) The test-and-ship checklist (matches the per-slice hygiene block from Phase-2/3/4 specs — `go build`, `go vet`, `go test`, no `init()`, etc.).
- [ ] The checklist in section (5) is copy-pasteable as a Markdown checklist (`- [ ]` items).
- [ ] Each section's code samples use the smallest illustrative module as a reference — recommended default: `internal/categories/` since it has zero cross-module dependencies and reads cleanest.

#### Acceptance Criteria — slice-level hygiene

- [ ] All four files are Markdown — `markdownlint` (if configured) is clean. No broken internal links — every `[text](path)` resolves to an existing file or anchor.
- [ ] All `task` commands in the docs work as documented — verified manually.
- [ ] All file-path citations resolve to real lines in real files. No phantom paths.
- [ ] `go build ./...` and `task test` still green (no Go code changed in this slice; just confirming the docs commit doesn't accidentally include a file change).

---

### Slice 7: `.claude/skills/improving-tdd-demo-go.md` Claude skill

Skill file teaching Claude Code the per-module template. Mirrors `GUIDE.md` but formatted for LLM consumption — imperative voice, condensed to under 200 lines.

#### Acceptance Criteria — skill file structure

- [ ] `.claude/skills/improving-tdd-demo-go.md` exists.
- [ ] Front-matter (YAML) at the top:
  - `name: improving-tdd-demo-go`
  - `description: This skill should be used when adding a new module to the test-n-design-go Go port of the improving-tdd-demo. Contains the per-module file template, the facade pattern, the wiring step, and the boundaries step. Use when the user says 'add a new module', 'port the X module from TS', 'follow the Go module template', or asks how to extend the codebase.`
- [ ] Body sections in order (each a short paragraph + a fenced code block where applicable): (1) When this skill applies; (2) The file template (a bullet list of the 14 files a new module ships, paths relative to `internal/<module>/`); (3) The facade pattern (one paragraph + a 10-line example from `internal/categories/facade.go`); (4) The repository pattern (one paragraph + a 6-line interface example); (5) The wiring step (a 15-line snippet showing the wiring.go additions); (6) The boundaries step (a 3-line `.claude/BOUNDARIES.md` example); (7) The test-and-ship checklist (copy of GUIDE.md's section 5).

#### Acceptance Criteria — skill content quality

- [ ] Skill body is under 200 lines.
- [ ] All code snippets are valid Go and reference real file paths in the repo.
- [ ] The skill loads in Claude Code (`/help` shows it in the available-skills list when this repo is the working directory).
- [ ] When a developer invokes `/bee:sdd "add a notifications module"`, the SDD workflow can read the skill, follow the template, and the resulting module conforms to the per-module convention without further prompting (verified manually with a dry-run during Slice 7 — the verification is a smoke test, not a formal AC).

#### Acceptance Criteria — slice-level hygiene

- [ ] The skill file is referenced from `README.md`'s "What to read next" section (or its own "Working with Claude Code" section) so a human reader can discover it.
- [ ] `go build ./...` and `task test` still green.

---

## File Map

| Slice | Files created (or significantly modified) |
| --- | --- |
| 1 | `internal/shared/chatgateway/gateway.go` (NEW), `internal/shared/chatgateway/in_memory_gateway.go` (NEW), `internal/shared/chatgateway/in_memory_gateway_test.go` (NEW) |
| 2 | `go.mod` + `go.sum` (modified: `github.com/sashabaranov/go-openai` added), `internal/shared/chatgateway/openai_gateway.go` (NEW) |
| 3 | `internal/chat/types.go` (NEW), `internal/chat/schema.go` (NEW), `internal/chat/sample_data.go` (NEW), `internal/chat/configuration.go` (NEW), `internal/chat/facade.go` (NEW), `internal/chat/facade_test.go` (NEW) |
| 4 | `internal/chat/http/dto.go` (NEW), `internal/chat/http/mapping.go` (NEW), `internal/chat/http/handlers.go` (NEW), `internal/chat/module.go` (NEW), `internal/app/wiring.go` (modified: chat facade construction + viper config load + error registry + `Wire` call + `app.Wired.ChatFacade`), `cmd/library/main.go` (modified: load `OPENAI_API_KEY` + `OPENAI_MODEL`), `.http/chat.http` (NEW) |
| 5 | `internal/chat/http/handlers_test.go` (NEW) — tests only, no production code |
| 6 | `README.md` (overwrites Phase-1 placeholder), `ARCHITECTURE.md` (NEW), `SAGA.md` (NEW), `GUIDE.md` (NEW) |
| 7 | `.claude/skills/improving-tdd-demo-go.md` (NEW), `README.md` (modified: link to the skill) |

No file is created in more than one slice. Slice 4 modifies two existing files (`wiring.go`, `main.go`) but doesn't conflict with any other slice's modifications. Slice 7's README modification is additive (one link added).

**Slice-ordering note**: Slices 1 → 2 are independent (the OpenAI gateway doesn't depend on the in-memory one; they share only the interface). Slices 3 → 4 → 5 are sequential (facade → handler → handler tests). Slice 6 (docs) can run in parallel with Slices 1–5 once the architectural shape is locked, but the declared order is sequential to ensure docs reflect what shipped. Slice 7 (skill) depends on Slice 6 (GUIDE.md is the source the skill condenses).

## Idiom Enforcement

Carried forward from Phase 1–4; no new Phase-5 conventions. Every slice must follow:

- Manual constructor wiring; no `wire`, no `fx`.
- HTTP DTOs live in `<module>/http/dto.go` and never escape.
- Stdlib testing only. No `testify`.
- Hand-written validation. No `go-playground/validator`.
- No mocks. Spec-local Throwing-Once decorators (unexported, in the test file). Phase 5 introduces `throwingChatGateway`.
- `log/slog` everywhere; every component takes a `*slog.Logger` constructor parameter.
- Functional options for sample data (`SampleChatRequest`).
- No `init()` for module wiring.
- Pointer-receiver errors: `*InvalidChatRequestError`, `*ChatGatewayError`, `*EmptyMessagesError`.
- Source-fidelity names. Match TS source 1:1. Slice 3 MUST cross-check `ChatMessage` / `ChatDelta` / `ChatFrame` / error names against the TS source and either match or record a justified deviation.
- `DisallowUnknownFields` on JSON decoders.
- Handlers return `error` → `sharedhttp.Handle` (pre-stream path); streaming path writes error frames inline and returns nil.
- Boundary enforcement: `internal/chat/` imports `internal/shared/chatgateway` + stdlib; `internal/chat/http/` imports `internal/chat` + `internal/shared/http` + chi + stdlib; `internal/shared/chatgateway/` imports stdlib + (Slice 2 only) `github.com/sashabaranov/go-openai`. No business-module imports.

## Definition of Done — Phase 5

Phase 5 is done when **all** of the following are true.

### Functional

- [ ] `task up && task migrate:apply && task run` boots with all five Phase-4 migrations applied. No new migrations in Phase 5.
- [ ] Phase 1/2/3/4 endpoints still work (regression).
- [ ] `curl -N -X POST localhost:3000/chat -d '{"messages":[{"role":"user","content":"hello world go"}]}'` returns a 200 SSE stream with three delta frames (`hello `, `world `, `go `) and one done frame in order.
- [ ] With `OPENAI_API_KEY` unset, the in-memory gateway is used (deterministic token-splits). With it set, the OpenAI gateway streams real LLM tokens. Manual verification: `OPENAI_API_KEY=sk-... task run` then `curl -N -X POST localhost:3000/chat -d '{"messages":[{"role":"user","content":"Tell me about The Hobbit in two sentences."}]}'` returns a coherent two-sentence response streamed token by token.
- [ ] `POST /chat` with empty messages / invalid role / malformed JSON each returns 400 + `{"error":"invalid_chat_request"}`.

### Streaming invariants

- [ ] SSE response headers are `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`, `X-Accel-Buffering: no`.
- [ ] Each frame ends with `\n\n`; each frame's `event:` line names the type (`delta` / `error` / `done`).
- [ ] The last frame is always `done` UNLESS the client cancelled (channel closes without a final `done`).
- [ ] Client disconnect closes the handler within one frame's latency — no goroutine leak.

### Test suite

- [ ] `task test` (unit, no build tags) is green and completes in well under 1.5 seconds (target: under 1.2 s). Includes: Phase 1/2/3/4 unit tests + chat-gateway tests (~6 scenarios) + chat-facade tests (~10 scenarios) + chat-handler tests (~12 scenarios).
- [ ] `task test -race` (unit with race detector) is green.
- [ ] `task test:integration` is green and completes in under 150 seconds. No new integration tests in Phase 5 (chat has no DB); the suite is identical to Phase 4's.

### Docs

- [ ] `README.md` exists and reads as a self-contained entry point. A reader who has never seen this repo can clone, follow Quick Start, and have a running server in under five minutes.
- [ ] `ARCHITECTURE.md` exists and is internally consistent — every code-sample file path resolves; every link to `SAGA.md` or `GUIDE.md` works.
- [ ] `SAGA.md` exists and the four canonical atomicity invariants match the Phase-4 spec.
- [ ] `GUIDE.md` exists and the file-template list matches what every existing module actually has on disk.
- [ ] None of the four docs contain phrases like "in TypeScript" or "as in NestJS" — the docs read as native Go documentation.

### Claude skill

- [ ] `.claude/skills/improving-tdd-demo-go.md` exists with the front-matter described in Slice 7.
- [ ] The skill loads in Claude Code (`/help` lists it).
- [ ] The skill body is under 200 lines.
- [ ] The skill is linked from `README.md`.

### Quality + hygiene

- [ ] `task fmt` and `task lint` pass with zero output.
- [ ] One new third-party direct dep added: `github.com/sashabaranov/go-openai` (or the chosen alternative per Open Question 1). No others.
- [ ] No `init()` function in any Phase-5 file.
- [ ] No file under `internal/chat/` imports a forbidden module per `.claude/BOUNDARIES.md` (chat is a leaf: forbidden imports are every other business module).
- [ ] No file under `internal/shared/chatgateway/` imports any business module.
- [ ] Every TS scenario in the chat module's `.spec.ts` files has a Go counterpart in `internal/chat/facade_test.go` or `internal/chat/http/handlers_test.go`. Verified by reading the TS files side by side during Slice 3 and Slice 5.
- [ ] `.claude/BOUNDARIES.md` reflects Phase 5's new modules: `shared/chatgateway → (stdlib + go-openai)`; `chat → shared/chatgateway, shared/http`.

## Open Questions

Defaults are aligned with discovery + source fidelity. Flag disagreements before slicing.

1. **OpenAI SDK.** Default: `github.com/sashabaranov/go-openai` (mature, well-documented streaming API). Alternative: `github.com/openai/openai-go` (official; newer, may shift API). Flag to switch — gateway impl differs by ~10 lines.
2. **Per-token gateway error: close channel vs. error frame on inner channel?** Default: close-channel-on-error; the facade emits a `done` frame regardless. Simpler, matches SSE convention. Flag to switch — extends `ChatDelta` with `Err error`.
3. **`accesscontrol.Authorize` on chat?** Default: no (matches TS). Flag to enable — requires adding a `chat.ask` policy row.
4. **Third-party SSE library?** Default: NONE. Stdlib `http.Flusher` + manual SSE framing is ~15 lines. Flag to switch — `tmaxmax/go-sse` is the most idiomatic Go option.
5. **Diff the TS chat module before Slice 3.** The TS chat module's `.spec.ts` scenarios are not enumerated in the discovery doc. Slice 3 MUST read the TS specs and either extend the facade-test ACs in-place OR record omissions explicitly. Locked.
6. **JSON-encode the `data:` payload?** Default: yes — `data: <json.Marshal(content)>\n\n`. Handles newlines / quotes / unicode trivially. Flag for raw text — cross-check TS source during Slice 4.
7. **`X-Accel-Buffering: no` header.** Default: include (harmless in dev, necessary behind nginx). Locked.
8. **Facade outer-channel buffer size.** Default: 8 (backpressure + non-blocking gateway gusts). Locked.
9. **`testing/synctest` for cancellation?** Default: no (experimental in 1.24; a reliable 100ms test is preferable). Locked.
10. **`SAGA.md` vs `ARCHITECTURE.md` overlap.** Default: SAGA.md is the saga-consumer deep-dive; ARCHITECTURE.md covers the event bus at a higher level; both link to each other. Mirrors TS source.

## Project completion criteria

Phase 5 is the final phase. The Go port is complete when all five phase specs' Definition-of-Done blocks are green. At that point:

1. **End-to-end runnable.** `task up && task migrate:apply && task run` boots a server with seven modules (access control, catalog, membership, lending, fines, categories, chat) on port 3000. Every TS-source HTTP endpoint has a Go counterpart with the right shape + status codes.
2. **Test suite green.** `task test` under 1.5 s; `task test:integration` under 150 s; `task test -race` green. Every TS facade-level scenario has a Go counterpart.
3. **Architectural invariants hold.** No cross-module joins; one tx per module; events as the only cross-module write path; no mocks (only in-memory doubles + Throwing-Once decorators).
4. **Docs read as a learnable artifact.** Four docs are internally consistent and reference real Go files. A reader who knows the TS source navigates without a map; a reader who doesn't can read the four in order and understand the architecture.
5. **Claude skill loads.** `/bee:sdd "add a notifications module"` produces a module conforming to the per-module convention without further prompting.
6. **No deferred-to-Phase-6 items.** Every "deferred to later phases" note in Phases 1–4 is either resolved here or marked "deferred indefinitely" in an Out of Scope block. Known gaps (durable outbox, distributed tracing, real auth, chat persistence, reservations unique constraint, mutex-map pruning) are documented with rationale.
7. **Clean shutdown.** `task down` stops the podman containers; no orphan processes.

When the seven items above are green, the Go port is done.

[ ] Reviewed
