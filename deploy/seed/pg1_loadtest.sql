\set ON_ERROR_STOP on
\if :{?rows}
\else
\set rows 2000000
\endif

DROP SCHEMA IF EXISTS loadtest CASCADE;
CREATE SCHEMA loadtest;

CREATE TABLE loadtest.transactions (
    id           BIGINT         PRIMARY KEY,
    account_id   INT            NOT NULL,
    amount       NUMERIC(12, 2) NOT NULL,
    amount_cents BIGINT,
    status       TEXT           NOT NULL,
    note         TEXT           NOT NULL,
    created_at   TIMESTAMPTZ    NOT NULL,
    updated_at   TIMESTAMPTZ    NOT NULL
);

INSERT INTO loadtest.transactions (id, account_id, amount, status, note, created_at, updated_at)
SELECT g,
       1 + (g * 7919) % 50000,
       round(((g * 104729) % 10000000) / 100.0, 2),
       (ARRAY['pending', 'settled', 'refunded', 'failed'])[1 + g % 4],
       'trx-' || lpad(g::text, 10, '0') || ' ' || md5(g::text),
       timestamptz '2026-01-01' + (g % 31536000) * interval '1 second',
       timestamptz '2026-01-01' + (g % 31536000) * interval '1 second'
FROM generate_series(1::bigint, :rows) AS g;

CREATE INDEX transactions_account_idx ON loadtest.transactions (account_id);
CREATE INDEX transactions_status_created_idx ON loadtest.transactions (status, created_at);

VACUUM ANALYZE loadtest.transactions;

SELECT count(*) AS rows, pg_size_pretty(pg_total_relation_size('loadtest.transactions')) AS total_size
FROM loadtest.transactions;
