-- +goose Up
ALTER TABLE groups
    ADD COLUMN leader_user_id BIGINT,
    ADD CONSTRAINT groups_leader_is_member
        FOREIGN KEY (id, leader_user_id) REFERENCES group_members (group_id, user_id);

ALTER TYPE mig_status ADD VALUE 'rolled_back';

ALTER TABLE migrations
    ADD COLUMN down_sql            TEXT,
    ADD COLUMN irreversible        BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN irreversible_reason TEXT    NOT NULL DEFAULT '',
    ADD COLUMN batch_table         TEXT,
    ADD COLUMN batch_column        TEXT,
    ADD COLUMN batch_size          INT,
    ADD CONSTRAINT migrations_batch_complete CHECK (
        (batch_table IS NULL AND batch_column IS NULL AND batch_size IS NULL)
        OR (batch_table IS NOT NULL AND batch_column IS NOT NULL AND batch_size > 0)
    );

UPDATE migrations
SET irreversible = TRUE, irreversible_reason = 'Dibuat pada Milestone 1 tanpa SQL down';

CREATE TYPE request_kind   AS ENUM ('apply', 'rollback', 'forward');
CREATE TYPE request_status AS ENUM (
    'pending_review', 'approved', 'rejected', 'queued', 'running', 'succeeded', 'failed', 'partial'
);

CREATE TABLE migration_requests (
    id           BIGSERIAL PRIMARY KEY,
    migration_id BIGINT         NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    database_id  BIGINT         NOT NULL REFERENCES database_connections(id) ON DELETE CASCADE,
    kind         request_kind   NOT NULL,
    revision     INT            NOT NULL,
    status       request_status NOT NULL DEFAULT 'pending_review',
    requested_by BIGINT         NOT NULL REFERENCES users(id),
    created_at   TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ    NOT NULL DEFAULT now()
);
CREATE INDEX ON migration_requests (database_id, created_at DESC);
CREATE UNIQUE INDEX migration_requests_one_open ON migration_requests (migration_id)
    WHERE status IN ('pending_review', 'approved', 'queued', 'running', 'partial');

CREATE TABLE migration_approvals (
    request_id BIGINT      NOT NULL REFERENCES migration_requests(id) ON DELETE CASCADE,
    group_id   BIGINT      NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    approved   BOOLEAN     NOT NULL,
    decided_by BIGINT      NOT NULL REFERENCES users(id),
    comment    TEXT        NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (request_id, group_id)
);

CREATE TYPE job_status AS ENUM ('pending', 'running', 'succeeded', 'failed', 'partial');

CREATE TABLE jobs (
    id                BIGSERIAL PRIMARY KEY,
    request_id        BIGINT      NOT NULL UNIQUE REFERENCES migration_requests(id) ON DELETE CASCADE,
    database_id       BIGINT      NOT NULL REFERENCES database_connections(id) ON DELETE CASCADE,
    status            job_status  NOT NULL DEFAULT 'pending',
    attempts          INT         NOT NULL DEFAULT 0,
    max_attempts      INT         NOT NULL DEFAULT 3,
    run_after         TIMESTAMPTZ NOT NULL DEFAULT now(),
    trace_context     JSONB       NOT NULL DEFAULT '{}',
    batches_committed INT         NOT NULL DEFAULT 0,
    rows_committed    BIGINT      NOT NULL DEFAULT 0,
    last_error        TEXT,
    created_by        BIGINT      NOT NULL REFERENCES users(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at        TIMESTAMPTZ,
    finished_at       TIMESTAMPTZ
);
CREATE INDEX ON jobs (run_after) WHERE status = 'pending';

ALTER TABLE migration_runs
    ADD COLUMN request_id     BIGINT REFERENCES migration_requests(id) ON DELETE SET NULL,
    ADD COLUMN job_id         BIGINT REFERENCES jobs(id) ON DELETE SET NULL,
    ADD COLUMN kind           request_kind NOT NULL DEFAULT 'apply',
    ADD COLUMN revision       INT          NOT NULL DEFAULT 1,
    ADD COLUMN attempt        INT          NOT NULL DEFAULT 1,
    ADD COLUMN reverts_run_id BIGINT REFERENCES migration_runs(id);

UPDATE migration_runs r
SET revision = m.revision
FROM migrations m
WHERE m.id = r.migration_id;

CREATE TABLE database_locks (
    database_id BIGINT PRIMARY KEY REFERENCES database_connections(id) ON DELETE CASCADE,
    job_id      BIGINT      NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    owner       TEXT        NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE database_locks;
ALTER TABLE migration_runs
    DROP COLUMN reverts_run_id,
    DROP COLUMN attempt,
    DROP COLUMN revision,
    DROP COLUMN kind,
    DROP COLUMN job_id,
    DROP COLUMN request_id;
DROP TABLE jobs;
DROP TABLE migration_approvals;
DROP TABLE migration_requests;
DROP TYPE job_status;
DROP TYPE request_status;
DROP TYPE request_kind;
ALTER TABLE migrations
    DROP COLUMN batch_size,
    DROP COLUMN batch_column,
    DROP COLUMN batch_table,
    DROP COLUMN irreversible_reason,
    DROP COLUMN irreversible,
    DROP COLUMN down_sql;
ALTER TABLE groups DROP COLUMN leader_user_id;
