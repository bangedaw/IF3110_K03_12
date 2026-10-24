package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

type statementIndexer interface{ StatementIndex() int }

func migratorFixture(t *testing.T) (dbms.Migrator, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.NewPostgresTarget(t)
	t.Cleanup(pool.Close)
	cc := pool.Config().ConnConfig
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := postgres.Driver{}.Open(ctx, dbms.ConnConfig{
		Kind: dbms.KindPostgres, Host: cc.Host, Port: int(cc.Port),
		Database: cc.Database, User: cc.User, Password: cc.Password, SSLMode: "disable",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn.Migrator(), pool
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists))
	return exists
}

func apply(t *testing.T, m dbms.Migrator, databaseID, runID int64, sql string) (*dbms.ApplyResult, error) {
	t.Helper()
	return m.Apply(context.Background(), dbms.ApplyInput{
		SQL: sql, DatabaseID: databaseID, MigrationID: runID, RunID: runID, Version: int(runID), Checksum: []byte("sum"),
	})
}

func TestMigratorReportsTransactionalDDL(t *testing.T) {
	m, _ := migratorFixture(t)

	assert.True(t, m.SupportsTransactionalDDL())
}

func TestMigratorCommitsAndRecordsHistoryAtomically(t *testing.T) {
	m, pool := migratorFixture(t)

	res, err := apply(t, m, 1, 21, "CREATE TABLE app.ledger (id int PRIMARY KEY); INSERT INTO app.ledger VALUES (1);")

	require.NoError(t, err)
	assert.Equal(t, 2, res.StatementsExecuted)
	assert.True(t, tableExists(t, pool, "app.ledger"))
	var version int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT version FROM _dbmp_schema_history WHERE run_id = 21`).Scan(&version))
	assert.Equal(t, 21, version)
}

func TestMigratorRollsBackEveryStatementAndReportsFailedIndex(t *testing.T) {
	m, pool := migratorFixture(t)

	_, err := apply(t, m, 1, 22, "CREATE TABLE app.one (id int); CREATE TABLE app.two (id int); SELECT * FROM app.tidak_ada;")

	require.Error(t, err)
	var si statementIndexer
	require.True(t, errors.As(err, &si), "error harus membawa index statement yang gagal")
	assert.Equal(t, 3, si.StatementIndex())
	assert.False(t, tableExists(t, pool, "app.one"))
	assert.False(t, tableExists(t, pool, "app.two"))
	if tableExists(t, pool, "_dbmp_schema_history") {
		var rows int
		require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM _dbmp_schema_history WHERE run_id = 22`).Scan(&rows))
		assert.Zero(t, rows, "riwayat target tidak boleh tercatat untuk run yang gagal")
	}
}

func TestMigratorRejectsTransactionControlAndEmptyScripts(t *testing.T) {
	m, pool := migratorFixture(t)

	for _, sql := range []string{"BEGIN; CREATE TABLE app.sneaky (id int); COMMIT;", "VACUUM;", "CREATE INDEX CONCURRENTLY x ON app.books (id);", "   ", "-- hanya komentar"} {
		_, err := apply(t, m, 1, 23, sql)
		assert.Error(t, err, sql)
	}
	assert.False(t, tableExists(t, pool, "app.sneaky"))
}

func TestMigratorFailsFastWhenTargetLockIsHeld(t *testing.T) {
	m, pool := migratorFixture(t)
	ctx := context.Background()
	holder, err := pool.Acquire(ctx)
	require.NoError(t, err)
	_, err = holder.Exec(ctx, `SELECT pg_advisory_lock($1::bigint)`, int64(7))
	require.NoError(t, err)

	start := time.Now()
	_, err = apply(t, m, 7, 24, "CREATE TABLE app.blocked (id int)")

	assert.ErrorIs(t, err, postgres.ErrLockBusy)
	assert.Less(t, time.Since(start), 3*time.Second, "tidak boleh menunggu lock")
	assert.False(t, tableExists(t, pool, "app.blocked"))

	_, err = apply(t, m, 8, 25, "CREATE TABLE app.other_database (id int)")
	require.NoError(t, err, "lock database lain tidak boleh ikut memblokir")

	_, err = holder.Exec(ctx, `SELECT pg_advisory_unlock($1::bigint)`, int64(7))
	require.NoError(t, err)
	holder.Release()
	_, err = apply(t, m, 7, 26, "CREATE TABLE app.after_release (id int)")
	require.NoError(t, err)
	assert.True(t, tableExists(t, pool, "app.after_release"))
}

func TestMigratorReleasesLockAfterFailure(t *testing.T) {
	m, _ := migratorFixture(t)

	_, err := apply(t, m, 9, 27, "SELECT * FROM app.tidak_ada")
	require.Error(t, err)
	_, err = apply(t, m, 9, 28, "CREATE TABLE app.second_try (id int)")

	require.NoError(t, err)
}

func TestMigratorCommittedVersionReflectsTargetHistory(t *testing.T) {
	m, _ := migratorFixture(t)
	ctx := context.Background()
	_, err := apply(t, m, 1, 31, "CREATE TABLE app.versioned (id int)")
	require.NoError(t, err)

	version, found, err := m.CommittedVersion(ctx, 31)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, 31, version)

	_, found, err = m.CommittedVersion(ctx, 999)
	require.NoError(t, err)
	assert.False(t, found)
}
