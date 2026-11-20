package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

func TestInternalTablesAreHiddenFromExplorer(t *testing.T) {
	pool := testutil.NewPostgresTarget(t)
	t.Cleanup(pool.Close)
	_, err := pool.Exec(context.Background(), `
		CREATE TABLE public._dbmp_schema_history (run_id BIGINT PRIMARY KEY);
		CREATE TABLE app._dbmp_batch_run (run_id BIGINT PRIMARY KEY);
		INSERT INTO public._dbmp_schema_history VALUES (1)`)
	require.NoError(t, err)

	cc := pool.Config().ConnConfig
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	conn, err := postgres.Driver{}.Open(ctx, dbms.ConnConfig{
		Kind: dbms.KindPostgres, Host: cc.Host, Port: int(cc.Port), Database: cc.Database,
		User: cc.User, Password: cc.Password, SSLMode: "disable",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	in := conn.Inspector()

	for _, s := range []string{"public", "app"} {
		tables, err := in.ListTables(ctx, s)
		require.NoError(t, err)
		for _, tb := range tables {
			require.NotContains(t, tb.Name, "_dbmp_", "tabel internal tampil di schema %s", s)
		}
	}
	apps, err := in.ListTables(ctx, "app")
	require.NoError(t, err)
	require.Len(t, apps, 3, "tabel milik pengguna tetap terdaftar")

	_, err = in.DescribeTable(ctx, "public", "_dbmp_schema_history")
	require.True(t, errors.Is(err, dbms.ErrNotFound), "describe tabel internal harus ErrNotFound, dapat %v", err)

	_, err = conn.Browser().Rows(ctx, dbms.RowsQuery{Schema: "public", Table: "_dbmp_schema_history", Limit: 10})
	require.True(t, errors.Is(err, dbms.ErrNotFound), "baca row tabel internal harus ErrNotFound, dapat %v", err)

	m, err := in.Snapshot(ctx)
	require.NoError(t, err)
	for _, s := range m.Schemas {
		for _, tb := range s.Tables {
			require.NotContains(t, tb.Name, "_dbmp_", "tabel internal ikut snapshot")
		}
	}
	_, ok := m.Find("app", "books")
	require.True(t, ok)
}
