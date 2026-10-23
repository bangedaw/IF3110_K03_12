package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

func inspectorFor(t *testing.T) dbms.Inspector {
	t.Helper()
	cfg := targetConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	conn, err := postgres.Driver{}.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn.Inspector()
}

func TestListSchemasExcludesSystem(t *testing.T) {
	got, err := inspectorFor(t).ListSchemas(context.Background())
	require.NoError(t, err)

	require.Contains(t, got, "app")
	require.Contains(t, got, "public")
	require.NotContains(t, got, "information_schema")
	for _, s := range got {
		require.False(t, len(s) >= 3 && s[:3] == "pg_", "schema sistem bocor: %s", s)
	}
}

func TestListTablesKnownSchema(t *testing.T) {
	got, err := inspectorFor(t).ListTables(context.Background(), "app")
	require.NoError(t, err)

	require.Len(t, got, 3, "view tidak ikut terdaftar (hanya relkind r/p)")
	byName := map[string]schema.TableSummary{}
	for _, tb := range got {
		byName[tb.Name] = tb
	}
	require.Contains(t, byName, "authors")
	require.Contains(t, byName, "books")
	require.Contains(t, byName, "attachments")

	for _, tb := range got {
		require.Equal(t, "app", tb.Schema)
		require.Equal(t, schema.TableKindTable, tb.Kind)
	}
	require.EqualValues(t, 5, byName["authors"].RowEstimate, "reltuples setelah ANALYZE seed")
	require.EqualValues(t, 12, byName["books"].RowEstimate)
	require.EqualValues(t, 3, byName["attachments"].RowEstimate)

	require.Equal(t, "attachments", got[0].Name)
	require.Equal(t, "authors", got[1].Name)
	require.Equal(t, "books", got[2].Name)
}

func TestListTablesUnknownSchemaIsEmpty(t *testing.T) {
	got, err := inspectorFor(t).ListTables(context.Background(), "tidak_ada")
	require.NoError(t, err)
	require.Empty(t, got)

	sys, err := inspectorFor(t).ListTables(context.Background(), "pg_catalog")
	require.NoError(t, err)
	require.Empty(t, sys, "schema sistem tidak boleh bisa ditelusuri via API")
}

func TestDescribeTableColumns(t *testing.T) {
	tb, err := inspectorFor(t).DescribeTable(context.Background(), "app", "books")
	require.NoError(t, err)
	require.Equal(t, "app", tb.Schema)
	require.Equal(t, "books", tb.Name)
	require.Equal(t, schema.TableKindTable, tb.Kind)
	require.EqualValues(t, 12, tb.RowEstimate)

	names := make([]string, len(tb.Columns))
	for i, c := range tb.Columns {
		names[i] = c.Name
	}
	require.Equal(t, []string{"id", "author_id", "title", "price", "published_on", "tags"}, names)

	byName := map[string]schema.Column{}
	for _, c := range tb.Columns {
		byName[c.Name] = c
	}
	id := byName["id"]
	require.Equal(t, "bigint", id.DataType)
	require.False(t, id.Nullable)
	require.Contains(t, id.Default, "nextval", "BIGSERIAL = default nextval, bukan identity")
	require.False(t, id.IsIdentity)

	price := byName["price"]
	require.Equal(t, "numeric(10,2)", price.DataType)
	require.False(t, price.Nullable)
	require.Equal(t, "0", price.Default)

	published := byName["published_on"]
	require.Equal(t, "date", published.DataType)
	require.True(t, published.Nullable)

	tags := byName["tags"]
	require.Equal(t, "text[]", tags.DataType)
	require.False(t, tags.Nullable)

	require.Equal(t, 1, byName["id"].Ordinal)
	require.Equal(t, 6, byName["tags"].Ordinal)
}

func TestDescribeTableConstraints(t *testing.T) {
	tb, err := inspectorFor(t).DescribeTable(context.Background(), "app", "books")
	require.NoError(t, err)

	byType := map[string]schema.Constraint{}
	for _, c := range tb.Constraints {
		byType[c.Type] = c
	}
	require.Contains(t, byType, schema.ConstraintPrimaryKey)
	pk := byType[schema.ConstraintPrimaryKey]
	require.Equal(t, []string{"id"}, pk.Columns)
	require.Contains(t, pk.Definition, "PRIMARY KEY")

	require.Contains(t, byType, schema.ConstraintForeignKey)
	fk := byType[schema.ConstraintForeignKey]
	require.Equal(t, []string{"author_id"}, fk.Columns)
	require.Equal(t, "app", fk.RefSchema)
	require.Equal(t, "authors", fk.RefTable)
	require.Equal(t, []string{"id"}, fk.RefColumns)
	require.Equal(t, "CASCADE", fk.OnDelete)
	require.Contains(t, fk.Definition, "ON DELETE CASCADE")

	require.Contains(t, byType, schema.ConstraintCheck)
	check := byType[schema.ConstraintCheck]
	require.Contains(t, check.Definition, "price")
}

func TestDescribeTableUniqueConstraintAndIndexes(t *testing.T) {
	tb, err := inspectorFor(t).DescribeTable(context.Background(), "app", "authors")
	require.NoError(t, err)

	byName := map[string]schema.Index{}
	for _, ix := range tb.Indexes {
		byName[ix.Name] = ix
	}
	require.Contains(t, byName, "authors_pkey")
	require.True(t, byName["authors_pkey"].Primary)
	require.True(t, byName["authors_pkey"].Unique)
	require.Equal(t, []string{"id"}, byName["authors_pkey"].Columns)

	require.Contains(t, byName, "authors_email_key", "UNIQUE column membuat index unik bawaan")
	require.True(t, byName["authors_email_key"].Unique)
	require.False(t, byName["authors_email_key"].Primary)
	require.Equal(t, []string{"email"}, byName["authors_email_key"].Columns)

	books, err := inspectorFor(t).DescribeTable(context.Background(), "app", "books")
	require.NoError(t, err)
	bookIdx := map[string]schema.Index{}
	for _, ix := range books.Indexes {
		bookIdx[ix.Name] = ix
	}
	require.Contains(t, bookIdx, "books_author_idx")
	require.False(t, bookIdx["books_author_idx"].Unique)
	require.Equal(t, []string{"author_id"}, bookIdx["books_author_idx"].Columns)
	require.Equal(t, "btree", bookIdx["books_author_idx"].Method)
	require.Contains(t, bookIdx["books_author_idx"].Definition, "CREATE INDEX")
}

func TestDescribeTableNotFound(t *testing.T) {
	_, err := inspectorFor(t).DescribeTable(context.Background(), "app", "tidak_ada")
	require.Error(t, err)
	require.True(t, errors.Is(err, dbms.ErrNotFound), "harus ErrNotFound agar explorer memetakan 404")
}

func TestDescribeTableIsIdentifierSafe(t *testing.T) {
	_, err := inspectorFor(t).DescribeTable(context.Background(), `app"; DROP TABLE app.books; --`, "x")
	require.Error(t, err)
}

func TestDescribeView(t *testing.T) {
	tb, err := inspectorFor(t).DescribeTable(context.Background(), "app", "book_authors")
	require.NoError(t, err)
	require.Equal(t, schema.TableKindView, tb.Kind)
	require.Len(t, tb.Columns, 3)
}

func TestSnapshotWholeDatabase(t *testing.T) {
	m, err := inspectorFor(t).Snapshot(context.Background())
	require.NoError(t, err)
	require.NotNil(t, m)
	m.Sort()

	require.NotEmpty(t, m.Schemas)
	require.Equal(t, "app", m.Schemas[0].Name, "schemas terurut abjad")
	for _, s := range m.Schemas {
		require.False(t, len(s.Name) >= 3 && s.Name[:3] == "pg_", "schema sistem bocor: %s", s.Name)
		require.NotEqual(t, "information_schema", s.Name)
	}

	books, ok := m.Find("app", "books")
	require.True(t, ok)
	require.Len(t, books.Columns, 6)
	require.NotEmpty(t, books.Constraints)
	require.NotEmpty(t, books.Indexes)

	_, ok = m.Find("app", "book_authors")
	require.False(t, ok, "view tidak ikut snapshot (hanya relkind r/p)")
}
