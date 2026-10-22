package schemadiff

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

// myGen menghasilkan DDL MySQL dari daftar perubahan. Perbedaan mendasar dari
// PostgreSQL: identifier di-quote backtick, ALTER COLUMN = MODIFY (menyatakan
// ulang seluruh definisi), constraint PK/UNIQUE/FK dibuang dengan sintaks
// berbeda-beda, dan index tidak bisa di-IF EXISTS-kan.
type myGen struct {
	autoIndex      map[string]bool
	dropConstraint map[string]bool // nama constraint yang dibuang (index-nya ikut hilang)
	dropPK         map[string]bool // tabel yang PK-nya dibuang (index PRIMARY ikut hilang)
}

func newMySQLGen(changes []Change) *myGen {
	g := &myGen{
		autoIndex:      map[string]bool{},
		dropConstraint: map[string]bool{},
		dropPK:         map[string]bool{},
	}
	// Pre-scan: index milik constraint (PK/UNIQUE) tidak boleh dibuat ulang
	// lewat CREATE INDEX, dan tidak boleh di-DROP dua kali setelah
	// constraint-nya dibuang.
	for _, c := range changes {
		key := indexKey(c.Schema, c.Table, c.Name)
		switch c.Kind {
		case KindAddConstraint:
			if c.Constraint != nil {
				g.autoIndex[key] = true
			}
		case KindDropConstraint:
			if c.Constraint != nil {
				g.dropConstraint[key] = true
				if c.Constraint.Type == schema.ConstraintPrimaryKey {
					g.dropPK[indexKey(c.Schema, c.Table, "")] = true
				}
			}
		case KindAddTable:
			if c.TableDef != nil {
				for _, cs := range c.TableDef.Constraints {
					g.autoIndex[indexKey(c.Schema, c.Table, cs.Name)] = true
				}
			}
		}
	}
	return g
}

func (g *myGen) statements(c Change) []stmt {
	switch c.Kind {
	case KindDropTable:
		return []stmt{{phaseDropTable, fmt.Sprintf("DROP TABLE %s", myQualified(c.Schema, c.Table))}}
	case KindAddTable:
		return g.addTable(c)
	case KindDropColumn:
		return []stmt{{phaseDropColumn, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s",
			myQualified(c.Schema, c.Table), myQuote(c.Name))}}
	case KindAddColumn:
		if c.Column == nil {
			return nil
		}
		return []stmt{{phaseAddColumn, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s",
			myQualified(c.Schema, c.Table), myColumnDef(*c.Column))}}
	case KindAlterColumn:
		return g.alterColumn(c)
	case KindDropConstraint:
		return g.dropConstraintStmt(c)
	case KindAddConstraint:
		return g.addConstraint(c)
	case KindDropIndex:
		return g.dropIndex(c)
	case KindAddIndex:
		return g.addIndex(c)
	default:
		return nil
	}
}

// dropConstraintStmt: MySQL tidak punya DROP CONSTRAINT umum.
func (g *myGen) dropConstraintStmt(c Change) []stmt {
	if c.Constraint == nil {
		return nil
	}
	tbl := myQualified(c.Schema, c.Table)
	switch c.Constraint.Type {
	case schema.ConstraintPrimaryKey:
		return []stmt{{phaseDropConstraint, fmt.Sprintf("ALTER TABLE %s DROP PRIMARY KEY", tbl)}}
	case schema.ConstraintForeignKey:
		return []stmt{{phaseDropConstraint, fmt.Sprintf("ALTER TABLE %s DROP FOREIGN KEY %s",
			tbl, myQuote(c.Name))}}
	case schema.ConstraintUnique:
		// UNIQUE = index unik; membuangnya = DROP INDEX.
		return []stmt{{phaseDropConstraint, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s",
			tbl, myQuote(c.Name))}}
	default: // check
		return []stmt{{phaseDropConstraint, fmt.Sprintf("ALTER TABLE %s DROP CHECK %s",
			tbl, myQuote(c.Name))}}
	}
}

func (g *myGen) addConstraint(c Change) []stmt {
	if c.Constraint == nil || c.Constraint.Definition == "" {
		return nil
	}
	tbl := myQualified(c.Schema, c.Table)
	// PK: simbol nama diabaikan MySQL; cukup ADD PRIMARY KEY (kolom).
	if c.Constraint.Type == schema.ConstraintPrimaryKey {
		return []stmt{{phaseAddConstraint, fmt.Sprintf("ALTER TABLE %s ADD PRIMARY KEY (%s)",
			tbl, myQuoteList(c.Constraint.Columns))}}
	}
	phase := phaseAddConstraint
	if c.Constraint.Type == schema.ConstraintForeignKey {
		phase = phaseAddFK
	}
	// Definisi dari information_schema sudah persis bagian setelah
	// "ADD CONSTRAINT <nama>" — tempel apa adanya.
	return []stmt{{phase, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s",
		tbl, myQuote(c.Name), c.Constraint.Definition)}}
}

// dropIndex: MySQL tidak mengenal IF EXISTS untuk DROP INDEX; index yang
// namanya sama dengan constraint yang baru dibuang sudah ikut hilang dan
// dilewati oleh pre-scan.
func (g *myGen) dropIndex(c Change) []stmt {
	key := indexKey(c.Schema, c.Table, c.Name)
	if g.dropConstraint[key] {
		return nil
	}
	if c.Index != nil && c.Index.Primary && g.dropPK[indexKey(c.Schema, c.Table, "")] {
		return nil
	}
	return []stmt{{phaseDropIndex, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s",
		myQualified(c.Schema, c.Table), myQuote(c.Name))}}
}

func (g *myGen) addIndex(c Change) []stmt {
	if c.Index == nil {
		return nil
	}
	// Index milik constraint PK/UNIQUE sudah dibuat oleh ADD CONSTRAINT.
	if g.autoIndex[indexKey(c.Schema, c.Table, c.Name)] {
		return nil
	}
	unique := ""
	if c.Index.Unique {
		unique = "UNIQUE "
	}
	return []stmt{{phaseAddIndex, fmt.Sprintf("CREATE %sINDEX %s ON %s (%s)",
		unique, myQuote(c.Name), myQualified(c.Schema, c.Table), myQuoteList(c.Index.Columns))}}
}

func (g *myGen) addTable(c Change) []stmt {
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
		defs = append(defs, "  "+myColumnDef(col))
	}
	// PK inline di CREATE TABLE: MySQL menolak AUTO_INCREMENT yang bukan key
	// (Error 1075), dan ADD PRIMARY KEY baru dieksekusi setelah fase create.
	for _, cs := range c.TableDef.Constraints {
		if cs.Type == schema.ConstraintPrimaryKey {
			defs = append(defs, "  PRIMARY KEY ("+myQuoteList(cs.Columns)+")")
		}
	}
	out := []stmt{{phaseCreateTable, fmt.Sprintf("CREATE TABLE %s (\n%s\n)",
		myQualified(c.Schema, c.Table), strings.Join(defs, ",\n"))}}

	for _, cs := range c.TableDef.Constraints {
		if cs.Type == schema.ConstraintPrimaryKey {
			continue // sudah inline di CREATE TABLE
		}
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

// alterColumn: MySQL hanya punya MODIFY COLUMN — satu pernyataan yang
// menyatakan ulang seluruh definisi kolom (tipe, null, default, auto_increment).
func (g *myGen) alterColumn(c Change) []stmt {
	if c.Column == nil {
		return nil
	}
	return []stmt{{phaseAlterColumn, fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s",
		myQualified(c.Schema, c.Table), myColumnDef(*c.Column))}}
}

func (g *myGen) warnings(c Change) []string {
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
		}
		return out
	}
	return nil
}

// myColumnDef merender definisi kolom MySQL. Urutan atribut mengikuti
// tata bahasa MySQL: tipe, NULL/NOT NULL, DEFAULT, AUTO_INCREMENT.
// RawType diprioritaskan (tipe mentah persis dari information_schema,
// mis. varchar(120) atau enum('a','b')); DataType kanonik tidak valid sebagai
// DDL MySQL (character varying tidak dikenal).
func myColumnDef(c schema.Column) string {
	typ := c.RawType
	if typ == "" {
		typ = c.DataType
	}
	var b strings.Builder
	b.WriteString(myQuote(c.Name))
	b.WriteString(" ")
	b.WriteString(typ)
	if c.Nullable {
		b.WriteString(" NULL")
	} else {
		b.WriteString(" NOT NULL")
	}
	if c.Default != "" && !c.IsIdentity {
		b.WriteString(" DEFAULT " + myDefault(c.Default))
	}
	if c.IsIdentity {
		b.WriteString(" AUTO_INCREMENT")
	}
	return b.String()
}

// myDefault merender nilai default dari COLUMN_DEFAULT MySQL (literal tak-
// berversir untuk string, angka apa adanya). Heuristic yang disengaja:
// literal numerik/fungsi/kelompok dieksekusi apa adanya, sisanya di-quote.
// ponytail: cukup untuk tipe default MySQL umum; bila muncul tipe eksotis
// (bit literal, expression default MySQL 8.0.13), tambahkan kasus di sini.
func myDefault(v string) string {
	up := strings.ToUpper(v)
	if up == "NULL" || up == "CURRENT_TIMESTAMP" || strings.HasPrefix(up, "CURRENT_TIMESTAMP(") {
		return v
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return v // literal numerik
	}
	if strings.Contains(v, "(") {
		return v // pemanggilan fungsi / ekspresi berkelompok
	}
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

func myQualified(schemaName, name string) string {
	if schemaName == "" {
		return myQuote(name)
	}
	return myQuote(schemaName) + "." + myQuote(name)
}

func myQuote(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}

func myQuoteList(idents []string) string {
	parts := make([]string, len(idents))
	for i, id := range idents {
		parts[i] = myQuote(id)
	}
	return strings.Join(parts, ", ")
}
