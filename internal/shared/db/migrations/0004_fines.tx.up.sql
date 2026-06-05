-- 0004_fines — fines + indexes. Ported from migrations/0004_fines.sql.

CREATE TABLE IF NOT EXISTS fines (
    fine_id      UUID PRIMARY KEY,
    member_id    UUID        NOT NULL,
    loan_id      UUID        NOT NULL,
    amount_cents BIGINT      NOT NULL,
    assessed_at  TIMESTAMPTZ NOT NULL,
    paid_at      TIMESTAMPTZ NULL
);

--bun:split

CREATE INDEX IF NOT EXISTS fines_member_id_idx ON fines (member_id);

--bun:split

CREATE INDEX IF NOT EXISTS fines_loan_id_idx ON fines (loan_id);
