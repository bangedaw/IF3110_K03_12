package mysql_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/mysql"
)

func TestInternalTablesAreHiddenFromExplorer(t *testing.T) {
	cfg := targetConfig(t)
	raw, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database))
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	_, err = raw.Exec(`CREATE TABLE _dbmp_schema_history (run_id BIGINT PRIMARY KEY)`)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	conn, err := mysql.Driver{}.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	in := conn.Inspector()

	tables, err := in.ListTables(ctx, cfg.Database)
	require.NoError(t, err)
	require.NotEmpty(t, tables)
	for _, tb := range tables {
		require.NotEqual(t, "_dbmp_schema_history", tb.Name)
	}

	_, err = in.DescribeTable(ctx, cfg.Database, "_dbmp_schema_history")
	require.True(t, errors.Is(err, dbms.ErrNotFound), "describe tabel internal harus ErrNotFound, dapat %v", err)

	_, err = conn.Browser().Rows(ctx, dbms.RowsQuery{Schema: cfg.Database, Table: "_dbmp_schema_history", Limit: 10})
	require.True(t, errors.Is(err, dbms.ErrNotFound), "baca row tabel internal harus ErrNotFound, dapat %v", err)

	m, err := in.Snapshot(ctx)
	require.NoError(t, err)
	_, ok := m.Find(cfg.Database, "_dbmp_schema_history")
	require.False(t, ok, "tabel internal ikut snapshot")
}
