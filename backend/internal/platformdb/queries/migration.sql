-- name: GetDatabaseKind :one
SELECT kind FROM database_connections WHERE id = @id;

-- name: MigrationHead :one
SELECT COALESCE(MAX(version), 0)::int AS head
FROM migrations
WHERE database_id = @database_id AND status = 'applied';

-- name: CreateMigrationDraft :one
INSERT INTO migrations (database_id, title, description, sql_text, checksum, base_version, created_by, updated_by)
VALUES (@database_id, @title, @description, @sql_text, @checksum, @base_version, @actor, @actor)
RETURNING *;

-- name: GetMigration :one
SELECT * FROM migrations WHERE id = @id AND database_id = @database_id;

-- name: ListMigrations :many
SELECT * FROM migrations
WHERE database_id = @database_id
  AND (sqlc.narg('status')::mig_status IS NULL OR status = sqlc.narg('status')::mig_status)
ORDER BY version DESC NULLS FIRST, id DESC
LIMIT @lim OFFSET @off;

-- name: CountMigrations :one
SELECT count(*) FROM migrations
WHERE database_id = @database_id
  AND (sqlc.narg('status')::mig_status IS NULL OR status = sqlc.narg('status')::mig_status);

-- name: UpdateMigrationDraft :one
UPDATE migrations
SET title       = @title,
    description = @description,
    sql_text    = @sql_text,
    checksum    = @checksum,
    updated_by  = @actor,
    revision    = revision + 1,
    updated_at  = now()
WHERE id = @id AND database_id = @database_id AND status = 'draft' AND revision = @revision
RETURNING *;

-- name: DeleteMigrationDraft :execrows
DELETE FROM migrations
WHERE id = @id AND database_id = @database_id AND status = 'draft' AND revision = @revision;

-- name: StartMigrationRun :one
INSERT INTO migration_runs (migration_id, database_id, sql_text, checksum, executed_by, status)
VALUES (@migration_id, @database_id, @sql_text, @checksum, @executed_by, 'running')
RETURNING id;

-- name: FinishMigrationRun :one
UPDATE migration_runs
SET status                 = @status,
    finished_at            = now(),
    error_code             = sqlc.narg('error_code'),
    error_message          = sqlc.narg('error_message'),
    failed_statement_index = sqlc.narg('failed_statement_index')
WHERE id = @id
RETURNING migration_id, executed_by;

-- name: MarkMigrationApplied :exec
UPDATE migrations
SET status     = 'applied',
    version    = @version,
    applied_by = @applied_by,
    applied_at = now(),
    updated_at = now()
WHERE id = @id;

-- name: GetMigrationRun :one
SELECT r.id, r.migration_id, r.database_id, r.sql_text, r.checksum, r.executed_by, r.status, r.started_at,
       r.finished_at, r.error_code, r.error_message, r.failed_statement_index,
       m.title, m.version, u.username AS executed_by_name
FROM migration_runs r
         JOIN migrations m ON m.id = r.migration_id
         JOIN users u ON u.id = r.executed_by
WHERE r.id = @id;

-- name: ListMigrationRuns :many
SELECT r.id, r.migration_id, r.database_id, r.sql_text, r.checksum, r.executed_by, r.status, r.started_at,
       r.finished_at, r.error_code, r.error_message, r.failed_statement_index,
       m.title, m.version, u.username AS executed_by_name
FROM migration_runs r
         JOIN migrations m ON m.id = r.migration_id
         JOIN users u ON u.id = r.executed_by
WHERE r.migration_id = @migration_id AND r.database_id = @database_id
ORDER BY r.started_at DESC, r.id DESC;

-- name: ListMigrationHistory :many
SELECT r.id, r.migration_id, r.database_id, r.sql_text, r.checksum, r.executed_by, r.status, r.started_at,
       r.finished_at, r.error_code, r.error_message, r.failed_statement_index,
       m.title, m.version, u.username AS executed_by_name
FROM migration_runs r
         JOIN migrations m ON m.id = r.migration_id
         JOIN users u ON u.id = r.executed_by
WHERE r.database_id = @database_id
ORDER BY r.started_at DESC, r.id DESC
LIMIT @lim OFFSET @off;

-- name: CountMigrationHistory :one
SELECT count(*) FROM migration_runs WHERE database_id = @database_id;

-- name: CreateRunSnapshot :exec
INSERT INTO schema_snapshots (database_id, migration_run_id, taken_by, model)
VALUES (@database_id, @migration_run_id, @taken_by, @model);

-- name: ListRunningMigrationRuns :many
SELECT r.id, r.migration_id, r.database_id, r.sql_text, r.checksum, r.executed_by, r.status, r.started_at,
       r.finished_at, r.error_code, r.error_message, r.failed_statement_index,
       m.title, m.version, u.username AS executed_by_name
FROM migration_runs r
         JOIN migrations m ON m.id = r.migration_id
         JOIN users u ON u.id = r.executed_by
WHERE r.status = 'running'
ORDER BY r.id;
