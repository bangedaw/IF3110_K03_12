package mysql_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/mysql"
)

func openTarget(t *testing.T) (dbms.Conn, *sql.DB, context.Context) {
	t.Helper()
	cfg := targetConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	conn, err := mysql.Driver{}.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	raw, err := sql.Open("mysql", cfg.User+":"+cfg.Password+"@tcp("+cfg.Host+":"+strconv.Itoa(cfg.Port)+")/"+cfg.Database)
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	return conn, raw, ctx
}

func count(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(query).Scan(&n))
	return n
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	return count(t, db, "SELECT count(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = '"+name+"'") > 0
}

func TestMigratorAppliesAndRecordsHistory(t *testing.T) {
	conn, db, ctx := openTarget(t)
	m := conn.Migrator()

	_, found, err := m.CommittedVersion(ctx, 1)
	require.NoError(t, err)
	assert.False(t, found, "tabel riwayat belum ada")

	res, err := m.Apply(ctx, dbms.ApplyInput{
		SQL: "CREATE TABLE notes (id INT PRIMARY KEY); INSERT INTO notes VALUES (1);", DatabaseID: 1, MigrationID: 7, RunID: 1, Version: 1, Checksum: []byte{1},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, res.StatementsExecuted)
	assert.Equal(t, 1, count(t, db, "SELECT count(*) FROM notes"))

	version, found, err := m.CommittedVersion(ctx, 1)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, 1, version)
	_, found, err = m.CommittedVersion(ctx, 99)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestMigratorRollsBackDMLOnFailure(t *testing.T) {
	conn, db, ctx := openTarget(t)
	before := count(t, db, "SELECT count(*) FROM customers")

	_, err := conn.Migrator().Apply(ctx, dbms.ApplyInput{
		SQL: "INSERT INTO customers (name, email) VALUES ('Dodi', 'dodi@example.com'); INSERT INTO tidak_ada VALUES (1);", RunID: 2, Version: 1, Checksum: []byte{1},
	})
	var se *mysql.StatementError
	require.True(t, errors.As(err, &se), "%v", err)
	assert.Equal(t, 2, se.StatementIndex())
	assert.Equal(t, before, count(t, db, "SELECT count(*) FROM customers"), "INSERT pertama harus ikut di-rollback")

	_, found, err := conn.Migrator().CommittedVersion(ctx, 2)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestMigratorDDLIsNotTransactional(t *testing.T) {
	conn, db, ctx := openTarget(t)
	assert.False(t, conn.Migrator().SupportsTransactionalDDL())

	_, err := conn.Migrator().Apply(ctx, dbms.ApplyInput{
		SQL: "CREATE TABLE partial_ddl (id INT); INSERT INTO tidak_ada VALUES (1);", RunID: 3, Version: 1, Checksum: []byte{1},
	})
	var se *mysql.StatementError
	require.True(t, errors.As(err, &se), "%v", err)
	assert.Equal(t, 1, se.LastSuccessful)
	assert.True(t, tableExists(t, db, "partial_ddl"), "DDL MySQL melakukan implicit commit; perubahan parsial tertinggal")
}

func TestExplainerNeverExecutesDDL(t *testing.T) {
	conn, db, ctx := openTarget(t)

	res, err := conn.Explainer().Explain(ctx, dbms.ExplainInput{SQL: "CREATE TABLE explain_ddl (id INT); SELECT * FROM customers WHERE id = 1;"})
	require.NoError(t, err)
	require.Len(t, res.Statements, 2)
	assert.False(t, res.Statements[0].Explainable)
	assert.Contains(t, res.Statements[0].Notice, "tidak dijalankan")
	assert.NotNil(t, res.Statements[1].Plan)
	assert.False(t, tableExists(t, db, "explain_ddl"))

	_, err = conn.Explainer().Explain(ctx, dbms.ExplainInput{SQL: "CREATE TABLE x (id INT); SELECT 1;", Analyze: true})
	assert.Error(t, err)
}

func TestExplainAnalyzeDoesNotChangeData(t *testing.T) {
	conn, db, ctx := openTarget(t)
	before := count(t, db, "SELECT count(*) FROM orders")

	res, err := conn.Explainer().Explain(ctx, dbms.ExplainInput{SQL: "DELETE FROM orders WHERE total > 0;", Analyze: true})
	require.NoError(t, err)
	require.Len(t, res.Statements, 1)
	assert.NotNil(t, res.Statements[0].Plan)
	assert.Equal(t, before, count(t, db, "SELECT count(*) FROM orders"))
}
