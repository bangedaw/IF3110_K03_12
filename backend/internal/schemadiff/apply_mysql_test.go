package schemadiff_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schemadiff"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"

	_ "github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/mysql"
)

func mysqlConfigOf(t *testing.T) dbms.ConnConfig {
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

func mysqlOpen(t *testing.T, cfg dbms.ConnConfig) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true", cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mysqlSnapshot(t *testing.T, cfg dbms.ConnConfig) *schema.Model {
	t.Helper()
	drv, ok := dbms.DriverFor(dbms.KindMySQL)
	require.True(t, ok, "driver mysql belum terdaftar")
	conn, err := drv.Open(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	model, err := conn.Inspector().Snapshot(context.Background())
	require.NoError(t, err)
	return model
}

var mysqlMutations = []string{

	`CREATE TABLE shipments (
		id         BIGINT AUTO_INCREMENT PRIMARY KEY,
		order_id   BIGINT NOT NULL,
		carrier    VARCHAR(60) NOT NULL DEFAULT 'jne',
		CONSTRAINT fk_shipments_order FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE
	)`,
	`CREATE INDEX idx_shipments_order ON shipments (order_id)`,

	`ALTER TABLE customers DROP COLUMN city`,
	`ALTER TABLE customers ADD COLUMN nickname VARCHAR(50) NULL`,
	`ALTER TABLE orders MODIFY COLUMN total DECIMAL(14,2) NOT NULL DEFAULT 0`,

	`ALTER TABLE customers DROP INDEX email`,

	`ALTER TABLE orders DROP FOREIGN KEY fk_orders_customer`,

	`DROP TABLE long_cells`,
}

func TestGeneratedSQLMySQL_MenyamakanSkemaAKeB(t *testing.T) {
	ctx := context.Background()

	cfgA := mysqlConfigOf(t)
	cfgB := mysqlConfigOf(t)
	dbA := mysqlOpen(t, cfgA)
	dbB := mysqlOpen(t, cfgB)

	for _, m := range mysqlMutations {
		_, err := dbB.ExecContext(ctx, m)
		require.NoErrorf(t, err, "mutasi skema B: %s", m)
	}

	modelA := mysqlSnapshot(t, cfgA)
	modelB := mysqlSnapshot(t, cfgB)

	schemadiff.AlignModels(modelA, modelB)
	changes := schemadiff.Diff(modelA, modelB)
	require.NotEmpty(t, changes, "seed A dan B harus berbeda setelah mutasi")

	res, err := schemadiff.Generate(schema.KindMySQL, changes)
	require.NoError(t, err)
	require.NotEmpty(t, res.Statements)

	for i, stmt := range res.Statements {
		_, err := dbA.ExecContext(ctx, stmt)
		require.NoErrorf(t, err, "pernyataan #%d gagal:\n%s", i, stmt)
	}

	after := mysqlSnapshot(t, cfgA)
	rest := schemadiff.Diff(after, modelB)
	require.Empty(t, rest, "skema A setelah apply SQL harus sama dengan B; sisa diff: %+v", rest)
}
