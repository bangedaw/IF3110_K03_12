package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

// config dari container testutil: host/port/user/password yang sesungguhnya.
func targetConfig(t *testing.T) dbms.ConnConfig {
	t.Helper()
	pool := testutil.NewPostgresTarget(t)
	cc := pool.Config().ConnConfig
	cfg := dbms.ConnConfig{
		Kind:     dbms.KindPostgres,
		Host:     cc.Host,
		Port:     int(cc.Port),
		Database: cc.Database,
		User:     cc.User,
		Password: cc.Password,
		SSLMode:  "disable",
	}
	t.Cleanup(pool.Close)
	return cfg
}

func TestOpenAndPing(t *testing.T) {
	cfg := targetConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, err := postgres.Driver{}.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, conn.Ping(ctx))
	require.Equal(t, dbms.KindPostgres, postgres.Driver{}.Kind())
}

func TestConnExposesInspectorAndBrowser(t *testing.T) {
	cfg := targetConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, err := postgres.Driver{}.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NotNil(t, conn.Inspector())
	require.NotNil(t, conn.Browser())
	require.NotNil(t, conn.Migrator(), "stub Migrator tidak boleh nil agar tidak panik")
	require.NotNil(t, conn.Explainer(), "stub Explainer tidak boleh nil agar tidak panik")
}

func TestOpenUnreachableHostFails(t *testing.T) {
	cfg := targetConfig(t)
	cfg.Host = "127.0.0.1"
	cfg.Port = 1

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := postgres.Driver{}.Open(ctx, cfg)
	require.Error(t, err)
	require.NotContains(t, err.Error(), cfg.Password, "password tidak boleh bocor di pesan error")
}

func TestOpenBadPasswordFails(t *testing.T) {
	cfg := targetConfig(t)
	cfg.Password = "salah-sekali"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := postgres.Driver{}.Open(ctx, cfg)
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), cfg.Password), "password tidak boleh bocor di pesan error")
}
