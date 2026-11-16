package dbconn_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"

	_ "github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
)

func newTargets(t *testing.T, host string, port int, database, user, password string) (dbconn.Targets, func()) {
	t.Helper()
	cipher, err := secret.New(testKey)
	require.NoError(t, err)
	store := fakeStore{rows: map[int64]dbconn.TargetCredentials{1: {
		ID: 1, Name: "target", Kind: dbms.KindPostgres, Host: host, Port: port,
		Database: database, User: user, PasswordCiphertext: cipher.Encrypt([]byte(password)),
		SSLMode: "disable", IsActive: true,
	}}}
	registry := dbconn.NewRegistry(store, cipher)
	return dbconn.Targets{Registry: registry}, registry.Close
}

func liveTargets(t *testing.T) (dbconn.Targets, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.NewPostgresTarget(t)
	t.Cleanup(pool.Close)
	cfg := pool.Config().ConnConfig
	targets, closeFn := newTargets(t, cfg.Host, int(cfg.Port), cfg.Database, cfg.User, cfg.Password)
	t.Cleanup(closeFn)
	return targets, pool
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists))
	return exists
}

func TestTargetApplyCommitsAndRecordsHistory(t *testing.T) {
	targets, pool := liveTargets(t)
	tgt, err := targets.Get(context.Background(), 1)
	require.NoError(t, err)

	res, err := tgt.Apply(context.Background(), migration.ApplyRequest{
		RunID: 11, MigrationID: 5, DatabaseID: 1, Version: 3, Checksum: []byte("sum"),
		SQL: "CREATE TABLE app.notes (id int PRIMARY KEY, body text); INSERT INTO app.notes VALUES (1, 'halo');",
	})

	require.NoError(t, err)
	assert.Equal(t, 2, res.StatementsExecuted)
	assert.False(t, res.AppliedAt.IsZero())
	assert.True(t, tableExists(t, pool, "app.notes"))
	var version int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT version FROM _dbmp_schema_history WHERE run_id = 11`).Scan(&version))
	assert.Equal(t, 3, version)
}

func TestTargetApplyRollsBackEverythingOnFailure(t *testing.T) {
	targets, pool := liveTargets(t)
	tgt, err := targets.Get(context.Background(), 1)
	require.NoError(t, err)

	_, err = tgt.Apply(context.Background(), migration.ApplyRequest{
		RunID: 12, MigrationID: 6, DatabaseID: 1, Version: 1, Checksum: []byte("sum"),
		SQL: "CREATE TABLE app.part_one (id int); CREATE TABLE app.part_two (id int); SELECT * FROM app.tidak_ada;",
	})

	require.Error(t, err)
	var ae *apperr.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, apperr.CodeMigrationFailed, ae.Code)
	assert.Contains(t, err.Error(), "statement 3")
	assert.False(t, tableExists(t, pool, "app.part_one"), "statement 1 harus ikut di-rollback")
	assert.False(t, tableExists(t, pool, "app.part_two"), "statement 2 harus ikut di-rollback")
}

func TestTargetApplyRejectsTransactionControl(t *testing.T) {
	targets, pool := liveTargets(t)
	tgt, err := targets.Get(context.Background(), 1)
	require.NoError(t, err)

	_, err = tgt.Apply(context.Background(), migration.ApplyRequest{
		RunID: 13, DatabaseID: 1, Version: 1, Checksum: []byte("sum"),
		SQL: "BEGIN; CREATE TABLE app.sneaky (id int); COMMIT;",
	})

	require.Error(t, err)
	assert.False(t, tableExists(t, pool, "app.sneaky"))
}

func TestTargetSnapshotReflectsAppliedChange(t *testing.T) {
	targets, _ := liveTargets(t)
	tgt, err := targets.Get(context.Background(), 1)
	require.NoError(t, err)
	_, err = tgt.Apply(context.Background(), migration.ApplyRequest{
		RunID: 14, DatabaseID: 1, Version: 1, Checksum: []byte("sum"),
		SQL: "CREATE TABLE app.snapshotted (id int PRIMARY KEY)",
	})
	require.NoError(t, err)

	snap, err := tgt.Snapshot(context.Background())

	require.NoError(t, err)
	model, ok := snap.(*schema.Model)
	require.True(t, ok)
	_, found := model.Find("app", "snapshotted")
	assert.True(t, found)
}

func TestTargetExplainDoesNotPersistChanges(t *testing.T) {
	targets, pool := liveTargets(t)
	tgt, err := targets.Get(context.Background(), 1)
	require.NoError(t, err)

	out, err := tgt.Explain(context.Background(),
		"CREATE TABLE app.explain_tmp (id int); SELECT * FROM app.books WHERE id = 1", false)

	require.NoError(t, err)
	res, ok := out.(*dbms.ExplainResult)
	require.True(t, ok)
	require.Len(t, res.Statements, 2)
	assert.False(t, res.Statements[0].Explainable)
	assert.True(t, res.Statements[1].Explainable)
	assert.NotNil(t, res.Statements[1].Plan)
	assert.False(t, tableExists(t, pool, "app.explain_tmp"))
}

func TestTargetExplainAnalyzeDoesNotChangeData(t *testing.T) {
	targets, pool := liveTargets(t)
	tgt, err := targets.Get(context.Background(), 1)
	require.NoError(t, err)

	_, err = tgt.Explain(context.Background(), "DELETE FROM app.attachments", true)

	require.NoError(t, err)
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM app.attachments`).Scan(&n))
	assert.Equal(t, 3, n)
}

func TestTargetExplainReportsSQLErrors(t *testing.T) {
	targets, _ := liveTargets(t)
	tgt, err := targets.Get(context.Background(), 1)
	require.NoError(t, err)

	_, err = tgt.Explain(context.Background(), "SELECT * FROM app.tidak_ada", false)

	var ae *apperr.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, apperr.CodeMigrationFailed, ae.Code)
}

func TestTargetsGetMapsUnreachableAndMissing(t *testing.T) {
	dead, closeDead := newTargets(t, "127.0.0.1", 1, "x", "x", "apa-saja")
	t.Cleanup(closeDead)

	_, err := dead.Get(context.Background(), 1)

	var ae *apperr.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, apperr.CodeDBUnreachable, ae.Code)
	assert.NotContains(t, err.Error(), "apa-saja")

	missing := dbconn.Targets{Registry: newRegistry(t, fakeStore{err: apperr.ErrNotFound}, nil)}
	_, err = missing.Get(context.Background(), 99)
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}
