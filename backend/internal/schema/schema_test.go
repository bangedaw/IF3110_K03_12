package schema_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

func TestNormalizeType(t *testing.T) {
	cases := []struct {
		kind string
		raw  string
		want string
	}{

		{schema.KindPostgres, "int4", "integer"},
		{schema.KindPostgres, "int2", "smallint"},
		{schema.KindPostgres, "int8", "bigint"},
		{schema.KindPostgres, "bool", "boolean"},
		{schema.KindPostgres, "float8", "double precision"},
		{schema.KindPostgres, "float4", "real"},
		{schema.KindPostgres, "varchar", "character varying"},
		{schema.KindPostgres, "varchar(20)", "character varying(20)"},
		{schema.KindPostgres, "character varying(20)", "character varying(20)"},
		{schema.KindPostgres, "bpchar(5)", "character(5)"},
		{schema.KindPostgres, "timestamptz", "timestamp with time zone"},
		{schema.KindPostgres, "timestamp with time zone", "timestamp with time zone"},
		{schema.KindPostgres, "numeric(10,2)", "numeric(10,2)"},

		{schema.KindMySQL, "int", "integer"},
		{schema.KindMySQL, "INTEGER", "integer"},
		{schema.KindMySQL, "tinyint(1)", "boolean"},
		{schema.KindMySQL, "bigint(20)", "bigint"},
		{schema.KindMySQL, "varchar(20)", "character varying(20)"},
		{schema.KindMySQL, "char(5)", "character(5)"},
		{schema.KindMySQL, "decimal(10,2)", "numeric(10,2)"},
		{schema.KindMySQL, "datetime", "datetime"},
	}
	for _, c := range cases {
		t.Run(c.kind+" "+c.raw, func(t *testing.T) {
			require.Equal(t, c.want, schema.NormalizeType(c.kind, c.raw))
		})
	}
}

func TestNormalizeTypeIsIdempotent(t *testing.T) {
	for _, kind := range []string{schema.KindPostgres, schema.KindMySQL} {
		for _, raw := range []string{"int4", "varchar(20)", "int", "tinyint(1)", "numeric", "text"} {
			once := schema.NormalizeType(kind, raw)
			require.Equal(t, once, schema.NormalizeType(kind, once), "normalisasi harus idempoten")
		}
	}
}

func TestNormalizeDefault(t *testing.T) {
	cases := []struct {
		kind string
		raw  string
		want string
	}{
		{schema.KindPostgres, "now()", "CURRENT_TIMESTAMP"},
		{schema.KindPostgres, "CURRENT_TIMESTAMP", "CURRENT_TIMESTAMP"},
		{schema.KindPostgres, "'active'::text", "'active'"},
		{schema.KindPostgres, "''::character varying", "''"},
		{schema.KindPostgres, "0", "0"},
		{schema.KindPostgres, "NULL", "NULL"},
		{schema.KindPostgres, "true", "TRUE"},
		{schema.KindPostgres, "'a'::inventory.kind_enum", "'a'"},
		{schema.KindMySQL, "current_timestamp()", "CURRENT_TIMESTAMP"},
		{schema.KindMySQL, "NULL", "NULL"},
		{schema.KindMySQL, "0", "0"},
	}
	for _, c := range cases {
		t.Run(c.kind+" "+c.raw, func(t *testing.T) {
			require.Equal(t, c.want, schema.NormalizeDefault(c.kind, c.raw))
		})
	}
}

func TestModelSortAndFind(t *testing.T) {
	m := &schema.Model{Schemas: []schema.Schema{
		{Name: "zeta", Tables: []schema.Table{{Name: "t2"}, {Name: "t1"}}},
		{Name: "alpha", Tables: []schema.Table{{Name: "b"}, {Name: "a"}}},
	}}
	m.Sort()

	require.Equal(t, "alpha", m.Schemas[0].Name)
	require.Equal(t, "a", m.Schemas[0].Tables[0].Name)
	require.Equal(t, "t1", m.Schemas[1].Tables[0].Name)

	tbl, ok := m.Find("alpha", "a")
	require.True(t, ok)
	require.Equal(t, "a", tbl.Name)

	_, ok = m.Find("alpha", "tidak-ada")
	require.False(t, ok)
}
