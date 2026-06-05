package db

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"

	dbmigrations "github.com/akshayvadher/test-n-design-go/internal/shared/db/migrations"
)

// RunBunMigrations applies every pending embedded migration in-process, using
// bun's own migrate package instead of shelling out to the atlas CLI. This is
// the prototype alternative to ApplyMigrations:
//
//   - No external binary. The migrations are compiled into this binary via
//     the embed.FS in internal/shared/db/migrations, so the distroless runtime
//     image can run them with no shell and no atlas image.
//   - Idempotent. bun records applied versions in the `bun_migrations` table
//     (created by Init) and only runs what is unapplied — safe to call on
//     every boot.
//   - Serialized across replicas. Lock/Unlock take a row lock in
//     `bun_migration_locks`, so concurrent pods don't double-apply. (Note: this
//     is a bun table lock, NOT a Postgres advisory lock like atlas uses.)
//
// The caller supplies an already-open *bun.DB (the same db.NewBunDB pool the
// server uses) so connection config lives in exactly one place.
func RunBunMigrations(ctx context.Context, bunDB *bun.DB, logger *slog.Logger) error {
	set := migrate.NewMigrations()
	if err := set.Discover(dbmigrations.FS); err != nil {
		return fmt.Errorf("discover embedded migrations: %w", err)
	}

	// WithMarkAppliedOnSuccess(true) is mandatory, not optional. Bun's DEFAULT
	// is to insert the bun_migrations row BEFORE running the migration — and
	// that insert commits on a separate connection from the .tx.up.sql's own
	// transaction. So a migration that fails and rolls back its DDL would still
	// be recorded as applied, and the next run would skip it forever, leaving
	// the schema permanently short a table. Marking applied only AFTER the Up
	// succeeds makes a failed migration retry on the next run.
	migrator := migrate.NewMigrator(bunDB, set, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		return fmt.Errorf("init bun migration tables: %w", err)
	}

	if err := migrator.Lock(ctx); err != nil {
		return fmt.Errorf("acquire bun migration lock: %w", err)
	}
	defer func() {
		if err := migrator.Unlock(ctx); err != nil {
			logger.Warn("bun migrate: unlock failed", slog.String("error", err.Error()))
		}
	}()

	group, err := migrator.Migrate(ctx)
	if err != nil {
		return fmt.Errorf("apply bun migrations: %w", err)
	}
	if group.IsZero() {
		logger.Info("bun migrate: schema already up to date, nothing to apply")
		return nil
	}
	logger.Info("bun migrate: applied",
		slog.String("group", group.String()),
		slog.Int("count", len(group.Migrations)),
	)
	return nil
}
