-- 0003_lending.sql
--
-- Creates the lending module's two tables: `loans` and `reservations`.
-- Column shapes match the bun struct tags declared in
-- internal/lending/bun_loan_repository.go and
-- internal/lending/bun_reservation_repository.go and the TS source's schema
-- 1:1 (loan_id / reservation_id as the primary keys, snake_case columns).
--
-- NO foreign keys to members / books / copies — cross-module FKs are
-- forbidden by the architectural conviction (no cross-module DB joins per
-- .claude/BOUNDARIES.md). Cross-module consistency is enforced via events
-- and the post-commit publish pattern, not at the database level.

CREATE TABLE IF NOT EXISTS loans (
    loan_id     UUID PRIMARY KEY,
    member_id   UUID        NOT NULL,
    copy_id     UUID        NOT NULL,
    book_id     UUID        NOT NULL,
    borrowed_at TIMESTAMPTZ NOT NULL,
    due_date    TIMESTAMPTZ NOT NULL,
    returned_at TIMESTAMPTZ NULL
);

CREATE TABLE IF NOT EXISTS reservations (
    reservation_id UUID PRIMARY KEY,
    member_id      UUID        NOT NULL,
    book_id        UUID        NOT NULL,
    reserved_at    TIMESTAMPTZ NOT NULL,
    fulfilled_at   TIMESTAMPTZ NULL
);
