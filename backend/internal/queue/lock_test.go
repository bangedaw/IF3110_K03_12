package queue_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

const ttl = time.Minute

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql, args...)
	require.NoError(t, err)
}

func acquire(t *testing.T, q *queue.Postgres, databaseID, jobID int64, owner string) bool {
	t.Helper()
	ok, err := q.Acquire(context.Background(), databaseID, jobID, owner, ttl)
	require.NoError(t, err)
	return ok
}

func TestLockAllowsOneJobPerDatabase(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	databaseID, userID := seedDatabase(t, pool)
	q := queue.NewPostgres(pool)
	first := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))
	second := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))
	exec(t, pool, `UPDATE jobs SET status = 'running'`)

	assert.True(t, acquire(t, q, databaseID, first, "w1"))
	assert.False(t, acquire(t, q, databaseID, second, "w2"), "database sedang dipakai")
	assert.True(t, acquire(t, q, databaseID, first, "w1"), "pemegang lock boleh mengambil ulang")

	exec(t, pool, `UPDATE jobs SET status = 'succeeded' WHERE id = $1`, first)
	require.NoError(t, q.Release(context.Background(), databaseID, first, "w1"))
	assert.True(t, acquire(t, q, databaseID, second, "w2"), "lock bebas setelah job selesai dan dilepas")
}

func TestLockIsHeldWhileHolderIsStillRunningAfterLeaseExpires(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	databaseID, userID := seedDatabase(t, pool)
	q := queue.NewPostgres(pool)
	crashed := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))
	next := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))
	exec(t, pool, `UPDATE jobs SET status = 'running'`)
	require.True(t, acquire(t, q, databaseID, crashed, "w1"))
	exec(t, pool, `UPDATE database_locks SET expires_at = now() - interval '1 second'`)

	assert.False(t, acquire(t, q, databaseID, next, "w2"), "hasil job lama belum pasti, migration berikutnya ditahan")

	exec(t, pool, `UPDATE jobs SET status = 'failed' WHERE id = $1`, crashed)
	assert.True(t, acquire(t, q, databaseID, next, "w2"), "setelah job lama diselesaikan, lease kedaluwarsa boleh diambil")
}

func TestPartialMigrationBlocksOnlyOtherMigrations(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	databaseID, userID := seedDatabase(t, pool)
	q := queue.NewPostgres(pool)
	applyReq := newRequest(t, pool, databaseID, userID)
	partial := publish(t, q, databaseID, userID, applyReq)
	other := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))
	exec(t, pool, `UPDATE jobs SET status = 'running'`)
	require.True(t, acquire(t, q, databaseID, partial, "w1"))
	exec(t, pool, `UPDATE jobs SET status = 'partial' WHERE id = $1`, partial)
	require.NoError(t, q.Release(ctx, databaseID, partial, "w1"))

	assert.False(t, acquire(t, q, databaseID, other, "w2"), "migration lain menunggu migration partial diselesaikan")

	exec(t, pool, `UPDATE migration_requests SET status = 'failed' WHERE id = $1`, applyReq)
	var rollbackReq int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO migration_requests (migration_id, database_id, kind, revision, status, requested_by)
		SELECT migration_id, database_id, 'rollback', 1, 'approved', $2 FROM migration_requests WHERE id = $1 RETURNING id`, applyReq, userID).Scan(&rollbackReq))
	rollback := publish(t, q, databaseID, userID, rollbackReq)
	exec(t, pool, `UPDATE jobs SET status = 'running' WHERE id = $1`, rollback)
	assert.True(t, acquire(t, q, databaseID, rollback, "w2"), "rollback migration yang sama boleh jalan untuk menyelesaikan partial")
}

func TestRenewOnlyWorksForTheCurrentHolder(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	databaseID, userID := seedDatabase(t, pool)
	q := queue.NewPostgres(pool)
	job := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))
	require.True(t, acquire(t, q, databaseID, job, "w1"))

	ok, err := q.Renew(ctx, databaseID, job, "w1", ttl)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = q.Renew(ctx, databaseID, job, "w2", ttl)
	require.NoError(t, err)
	assert.False(t, ok, "worker lain tidak bisa memperpanjang lease")
}

func TestDeferPutsJobBackWithoutSpendingAnAttempt(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	databaseID, userID := seedDatabase(t, pool)
	q := queue.NewPostgres(pool)
	id := publish(t, q, databaseID, userID, newRequest(t, pool, databaseID, userID))
	job, err := q.Claim(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, job.Attempt)

	require.NoError(t, q.Defer(ctx, id, time.Hour))
	row := readJob(t, pool, id)
	assert.Equal(t, "pending", row.status)
	assert.Equal(t, 0, row.attempts)
	next, err := q.Claim(ctx)
	require.NoError(t, err)
	assert.Nil(t, next, "job ditunda belum bisa diambil sebelum jedanya lewat")
}
