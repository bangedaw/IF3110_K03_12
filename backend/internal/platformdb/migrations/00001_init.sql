-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TYPE access_level AS ENUM ('view', 'edit');
CREATE TYPE dbms_kind    AS ENUM ('postgres', 'mysql');
CREATE TYPE run_status   AS ENUM ('running', 'succeeded', 'failed');
CREATE TYPE mig_status   AS ENUM ('draft', 'applied');

CREATE TABLE users (
    id                  BIGSERIAL PRIMARY KEY,
    username            CITEXT      NOT NULL UNIQUE,
    display_name        TEXT        NOT NULL DEFAULT '',
    password_hash       TEXT        NOT NULL,
    is_platform_admin   BOOLEAN     NOT NULL DEFAULT FALSE,
    is_active           BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE session_families (
    id                  UUID        PRIMARY KEY,
    user_id             BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_rotated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    rotate_required     BOOLEAN     NOT NULL DEFAULT FALSE,
    revoked_at          TIMESTAMPTZ,
    user_agent          TEXT,
    ip                  INET
);
CREATE INDEX ON session_families (user_id);

CREATE TABLE session_tokens (
    token_hash          BYTEA       PRIMARY KEY,
    family_id           UUID        NOT NULL REFERENCES session_families(id) ON DELETE CASCADE,
    issued_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at       TIMESTAMPTZ
);
CREATE INDEX ON session_tokens (family_id);

CREATE TABLE groups (
    id          BIGSERIAL PRIMARY KEY,
    name        CITEXT      NOT NULL UNIQUE,
    description TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE group_members (
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id  BIGINT NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX ON group_members (user_id);

CREATE TABLE database_connections (
    id                  BIGSERIAL PRIMARY KEY,
    name                CITEXT      NOT NULL UNIQUE,
    kind                dbms_kind   NOT NULL,
    host                TEXT        NOT NULL,
    port                INT         NOT NULL,
    database_name       TEXT        NOT NULL,
    username            TEXT        NOT NULL,
    password_ciphertext BYTEA       NOT NULL,
    ssl_mode            TEXT        NOT NULL DEFAULT 'disable',
    is_active           BOOLEAN     NOT NULL DEFAULT TRUE,
    created_by          BIGINT      REFERENCES users(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE group_database_grants (
    group_id    BIGINT       NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    database_id BIGINT       NOT NULL REFERENCES database_connections(id) ON DELETE CASCADE,
    level       access_level NOT NULL,
    PRIMARY KEY (group_id, database_id)
);
CREATE INDEX ON group_database_grants (database_id);

CREATE TABLE migrations (
    id           BIGSERIAL PRIMARY KEY,
    database_id  BIGINT      NOT NULL REFERENCES database_connections(id) ON DELETE CASCADE,
    title        TEXT        NOT NULL,
    description  TEXT        NOT NULL DEFAULT '',
    sql_text     TEXT        NOT NULL,
    checksum     BYTEA       NOT NULL,
    status       mig_status  NOT NULL DEFAULT 'draft',
    version      INT,
    base_version INT         NOT NULL DEFAULT 0,
    revision     INT         NOT NULL DEFAULT 1,
    created_by   BIGINT      NOT NULL REFERENCES users(id),
    updated_by   BIGINT      NOT NULL REFERENCES users(id),
    applied_by   BIGINT      REFERENCES users(id),
    applied_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (database_id, version),
    CHECK ((status = 'applied') = (version IS NOT NULL))
);

CREATE TABLE migration_runs (
    id                     BIGSERIAL PRIMARY KEY,
    migration_id           BIGINT      NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    database_id            BIGINT      NOT NULL REFERENCES database_connections(id) ON DELETE CASCADE,
    sql_text               TEXT        NOT NULL,
    checksum               BYTEA       NOT NULL,
    executed_by            BIGINT      NOT NULL REFERENCES users(id),
    status                 run_status  NOT NULL,
    started_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at            TIMESTAMPTZ,
    error_code             TEXT,
    error_message          TEXT,
    failed_statement_index INT
);
CREATE INDEX ON migration_runs (database_id, started_at DESC);

CREATE TABLE schema_snapshots (
    id               BIGSERIAL PRIMARY KEY,
    database_id      BIGINT      NOT NULL REFERENCES database_connections(id) ON DELETE CASCADE,
    migration_run_id BIGINT      REFERENCES migration_runs(id) ON DELETE SET NULL,
    taken_by         BIGINT      REFERENCES users(id),
    taken_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    model            JSONB       NOT NULL
);

CREATE TABLE audit_logs (
    id            BIGSERIAL PRIMARY KEY,
    actor_user_id BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    action        TEXT        NOT NULL,
    resource_type TEXT        NOT NULL,
    resource_id   TEXT,
    outcome       TEXT        NOT NULL,
    metadata      JSONB       NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE audit_logs;
DROP TABLE schema_snapshots;
DROP TABLE migration_runs;
DROP TABLE migrations;
DROP TABLE group_database_grants;
DROP TABLE database_connections;
DROP TABLE group_members;
DROP TABLE groups;
DROP TABLE session_tokens;
DROP TABLE session_families;
DROP TABLE users;
DROP TYPE mig_status;
DROP TYPE run_status;
DROP TYPE dbms_kind;
DROP TYPE access_level;
