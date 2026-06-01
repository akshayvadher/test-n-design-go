-- 0001_catalog — books + copies.
-- Ported verbatim from the atlas migration migrations/0001_catalog.sql.
-- `.tx.up.sql` makes bun run the whole file in one transaction; `--bun:split`
-- separates the statements so each runs as its own Exec on the pgdriver.

CREATE TABLE IF NOT EXISTS books (
    book_id UUID PRIMARY KEY,
    title   TEXT        NOT NULL,
    authors TEXT[]      NOT NULL,
    isbn    TEXT        NOT NULL UNIQUE
);

--bun:split

CREATE TABLE IF NOT EXISTS copies (
    copy_id   UUID PRIMARY KEY,
    book_id   UUID NOT NULL REFERENCES books(book_id),
    condition TEXT NOT NULL,
    status    TEXT NOT NULL
);
