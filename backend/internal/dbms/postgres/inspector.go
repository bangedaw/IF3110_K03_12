// Inspector membaca pg_catalog pada database target, bersifat read-only.
// Semua nama objek dari pengguna dilewatkan lewat parameter ($1) atau
// pgx.Identifier.Sanitize sehingga tidak bisa menyuntik SQL.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

type Inspector struct {
	pool *pgxpool.Pool
}

var _ dbms.Inspector = (*Inspector)(nil)

// excludedSchema: schema sistem tidak pernah ditampilkan ke pengguna.
func excludedSchema(name string) bool {
	return name == "information_schema" || strings.HasPrefix(name, "pg_")
}

const listSchemasSQL = `
SELECT nspname
FROM pg_namespace
WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema'
ORDER BY nspname`

func (in *Inspector) ListSchemas(ctx context.Context) ([]string, error) {
	rows, err := in.pool.Query(ctx, listSchemasSQL)
	if err != nil {
		return nil, fmt.Errorf("daftar schema: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("baca schema: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

const listTablesSQL = `
SELECT c.relname, c.reltuples::bigint
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind IN ('r', 'p')
  AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'
ORDER BY c.relname`

func (in *Inspector) ListTables(ctx context.Context, schemaName string) ([]schema.TableSummary, error) {
	if excludedSchema(schemaName) {
		return nil, nil
	}
	rows, err := in.pool.Query(ctx, listTablesSQL, schemaName)
	if err != nil {
		return nil, fmt.Errorf("daftar tabel %s: %w", schemaName, err)
	}
	defer rows.Close()

	// Slice diinisialisasi agar JSON selalu array (bukan null).
	out := []schema.TableSummary{}
	for rows.Next() {
		var ts schema.TableSummary
		if err := rows.Scan(&ts.Name, &ts.RowEstimate); err != nil {
			return nil, fmt.Errorf("baca tabel %s: %w", schemaName, err)
		}
		ts.Schema = schemaName
		ts.Kind = schema.TableKindTable
		ts.RowEstimate = nonNegative(ts.RowEstimate)
		out = append(out, ts)
	}
	return out, rows.Err()
}

func (in *Inspector) DescribeTable(ctx context.Context, schemaName, table string) (*schema.Table, error) {
	if excludedSchema(schemaName) {
		return nil, fmt.Errorf("tabel %s.%s: %w", schemaName, table, dbms.ErrNotFound)
	}
	reg := pgx.Identifier{schemaName, table}.Sanitize()

	kind, estimate, err := in.findTable(ctx, reg)
	if err != nil {
		return nil, fmt.Errorf("tabel %s.%s: %w", schemaName, table, err)
	}

	tb := &schema.Table{
		Schema:      schemaName,
		Name:        table,
		Kind:        kind,
		RowEstimate: nonNegative(estimate),
	}

	cols, attrs, err := in.columns(ctx, reg)
	if err != nil {
		return nil, err
	}
	tb.Columns = cols

	tb.Constraints, err = in.constraints(ctx, reg, attrs)
	if err != nil {
		return nil, err
	}

	tb.Indexes, err = in.indexes(ctx, reg, attrs)
	if err != nil {
		return nil, err
	}
	return tb, nil
}

const findTableSQL = `
SELECT c.relkind::text, c.reltuples::bigint
FROM pg_class c
WHERE c.oid = to_regclass($1)`

func (in *Inspector) findTable(ctx context.Context, reg string) (string, int64, error) {
	var relKind string
	var est int64
	err := in.pool.QueryRow(ctx, findTableSQL, reg).Scan(&relKind, &est)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, dbms.ErrNotFound
	}
	if err != nil {
		return "", 0, fmt.Errorf("cari tabel: %w", err)
	}
	switch relKind {
	case "r", "p":
		return schema.TableKindTable, est, nil
	case "v":
		return schema.TableKindView, est, nil
	default:
		return "", 0, dbms.ErrNotFound
	}
}

const columnsSQL = `
SELECT a.attnum, a.attname, format_type(a.atttypid, a.atttypmod),
       NOT a.attnotnull, COALESCE(pg_get_expr(d.adbin, d.adrelid), ''),
       a.attidentity::text, a.attgenerated::text
FROM pg_attribute a
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE a.attrelid = to_regclass($1) AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`

// columns mengembalikan kolom terurut + pemetaan attnum->nama (dipakai
// constraints/indexes yang merujuk kolom lewat attnum).
func (in *Inspector) columns(ctx context.Context, reg string) ([]schema.Column, map[int16]string, error) {
	rows, err := in.pool.Query(ctx, columnsSQL, reg)
	if err != nil {
		return nil, nil, fmt.Errorf("kolom: %w", err)
	}
	defer rows.Close()

	// Slice diinisialisasi agar JSON selalu berupa array, bukan null.
	cols := []schema.Column{}
	attrs := map[int16]string{}
	for rows.Next() {
		var (
			attnum             int16
			name, rawType      string
			nullable           bool
			def, identity, gen string
		)
		if err := rows.Scan(&attnum, &name, &rawType, &nullable, &def, &identity, &gen); err != nil {
			return nil, nil, fmt.Errorf("baca kolom: %w", err)
		}
		attrs[attnum] = name
		cols = append(cols, schema.Column{
			Name:        name,
			DataType:    schema.NormalizeType(schema.KindPostgres, rawType),
			RawType:     rawType,
			Nullable:    nullable,
			Default:     schema.NormalizeDefault(schema.KindPostgres, def),
			IsIdentity:  identity == "a" || identity == "d",
			IsGenerated: gen != "",
			Ordinal:     int(attnum),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("baca kolom: %w", err)
	}
	return cols, attrs, nil
}

const constraintsSQL = `
SELECT con.conname, con.contype::text, COALESCE(con.conkey, ARRAY[]::smallint[]),
       COALESCE(con.confupdtype::text, ''), COALESCE(con.confdeltype::text, ''),
       pg_get_constraintdef(con.oid),
       COALESCE((SELECT n.nspname
                 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
                 WHERE c.oid = con.confrelid), ''),
       COALESCE((SELECT c.relname FROM pg_class c WHERE c.oid = con.confrelid), ''),
       COALESCE((SELECT array_agg(a.attname ORDER BY o.ord)
                 FROM unnest(con.confkey) WITH ORDINALITY AS o(attnum, ord)
                 JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = o.attnum),
                ARRAY[]::text[])
FROM pg_constraint con
WHERE con.conrelid = to_regclass($1) AND con.contype IN ('p', 'f', 'u', 'c')
ORDER BY con.conname`

func (in *Inspector) constraints(ctx context.Context, reg string, attrs map[int16]string) ([]schema.Constraint, error) {
	rows, err := in.pool.Query(ctx, constraintsSQL, reg)
	if err != nil {
		return nil, fmt.Errorf("constraint: %w", err)
	}
	defer rows.Close()

	out := []schema.Constraint{}
	for rows.Next() {
		var (
			name, contype         string
			conkey                []int16
			updType, delType, def string
			refSchema, refTable   string
			refColumns            []string
		)
		if err := rows.Scan(&name, &contype, &conkey, &updType, &delType, &def, &refSchema, &refTable, &refColumns); err != nil {
			return nil, fmt.Errorf("baca constraint: %w", err)
		}
		c := schema.Constraint{
			Name:       name,
			Type:       constraintType(contype),
			Definition: def,
			Columns:    columnNames(conkey, attrs),
		}
		if contype == "f" {
			c.RefSchema = refSchema
			c.RefTable = refTable
			c.RefColumns = refColumns
			c.OnUpdate = onAction(updType)
			c.OnDelete = onAction(delType)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const indexesSQL = `
SELECT i.relname, ix.indisunique, ix.indisprimary, am.amname::text,
       ix.indkey::text, pg_get_indexdef(ix.indexrelid)
FROM pg_index ix
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_am am ON am.oid = i.relam
WHERE ix.indrelid = to_regclass($1)
ORDER BY i.relname`

func (in *Inspector) indexes(ctx context.Context, reg string, attrs map[int16]string) ([]schema.Index, error) {
	rows, err := in.pool.Query(ctx, indexesSQL, reg)
	if err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}
	defer rows.Close()

	out := []schema.Index{}
	for rows.Next() {
		var (
			name, method, indkey, def string
			unique, primary           bool
		)
		if err := rows.Scan(&name, &unique, &primary, &method, &indkey, &def); err != nil {
			return nil, fmt.Errorf("baca index: %w", err)
		}
		out = append(out, schema.Index{
			Name:       name,
			Columns:    indexColumns(indkey, attrs),
			Unique:     unique,
			Primary:    primary,
			Method:     method,
			Definition: def,
		})
	}
	return out, rows.Err()
}

// Snapshot mengambil seluruh schema non-sistem; dipakai schema diff dan
// disimpan setelah migration sukses.
func (in *Inspector) Snapshot(ctx context.Context) (*schema.Model, error) {
	schemas, err := in.ListSchemas(ctx)
	if err != nil {
		return nil, err
	}
	m := &schema.Model{}
	for _, s := range schemas {
		tables, err := in.ListTables(ctx, s)
		if err != nil {
			return nil, err
		}
		sd := schema.Schema{Name: s}
		for _, ts := range tables {
			tb, err := in.DescribeTable(ctx, s, ts.Name)
			if err != nil {
				return nil, err
			}
			sd.Tables = append(sd.Tables, *tb)
		}
		m.Schemas = append(m.Schemas, sd)
	}
	m.Sort()
	return m, nil
}

func constraintType(contype string) string {
	switch contype {
	case "p":
		return schema.ConstraintPrimaryKey
	case "f":
		return schema.ConstraintForeignKey
	case "u":
		return schema.ConstraintUnique
	default:
		return schema.ConstraintCheck
	}
}

func onAction(code string) string {
	switch code {
	case "a":
		return "NO ACTION"
	case "r":
		return "RESTRICT"
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	default:
		return ""
	}
}

func columnNames(attnums []int16, attrs map[int16]string) []string {
	var out []string
	for _, a := range attnums {
		if name, ok := attrs[a]; ok {
			out = append(out, name)
		}
	}
	return out
}

// indexColumns memparsing indkey (int2vector sebagai teks "1 2 0");
// angka <= 0 berarti kolom ekspresisi — sengaja dilewati, teks ekspresi
// tetap tersedia di Definition.
func indexColumns(indkey string, attrs map[int16]string) []string {
	var out []string
	for _, f := range strings.Fields(indkey) {
		var n int16
		if _, err := fmt.Sscanf(f, "%d", &n); err != nil || n <= 0 {
			continue
		}
		if name, ok := attrs[n]; ok {
			out = append(out, name)
		}
	}
	return out
}

// nonNegative: reltuples -1 = belum pernah di-VACUUM/ANALYZE (PG14+),
// tampilkan 0 sebagai "belum diketahui" alih-alih angka negatif.
func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
