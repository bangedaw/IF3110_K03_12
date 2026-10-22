package schemadiff_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schemadiff"
)

// -update menulis ulang seluruh file golden (jalankan lalu review diff-nya).
var update = flag.Bool("update", false, "tulis ulang file golden di testdata/")

// helper model — definisi constraint/index ditulis persis seperti hasil
// pg_get_constraintdef / pg_get_indexdef supaya golden file mewakili dunia nyata.
func column(name, typ string, nullable bool, def string) schema.Column {
	return schema.Column{Name: name, DataType: typ, Nullable: nullable, Default: def}
}

func table(name string, cols []schema.Column, cs []schema.Constraint, is []schema.Index) schema.Table {
	return schema.Table{
		Schema: "app", Name: name, Kind: schema.TableKindTable,
		Columns: cols, Constraints: cs, Indexes: is,
	}
}

func model(tables ...schema.Table) *schema.Model {
	return &schema.Model{Schemas: []schema.Schema{{Name: "app", Tables: tables}}}
}

func TestGenerateGolden(t *testing.T) {
	authorsFrom := table("authors",
		[]schema.Column{
			column("id", "bigint", false, ""),
			column("name", "text", false, ""),
			column("email", "text", true, ""),
			column("homepage", "text", true, ""),
		},
		[]schema.Constraint{{Name: "authors_pkey", Type: schema.ConstraintPrimaryKey, Definition: "PRIMARY KEY (id)", Columns: []string{"id"}}},
		nil)

	booksFrom := table("books",
		[]schema.Column{
			column("id", "bigint", false, ""),
			column("author_id", "bigint", false, ""),
			column("title", "text", false, ""),
			column("price", "numeric(10,2)", false, "0"),
		},
		[]schema.Constraint{
			{Name: "books_pkey", Type: schema.ConstraintPrimaryKey, Definition: "PRIMARY KEY (id)", Columns: []string{"id"}},
			{Name: "books_author_fkey", Type: schema.ConstraintForeignKey,
				Definition: "FOREIGN KEY (author_id) REFERENCES app.authors(id) ON DELETE CASCADE",
				Columns:    []string{"author_id"}, RefSchema: "app", RefTable: "authors",
				RefColumns: []string{"id"}, OnDelete: "CASCADE"},
		},
		[]schema.Index{{Name: "books_author_idx", Definition: "CREATE INDEX books_author_idx ON app.books USING btree (author_id)", Columns: []string{"author_id"}}})

	cases := []struct {
		name string
		from *schema.Model
		to   *schema.Model
	}{
		{
			name: "postgres_add_table",
			from: model(authorsFrom),
			to: model(authorsFrom, table("reviews",
				[]schema.Column{
					// identity: PG melaporkan IsIdentity tanpa default.
					{Name: "id", DataType: "bigint", IsIdentity: true, Ordinal: 1},
					column("book_id", "bigint", false, ""),
					column("body", "text", false, "''"),
				},
				[]schema.Constraint{
					{Name: "reviews_pkey", Type: schema.ConstraintPrimaryKey, Definition: "PRIMARY KEY (id)", Columns: []string{"id"}},
					{Name: "reviews_book_fkey", Type: schema.ConstraintForeignKey,
						Definition: "FOREIGN KEY (book_id) REFERENCES app.books(id) ON DELETE CASCADE",
						Columns:    []string{"book_id"}, RefSchema: "app", RefTable: "books",
						RefColumns: []string{"id"}, OnDelete: "CASCADE"},
				},
				[]schema.Index{{Name: "reviews_book_idx", Definition: "CREATE INDEX reviews_book_idx ON app.reviews USING btree (book_id)", Columns: []string{"book_id"}}})),
		},
		{
			name: "postgres_drop_alter_column",
			from: model(table("articles",
				[]schema.Column{
					column("id", "bigint", false, ""),
					column("title", "text", false, ""),
					column("note", "text", true, ""),
					column("subtitle", "text", true, ""),
					column("price", "numeric(10,2)", false, "0"),
				}, nil, nil)),
			to: model(table("articles",
				[]schema.Column{
					column("id", "bigint", false, ""),
					column("title", "character varying(200)", false, ""),
					// note dihapus; subtitle diencangkan; price tipenya berubah.
					column("subtitle", "text", false, "''"),
					column("price", "numeric(12,2)", false, "0"),
				}, nil, nil)),
		},
		{
			name: "postgres_constraint_index_change",
			from: model(booksFrom),
			to: model(table("books",
				[]schema.Column{
					column("id", "bigint", false, ""),
					column("author_id", "bigint", false, ""),
					column("title", "text", false, ""),
					column("price", "numeric(10,2)", false, "0"),
				},
				[]schema.Constraint{
					{Name: "books_pkey", Type: schema.ConstraintPrimaryKey, Definition: "PRIMARY KEY (id)", Columns: []string{"id"}},
					// FK dihapus; check baru ditambahkan.
					{Name: "books_price_check", Type: schema.ConstraintCheck, Definition: "CHECK (price >= (0))", Columns: []string{"price"}},
				},
				[]schema.Index{{Name: "books_author_idx", Definition: "CREATE INDEX books_author_idx ON app.books USING btree (author_id, title)", Columns: []string{"author_id", "title"}}})),
		},
		{
			// Semua fase urutan eksekusi: drop FK -> drop index -> drop column
			// -> drop table -> alter column -> create table -> add column ->
			// add constraint -> add index -> add FK.
			name: "postgres_full_order",
			from: model(authorsFrom, booksFrom, table("attachments",
				[]schema.Column{
					column("id", "bigint", false, ""),
					column("book_id", "bigint", true, ""),
					column("note", "text", false, "''"),
				},
				[]schema.Constraint{
					{Name: "attachments_pkey", Type: schema.ConstraintPrimaryKey, Definition: "PRIMARY KEY (id)", Columns: []string{"id"}},
					{Name: "attachments_book_fkey", Type: schema.ConstraintForeignKey,
						Definition: "FOREIGN KEY (book_id) REFERENCES app.books(id) ON DELETE SET NULL",
						Columns:    []string{"book_id"}, RefSchema: "app", RefTable: "books",
						RefColumns: []string{"id"}, OnDelete: "SET NULL"},
				},
				nil)),
			to: model(table("authors",
				[]schema.Column{
					column("id", "bigint", false, ""),
					column("name", "text", false, ""),
					column("email", "text", true, ""),
					// homepage di-drop, bio ditambah.
					column("bio", "text", true, ""),
				},
				[]schema.Constraint{{Name: "authors_pkey", Type: schema.ConstraintPrimaryKey, Definition: "PRIMARY KEY (id)", Columns: []string{"id"}}},
				[]schema.Index{{Name: "authors_name_key", Definition: "CREATE UNIQUE INDEX authors_name_key ON app.authors USING btree (name)", Unique: true, Columns: []string{"name"}}}),
				table("books",
					[]schema.Column{
						column("id", "bigint", false, ""),
						column("author_id", "bigint", false, ""),
						// title berubah tipe, isbn baru, price berubah tipe.
						column("title", "character varying(300)", false, ""),
						column("price", "numeric(12,2)", false, "0"),
						column("isbn", "character varying(20)", true, ""),
					},
					// FK ke authors dilepas, check baru dipasang.
					[]schema.Constraint{
						{Name: "books_pkey", Type: schema.ConstraintPrimaryKey, Definition: "PRIMARY KEY (id)", Columns: []string{"id"}},
						{Name: "books_price_check", Type: schema.ConstraintCheck, Definition: "CHECK (price >= (0))", Columns: []string{"price"}},
					},
					// index diganti definisinya -> drop + add.
					[]schema.Index{{Name: "books_author_idx", Definition: "CREATE INDEX books_author_idx ON app.books USING btree (author_id, title)", Columns: []string{"author_id", "title"}}}),
				table("reviews",
					[]schema.Column{
						{Name: "id", DataType: "bigint", IsIdentity: true, Ordinal: 1},
						column("book_id", "bigint", false, ""),
					},
					[]schema.Constraint{
						{Name: "reviews_pkey", Type: schema.ConstraintPrimaryKey, Definition: "PRIMARY KEY (id)", Columns: []string{"id"}},
						{Name: "reviews_book_fkey", Type: schema.ConstraintForeignKey,
							Definition: "FOREIGN KEY (book_id) REFERENCES app.books(id) ON DELETE CASCADE",
							Columns:    []string{"book_id"}, RefSchema: "app", RefTable: "books",
							RefColumns: []string{"id"}, OnDelete: "CASCADE"},
					},
					nil)),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changes := schemadiff.Diff(tc.from, tc.to)
			got, err := schemadiff.Generate(schema.KindPostgres, changes)
			require.NoError(t, err)

			var b strings.Builder
			for _, s := range got.Statements {
				b.WriteString(s)
				b.WriteString("\n")
			}
			if len(got.Warnings) > 0 {
				b.WriteString("\n")
				for _, w := range got.Warnings {
					b.WriteString("-- warning: " + w + "\n")
				}
			}

			path := filepath.Join("testdata", tc.name+".sql")
			if *update {
				require.NoError(t, os.MkdirAll("testdata", 0o755))
				require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
			}
			want, err := os.ReadFile(path)
			require.NoError(t, err, "file golden hilang; jalankan: go test ./internal/schemadiff/ -run Golden -update")
			require.Equal(t, string(want), b.String())
		})
	}
}

func TestGenerate_EmptyAndUnsupported(t *testing.T) {
	// Tidak ada perubahan -> tidak ada SQL, bukan error.
	res, err := schemadiff.Generate(schema.KindPostgres, nil)
	require.NoError(t, err)
	require.Empty(t, res.Statements)
	require.Empty(t, res.Warnings)

	// Dialect yang belum ada generator-nya -> error tegas, bukan SQL kosong
	// yang dikira "tidak ada perubahan".
	_, err = schemadiff.Generate(schema.KindMySQL, []schemadiff.Change{{Kind: schemadiff.KindDropTable}})
	require.ErrorIs(t, err, schemadiff.ErrUnsupportedDialect)
}
