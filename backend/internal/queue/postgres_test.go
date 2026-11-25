package queue_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

func seedDatabase(t *testing.T, pool *pgxpool.Pool) (databaseID, userID int64) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('u', 'x') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO database_connections (name, kind, host, port, database_name, username, password_ciphertext)
		VALUES ('d', 'postgres', 'h', 5432, 'd', 'u', '\x00') RETURNING id`).Scan(&databaseID))
	return databaseID, userID
}

func newRequest(t *testing.T, pool *pgxpool.Pool, databaseID, userID int64) int64 {
	t.Helper()
	ctx := context.Background()
	var migrationID, requestID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO migrations (database_id, title, sql_text, checksum, created_by, updated_by)
		VALUES ($1, 't', 'SELECT 1', '\x00', $2, $2) RETURNING id`, databaseID, userID).Scan(&migrationID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO migration_requests (migration_id, database_id, kind, revision, status, requested_by)
		VALUES ($1, $2, 'apply', 1, 'approved', $3) RETURNING id`, migrationID, databaseID, userID).Scan(&requestID))
	return requestID
}

func publish(t *testing.T, q *queue.Postgres, databaseID, userID, requestID int64) int64 {
	t.Helper()
	id, err := q.Publish(context.Background(), queue.PublishInput{RequestID: requestID, DatabaseID: databaseID, ActorID: userID})
	require.NoError(t, err)
	return id
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
	databaseID, userID := seedDatabase(t, pool)
	requestID := newRequest(t, pool, databaseID, userID)
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
	databaseID, userID := seedDatabase(t, pool)
	requestID := newRequest(t, pool, databaseID, userID)
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

func TestClaimTakesDueJobsOnceAndCountsAttempts(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	databaseID, userID := seedDatabase(t, pool)
	q := queue.NewPostgres(pool)

	job, err := q.Claim(ctx)
	require.NoError(t, err)
	assert.Nil(t, job, "antrean kosong")

	later := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))
	_, err = pool.Exec(ctx, `UPDATE jobs SET run_after = now() + interval '1 hour' WHERE id = $1`, later)
	require.NoError(t, err)
	due := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))

	job, err = q.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, job)
	assert.Equal(t, due, job.ID, "job yang run_after-nya belum lewat dilewati")
	assert.Equal(t, 1, job.Attempt)
	assert.Equal(t, 3, job.MaxAttempts)
	assert.Equal(t, "running", readJob(t, pool, due).status)

	job, err = q.Claim(ctx)
	require.NoError(t, err)
	assert.Nil(t, job, "job running tidak diambil lagi dan job later belum waktunya")

	_, err = pool.Exec(ctx, `UPDATE jobs SET status = 'pending' WHERE id = $1`, due)
	require.NoError(t, err)
	job, err = q.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, job)
	assert.Equal(t, 2, job.Attempt, "attempt bertambah setiap kali job diambil")
}

func TestClaimSkipsJobsLockedByAnotherWorker(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	databaseID, userID := seedDatabase(t, pool)
	q := queue.NewPostgres(pool)
	first := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))
	second := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	held, err := queue.NewPostgres(tx).Claim(ctx)
	require.NoError(t, err)
	require.Equal(t, first, held.ID)

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	other, err := q.Claim(waitCtx)
	require.NoError(t, err, "worker lain tidak menunggu baris yang terkunci")
	require.NotNil(t, other)
	assert.Equal(t, second, other.ID)
}

func TestConcurrentWorkersNeverClaimTheSameJob(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	databaseID, userID := seedDatabase(t, pool)
	q := queue.NewPostgres(pool)
	const jobs, workers = 20, 5
	for range jobs {
		publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))
	}

	var mu sync.Mutex
	claimed := map[int64]int{}
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				job, err := q.Claim(ctx)
				if err != nil || job == nil {
					assert.NoError(t, err)
					return
				}
				mu.Lock()
				claimed[job.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Len(t, claimed, jobs)
	for id, n := range claimed {
		assert.Equal(t, 1, n, "job %d diambil lebih dari sekali", id)
	}
}

func TestClaimedJobRestoresTraceContext(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	databaseID, userID := seedDatabase(t, pool)
	requestID := newRequest(t, pool, databaseID, userID)
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	})
	q := queue.NewPostgres(pool)
	_, err := q.Publish(trace.ContextWithSpanContext(context.Background(), sc), queue.PublishInput{RequestID: requestID, DatabaseID: databaseID, ActorID: userID})
	require.NoError(t, err)

	job, err := q.Claim(context.Background())
	require.NoError(t, err)
	restored := trace.SpanContextFromContext(job.Context(context.Background()))
	assert.Equal(t, sc.TraceID(), restored.TraceID())
	assert.Equal(t, sc.SpanID(), restored.SpanID())
	assert.True(t, restored.IsRemote(), "span asal dianggap berasal dari proses lain")
}

func TestProgressAndCompletionAreStoredOnTheJob(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	databaseID, userID := seedDatabase(t, pool)
	q := queue.NewPostgres(pool)
	id := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))
	_, err := q.Claim(ctx)
	require.NoError(t, err)

	require.NoError(t, q.SaveProgress(ctx, id, dbms.BatchProgress{BatchesCommitted: 5, RowsCommitted: 500, BatchesReverted: 2, RowsReverted: 200}))
	require.NoError(t, q.Complete(ctx, id, queue.JobPartial, "batch 6 gagal"))
	require.NoError(t, q.Complete(ctx, id, queue.JobSucceeded, ""), "job yang sudah selesai tidak diubah")

	var status, lastError string
	var batches int
	var rows int64
	var finished bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT status::text, last_error, batches_committed, rows_committed, finished_at IS NOT NULL FROM jobs WHERE id = $1`, id).
		Scan(&status, &lastError, &batches, &rows, &finished))
	assert.Equal(t, "partial", status)
	assert.Equal(t, "batch 6 gagal", lastError)
	assert.Equal(t, 3, batches, "batch yang masih ter-commit setelah dikurangi yang dibatalkan")
	assert.Equal(t, int64(300), rows)
	assert.True(t, finished)
}

func TestGetReturnsJobOnlyForItsDatabase(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	databaseID, userID := seedDatabase(t, pool)
	q := queue.NewPostgres(pool)
	id := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))

	job, err := q.Get(ctx, databaseID, id)
	require.NoError(t, err)
	assert.Equal(t, apigen.JobStatusPending, job.Status)
	assert.Equal(t, 3, job.MaxAttempts)
	assert.Nil(t, job.StartedAt)
	assert.Nil(t, job.LastError)
	assert.Equal(t, apigen.JobKindApply, job.Kind)
	require.NotNil(t, job.RunAfter, "job pending memberi tahu kapan diambil worker")

	_, err = q.Claim(ctx)
	require.NoError(t, err)
	job, err = q.Get(ctx, databaseID, id)
	require.NoError(t, err)
	assert.Nil(t, job.RunAfter, "runAfter hanya bermakna saat pending")

	_, err = q.Get(ctx, databaseID+1, id)
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}
