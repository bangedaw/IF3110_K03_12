package mysql_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/mysql"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

func inspectorFor(t *testing.T, cfg dbms.ConnConfig) dbms.Inspector {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	conn, err := mysql.Driver{}.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn.Inspector()
}

func TestListSchemasExcludesSystem(t *testing.T) {
	got, err := inspectorFor(t, targetConfig(t)).ListSchemas(context.Background())
	require.NoError(t, err)

	for _, s := range got {
		require.NotEqual(t, "mysql", s)
		require.NotEqual(t, "information_schema", s)
		require.NotEqual(t, "performance_schema", s)
		require.NotEqual(t, "sys", s)
	}

	require.NotEmpty(t, got)
}

func TestListTablesKnownSchema(t *testing.T) {
	cfg := targetConfig(t)
	ins := inspectorFor(t, cfg)
	dbName := cfg.Database

	got, err := ins.ListTables(context.Background(), dbName)
	require.NoError(t, err)

	byName := map[string]schema.TableSummary{}
	for _, tb := range got {
		require.Equal(t, dbName, tb.Schema)
		require.Equal(t, schema.TableKindTable, tb.Kind)
		byName[tb.Name] = tb
	}
	require.Contains(t, byName, "customers")
	require.Contains(t, byName, "orders")

	require.Positive(t, byName["customers"].RowEstimate)
	require.Positive(t, byName["orders"].RowEstimate)

	require.Equal(t, "customers", got[0].Name)
	require.Equal(t, "long_cells", got[1].Name)
	require.Equal(t, "orders", got[2].Name)
}

func TestListTablesUnknownSchemaIsEmpty(t *testing.T) {
	ins := inspectorFor(t, targetConfig(t))
	got, err := ins.ListTables(context.Background(), "tidak_ada")
	require.NoError(t, err)
	require.Empty(t, got)

	sys, err := ins.ListTables(context.Background(), "mysql")
	require.NoError(t, err)
	require.Empty(t, sys, "schema sistem tidak boleh bisa ditelusuri via API")
}

func TestDescribeTableColumns(t *testing.T) {
	cfg := targetConfig(t)
	ins := inspectorFor(t, cfg)
	dbName := cfg.Database

	tb, err := ins.DescribeTable(context.Background(), dbName, "customers")
	require.NoError(t, err)
	require.Equal(t, dbName, tb.Schema)
	require.Equal(t, "customers", tb.Name)
	require.Equal(t, schema.TableKindTable, tb.Kind)

	names := make([]string, len(tb.Columns))
	for i, c := range tb.Columns {
		names[i] = c.Name
	}
	require.Equal(t, []string{"id", "name", "email", "city"}, names)

	byName := map[string]schema.Column{}
	for _, c := range tb.Columns {
		byName[c.Name] = c
	}
	id := byName["id"]
	require.Equal(t, "bigint", id.DataType)
	require.False(t, id.Nullable)
	require.True(t, id.IsIdentity, "AUTO_INCREMENT = identity")

	name := byName["name"]
	require.Equal(t, "character varying(120)", name.DataType)
	require.False(t, name.Nullable)

	city := byName["city"]
	require.True(t, city.Nullable)

	require.Equal(t, 1, byName["id"].Ordinal)
	require.Equal(t, 4, byName["city"].Ordinal)
}

func TestDescribeTableDefaultsAndEnum(t *testing.T) {
	cfg := targetConfig(t)
	ins := inspectorFor(t, cfg)
	dbName := cfg.Database

	tb, err := ins.DescribeTable(context.Background(), dbName, "orders")
	require.NoError(t, err)
	byName := map[string]schema.Column{}
	for _, c := range tb.Columns {
		byName[c.Name] = c
	}

	total := byName["total"]
	require.Equal(t, "numeric(12,2)", total.DataType)
	require.False(t, total.Nullable)
	require.Equal(t, "0.00", total.Default, "MySQL menormalkan default decimal")

	ordered := byName["ordered_at"]
	require.Equal(t, "timestamp", ordered.DataType)
	require.Equal(t, "CURRENT_TIMESTAMP", ordered.Default)

	status := byName["status"]
	require.Contains(t, status.DataType, "enum")
	require.Contains(t, status.DataType, "pending")
}

func TestDescribeTableConstraints(t *testing.T) {
	cfg := targetConfig(t)
	ins := inspectorFor(t, cfg)
	dbName := cfg.Database

	tb, err := ins.DescribeTable(context.Background(), dbName, "orders")
	require.NoError(t, err)

	byType := map[string]schema.Constraint{}
	for _, c := range tb.Constraints {
		byType[c.Type] = c
	}
	require.Contains(t, byType, schema.ConstraintPrimaryKey)
	pk := byType[schema.ConstraintPrimaryKey]
	require.Equal(t, []string{"id"}, pk.Columns)

	require.Contains(t, byType, schema.ConstraintForeignKey)
	fk := byType[schema.ConstraintForeignKey]
	require.Equal(t, "fk_orders_customer", fk.Name)
	require.Equal(t, []string{"customer_id"}, fk.Columns)
	require.Equal(t, dbName, fk.RefSchema)
	require.Equal(t, "customers", fk.RefTable)
	require.Equal(t, []string{"id"}, fk.RefColumns)
}

func TestDescribeTableUniqueAndIndexes(t *testing.T) {
	cfg := targetConfig(t)
	ins := inspectorFor(t, cfg)
	dbName := cfg.Database

	cust, err := ins.DescribeTable(context.Background(), dbName, "customers")
	require.NoError(t, err)
	byName := map[string]schema.Index{}
	for _, ix := range cust.Indexes {
		byName[ix.Name] = ix
	}
	require.Contains(t, byName, "PRIMARY")
	require.True(t, byName["PRIMARY"].Primary)
	require.True(t, byName["PRIMARY"].Unique)
	require.Equal(t, []string{"id"}, byName["PRIMARY"].Columns)

	require.Contains(t, byName, "email", "UNIQUE column membuat index unik bawaan")
	require.True(t, byName["email"].Unique)
	require.False(t, byName["email"].Primary)
	require.Equal(t, []string{"email"}, byName["email"].Columns)

	orders, err := ins.DescribeTable(context.Background(), dbName, "orders")
	require.NoError(t, err)
	ordIdx := map[string]schema.Index{}
	for _, ix := range orders.Indexes {
		ordIdx[ix.Name] = ix
	}
	require.Contains(t, ordIdx, "idx_orders_ordered_at")
	require.False(t, ordIdx["idx_orders_ordered_at"].Unique)
	require.Equal(t, []string{"ordered_at"}, ordIdx["idx_orders_ordered_at"].Columns)
}

func TestDescribeTableNotFound(t *testing.T) {
	cfg := targetConfig(t)
	ins := inspectorFor(t, cfg)
	dbName := cfg.Database

	_, err := ins.DescribeTable(context.Background(), dbName, "tidak_ada")
	require.Error(t, err)
	require.True(t, errors.Is(err, dbms.ErrNotFound), "harus ErrNotFound agar explorer memetakan 404")

	_, err = ins.DescribeTable(context.Background(), "mysql", "user")
	require.Error(t, err, "schema sistem tidak bisa ditelusuri")
}

func TestDescribeTableIsIdentifierSafe(t *testing.T) {
	cfg := targetConfig(t)
	ins := inspectorFor(t, cfg)
	dbName := cfg.Database

	_, err := ins.DescribeTable(context.Background(), dbName, "x`; DROP TABLE customers; --")
	require.Error(t, err)
	require.True(t, errors.Is(err, dbms.ErrNotFound))

	cust, err := ins.DescribeTable(context.Background(), dbName, "customers")
	require.NoError(t, err, "tabel asli tidak ikut terpengaruh")
	require.NotEmpty(t, cust.Columns)
}

func TestSnapshotWholeDatabase(t *testing.T) {
	cfg := targetConfig(t)
	ins := inspectorFor(t, cfg)
	dbName := cfg.Database

	m, err := ins.Snapshot(context.Background())
	require.NoError(t, err)
	require.NotNil(t, m)
	m.Sort()

	for _, s := range m.Schemas {
		require.NotContains(t,
			[]string{"mysql", "information_schema", "performance_schema", "sys"}, s.Name,
			"schema sistem bocor: %s", s.Name)
	}

	cust, ok := m.Find(dbName, "customers")
	require.True(t, ok)
	require.Len(t, cust.Columns, 4)
	require.NotEmpty(t, cust.Constraints)
	require.NotEmpty(t, cust.Indexes)

	orders, ok := m.Find(dbName, "orders")
	require.True(t, ok)
	require.NotEmpty(t, orders.Constraints)
}
