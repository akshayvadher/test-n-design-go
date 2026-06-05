-- 0003_lending — loans + reservations. Ported from migrations/0003_lending.sql.
-- No cross-module FKs (see .claude/BOUNDARIES.md).

CREATE TABLE IF NOT EXISTS loans (
    loan_id     UUID PRIMARY KEY,
    member_id   UUID        NOT NULL,
    copy_id     UUID        NOT NULL,
    book_id     UUID        NOT NULL,
    borrowed_at TIMESTAMPTZ NOT NULL,
    due_date    TIMESTAMPTZ NOT NULL,
    returned_at TIMESTAMPTZ NULL
);

--bun:split

CREATE TABLE IF NOT EXISTS reservations (
    reservation_id UUID PRIMARY KEY,
    member_id      UUID        NOT NULL,
    book_id        UUID        NOT NULL,
    reserved_at    TIMESTAMPTZ NOT NULL,
    fulfilled_at   TIMESTAMPTZ NULL
);
