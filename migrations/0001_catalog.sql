-- 0001_catalog.sql
--
-- Creates the catalog module's two tables: `books` and `copies`. Column
-- shapes match the bun struct tags declared in internal/catalog/bun_repository.go
-- and the TS source's schema 1:1 (book_id / copy_id as the primary keys,
-- snake_case columns, isbn uniqueness enforced at the database level).
--
-- The copies.book_id foreign key keeps cascade behaviour OFF: the catalog
-- facade does NOT pre-delete copies in Phase 2 (the TS source does not
-- either; cascade behaviour is not covered by catalog.facade.spec.ts).

CREATE TABLE IF NOT EXISTS books (
    book_id UUID PRIMARY KEY,
    title   TEXT        NOT NULL,
    authors TEXT[]      NOT NULL,
    isbn    TEXT        NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS copies (
    copy_id   UUID PRIMARY KEY,
    book_id   UUID NOT NULL REFERENCES books(book_id),
    condition TEXT NOT NULL,
    status    TEXT NOT NULL
);
