package bun

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	upstreambun "github.com/uptrace/bun"

	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	"github.com/akshayvadher/test-n-design-go/internal/shared/tx"
)

// TransactionalContext is the bun-backed substrate for the
// tx.TransactionalContext interface. It wraps bun.DB.RunInTx so staged
// closures execute IMMEDIATELY inside the tx callback against the live
// tx handle, and staged events publish AFTER the underlying transaction
// commits.
//
// Semantics matrix:
//
//   - work returns error: bun rolls back; staged events are discarded;
//     Run returns the wrapped work error.
//   - Stage closure returns error during the tx: the closure error is
//     captured into stageErr; Run's tx callback returns it after work
//     finishes, causing bun to roll back; staged events are discarded;
//     Run returns the captured error directly (unwrapped, so errors.Is
//     works the same way the in-memory impl returns stage errors).
//   - work + every staged closure succeed: bun commits; staged events
//     publish in stage order; an individual publish error is logged and
//     skipped; Run returns nil.
//
// Stage closures receive a context.Context carrying the live bun.Tx so
// repositories can resolve the active handle via TxFromContext. Calls
// to Stage made OUTSIDE Run (a programming error) execute the closure
// against context.Background — defensive but undefined; the documented
// contract is "stage only inside Run".
//
// The contract is single-shot and single-goroutine: construct a fresh
// instance per business operation via tx.TransactionalContextFactory.
// Concurrent reuse of the same instance across goroutines is NOT
// supported.
type TransactionalContext struct {
	db       *upstreambun.DB
	bus      events.EventBus
	logger   *slog.Logger
	events   []events.DomainEvent
	txCtx    context.Context
	stageErr error
}

// Compile-time assertion that *TransactionalContext satisfies the
// tx.TransactionalContext port.
var _ tx.TransactionalContext = (*TransactionalContext)(nil)

// NewTransactionalContext returns a fresh *TransactionalContext bound
// to db. The bus receives staged events after a successful commit; the
// logger receives error-level records when an individual post-commit
// publish fails (bus failures do not roll back the commit).
func NewTransactionalContext(db *upstreambun.DB, bus events.EventBus, logger *slog.Logger) *TransactionalContext {
	return &TransactionalContext{
		db:     db,
		bus:    bus,
		logger: logger,
	}
}

// Stage executes apply immediately against the current tx-scoped
// context if one is active (i.e. inside Run). The first error any stage
// closure returns is captured into stageErr; subsequent Stage calls
// within the same Run are no-ops so a later closure cannot mask an
// earlier failure. Run propagates the captured error back to bun, which
// rolls the tx back.
//
// Calling Stage outside Run is a programming error. The defensive
// fallback runs the closure against context.Background so the call does
// not panic; the resulting error (if any) is captured into stageErr but
// is never surfaced because Run will not be called.
func (c *TransactionalContext) Stage(apply func(ctx context.Context) error) {
	if c.stageErr != nil {
		return
	}
	ctx := c.txCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := apply(ctx); err != nil {
		c.stageErr = err
	}
}

// StageEvent appends evt to the staged-event buffer. Events publish
// after the bun tx commits, in stage order, via the bus the context was
// constructed with.
func (c *TransactionalContext) StageEvent(evt events.DomainEvent) {
	c.events = append(c.events, evt)
}

// Run wraps db.RunInTx and orchestrates the tx -> commit -> publish
// sequence. On a successful commit the staged events publish in stage
// order; bus failures during the publish loop are logged and skipped.
// On any failure (work error or captured stage error) the buffered
// events are discarded and Run returns the wrapped work error or the
// unwrapped stage error.
func (c *TransactionalContext) Run(ctx context.Context, work func(ctx context.Context) error) error {
	txErr := c.db.RunInTx(ctx, &sql.TxOptions{}, func(txCtx context.Context, tx upstreambun.Tx) error {
		c.txCtx = contextWithTx(txCtx, tx)
		defer func() { c.txCtx = nil }()
		return c.runWork(work)
	})
	if txErr != nil {
		c.events = nil
		return c.wrapTxError(txErr)
	}
	c.publishStaged(ctx)
	return nil
}

// runWork executes work and, on success, surfaces any captured stage
// error so bun rolls back. The work error is wrapped to match the
// in-memory impl; the stage error is returned unwrapped so errors.Is
// comparisons in the caller succeed.
func (c *TransactionalContext) runWork(work func(ctx context.Context) error) error {
	if err := work(c.txCtx); err != nil {
		return fmt.Errorf("tx work: %w", err)
	}
	return c.stageErr
}

// wrapTxError preserves the wrapped "tx work" envelope when the work
// failed (already wrapped inside runWork) and returns the captured
// stage error unwrapped when a stage closure caused the rollback. Any
// other bun-side error surfaces as-is.
func (c *TransactionalContext) wrapTxError(txErr error) error {
	if c.stageErr != nil && txErr == c.stageErr {
		return c.stageErr
	}
	return txErr
}

// publishStaged publishes every staged event in order. A bus failure
// for one event is logged at error level but does not abort the loop or
// fail Run — the commit has already happened and the bus is
// fire-and-forget at the publisher boundary.
func (c *TransactionalContext) publishStaged(ctx context.Context) {
	for index, evt := range c.events {
		if err := c.bus.Publish(ctx, evt); err != nil {
			c.logger.Error("staged event publish failed",
				slog.String("event_type", evt.Type()),
				slog.Int("event_index", index),
				slog.String("error", err.Error()),
			)
		}
	}
}

// txKey is the package-private context key that carries the live bun
// tx handle. The unexported type prevents collisions with any other ctx
// key in the codebase.
type txKey struct{}

// contextWithTx returns a child of parent carrying the live bun.Tx so
// repositories that ask TxFromContext for the handle can find it. The
// tx is stored as bun.IDB so callers cannot widen the assertion to
// *sql.Tx or peek at concrete-only methods on bun.Tx.
func contextWithTx(parent context.Context, tx upstreambun.Tx) context.Context {
	return context.WithValue(parent, txKey{}, upstreambun.IDB(tx))
}

// TxFromContext resolves the active bun handle from ctx. Repositories
// use this to participate in the current tx: inside a Run-scoped
// callback the returned handle is the live tx; outside any Run the
// returned handle is the zero IDB and active is false (callers fall
// back to their base *bun.DB). Always returns a usable bool for callers
// that want to assert tx-presence; the bun repos in Slice 3 use it
// informationally.
func TxFromContext(ctx context.Context) (upstreambun.IDB, bool) {
	handle, ok := ctx.Value(txKey{}).(upstreambun.IDB)
	return handle, ok
}
