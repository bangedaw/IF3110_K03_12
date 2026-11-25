-- name: GetDatabaseKind :one
SELECT kind FROM database_connections WHERE id = @id;

-- name: MigrationHead :one
SELECT COALESCE(MAX(version), 0)::int AS head
FROM migrations
WHERE database_id = @database_id AND status = 'applied';

-- name: CreateMigration :one
INSERT INTO migrations (database_id, title, description, sql_text, down_sql, checksum, irreversible, irreversible_reason,
                        batch_schema, batch_table, batch_column, batch_size, base_version, created_by, updated_by)
VALUES (@database_id, @title, @description, @sql_text, sqlc.narg('down_sql'), @checksum, @irreversible, @irreversible_reason,
        sqlc.narg('batch_schema'), sqlc.narg('batch_table'), sqlc.narg('batch_column'), sqlc.narg('batch_size'),
        @base_version, @actor, @actor)
RETURNING id;

-- name: UpdateMigration :exec
UPDATE migrations
SET title               = @title,
    description         = @description,
    sql_text            = @sql_text,
    down_sql            = sqlc.narg('down_sql'),
    checksum            = @checksum,
    irreversible        = @irreversible,
    irreversible_reason = @irreversible_reason,
    batch_schema        = sqlc.narg('batch_schema'),
    batch_table         = sqlc.narg('batch_table'),
    batch_column        = sqlc.narg('batch_column'),
    batch_size          = sqlc.narg('batch_size'),
    updated_by          = @actor,
    revision            = revision + 1,
    updated_at          = now()
WHERE id = @id;

-- name: DeleteMigration :exec
DELETE FROM migrations WHERE id = @id;

-- name: GetMigration :one
SELECT sqlc.embed(m),
       u.username AS created_by_name,
       EXISTS (SELECT 1 FROM migration_runs r WHERE r.migration_id = m.id) AS sql_locked
FROM migrations m
         JOIN users u ON u.id = m.created_by
WHERE m.id = @id;

-- name: ListMigrations :many
SELECT sqlc.embed(m),
       u.username AS created_by_name,
       EXISTS (SELECT 1 FROM migration_runs r WHERE r.migration_id = m.id) AS sql_locked
FROM migrations m
         JOIN users u ON u.id = m.created_by
WHERE m.database_id = @database_id
ORDER BY m.id DESC;

-- name: LockMigration :one
SELECT id FROM migrations WHERE id = @id FOR UPDATE;

-- name: ListDatabaseRequests :many
SELECT r.id, r.migration_id, r.kind, r.status, r.revision, r.created_at,
       u.username AS requested_by_name,
       j.id AS job_id,
       COALESCE(j.status::text, '')::text AS job_status
FROM migration_requests r
         JOIN users u ON u.id = r.requested_by
         LEFT JOIN jobs j ON j.request_id = r.id
WHERE r.database_id = @database_id
ORDER BY r.id;

-- name: CreateMigrationRequest :one
INSERT INTO migration_requests (migration_id, database_id, kind, revision, requested_by)
VALUES (@migration_id, @database_id, @kind, @revision, @requested_by)
RETURNING id;

-- name: LockMigrationRequest :one
SELECT * FROM migration_requests WHERE id = @id FOR UPDATE;

-- name: SetMigrationRequestStatus :exec
UPDATE migration_requests SET status = @status, updated_at = now() WHERE id = @id;

-- name: ClosePartialRequests :exec
UPDATE migration_requests
SET status = 'failed', updated_at = now()
WHERE migration_id = @migration_id AND status = 'partial';

-- name: ListRequestApprovals :many
SELECT g.id AS group_id, g.name AS group_name, g.leader_user_id,
       COALESCE(l.username::text, '')::text AS leader_name,
       a.approved,
       COALESCE(a.comment, '')::text AS comment,
       COALESCE(d.username::text, '')::text AS decided_by_name,
       a.decided_at
FROM group_database_grants gr
         JOIN groups g ON g.id = gr.group_id
         LEFT JOIN users l ON l.id = g.leader_user_id
         LEFT JOIN migration_approvals a ON a.group_id = g.id AND a.request_id = @request_id
         LEFT JOIN users d ON d.id = a.decided_by
WHERE gr.database_id = @database_id
ORDER BY g.name;

-- name: InsertApproval :exec
INSERT INTO migration_approvals (request_id, group_id, approved, decided_by, comment)
VALUES (@request_id, @group_id, @approved, @decided_by, @comment);

-- name: ListSucceededRuns :many
SELECT id, migration_id, kind
FROM migration_runs
WHERE database_id = @database_id AND status = 'succeeded'
ORDER BY id;

-- name: LastUpRun :one
SELECT r.id, COALESCE(r.checkpoint_run_id, r.id)::bigint AS checkpoint_run_id, r.version
FROM migration_runs r
         JOIN migrations m ON m.id = r.migration_id
WHERE r.migration_id = @migration_id
  AND r.kind IN ('apply', 'forward')
  AND (m.status <> 'applied' OR r.status = 'succeeded')
ORDER BY r.id DESC
LIMIT 1;

-- name: LastRequestRun :one
SELECT id, COALESCE(checkpoint_run_id, id)::bigint AS checkpoint_run_id, version
FROM migration_runs
WHERE request_id = @request_id
ORDER BY id DESC
LIMIT 1;

-- name: InsertRun :one
INSERT INTO migration_runs (migration_id, database_id, sql_text, checksum, executed_by, status, request_id, job_id,
                            kind, revision, attempt, version, reverts_run_id, checkpoint_run_id)
VALUES (@migration_id, @database_id, @sql_text, @checksum, @executed_by, 'running', @request_id, @job_id,
        @kind, @revision, @attempt, @version, sqlc.narg('reverts_run_id'), sqlc.narg('checkpoint_run_id'))
RETURNING *;

-- name: LockRun :one
SELECT * FROM migration_runs WHERE id = @id FOR UPDATE;

-- name: InterruptedRun :one
SELECT * FROM migration_runs
WHERE job_id = @job_id AND attempt = @attempt AND status = 'running'
ORDER BY id DESC
LIMIT 1;

-- name: GetJobProgress :one
SELECT batches_committed FROM jobs WHERE id = @id;

-- name: FinishRun :exec
UPDATE migration_runs
SET status                 = @status,
    finished_at            = now(),
    error_code             = sqlc.narg('error_code'),
    error_message          = sqlc.narg('error_message'),
    failed_statement_index = sqlc.narg('failed_statement_index')
WHERE id = @id;

-- name: MarkMigrationApplied :exec
UPDATE migrations
SET status     = 'applied',
    version    = @version,
    applied_by = @applied_by,
    applied_at = now(),
    updated_at = now()
WHERE id = @id;

-- name: MarkMigrationRolledBack :exec
UPDATE migrations
SET status     = 'rolled_back',
    version    = NULL,
    updated_at = now()
WHERE id = @id;

-- name: ListExecutions :many
SELECT sqlc.embed(r), m.title, u.username AS executed_by_name
FROM migration_runs r
         JOIN migrations m ON m.id = r.migration_id
         JOIN users u ON u.id = r.executed_by
WHERE r.database_id = @database_id
ORDER BY r.id DESC;

-- name: ListRunningMigrationRuns :many
SELECT id, migration_id, database_id, executed_by
FROM migration_runs
WHERE status = 'running' AND job_id IS NULL
ORDER BY id;
