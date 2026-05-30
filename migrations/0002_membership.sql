-- 0002_membership.sql
--
-- Creates the membership module's table: `members`. Column shapes match
-- the bun struct tags declared in internal/membership/bun_repository.go
-- and the TS source's schema 1:1 (member_id as the primary key,
-- snake_case columns, email uniqueness enforced at the database level).

CREATE TABLE IF NOT EXISTS members (
    member_id UUID PRIMARY KEY,
    name      TEXT NOT NULL,
    email     TEXT NOT NULL UNIQUE,
    tier      TEXT NOT NULL,
    status    TEXT NOT NULL
);
