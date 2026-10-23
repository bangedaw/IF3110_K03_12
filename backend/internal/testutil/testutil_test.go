package testutil_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

func TestPlatformDBIsMigrated(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = 'database_connections'`).Scan(&n)
	require.NoError(t, err)
	require.Equal(t, 1, n, "tabel platform database_connections harus ada setelah migrate")
}

func TestPostgresTargetHasKnownSeedSchema(t *testing.T) {
	pool := testutil.NewPostgresTarget(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var schemas int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'app'`).Scan(&schemas))
	require.Equal(t, 1, schemas)

	var tables int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'app' AND table_name IN ('authors', 'books', 'attachments')`).Scan(&tables))
	require.Equal(t, 3, tables)

	var books int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app.books`).Scan(&books))
	require.GreaterOrEqual(t, books, 10, "seed harus berisi beberapa buku")
}

func TestPostgresTargetIsolatesDatabases(t *testing.T) {
	if os.Getenv("TEST_PG_URL") != "" {
		t.Skip("fallback TEST_PG_URL memakai satu database bersama; isolasi hanya diuji lewat container")
	}
	a := testutil.NewPostgresTarget(t)
	b := testutil.NewPostgresTarget(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := a.Exec(ctx, `INSERT INTO app.authors (name) VALUES ('hanya-di-a')`)
	require.NoError(t, err)

	var n int
	require.NoError(t, b.QueryRow(ctx, `SELECT count(*) FROM app.authors WHERE name = 'hanya-di-a'`).Scan(&n))
	require.Zero(t, n, "tiap panggilan NewPostgresTarget harus dapat database sendiri")
}

func TestMySQLTargetHasKnownSeedSchema(t *testing.T) {
	db := testutil.NewMySQLTarget(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var tables int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name IN ('customers', 'orders')`).Scan(&tables))
	require.Equal(t, 2, tables)

	var orders int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM orders`).Scan(&orders))
	require.GreaterOrEqual(t, orders, 10)
}
