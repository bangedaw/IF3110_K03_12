-- name: UpsertJob :one
INSERT INTO jobs (request_id, database_id, created_by, trace_context)
VALUES (@request_id, @database_id, @created_by, @trace_context)
ON CONFLICT (request_id) DO UPDATE SET request_id = EXCLUDED.request_id
RETURNING id;

-- name: RequeueJob :exec
UPDATE jobs
SET status        = 'pending',
    attempts      = 0,
    run_after     = now(),
    last_error    = NULL,
    finished_at   = NULL,
    trace_context = @trace_context
WHERE id = @id AND status IN ('failed', 'partial');

-- name: ClaimJob :one
UPDATE jobs
SET status     = 'running',
    attempts   = attempts + 1,
    started_at = COALESCE(started_at, now())
WHERE id = (
    SELECT id FROM jobs
    WHERE status = 'pending' AND run_after <= now()
    ORDER BY run_after, id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, request_id, database_id, created_by, attempts, max_attempts, trace_context;

-- name: SaveJobProgress :exec
UPDATE jobs
SET batches_committed = @batches_committed,
    rows_committed    = @rows_committed
WHERE id = @id;

-- name: CompleteJob :exec
UPDATE jobs
SET status      = @status,
    last_error  = sqlc.narg('last_error'),
    finished_at = now()
WHERE id = @id AND status = 'running';

-- name: GetJob :one
SELECT * FROM jobs WHERE id = @id AND database_id = @database_id;

-- name: DeferJob :exec
UPDATE jobs
SET status    = 'pending',
    attempts  = attempts - 1,
    run_after = now() + make_interval(secs => @delay_seconds::float8)
WHERE id = @id AND status = 'running';

-- name: RetryJob :exec
UPDATE jobs
SET status     = 'pending',
    run_after  = now() + make_interval(secs => @delay_seconds::float8),
    last_error = @last_error
WHERE id = @id AND status = 'running';
