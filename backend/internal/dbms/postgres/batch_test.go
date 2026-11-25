package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
)

const (
	upSQL   = "UPDATE app.items SET hits = hits + 1 WHERE id > $1 AND id <= $2"
	downSQL = "UPDATE app.items SET hits = hits - 1 WHERE id > $1 AND id <= $2"
)

func batchFixture(t *testing.T, rows int) (dbms.BatchMigrator, *pgxpool.Pool) {
	t.Helper()
	m, pool := migratorFixture(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE app.items (id bigint PRIMARY KEY, hits int NOT NULL DEFAULT 0, label text NOT NULL, grp int NOT NULL);
		CREATE INDEX items_grp_idx ON app.items (grp);`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO app.items (id, label, grp) SELECT g, 'item-' || g, g % 3 FROM generate_series(1, $1::int) g`, rows)
	require.NoError(t, err)
	bm, ok := m.(dbms.BatchMigrator)
	require.True(t, ok, "migrator PostgreSQL harus mendukung mode batch")
	return bm, pool
}

func batchInput(runID int64, size int) dbms.BatchApplyInput {
	return dbms.BatchApplyInput{
		SQL:        upSQL,
		Batch:      dbms.BatchSpec{Schema: "app", Table: "items", KeyColumn: "id", Size: size},
		DatabaseID: 1, MigrationID: 9, RunID: runID, Version: 1, Checksum: []byte("sum"),
		LockTimeout: 300 * time.Millisecond,
	}
}

func hitsBetween(t *testing.T, pool *pgxpool.Pool, from, to int64) map[int]int {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT hits, count(*) FROM app.items WHERE id BETWEEN $1 AND $2 GROUP BY hits`, from, to)
	require.NoError(t, err)
	out := map[int]int{}
	for rows.Next() {
		var hits, n int
		require.NoError(t, rows.Scan(&hits, &n))
		out[hits] = n
	}
	require.NoError(t, rows.Err())
	return out
}

func lockRow(t *testing.T, pool *pgxpool.Pool, id int64) func() {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT 1 FROM app.items WHERE id = $1 FOR UPDATE`, id)
	require.NoError(t, err)
	return func() { _ = tx.Rollback(ctx) }
}

func TestBatchAppliesEveryRowOnceAndReportsProgress(t *testing.T) {
	m, pool := batchFixture(t, 1050)
	var seen []dbms.BatchProgress

	in := batchInput(101, 100)
	in.OnBatch = func(p dbms.BatchProgress) { seen = append(seen, p) }
	p, err := m.ApplyBatches(context.Background(), in)

	require.NoError(t, err)
	assert.Equal(t, 11, p.BatchesCommitted)
	assert.EqualValues(t, 1050, p.RowsCommitted)
	assert.EqualValues(t, 1050, p.Cursor)
	assert.Equal(t, dbms.BatchCompleted, p.State())
	assert.Equal(t, map[int]int{1: 1050}, hitsBetween(t, pool, 1, 1050))
	require.Len(t, seen, 11)
	assert.Equal(t, 1, seen[0].BatchesCommitted)
	assert.EqualValues(t, 100, seen[0].RowsCommitted)
	assert.Equal(t, dbms.BatchPartial, seen[0].State())
	assert.EqualValues(t, 50, seen[10].RowsCommitted-seen[9].RowsCommitted)

	stored, err := m.BatchProgress(context.Background(), 101)
	require.NoError(t, err)
	assert.Equal(t, *p, *stored)
	_, found, err := m.(dbms.Migrator).CommittedVersion(context.Background(), 101)
	require.NoError(t, err)
	assert.True(t, found, "run batch yang selesai tercatat di riwayat target")

	again, err := m.ApplyBatches(context.Background(), batchInput(101, 100))
	require.NoError(t, err)
	assert.Equal(t, 11, again.BatchesCommitted)
	assert.Equal(t, map[int]int{1: 1050}, hitsBetween(t, pool, 1, 1050), "pesan duplikat tidak boleh menjalankan ulang batch")
}

func TestBatchFailureKeepsEarlierBatchesAndResumesFromCheckpoint(t *testing.T) {
	m, pool := batchFixture(t, 1050)
	unlock := lockRow(t, pool, 550)

	p, err := m.ApplyBatches(context.Background(), batchInput(102, 100))

	var se *dbms.StatementError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, 6, se.Batch)
	assert.Equal(t, "55P03", se.SQLState)
	assert.True(t, se.Retryable())
	assert.Equal(t, upSQL, se.SQL)
	assert.Contains(t, se.Error(), "batch 6:")
	assert.Equal(t, 5, p.BatchesCommitted)
	assert.EqualValues(t, 500, p.RowsCommitted)
	assert.Equal(t, dbms.BatchPartial, p.State())
	assert.Equal(t, map[int]int{1: 500}, hitsBetween(t, pool, 1, 500), "batch yang sudah commit tetap committed")
	assert.Equal(t, map[int]int{0: 550}, hitsBetween(t, pool, 501, 1050), "batch gagal dibatalkan seluruhnya")
	_, found, err := m.(dbms.Migrator).CommittedVersion(context.Background(), 102)
	require.NoError(t, err)
	assert.False(t, found, "run partial belum tercatat selesai")

	unlock()
	_, err = pool.Exec(context.Background(), `INSERT INTO app.items (id, label, grp) VALUES (5000, 'baru', 0)`)
	require.NoError(t, err)

	p, err = m.ApplyBatches(context.Background(), batchInput(102, 100))

	require.NoError(t, err)
	assert.Equal(t, 11, p.BatchesCommitted, "5 batch lama + 6 batch sisanya")
	assert.EqualValues(t, 1050, p.RowsCommitted)
	assert.Equal(t, dbms.BatchCompleted, p.State())
	assert.Equal(t, map[int]int{1: 1050}, hitsBetween(t, pool, 1, 1050), "tidak ada batch yang diulang")
	assert.Equal(t, map[int]int{0: 1}, hitsBetween(t, pool, 5000, 5000), "baris di luar cakupan awal tidak diproses")
}

func TestBatchRevertRunsInReverseOrderAndCanResume(t *testing.T) {
	m, pool := batchFixture(t, 1050)
	_, err := m.ApplyBatches(context.Background(), batchInput(103, 100))
	require.NoError(t, err)
	unlock := lockRow(t, pool, 550)

	revert := dbms.BatchRevertInput{SQL: downSQL, DatabaseID: 1, RunID: 103, ByRunID: 104, LockTimeout: 300 * time.Millisecond}
	p, err := m.RevertBatches(context.Background(), revert)

	var se *dbms.StatementError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, 6, se.Batch)
	assert.Equal(t, 5, p.BatchesReverted, "batch 11 sampai 7 dibatalkan lebih dulu")
	assert.EqualValues(t, 450, p.RowsReverted)
	assert.Equal(t, dbms.BatchPartiallyReverted, p.State())
	assert.Equal(t, map[int]int{0: 450}, hitsBetween(t, pool, 601, 1050))
	assert.Equal(t, map[int]int{1: 600}, hitsBetween(t, pool, 1, 600))

	_, err = m.ApplyBatches(context.Background(), batchInput(103, 100))
	assert.ErrorIs(t, err, dbms.ErrBatchRunReverted, "run yang sudah dibatalkan tidak boleh dilanjutkan ke depan")

	unlock()
	p, err = m.RevertBatches(context.Background(), revert)

	require.NoError(t, err)
	assert.Equal(t, 11, p.BatchesReverted)
	assert.EqualValues(t, 1050, p.RowsReverted)
	assert.Equal(t, dbms.BatchReverted, p.State())
	assert.Equal(t, map[int]int{0: 1050}, hitsBetween(t, pool, 1, 1050))
	var direction string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT direction FROM _dbmp_schema_history WHERE run_id = 104`).Scan(&direction))
	assert.Equal(t, "down", direction)

	var order []int
	rows, err := pool.Query(context.Background(), `SELECT batch_no FROM _dbmp_batch_checkpoint WHERE run_id = 103 AND direction = 'down' ORDER BY committed_at, batch_no DESC`)
	require.NoError(t, err)
	order, err = pgx.CollectRows(rows, pgx.RowTo[int])
	require.NoError(t, err)
	assert.Equal(t, []int{11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}, order)

	p, err = m.RevertBatches(context.Background(), revert)
	require.NoError(t, err)
	assert.Equal(t, dbms.BatchReverted, p.State())
	assert.Equal(t, 11, p.BatchesReverted)
	assert.Equal(t, map[int]int{0: 1050}, hitsBetween(t, pool, 1, 1050), "revert ulang tidak membatalkan dua kali")
}

func TestBatchRevertOfPartialRunOnlyTouchesCommittedBatches(t *testing.T) {
	m, pool := batchFixture(t, 1050)
	unlock := lockRow(t, pool, 350)
	_, err := m.ApplyBatches(context.Background(), batchInput(105, 100))
	require.Error(t, err)
	unlock()

	p, err := m.RevertBatches(context.Background(), dbms.BatchRevertInput{SQL: downSQL, DatabaseID: 1, RunID: 105, ByRunID: 106})

	require.NoError(t, err)
	assert.Equal(t, 3, p.BatchesReverted)
	assert.Equal(t, dbms.BatchReverted, p.State())
	assert.Equal(t, map[int]int{0: 1050}, hitsBetween(t, pool, 1, 1050), "baris yang tidak pernah diproses tidak ikut dikurangi")
}

func TestBatchRejectsInvalidInput(t *testing.T) {
	m, pool := batchFixture(t, 10)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `ALTER TABLE app.items ADD COLUMN code text UNIQUE`)
	require.NoError(t, err)

	cases := map[string]struct {
		mutate func(*dbms.BatchApplyInput)
		want   error
	}{
		"dua statement":    {func(in *dbms.BatchApplyInput) { in.SQL = upSQL + "; " + upSQL }, dbms.ErrBatchSQL},
		"tanpa $2":         {func(in *dbms.BatchApplyInput) { in.SQL = "UPDATE app.items SET hits = 1 WHERE id > $1" }, dbms.ErrBatchSQL},
		"kolom tidak unik": {func(in *dbms.BatchApplyInput) { in.Batch.KeyColumn = "grp" }, dbms.ErrBatchKey},
		"kolom teks":       {func(in *dbms.BatchApplyInput) { in.Batch.KeyColumn = "code" }, dbms.ErrBatchKey},
		"kolom tidak ada":  {func(in *dbms.BatchApplyInput) { in.Batch.KeyColumn = "nope" }, dbms.ErrBatchKey},
		"tabel tidak ada":  {func(in *dbms.BatchApplyInput) { in.Batch.Table = "nope" }, dbms.ErrBatchKey},
	}
	for name, tc := range cases {
		in := batchInput(200, 5)
		tc.mutate(&in)
		_, err := m.ApplyBatches(ctx, in)
		assert.ErrorIs(t, err, tc.want, name)
	}
	in := batchInput(200, 0)
	_, err = m.ApplyBatches(ctx, in)
	assert.Error(t, err, "ukuran batch 0")
	assert.Equal(t, map[int]int{0: 10}, hitsBetween(t, pool, 1, 10))

	_, err = m.ApplyBatches(ctx, batchInput(201, 5))
	require.NoError(t, err)
	mismatch := batchInput(201, 5)
	mismatch.Version = 2
	_, err = m.ApplyBatches(ctx, mismatch)
	assert.ErrorIs(t, err, dbms.ErrBatchScopeMismatch)

	_, err = m.RevertBatches(ctx, dbms.BatchRevertInput{SQL: "  ", DatabaseID: 1, RunID: 201, ByRunID: 202})
	assert.ErrorIs(t, err, dbms.ErrIrreversible)
	_, err = m.RevertBatches(ctx, dbms.BatchRevertInput{SQL: downSQL, DatabaseID: 1, RunID: 999, ByRunID: 202})
	assert.ErrorIs(t, err, dbms.ErrBatchRunUnknown)
	_, err = m.BatchProgress(ctx, 999)
	assert.ErrorIs(t, err, dbms.ErrBatchRunUnknown)
}

func TestBatchProgressBeforeAnyBatchRunIsUnknown(t *testing.T) {
	m, _ := batchFixture(t, 1)

	_, err := m.BatchProgress(context.Background(), 1)

	assert.ErrorIs(t, err, dbms.ErrBatchRunUnknown)
}

func TestBatchOnEmptyTableCompletesWithoutBatches(t *testing.T) {
	m, _ := batchFixture(t, 0)

	p, err := m.ApplyBatches(context.Background(), batchInput(300, 10))

	require.NoError(t, err)
	assert.Zero(t, p.BatchesCommitted)
	assert.Equal(t, dbms.BatchCompleted, p.State())
}

func TestBatchFailsFastWhenTargetLockIsHeld(t *testing.T) {
	m, pool := batchFixture(t, 10)
	ctx := context.Background()
	holder, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer holder.Release()
	_, err = holder.Exec(ctx, `SELECT pg_advisory_lock(1::bigint)`)
	require.NoError(t, err)
	defer func() { _, _ = holder.Exec(ctx, `SELECT pg_advisory_unlock(1::bigint)`) }()

	_, err = m.ApplyBatches(ctx, batchInput(400, 5))

	assert.ErrorIs(t, err, postgres.ErrLockBusy)
	assert.Equal(t, map[int]int{0: 10}, hitsBetween(t, pool, 1, 10))
}

func TestBatchStopsBetweenBatchesWhenContextIsCancelled(t *testing.T) {
	m, pool := batchFixture(t, 100)
	ctx, cancel := context.WithCancel(context.Background())
	in := batchInput(500, 10)
	in.OnBatch = func(p dbms.BatchProgress) {
		if p.BatchesCommitted == 3 {
			cancel()
		}
	}

	p, err := m.ApplyBatches(ctx, in)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 3, p.BatchesCommitted)
	assert.Equal(t, map[int]int{1: 30}, hitsBetween(t, pool, 1, 30))
	assert.Equal(t, map[int]int{0: 70}, hitsBetween(t, pool, 31, 100))

	p, err = m.ApplyBatches(context.Background(), batchInput(500, 10))
	require.NoError(t, err)
	assert.Equal(t, 10, p.BatchesCommitted)
	assert.Equal(t, map[int]int{1: 100}, hitsBetween(t, pool, 1, 100))
}
