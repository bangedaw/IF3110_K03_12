package queue_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

func seedRequest(t *testing.T, pool *pgxpool.Pool) (requestID, databaseID, userID int64) {
	t.Helper()
	ctx := context.Background()
	var migrationID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('u', 'x') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO database_connections (name, kind, host, port, database_name, username, password_ciphertext)
		VALUES ('d', 'postgres', 'h', 5432, 'd', 'u', '\x00') RETURNING id`).Scan(&databaseID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO migrations (database_id, title, sql_text, checksum, created_by, updated_by)
		VALUES ($1, 't', 'SELECT 1', '\x00', $2, $2) RETURNING id`, databaseID, userID).Scan(&migrationID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO migration_requests (migration_id, database_id, kind, revision, status, requested_by)
		VALUES ($1, $2, 'apply', 1, 'approved', $3) RETURNING id`, migrationID, databaseID, userID).Scan(&requestID))
	return requestID, databaseID, userID
}

type jobRow struct {
	status, traceparent string
	attempts            int
}

func readJob(t *testing.T, pool *pgxpool.Pool, id int64) jobRow {
	t.Helper()
	var r jobRow
	var tc []byte
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status::text, attempts, trace_context FROM jobs WHERE id = $1`, id).Scan(&r.status, &r.attempts, &tc))
	var m map[string]string
	require.NoError(t, json.Unmarshal(tc, &m))
	r.traceparent = m["traceparent"]
	return r
}

func TestPublishIsIdempotentAndRequeuesFinishedFailures(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	requestID, databaseID, userID := seedRequest(t, pool)
	q := queue.NewPostgres(pool)
	in := queue.PublishInput{RequestID: requestID, DatabaseID: databaseID, ActorID: userID}

	id, err := q.Publish(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, jobRow{status: "pending"}, readJob(t, pool, id))

	_, err = pool.Exec(ctx, `UPDATE jobs SET status = 'running', attempts = 1 WHERE id = $1`, id)
	require.NoError(t, err)
	again, err := q.Publish(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, id, again, "publish ulang memakai job yang sama")
	assert.Equal(t, "running", readJob(t, pool, id).status, "job yang sedang berjalan tidak diubah")

	_, err = pool.Exec(ctx, `UPDATE jobs SET status = 'partial', attempts = 3, last_error = 'x' WHERE id = $1`, id)
	require.NoError(t, err)
	again, err = q.Publish(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, id, again)
	assert.Equal(t, jobRow{status: "pending"}, readJob(t, pool, id), "job partial diantrekan ulang dengan jatah retry baru")

	var jobs int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&jobs))
	assert.Equal(t, 1, jobs)
}

func TestPublishStoresTraceContext(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	requestID, databaseID, userID := seedRequest(t, pool)
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x0a, 0xf7, 0x65, 0x19, 0x16, 0xcd, 0x43, 0xdd, 0x84, 0x48, 0xeb, 0x21, 0x1c, 0x80, 0x31, 0x9c},
		SpanID:     trace.SpanID{0xb7, 0xad, 0x6b, 0x71, 0x69, 0x20, 0x33, 0x31},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	id, err := queue.NewPostgres(pool).Publish(ctx, queue.PublishInput{RequestID: requestID, DatabaseID: databaseID, ActorID: userID})
	require.NoError(t, err)
	assert.Equal(t, "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", readJob(t, pool, id).traceparent)
}
