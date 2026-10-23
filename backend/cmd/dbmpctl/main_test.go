package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
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
