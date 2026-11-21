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
