-- name: CreateSnapshot :one
INSERT INTO schema_snapshots (database_id, taken_by, model)
VALUES ($1, $2, $3)
RETURNING id, database_id, migration_run_id, taken_by, taken_at, model;

-- name: ListSnapshots :many
SELECT id, database_id, migration_run_id, taken_by, taken_at, model
FROM schema_snapshots
WHERE database_id = $1
ORDER BY taken_at DESC, id DESC;

-- name: GetSnapshotByID :one
SELECT id, database_id, migration_run_id, taken_by, taken_at, model
FROM schema_snapshots
WHERE id = $1 AND database_id = $2;
