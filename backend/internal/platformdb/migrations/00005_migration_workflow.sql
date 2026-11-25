-- +goose Up
-- Skema workflow migration M2 (internal/migration): status, urutan linear,
-- approval per leader, dan riwayat eksekusi. Menggantikan migration_approvals
-- dari 00002 yang terikat ke migration_requests dan belum dipakai kode.

ALTER TYPE mig_status ADD VALUE IF NOT EXISTS 'pending_review';
ALTER TYPE mig_status ADD VALUE IF NOT EXISTS 'approved';
ALTER TYPE mig_status ADD VALUE IF NOT EXISTS 'pending_queue';
ALTER TYPE mig_status ADD VALUE IF NOT EXISTS 'running';
ALTER TYPE mig_status ADD VALUE IF NOT EXISTS 'success';
ALTER TYPE mig_status ADD VALUE IF NOT EXISTS 'failed';

ALTER TABLE migrations
    ADD COLUMN seq        INT,
    ADD COLUMN up_sql     TEXT,
    ADD COLUMN sql_locked BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE migrations m
SET seq = o.n, up_sql = m.sql_text, sql_locked = (m.status = 'applied')
FROM (SELECT id, row_number() OVER (PARTITION BY database_id ORDER BY id) AS n FROM migrations) o
WHERE o.id = m.id;

ALTER TABLE migrations
    ALTER COLUMN seq SET NOT NULL,
    ALTER COLUMN up_sql SET NOT NULL,
    ADD CONSTRAINT migrations_seq_unique UNIQUE (database_id, seq);

DROP TABLE migration_approvals;
CREATE TABLE migration_approvals (
    migration_id BIGINT       NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    revision     INT          NOT NULL,
    kind         request_kind NOT NULL,
    leader_id    BIGINT       NOT NULL REFERENCES users(id),
    group_id     BIGINT       NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    decision     TEXT         NOT NULL DEFAULT 'pending' CHECK (decision IN ('pending', 'approved', 'rejected')),
    comment      TEXT         NOT NULL DEFAULT '',
    decided_at   TIMESTAMPTZ,
    PRIMARY KEY (migration_id, revision, kind, leader_id)
);

-- Satu baris per attempt (berhasil dan gagal). Baris yang sudah final tidak
-- boleh diubah.
CREATE TABLE migration_executions (
    id                   BIGSERIAL PRIMARY KEY,
    migration_id         BIGINT       NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    revision             INT          NOT NULL,
    user_id              BIGINT       NOT NULL REFERENCES users(id),
    kind                 request_kind NOT NULL,
    status               TEXT         NOT NULL CHECK (status IN ('running', 'success', 'failed')),
    executed_sql         TEXT         NOT NULL DEFAULT '',
    reverts_execution_id BIGINT       REFERENCES migration_executions(id),
    error_msg            TEXT         NOT NULL DEFAULT '',
    sql_state            TEXT         NOT NULL DEFAULT '',
    started_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
    finished_at          TIMESTAMPTZ
);
CREATE INDEX ON migration_executions (migration_id, started_at);

-- +goose StatementBegin
CREATE FUNCTION migration_executions_immutable() RETURNS trigger AS $$
BEGIN
    IF OLD.status <> 'running' THEN
        RAISE EXCEPTION 'riwayat eksekusi % sudah final dan tidak boleh diubah', OLD.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER migration_executions_immutable BEFORE UPDATE ON migration_executions
    FOR EACH ROW EXECUTE FUNCTION migration_executions_immutable();

-- +goose Down
DROP TABLE migration_executions;
DROP FUNCTION migration_executions_immutable();
DROP TABLE migration_approvals;
CREATE TABLE migration_approvals (
    request_id BIGINT      NOT NULL REFERENCES migration_requests(id) ON DELETE CASCADE,
    group_id   BIGINT      NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    approved   BOOLEAN     NOT NULL,
    decided_by BIGINT      NOT NULL REFERENCES users(id),
    comment    TEXT        NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (request_id, group_id)
);
ALTER TABLE migrations
    DROP CONSTRAINT migrations_seq_unique,
    DROP COLUMN sql_locked,
    DROP COLUMN up_sql,
    DROP COLUMN seq;
