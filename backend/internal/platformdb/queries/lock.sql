-- name: AcquireDatabaseLock :execrows
INSERT INTO database_locks (database_id, job_id, owner, expires_at)
VALUES (@database_id, @job_id, @owner, now() + make_interval(secs => @ttl_seconds::float8))
ON CONFLICT (database_id) DO UPDATE
SET job_id     = EXCLUDED.job_id,
    owner      = EXCLUDED.owner,
    expires_at = EXCLUDED.expires_at
WHERE database_locks.job_id = EXCLUDED.job_id
   OR (database_locks.expires_at < now() AND NOT EXISTS (
        SELECT 1
        FROM jobs holder
        JOIN migration_requests holder_req ON holder_req.id = holder.request_id
        JOIN jobs me ON me.id = EXCLUDED.job_id
        JOIN migration_requests me_req ON me_req.id = me.request_id
        WHERE holder.id = database_locks.job_id
          AND (holder.status IN ('pending', 'running')
               OR (holder.status = 'partial' AND holder_req.migration_id <> me_req.migration_id))));

-- name: RenewDatabaseLock :execrows
UPDATE database_locks
SET expires_at = now() + make_interval(secs => @ttl_seconds::float8)
WHERE database_id = @database_id AND job_id = @job_id AND owner = @owner;

-- name: ReleaseDatabaseLock :exec
UPDATE database_locks
SET expires_at = now()
WHERE database_id = @database_id AND job_id = @job_id AND owner = @owner;
