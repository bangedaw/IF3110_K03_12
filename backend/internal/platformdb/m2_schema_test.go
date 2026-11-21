package platformdb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestM2SchemaConstraints(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) error {
		_, err := pool.Exec(ctx, sql, args...)
		return err
	}
	var leader, outsider, group, database, migration, request int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('lead', 'x') RETURNING id`).Scan(&leader))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('out', 'x') RETURNING id`).Scan(&outsider))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO groups (name) VALUES ('g') RETURNING id`).Scan(&group))
	require.NoError(t, exec(`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, group, leader))

	assert.Equal(t, "23503", sqlState(exec(`UPDATE groups SET leader_user_id = $2 WHERE id = $1`, group, outsider)), "leader harus anggota group")
	require.NoError(t, exec(`UPDATE groups SET leader_user_id = $2 WHERE id = $1`, group, leader))
	assert.Equal(t, "23503", sqlState(exec(`DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, group, leader)), "leader tidak bisa dikeluarkan")
	assert.Equal(t, "23503", sqlState(exec(`DELETE FROM users WHERE id = $1`, leader)), "user yang masih leader tidak bisa dihapus")

	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO database_connections (name, kind, host, port, database_name, username, password_ciphertext)
		VALUES ('d', 'postgres', 'h', 5432, 'd', 'u', '\x00') RETURNING id`).Scan(&database))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO migrations (database_id, title, sql_text, checksum, created_by, updated_by)
		VALUES ($1, 't', 'SELECT 1', '\x00', $2, $2) RETURNING id`, database, leader).Scan(&migration))
	assert.Equal(t, "23514", sqlState(exec(`UPDATE migrations SET batch_column = 'id' WHERE id = $1`, migration)), "konfigurasi batch harus lengkap")
	assert.Equal(t, "23514", sqlState(exec(`UPDATE migrations SET batch_table = 't', batch_column = 'id', batch_size = 100 WHERE id = $1`, migration)), "schema batch wajib diisi")
	require.NoError(t, exec(`UPDATE migrations SET batch_schema = 'app', batch_table = 't', batch_column = 'id', batch_size = 100 WHERE id = $1`, migration))

	const newRequest = `INSERT INTO migration_requests (migration_id, database_id, kind, revision, requested_by) VALUES ($1, $2, 'apply', 1, $3) RETURNING id`
	require.NoError(t, pool.QueryRow(ctx, newRequest, migration, database, leader).Scan(&request))
	assert.Equal(t, "23505", sqlState(pool.QueryRow(ctx, newRequest, migration, database, leader).Scan(new(int64))), "satu request terbuka per migration")

	const newJob = `INSERT INTO jobs (request_id, database_id, created_by) VALUES ($1, $2, $3)`
	require.NoError(t, exec(newJob, request, database, leader))
	assert.Equal(t, "23505", sqlState(exec(newJob, request, database, leader)), "satu job per request")

	require.NoError(t, exec(`UPDATE migration_requests SET status = 'succeeded' WHERE id = $1`, request))
	require.NoError(t, pool.QueryRow(ctx, newRequest, migration, database, leader).Scan(new(int64)), "request baru boleh setelah yang lama selesai")

	require.NoError(t, exec(`DELETE FROM groups WHERE id = $1`, group), "group berleader tetap bisa dihapus")
}
