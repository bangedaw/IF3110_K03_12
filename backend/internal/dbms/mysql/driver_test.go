package mysql_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/mysql"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

// targetConfig dari container testutil: host/port/user/password sesungguhnya.
func targetConfig(t *testing.T) dbms.ConnConfig {
	t.Helper()
	tc := testutil.MySQLTargetConfig(t)
	return dbms.ConnConfig{
		Kind:     dbms.KindMySQL,
		Host:     tc.Host,
		Port:     tc.Port,
		Database: tc.Database,
		User:     tc.User,
		Password: tc.Password,
		SSLMode:  "disable",
	}
}

func TestOpenAndPing(t *testing.T) {
	cfg := targetConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, err := mysql.Driver{}.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, conn.Ping(ctx))
	require.Equal(t, dbms.KindMySQL, mysql.Driver{}.Kind())
}

func TestConnExposesInspectorAndBrowser(t *testing.T) {
	cfg := targetConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, err := mysql.Driver{}.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NotNil(t, conn.Inspector())
	require.NotNil(t, conn.Browser())
	require.NotNil(t, conn.Migrator())
	require.NotNil(t, conn.Explainer())
	require.False(t, conn.Migrator().SupportsTransactionalDDL(),
		"DDL MySQL implicit commit: transaksional = false")
}

func TestOpenUnreachableHostFails(t *testing.T) {
	cfg := targetConfig(t)
	cfg.Host = "127.0.0.1"
	cfg.Port = 1

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := mysql.Driver{}.Open(ctx, cfg)
	require.Error(t, err)
	require.NotContains(t, err.Error(), cfg.Password, "password tidak boleh bocor di pesan error")
}

func TestOpenBadPasswordFails(t *testing.T) {
	cfg := targetConfig(t)
	cfg.Password = "salah-sekali"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := mysql.Driver{}.Open(ctx, cfg)
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), cfg.Password), "password tidak boleh bocor di pesan error")
}
