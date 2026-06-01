-- 0005_categories — categories + case-insensitive unique name.
-- Ported from migrations/0005_categories.sql.

CREATE TABLE IF NOT EXISTS categories (
    category_id UUID        PRIMARY KEY,
    name        TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL
);

--bun:split

CREATE UNIQUE INDEX IF NOT EXISTS categories_name_unique ON categories (LOWER(name));
