package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

func TestCreateAdmin(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	svc := auth.NewService(pool, config.Session{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour, RotationInterval: time.Hour}, "k", false)

	_, err := createAdmin(ctx, pool, "admin", "pendek")
	assert.EqualError(t, err, "password minimal 8 karakter")
	_, err = createAdmin(ctx, pool, "  ", "rahasia123")
	assert.EqualError(t, err, "username wajib diisi")

	id, err := createAdmin(ctx, pool, "admin", "rahasia123")
	require.NoError(t, err)
	user, sess, err := svc.Login(ctx, auth.LoginInput{Username: "admin", Password: "rahasia123"})
	require.NoError(t, err)
	assert.Equal(t, id, user.ID)
	assert.True(t, user.IsPlatformAdmin)

	_, err = pool.Exec(ctx, `UPDATE users SET is_active = FALSE, is_platform_admin = FALSE WHERE id = $1`, id)
	require.NoError(t, err)

	again, err := createAdmin(ctx, pool, "admin", "passwordbaru")
	require.NoError(t, err)
	assert.Equal(t, id, again)

	_, err = svc.Authenticate(ctx, sess.NewToken)
	assert.ErrorIs(t, err, apperr.ErrUnauthenticated)
	_, _, err = svc.Login(ctx, auth.LoginInput{Username: "admin", Password: "rahasia123"})
	assert.ErrorIs(t, err, apperr.ErrUnauthenticated)
	user, _, err = svc.Login(ctx, auth.LoginInput{Username: "admin", Password: "passwordbaru"})
	require.NoError(t, err)
	assert.True(t, user.IsPlatformAdmin)
	assert.True(t, user.IsActive)
}

func effective(t *testing.T, az *authz.DBAuthorizer, pool *pgxpool.Pool, username, database string) authz.Level {
	t.Helper()
	var userID, databaseID int64
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username = $1`, username).Scan(&userID))
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT id FROM database_connections WHERE name = $1`, database).Scan(&databaseID))
	level, err := az.EffectiveLevel(context.Background(), userID, databaseID)
	require.NoError(t, err)
	return level
}

func counts(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var out string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM users)::text || '/' ||
		(SELECT count(*) FROM groups)::text || '/' || (SELECT count(*) FROM database_connections)::text || '/' ||
		(SELECT count(*) FROM group_members)::text || '/' || (SELECT count(*) FROM group_database_grants)::text`).Scan(&out))
	return out
}

func TestSeedDemoCreatesDemoScenarioAndIsIdempotent(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	cipher, err := secret.New("REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8=")
	require.NoError(t, err)
	az := authz.NewDBAuthorizer(pool)

	summary, err := seedDemo(ctx, pool, cipher, "demo12345", "localhost")
	require.NoError(t, err)
	assert.Equal(t, demoSummary{Users: 4, Groups: 2, Connections: 3, Grants: 4}, summary)
	first := counts(t, pool)
	assert.Equal(t, "4/2/3/4/4", first)

	_, err = seedDemo(ctx, pool, cipher, "demo12345", "localhost")
	require.NoError(t, err)
	assert.Equal(t, first, counts(t, pool), "menjalankan ulang tidak boleh menggandakan data")

	assert.Equal(t, authz.LevelEdit, effective(t, az, pool, "alice", "inventory"))
	assert.Equal(t, authz.LevelView, effective(t, az, pool, "alice", "staging"))
	assert.Equal(t, authz.LevelNone, effective(t, az, pool, "alice", "sales"))
	assert.Equal(t, authz.LevelView, effective(t, az, pool, "bob", "inventory"))
	assert.Equal(t, authz.LevelView, effective(t, az, pool, "bob", "sales"))
	assert.Equal(t, authz.LevelNone, effective(t, az, pool, "bob", "staging"))
	assert.Equal(t, authz.LevelEdit, effective(t, az, pool, "carol", "inventory"), "multi-group: view + edit menjadi edit")
	assert.Equal(t, authz.LevelView, effective(t, az, pool, "carol", "sales"))
	for _, db := range []string{"inventory", "staging", "sales"} {
		assert.Equal(t, authz.LevelNone, effective(t, az, pool, "admin", db), "Platform Admin tidak otomatis punya akses data")
	}
}

func TestSeedDemoAccountsCanLoginAndSecretsAreProtected(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	cipher, err := secret.New("REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8=")
	require.NoError(t, err)
	_, err = seedDemo(ctx, pool, cipher, "demo12345", "db.local")
	require.NoError(t, err)
	svc := auth.NewService(pool, config.Session{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour, RotationInterval: time.Hour}, "k", false)

	for _, username := range []string{"admin", "alice", "bob", "carol"} {
		user, _, err := svc.Login(ctx, auth.LoginInput{Username: username, Password: "demo12345"})
		require.NoError(t, err, username)
		assert.Equal(t, username == "admin", user.IsPlatformAdmin, username)
	}

	var hash, host string
	var ciphertext []byte
	require.NoError(t, pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE username = 'alice'`).Scan(&hash))
	assert.NotContains(t, hash, "demo12345")
	require.NoError(t, pool.QueryRow(ctx, `SELECT host, password_ciphertext FROM database_connections WHERE name = 'sales'`).Scan(&host, &ciphertext))
	assert.Equal(t, "db.local", host)
	assert.NotContains(t, string(ciphertext), "dbmp")
	plain, err := cipher.Decrypt(ciphertext)
	require.NoError(t, err)
	assert.Equal(t, "dbmp", string(plain))
}

func TestSeedDemoRejectsEmptyHost(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)
	cipher, err := secret.New("REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8=")
	require.NoError(t, err)

	_, err = seedDemo(context.Background(), pool, cipher, "demo12345", "  ")

	assert.EqualError(t, err, "host wajib diisi")
}
