-- 0002_membership — members. Ported from migrations/0002_membership.sql.

CREATE TABLE IF NOT EXISTS members (
    member_id UUID PRIMARY KEY,
    name      TEXT NOT NULL,
    email     TEXT NOT NULL UNIQUE,
    tier      TEXT NOT NULL,
    status    TEXT NOT NULL
);
