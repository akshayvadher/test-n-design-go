-- 0005_categories.sql
--
-- Creates the categories module's single table.
-- Column shapes match the bun struct tags declared in
-- internal/categories/bun_repository.go (CategoryRow) and the TS source's
-- drizzle schema 1:1.
--
-- NO foreign keys to any other module's tables — cross-module FKs are
-- forbidden by the architectural conviction (no cross-module DB joins
-- per .claude/BOUNDARIES.md). Cross-module consistency, where it exists,
-- is enforced via events and synchronous facade reads.
--
-- The UNIQUE INDEX on LOWER(name) implements case-insensitive name
-- uniqueness at the database layer. The same rule is enforced by the
-- in-memory repository via a linear scan; both paths surface
-- *DuplicateCategoryError to the facade.

CREATE TABLE IF NOT EXISTS categories (
    category_id UUID        PRIMARY KEY,
    name        TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS categories_name_unique ON categories (LOWER(name));
