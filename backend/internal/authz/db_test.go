package authz_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

type fixture struct {
	pool *pgxpool.Pool
	az   *authz.DBAuthorizer
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)
	return fixture{pool: pool, az: authz.NewDBAuthorizer(pool)}
}

func (f fixture) id(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var id int64
	require.NoError(t, f.pool.QueryRow(context.Background(), sql, args...).Scan(&id))
	return id
}

func (f fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), sql, args...)
	require.NoError(t, err)
}

func (f fixture) user(t *testing.T, name string, admin bool) int64 {
	return f.id(t, `INSERT INTO users (username, password_hash, is_platform_admin) VALUES ($1, 'x', $2) RETURNING id`, name, admin)
}

func (f fixture) database(t *testing.T, name string) int64 {
	return f.id(t, `INSERT INTO database_connections (name, kind, host, port, database_name, username, password_ciphertext)
		VALUES ($1, 'postgres', 'localhost', 5432, $2, 'u', '\x00') RETURNING id`, name, name)
}

func (f fixture) group(t *testing.T, name string, members ...int64) int64 {
	g := f.id(t, `INSERT INTO groups (name) VALUES ($1) RETURNING id`, name)
	for _, u := range members {
		f.exec(t, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, g, u)
	}
	return g
}

func (f fixture) grant(t *testing.T, group, database int64, level authz.Level) {
	f.exec(t, `INSERT INTO group_database_grants (group_id, database_id, level) VALUES ($1, $2, $3)`, group, database, string(level))
}

func TestEffectiveLevel(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	inventory := f.database(t, "inventory")
	sales := f.database(t, "sales")
	alice := f.user(t, "alice", false)
	bob := f.user(t, "bob", false)
	carol := f.user(t, "carol", false)
	dave := f.user(t, "dave", false)

	backend := f.group(t, "backend-team", alice, carol)
	analyst := f.group(t, "analyst", bob, carol)
	f.grant(t, backend, inventory, authz.LevelEdit)
	f.grant(t, analyst, inventory, authz.LevelView)
	f.grant(t, analyst, sales, authz.LevelView)

	cases := []struct {
		name     string
		user, db int64
		want     authz.Level
	}{
		{"edit lewat satu group", alice, inventory, authz.LevelEdit},
		{"view lewat satu group", bob, inventory, authz.LevelView},
		{"multi-group view + edit = edit", carol, inventory, authz.LevelEdit},
		{"grant hanya berlaku pada database-nya", alice, sales, authz.LevelNone},
		{"tanpa group", dave, inventory, authz.LevelNone},
		{"database tidak ada", alice, 9999, authz.LevelNone},
	}
	for _, tc := range cases {
		got, err := f.az.EffectiveLevel(ctx, tc.user, tc.db)
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
	}
}

func TestAuthorize(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	inventory := f.database(t, "inventory")
	alice := f.user(t, "alice", false)
	bob := f.user(t, "bob", false)
	admin := f.user(t, "admin", true)
	backend := f.group(t, "backend-team", alice)
	analyst := f.group(t, "analyst", bob)
	f.grant(t, backend, inventory, authz.LevelEdit)
	f.grant(t, analyst, inventory, authz.LevelView)

	res := authz.Resource{DatabaseID: inventory}
	pAlice := authz.Principal{UserID: alice}
	pBob := authz.Principal{UserID: bob}
	pAdmin := authz.Principal{UserID: admin, IsPlatformAdmin: true}

	assert.NoError(t, f.az.Authorize(ctx, pAlice, authz.ActionMigrationExecute, res))
	assert.NoError(t, f.az.Authorize(ctx, pBob, authz.ActionDBView, res))
	assert.ErrorIs(t, f.az.Authorize(ctx, pBob, authz.ActionMigrationExecute, res), authz.ErrForbidden)

	assert.NoError(t, f.az.Authorize(ctx, pAdmin, authz.ActionManageGrants, authz.Resource{}))
	assert.ErrorIs(t, f.az.Authorize(ctx, pAlice, authz.ActionManageGrants, authz.Resource{}), authz.ErrForbidden)
	assert.ErrorIs(t, f.az.Authorize(ctx, pAdmin, authz.ActionDBView, res), authz.ErrForbidden)

	f.exec(t, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, analyst, admin)
	assert.NoError(t, f.az.Authorize(ctx, pAdmin, authz.ActionDBView, res))
	assert.ErrorIs(t, f.az.Authorize(ctx, pAdmin, authz.ActionMigrationWrite, res), authz.ErrForbidden)
}

func TestAccessChangesTakeEffectImmediately(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	inventory := f.database(t, "inventory")
	alice := f.user(t, "alice", false)
	g := f.group(t, "backend-team", alice)
	f.grant(t, g, inventory, authz.LevelEdit)
	p := authz.Principal{UserID: alice}
	res := authz.Resource{DatabaseID: inventory}

	require.NoError(t, f.az.Authorize(ctx, p, authz.ActionMigrationWrite, res))

	f.exec(t, `UPDATE group_database_grants SET level = 'view' WHERE group_id = $1`, g)
	assert.ErrorIs(t, f.az.Authorize(ctx, p, authz.ActionMigrationWrite, res), authz.ErrForbidden)
	assert.NoError(t, f.az.Authorize(ctx, p, authz.ActionDBView, res))

	f.exec(t, `DELETE FROM group_members WHERE user_id = $1`, alice)
	assert.ErrorIs(t, f.az.Authorize(ctx, p, authz.ActionDBView, res), authz.ErrForbidden)
}

var _ authz.Authorizer = (*authz.DBAuthorizer)(nil)
