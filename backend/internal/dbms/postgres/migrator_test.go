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

func run(t *testing.T, m dbms.Migrator, in dbms.ApplyInput) (*dbms.ApplyResult, error) {
	t.Helper()
	if in.DatabaseID == 0 {
		in.DatabaseID = 1
	}
	if in.MigrationID == 0 {
		in.MigrationID = 1
	}
	in.Checksum = []byte("sum")
	return m.Apply(context.Background(), in)
}

func historyDirection(t *testing.T, pool *pgxpool.Pool, runID int64) string {
	t.Helper()
	var direction string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT direction FROM _dbmp_schema_history WHERE run_id = $1`, runID).Scan(&direction))
	return direction
}

func TestMigratorRunsUpThenDownAndRecordsDirection(t *testing.T) {
	m, pool := migratorFixture(t)

	up, err := run(t, m, dbms.ApplyInput{RunID: 41, Version: 1, SQL: "CREATE TABLE app.toggles (id int); INSERT INTO app.toggles VALUES (1);"})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE app.toggles (id int)", "INSERT INTO app.toggles VALUES (1)"}, up.Statements)
	assert.True(t, tableExists(t, pool, "app.toggles"))

	down, err := run(t, m, dbms.ApplyInput{RunID: 42, Version: 1, Direction: dbms.DirectionDown, SQL: "DROP TABLE app.toggles;"})
	require.NoError(t, err)
	assert.Equal(t, 1, down.StatementsExecuted)
	assert.False(t, tableExists(t, pool, "app.toggles"))

	assert.Equal(t, "up", historyDirection(t, pool, 41))
	assert.Equal(t, "down", historyDirection(t, pool, 42))
	version, found, err := m.CommittedVersion(context.Background(), 42)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, 1, version)
}

func TestMigratorFailedDownKeepsEverythingAndReportsStatement(t *testing.T) {
	m, pool := migratorFixture(t)
	_, err := run(t, m, dbms.ApplyInput{RunID: 43, Version: 1, SQL: "CREATE TABLE app.keep_me (id int); CREATE TABLE app.keep_too (id int);"})
	require.NoError(t, err)

	_, err = run(t, m, dbms.ApplyInput{RunID: 44, Version: 1, Direction: dbms.DirectionDown,
		SQL: "DROP TABLE app.keep_me; DROP TABLE app.tidak_ada; DROP TABLE app.keep_too;"})

	var se *dbms.StatementError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, 2, se.Index)
	assert.Equal(t, "DROP TABLE app.tidak_ada", se.SQL)
	assert.Equal(t, "42P01", se.SQLState)
	assert.NotErrorIs(t, err, dbms.ErrCommitUncertain)
	assert.True(t, tableExists(t, pool, "app.keep_me"), "statement pertama ikut dibatalkan")
	assert.True(t, tableExists(t, pool, "app.keep_too"))
	_, found, err := m.CommittedVersion(context.Background(), 44)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestMigratorRejectsUnknownDirectionBeforeTouchingTarget(t *testing.T) {
	m, pool := migratorFixture(t)

	_, err := run(t, m, dbms.ApplyInput{RunID: 45, Version: 1, Direction: "sideways", SQL: "CREATE TABLE app.never (id int)"})

	assert.ErrorIs(t, err, dbms.ErrInvalidDirection)
	assert.False(t, tableExists(t, pool, "app.never"))
}

func TestMigratorUpgradesHistoryTableFromMilestoneOne(t *testing.T) {
	m, pool := migratorFixture(t)
	_, err := pool.Exec(context.Background(), `
		CREATE TABLE _dbmp_schema_history (
		  run_id BIGINT PRIMARY KEY, migration_id BIGINT NOT NULL, version INTEGER NOT NULL,
		  checksum BYTEA NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		INSERT INTO _dbmp_schema_history(run_id, migration_id, version, checksum) VALUES (40, 1, 1, 'x');`)
	require.NoError(t, err)

	_, err = run(t, m, dbms.ApplyInput{RunID: 46, Version: 2, Direction: dbms.DirectionDown, SQL: "CREATE TABLE app.upgraded (id int)"})

	require.NoError(t, err)
	assert.Equal(t, "up", historyDirection(t, pool, 40), "baris M1 dianggap up")
	assert.Equal(t, "down", historyDirection(t, pool, 46))
}

func TestMigratorDeferredConstraintFailingAtCommitIsCertain(t *testing.T) {
	m, pool := migratorFixture(t)
	_, err := pool.Exec(context.Background(), `
		CREATE TABLE app.parents (id int PRIMARY KEY);
		CREATE TABLE app.children (id int, parent_id int REFERENCES app.parents(id) DEFERRABLE INITIALLY DEFERRED);`)
	require.NoError(t, err)

	_, err = run(t, m, dbms.ApplyInput{RunID: 47, Version: 1, SQL: "INSERT INTO app.children VALUES (1, 99);"})

	var se *dbms.StatementError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, 0, se.Index)
	assert.Equal(t, "COMMIT", se.SQL)
	assert.Equal(t, "23503", se.SQLState)
	assert.NotErrorIs(t, err, dbms.ErrCommitUncertain, "server menolak commit, jadi hasilnya pasti gagal")
	var rows int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM app.children`).Scan(&rows))
	assert.Zero(t, rows)
	_, found, err := m.CommittedVersion(context.Background(), 47)
	require.NoError(t, err)
	assert.False(t, found)
}
