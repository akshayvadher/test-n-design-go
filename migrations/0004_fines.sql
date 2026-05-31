-- 0004_fines.sql
--
-- Creates the fines module's single table.
-- Column shapes match the bun struct tags declared in
-- internal/fines/bun_repository.go (FineRow) and the TS source's drizzle
-- schema 1:1.
--
-- NO foreign keys to members / loans — cross-module FKs are forbidden by
-- the architectural conviction (no cross-module DB joins per
-- .claude/BOUNDARIES.md). Cross-module consistency is enforced via events
-- and synchronous facade reads, not at the database level.

CREATE TABLE IF NOT EXISTS fines (
    fine_id      UUID PRIMARY KEY,
    member_id    UUID        NOT NULL,
    loan_id      UUID        NOT NULL,
    amount_cents BIGINT      NOT NULL,
    assessed_at  TIMESTAMPTZ NOT NULL,
    paid_at      TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS fines_member_id_idx ON fines (member_id);
CREATE INDEX IF NOT EXISTS fines_loan_id_idx ON fines (loan_id);
