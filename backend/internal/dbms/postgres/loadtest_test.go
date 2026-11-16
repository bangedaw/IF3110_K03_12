package postgres_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
)

func TestLoadtestBatchMigration(t *testing.T) {
	dsn := os.Getenv("LOADTEST_DSN")
	if dsn == "" {
		t.Skip("set LOADTEST_DSN untuk mengukur migration per batch pada dataset loadtest")
	}
	size := 50000
	if v := os.Getenv("LOADTEST_BATCH"); v != "" {
		n, err := strconv.Atoi(v)
		require.NoError(t, err)
		size = n
	}
	ctx := context.Background()
	m := postgres.Migrator{Connector: postgres.DSNConnector{DSN: dsn}}
	runID := time.Now().UnixNano()

	start := time.Now()
	p, err := m.ApplyBatches(ctx, dbms.BatchApplyInput{
		SQL:        "UPDATE loadtest.transactions SET amount_cents = (amount * 100)::bigint, updated_at = now() WHERE id > $1 AND id <= $2",
		Batch:      dbms.BatchSpec{Schema: "loadtest", Table: "transactions", KeyColumn: "id", Size: size},
		DatabaseID: 1, MigrationID: 1, RunID: runID, Version: 1, Checksum: []byte("loadtest"),
	})
	require.NoError(t, err)
	up := time.Since(start)
	t.Logf("up   : batch=%d ukuran=%d baris=%d durasi=%s rata-rata/batch=%s",
		p.BatchesCommitted, size, p.RowsCommitted, up.Round(time.Millisecond), (up / time.Duration(max(p.BatchesCommitted, 1))).Round(time.Millisecond))

	start = time.Now()
	p, err = m.RevertBatches(ctx, dbms.BatchRevertInput{
		SQL:        "UPDATE loadtest.transactions SET amount_cents = NULL WHERE id > $1 AND id <= $2",
		DatabaseID: 1, RunID: runID, ByRunID: runID + 1,
	})
	require.NoError(t, err)
	down := time.Since(start)
	t.Logf("down : batch=%d baris=%d durasi=%s", p.BatchesReverted, p.RowsReverted, down.Round(time.Millisecond))
}
