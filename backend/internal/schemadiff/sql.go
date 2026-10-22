package schemadiff

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

// ErrUnsupportedDialect dipakai bila belum ada generator untuk dialect target.
// MySQL menyusul (Task 14); error tegas lebih baik daripada mengembalikan SQL
// kosong yang disalahtafsirkan "tidak ada perubahan".
var ErrUnsupportedDialect = errors.New("belum ada generator SQL untuk database ini")

// Result adalah SQL yang siap dieksekusi beserta peringatan untuk user.
type Result struct {
	Statements []string
	Warnings   []string
}

// Urutan eksekusi aman (spesifikasi §8.10): FK lama dilepas dulu supaya drop
// column/table tidak bentrok, constraint/index lama bersih sebelum kolomnya
// disentuh, table baru dibuat sebelum dikolom/di-constraint, dan FK baru
// paling akhir karena menunggu tabel rujukannya ada.
const (
	phaseDropFK = iota
	phaseDropConstraint
	phaseDropIndex
	phaseDropColumn
	phaseDropTable
	phaseAlterColumn
	phaseCreateTable
	phaseAddColumn
	phaseAddConstraint
	phaseAddIndex
	phaseAddFK
	phaseCount
)

// Generate mengubah daftar perubahan menjadi pernyataan SQL untuk dialect
// tertentu. Urutan pernyataan mengikuti urutan eksekusi aman di atas, bukan
// urutan masuknya perubahan.
func Generate(dialect string, changes []Change) (Result, error) {
	if dialect != "postgres" {
		return Result{}, fmt.Errorf("%w: %q", ErrUnsupportedDialect, dialect)
	}

	g := pgGen{autoIndex: map[string]bool{}}
	// Index yang namanya sama dengan constraint yang ikut dipasang adalah
	// index milik constraint (PK/UNIQUE): PG membuatnya otomatis, jadi
	// CREATE INDEX untuk namanya pasti gagal "sudah ada".
	for _, c := range changes {
		if c.Kind == KindAddConstraint && c.Constraint != nil {
			g.autoIndex[indexKey(c.Schema, c.Table, c.Name)] = true
		}
		if c.Kind == KindAddTable && c.TableDef != nil {
			for _, cs := range c.TableDef.Constraints {
				g.autoIndex[indexKey(c.Schema, c.Table, cs.Name)] = true
			}
		}
	}

	// Buckets per fase; stable agar deterministik.
	buckets := make([][]stmt, phaseCount)
	var out Result

	for _, c := range changes {
		for _, s := range g.statements(c) {
			buckets[s.phase] = append(buckets[s.phase], s)
		}
		out.Warnings = append(out.Warnings, g.warnings(c)...)
	}
	for _, b := range buckets {
		for _, s := range b {
			out.Statements = append(out.Statements, s.sql)
		}
	}
	return out, nil
}

func indexKey(schemaName, table, index string) string {
	return schemaName + "\x00" + table + "\x00" + index
}

type stmt struct {
	phase int
	sql   string
}

type pgGen struct{ autoIndex map[string]bool }

// statements memetakan satu perubahan ke pernyataan + fasesnya.
func (g pgGen) statements(c Change) []stmt {
	switch c.Kind {
	case KindDropTable:
		return []stmt{{phaseDropTable, fmt.Sprintf("DROP TABLE %s", qualified(c.Schema, c.Table))}}
	case KindAddTable:
		return g.addTable(c)
	case KindDropColumn:
		return []stmt{{phaseDropColumn, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s",
			qualified(c.Schema, c.Table), quote(c.Name))}}
	case KindAddColumn:
		if c.Column == nil {
			return nil
		}
		return []stmt{{phaseAddColumn, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s",
			qualified(c.Schema, c.Table), columnDef(*c.Column))}}
	case KindAlterColumn:
		return alterColumn(c)
	case KindDropConstraint:
		return []stmt{{phaseDropConstraint, fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s",
			qualified(c.Schema, c.Table), quote(c.Name))}}
	case KindAddConstraint:
		if c.Constraint == nil || c.Constraint.Definition == "" {
			return nil
		}
		// Definisi dari pg_get_constraintdef sudah persis bagian setelah
		// "ADD CONSTRAINT <nama>" — tempel apa adanya.
		phase := phaseAddConstraint
		if c.Constraint.Type == "foreign_key" {
			phase = phaseAddFK
		}
		return []stmt{{phase, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s",
			qualified(c.Schema, c.Table), quote(c.Name), c.Constraint.Definition)}}
	case KindDropIndex:
		// IF EXISTS: index bisa saja ikut terhapus bersama constraint uniknya.
		return []stmt{{phaseDropIndex, fmt.Sprintf("DROP INDEX IF EXISTS %s",
			qualified(c.Schema, c.Name))}}
	case KindAddIndex:
		return g.addIndex(c)
	default:
		return nil
	}
}

// addTable memecah add_table menjadi CREATE TABLE + constraint/index baru
// (constraint/index mengikuti fase masing-masing supaya FK tetap terakhir).
func (g pgGen) addTable(c Change) []stmt {
	if c.TableDef == nil {
		return nil
	}
	var defs []string
	for _, col := range c.TableDef.Columns {
		if col.IsGenerated {
			// Ekspresi generated column tidak tersimpan di model; SQL yang
			// ditebak akan salah, jadi dilewati dan dilaporkan lewat warning.
			continue
		}
		defs = append(defs, "  "+columnDef(col))
	}
	out := []stmt{{phaseCreateTable, fmt.Sprintf("CREATE TABLE %s (\n%s\n)",
		qualified(c.Schema, c.Table), strings.Join(defs, ",\n"))}}

	for _, cs := range c.TableDef.Constraints {
		out = append(out, g.statements(Change{
			Kind: KindAddConstraint, Schema: c.Schema, Table: c.Table,
			Name: cs.Name, Constraint: &cs,
		})...)
	}
	for _, ix := range c.TableDef.Indexes {
		out = append(out, g.statements(Change{
			Kind: KindAddIndex, Schema: c.Schema, Table: c.Table,
			Name: ix.Name, Index: &ix,
		})...)
	}
	return out
}

func (g pgGen) addIndex(c Change) []stmt {
	if c.Index == nil {
		return nil
	}
	// Index milik constraint PK/UNIQUE sudah dibuat oleh ADD CONSTRAINT;
	// membuat ulang pasti gagal "relation sudah ada".
	if g.autoIndex[indexKey(c.Schema, c.Table, c.Name)] {
		return nil
	}
	// pg_get_indexdef menghasilkan pernyataan CREATE INDEX utuh — pakai
	// apa adanya supaya metode/kolom/klausa tidak salah tulis ulang.
	if c.Index.Definition != "" {
		return []stmt{{phaseAddIndex, c.Index.Definition}}
	}
	return []stmt{{phaseAddIndex, fmt.Sprintf("CREATE INDEX %s ON %s (%s)",
		quote(c.Name), qualified(c.Schema, c.Table), strings.Join(c.Index.Columns, ", "))}}
}

// alterColumn: satu aspek per pernyataan (tipe, nullability, default) supaya
// kegagalan menunjuk tepat pada bagian yang bermasalah.
func alterColumn(c Change) []stmt {
	if c.Column == nil || c.Previous == nil {
		return nil
	}
	prev, cur := *c.Previous, *c.Column
	tbl := qualified(c.Schema, c.Table)
	col := quote(c.Name)
	var out []stmt

	if prev.DataType != cur.DataType {
		out = append(out, stmt{phaseAlterColumn, fmt.Sprintf(
			"ALTER TABLE %s ALTER COLUMN %s TYPE %s USING %s::%s",
			tbl, col, cur.DataType, col, cur.DataType)})
	}
	if prev.Nullable != cur.Nullable {
		clause := "SET NOT NULL"
		if cur.Nullable {
			clause = "DROP NOT NULL"
		}
		out = append(out, stmt{phaseAlterColumn, fmt.Sprintf(
			"ALTER TABLE %s ALTER COLUMN %s %s", tbl, col, clause)})
	}
	if prev.Default != cur.Default {
		clause := "DROP DEFAULT"
		if cur.Default != "" {
			clause = "DEFAULT " + cur.Default
		}
		out = append(out, stmt{phaseAlterColumn, fmt.Sprintf(
			"ALTER TABLE %s ALTER COLUMN %s %s", tbl, col, clause)})
	}
	return out
}

func (g pgGen) warnings(c Change) []string {
	tbl := c.Schema + "." + c.Table
	switch c.Kind {
	case KindDropTable:
		return []string{fmt.Sprintf("tabel %s dan seluruh datanya akan dihapus permanen", tbl)}
	case KindDropColumn:
		return []string{fmt.Sprintf("kolom %s.%s beserta datanya akan dihapus permanen", tbl, c.Name)}
	case KindAlterColumn:
		return alterWarnings(c)
	case KindAddColumn:
		if c.Column != nil && c.Column.IsGenerated {
			return []string{fmt.Sprintf("kolom generated %s.%s tidak bisa dibuat otomatis; tulis definisinya manual", tbl, c.Name)}
		}
	}
	if c.Kind == KindAddTable && c.TableDef != nil {
		var out []string
		for _, col := range c.TableDef.Columns {
			if col.IsGenerated {
				out = append(out, fmt.Sprintf("kolom generated %s.%s tidak ikut dibuat; tulis definisinya manual", tbl, col.Name))
			}
			if strings.HasPrefix(col.Default, "nextval(") {
				out = append(out, fmt.Sprintf("default kolom %s.%s merujuk sekuens; pastikan sekuens sudah ada di database tujuan", tbl, col.Name))
			}
		}
		return out
	}
	return nil
}

func alterWarnings(c Change) []string {
	if c.Column == nil || c.Previous == nil {
		return nil
	}
	prev, cur := *c.Previous, *c.Column
	tbl, name := c.Schema+"."+c.Table, c.Name
	var out []string
	if prev.DataType != cur.DataType {
		out = append(out, fmt.Sprintf("tipe %s.%s diubah %s -> %s; konversi bisa gagal atau memotong data",
			tbl, name, prev.DataType, cur.DataType))
	}
	if prev.Nullable && !cur.Nullable {
		out = append(out, fmt.Sprintf("kolom %s.%s menjadi NOT NULL; baris yang masih NULL akan menggagalkan migrasi", tbl, name))
	}
	return out
}

// columnDef merender definisi kolom untuk CREATE/ADD COLUMN.
func columnDef(c schema.Column) string {
	var b strings.Builder
	b.WriteString(quote(c.Name))
	b.WriteString(" ")
	b.WriteString(c.DataType)
	if c.IsIdentity {
		// Model tidak menyimpan ALWAYS vs BY DEFAULT; keduanya diperlakukan
		// sama karena diff tidak membandingkan gaya identity.
		b.WriteString(" GENERATED BY DEFAULT AS IDENTITY")
	}
	if !c.Nullable {
		b.WriteString(" NOT NULL")
	}
	if c.Default != "" && !c.IsIdentity {
		b.WriteString(" DEFAULT " + c.Default)
	}
	return b.String()
}

func qualified(schemaName, name string) string {
	if schemaName == "" {
		return quote(name)
	}
	return quote(schemaName) + "." + quote(name)
}

func quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
