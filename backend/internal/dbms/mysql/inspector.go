// Inspector membaca information_schema pada database target, bersifat
// read-only. MySQL tidak punya "schema" terpisah: schema = nama database, dan
// nama database/kolom/tabel dilewatkan lewat parameter sehingga tidak bisa
// menyuntik SQL.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

type Inspector struct {
	db *sql.DB
}

var _ dbms.Inspector = (*Inspector)(nil)

// excludedSchema: schema sistem tidak pernah ditampilkan ke pengguna.
func excludedSchema(name string) bool {
	switch name {
	case "information_schema", "mysql", "performance_schema", "sys":
		return true
	}
	return false
}

const listSchemasSQL = `
SELECT SCHEMA_NAME
FROM information_schema.SCHEMATA
WHERE SCHEMA_NAME NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys')
ORDER BY SCHEMA_NAME`

func (in *Inspector) ListSchemas(ctx context.Context) ([]string, error) {
	rows, err := in.db.QueryContext(ctx, listSchemasSQL)
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

// TABLE_ROWS adalah estimasi InnoDB (bisa 0 bila belum pernah di-analyze);
// tetap dipakai sebagai "totalEstimate" seperti reltuples di PostgreSQL.
const listTablesSQL = `
SELECT TABLE_NAME, COALESCE(TABLE_ROWS, 0)
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'
ORDER BY TABLE_NAME`

func (in *Inspector) ListTables(ctx context.Context, schemaName string) ([]schema.TableSummary, error) {
	if excludedSchema(schemaName) {
		return nil, nil
	}
	rows, err := in.db.QueryContext(ctx, listTablesSQL, schemaName)
	if err != nil {
		return nil, fmt.Errorf("daftar tabel %s: %w", schemaName, err)
	}
	defer rows.Close()

	// Slice diinisialisasi agar JSON selalu array (bukan null).
	out := []schema.TableSummary{}
	for rows.Next() {
		var ts schema.TableSummary
		var est int64
		if err := rows.Scan(&ts.Name, &est); err != nil {
			return nil, fmt.Errorf("baca tabel %s: %w", schemaName, err)
		}
		ts.Schema = schemaName
		ts.Kind = schema.TableKindTable
		ts.RowEstimate = nonNegative(est)
		out = append(out, ts)
	}
	return out, rows.Err()
}

const findTableSQL = `
SELECT TABLE_TYPE
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`

func (in *Inspector) DescribeTable(ctx context.Context, schemaName, table string) (*schema.Table, error) {
	if excludedSchema(schemaName) {
		return nil, fmt.Errorf("tabel %s.%s: %w", schemaName, table, dbms.ErrNotFound)
	}

	var tableType string
	err := in.db.QueryRowContext(ctx, findTableSQL, schemaName, table).Scan(&tableType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("tabel %s.%s: %w", schemaName, table, dbms.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("cari tabel: %w", err)
	}

	tb := &schema.Table{
		Schema: schemaName,
		Name:   table,
		Kind:   schema.TableKindTable,
	}
	if tableType == "VIEW" {
		tb.Kind = schema.TableKindView
	} else if tableType != "BASE TABLE" {
		return nil, fmt.Errorf("tabel %s.%s: %w", schemaName, table, dbms.ErrNotFound)
	}

	cols, err := in.columns(ctx, schemaName, table)
	if err != nil {
		return nil, err
	}
	tb.Columns = cols
	tb.RowEstimate = in.tableEstimate(ctx, schemaName, table)

	tb.Constraints, err = in.constraints(ctx, schemaName, table)
	if err != nil {
		return nil, err
	}

	tb.Indexes, err = in.indexes(ctx, schemaName, table)
	if err != nil {
		return nil, err
	}
	return tb, nil
}

const tableEstimateSQL = `
SELECT COALESCE(TABLE_ROWS, 0)
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`

func (in *Inspector) tableEstimate(ctx context.Context, schemaName, table string) int64 {
	var est int64
	if err := in.db.QueryRowContext(ctx, tableEstimateSQL, schemaName, table).Scan(&est); err != nil {
		return 0
	}
	return nonNegative(est)
}

const columnsSQL = `
SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA, ORDINAL_POSITION,
       COALESCE(GENERATION_EXPRESSION, '')
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY ORDINAL_POSITION`

func (in *Inspector) columns(ctx context.Context, schemaName, table string) ([]schema.Column, error) {
	rows, err := in.db.QueryContext(ctx, columnsSQL, schemaName, table)
	if err != nil {
		return nil, fmt.Errorf("kolom: %w", err)
	}
	defer rows.Close()

	// Slice diinisialisasi agar JSON selalu berupa array, bukan null.
	cols := []schema.Column{}
	for rows.Next() {
		var (
			name, rawType, nullable, extra, genExpr string
			ordinal                                 int64
			def                                     sql.NullString
		)
		if err := rows.Scan(&name, &rawType, &nullable, &def, &extra, &ordinal, &genExpr); err != nil {
			return nil, fmt.Errorf("baca kolom: %w", err)
		}
		cols = append(cols, schema.Column{
			Name:        name,
			DataType:    schema.NormalizeType(schema.KindMySQL, rawType),
			RawType:     rawType,
			Nullable:    nullable == "YES",
			Default:     schema.NormalizeDefault(schema.KindMySQL, def.String),
			IsIdentity:  strings.Contains(extra, "auto_increment"),
			IsGenerated: genExpr != "" || strings.Contains(extra, "GENERATED"),
			Ordinal:     int(ordinal),
		})
	}
	return cols, rows.Err()
}

// constraints menggabungkan TABLE_CONSTRAINTS + KEY_COLUMN_USAGE (kolom, FK
// rujukan) + REFERENTIAL_CONSTRAINTS (aksi) + CHECK_CONSTRAINTS (ekspresi).
// Definisi direkonstruksi karena MySQL tidak menyimpan SQL mentah constraint.
func (in *Inspector) constraints(ctx context.Context, schemaName, table string) ([]schema.Constraint, error) {
	type pending struct {
		schema.Constraint
		hasRef bool
	}
	byName := map[string]*pending{}
	var order []string

	// 1) Daftar constraint + jenisnya.
	rows, err := in.db.QueryContext(ctx, `
SELECT CONSTRAINT_NAME, CONSTRAINT_TYPE
FROM information_schema.TABLE_CONSTRAINTS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
  AND CONSTRAINT_TYPE IN ('PRIMARY KEY', 'UNIQUE', 'FOREIGN KEY', 'CHECK')
ORDER BY CONSTRAINT_NAME`, schemaName, table)
	if err != nil {
		return nil, fmt.Errorf("constraint: %w", err)
	}
	for rows.Next() {
		var name, ctype string
		if err := rows.Scan(&name, &ctype); err != nil {
			rows.Close()
			return nil, fmt.Errorf("baca constraint: %w", err)
		}
		order = append(order, name)
		byName[name] = &pending{Constraint: schema.Constraint{
			Name: name,
			Type: mapConstraintType(ctype),
		}}
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("baca constraint: %w", err)
	}

	// 2) Kolom (dan rujukan FK) per constraint, terurut ordinal.
	krows, err := in.db.QueryContext(ctx, `
SELECT CONSTRAINT_NAME, COLUMN_NAME, ORDINAL_POSITION,
       REFERENCED_TABLE_SCHEMA, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
FROM information_schema.KEY_COLUMN_USAGE
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY CONSTRAINT_NAME, ORDINAL_POSITION`, schemaName, table)
	if err != nil {
		return nil, fmt.Errorf("kolom constraint: %w", err)
	}
	defer krows.Close()
	for krows.Next() {
		var name, col string
		var ordinal int64
		var refSchema, refTable, refCol sql.NullString
		if err := krows.Scan(&name, &col, &ordinal, &refSchema, &refTable, &refCol); err != nil {
			return nil, fmt.Errorf("baca kolom constraint: %w", err)
		}
		p, ok := byName[name]
		if !ok {
			continue
		}
		p.Columns = append(p.Columns, col)
		if refCol.Valid {
			p.hasRef = true
			p.RefSchema = refSchema.String
			p.RefTable = refTable.String
			p.RefColumns = append(p.RefColumns, refCol.String)
		}
	}
	if err := krows.Err(); err != nil {
		return nil, fmt.Errorf("baca kolom constraint: %w", err)
	}

	// 3) Aksi ON DELETE/UPDATE FK.
	frows, err := in.db.QueryContext(ctx, `
SELECT CONSTRAINT_NAME, DELETE_RULE, UPDATE_RULE
FROM information_schema.REFERENTIAL_CONSTRAINTS
WHERE CONSTRAINT_SCHEMA = ? AND TABLE_NAME = ?`, schemaName, table)
	if err != nil {
		return nil, fmt.Errorf("constraint rujukan: %w", err)
	}
	defer frows.Close()
	for frows.Next() {
		var name, del, upd string
		if err := frows.Scan(&name, &del, &upd); err != nil {
			return nil, fmt.Errorf("baca constraint rujukan: %w", err)
		}
		if p, ok := byName[name]; ok {
			p.OnDelete = ruleAction(del)
			p.OnUpdate = ruleAction(upd)
		}
	}
	if err := frows.Err(); err != nil {
		return nil, fmt.Errorf("baca constraint rujukan: %w", err)
	}

	// 4) Ekspresi CHECK.
	crows, err := in.db.QueryContext(ctx, `
SELECT tc.CONSTRAINT_NAME, cc.CHECK_CLAUSE
FROM information_schema.TABLE_CONSTRAINTS tc
JOIN information_schema.CHECK_CONSTRAINTS cc
  ON cc.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA
 AND cc.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
WHERE tc.TABLE_SCHEMA = ? AND tc.TABLE_NAME = ? AND tc.CONSTRAINT_TYPE = 'CHECK'`,
		schemaName, table)
	if err != nil {
		return nil, fmt.Errorf("constraint check: %w", err)
	}
	defer crows.Close()
	for crows.Next() {
		var name, clause string
		if err := crows.Scan(&name, &clause); err != nil {
			return nil, fmt.Errorf("baca constraint check: %w", err)
		}
		if p, ok := byName[name]; ok {
			p.Definition = clause
		}
	}
	if err := crows.Err(); err != nil {
		return nil, fmt.Errorf("baca constraint check: %w", err)
	}

	out := []schema.Constraint{}
	for _, name := range order {
		p := byName[name]
		if p.Definition == "" {
			p.Definition = reconstructDefinition(p.Constraint, p.hasRef)
		}
		out = append(out, p.Constraint)
	}
	return out, nil
}

// reconstructDefinition membangun teks definisi ringkas untuk PK/UNIQUE/FK
// (CHECK sudah punya CHECK_CLAUSE). Cukup untuk tampilan dan diff.
func reconstructDefinition(c schema.Constraint, hasRef bool) string {
	cols := backtickList(c.Columns)
	switch c.Type {
	case schema.ConstraintPrimaryKey:
		return "PRIMARY KEY (" + cols + ")"
	case schema.ConstraintUnique:
		return "UNIQUE (" + cols + ")"
	case schema.ConstraintForeignKey:
		if !hasRef {
			return ""
		}
		def := fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s (%s)",
			cols, backtickQualified(c.RefSchema, c.RefTable), backtickList(c.RefColumns))
		if c.OnDelete != "" && c.OnDelete != "NO ACTION" {
			def += " ON DELETE " + c.OnDelete
		}
		if c.OnUpdate != "" && c.OnUpdate != "NO ACTION" {
			def += " ON UPDATE " + c.OnUpdate
		}
		return def
	}
	return ""
}

// indexes: satu baris per kolom index di STATISTICS; PRIMARY = index unik
// bawaan PK, dan kolom UNIQUE column juga muncul sebagai index unik.
const indexesSQL = `
SELECT INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME, INDEX_TYPE
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY INDEX_NAME, SEQ_IN_INDEX`

func (in *Inspector) indexes(ctx context.Context, schemaName, table string) ([]schema.Index, error) {
	rows, err := in.db.QueryContext(ctx, indexesSQL, schemaName, table)
	if err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}
	defer rows.Close()

	byName := map[string]*schema.Index{}
	var order []string
	for rows.Next() {
		var name, method string
		var nonUnique, seq int64
		var col sql.NullString
		if err := rows.Scan(&name, &nonUnique, &seq, &col, &method); err != nil {
			return nil, fmt.Errorf("baca index: %w", err)
		}
		ix, ok := byName[name]
		if !ok {
			ix = &schema.Index{
				Name:    name,
				Unique:  nonUnique == 0,
				Primary: name == "PRIMARY",
				Method:  strings.ToLower(method),
			}
			byName[name] = ix
			order = append(order, name)
		}
		if col.Valid {
			ix.Columns = append(ix.Columns, col.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("baca index: %w", err)
	}

	out := []schema.Index{}
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, nil
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
		if len(tables) == 0 {
			continue
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

func mapConstraintType(ctype string) string {
	switch ctype {
	case "PRIMARY KEY":
		return schema.ConstraintPrimaryKey
	case "FOREIGN KEY":
		return schema.ConstraintForeignKey
	case "UNIQUE":
		return schema.ConstraintUnique
	default:
		return schema.ConstraintCheck
	}
}

func ruleAction(rule string) string {
	switch strings.ToUpper(rule) {
	case "CASCADE":
		return "CASCADE"
	case "RESTRICT":
		return "RESTRICT"
	case "SET NULL":
		return "SET NULL"
	case "SET DEFAULT":
		return "SET DEFAULT"
	case "NO ACTION":
		return "NO ACTION"
	default:
		return ""
	}
}

func backtickList(cols []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = backtick(c)
	}
	return strings.Join(parts, ", ")
}

func backtickQualified(schemaName, name string) string {
	if schemaName == "" {
		return backtick(name)
	}
	return backtick(schemaName) + "." + backtick(name)
}

func backtick(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}

// nonNegative: TABLE_ROWS bisa negatif pada galat statistik; tampilkan 0
// sebagai "belum diketahui" alih-alih angka negatif.
func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
