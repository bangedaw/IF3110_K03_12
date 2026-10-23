package schemadiff

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

type myGen struct {
	autoIndex      map[string]bool
	dropConstraint map[string]bool
	dropPK         map[string]bool
}

func newMySQLGen(changes []Change) *myGen {
	g := &myGen{
		autoIndex:      map[string]bool{},
		dropConstraint: map[string]bool{},
		dropPK:         map[string]bool{},
	}

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

		return []stmt{{phaseDropConstraint, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s",
			tbl, myQuote(c.Name))}}
	default:
		return []stmt{{phaseDropConstraint, fmt.Sprintf("ALTER TABLE %s DROP CHECK %s",
			tbl, myQuote(c.Name))}}
	}
}

func (g *myGen) addConstraint(c Change) []stmt {
	if c.Constraint == nil || c.Constraint.Definition == "" {
		return nil
	}
	tbl := myQualified(c.Schema, c.Table)

	if c.Constraint.Type == schema.ConstraintPrimaryKey {
		return []stmt{{phaseAddConstraint, fmt.Sprintf("ALTER TABLE %s ADD PRIMARY KEY (%s)",
			tbl, myQuoteList(c.Constraint.Columns))}}
	}
	phase := phaseAddConstraint
	if c.Constraint.Type == schema.ConstraintForeignKey {
		phase = phaseAddFK
	}

	return []stmt{{phase, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s",
		tbl, myQuote(c.Name), c.Constraint.Definition)}}
}

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

			continue
		}
		defs = append(defs, "  "+myColumnDef(col))
	}

	for _, cs := range c.TableDef.Constraints {
		if cs.Type == schema.ConstraintPrimaryKey {
			defs = append(defs, "  PRIMARY KEY ("+myQuoteList(cs.Columns)+")")
		}
	}
	out := []stmt{{phaseCreateTable, fmt.Sprintf("CREATE TABLE %s (\n%s\n)",
		myQualified(c.Schema, c.Table), strings.Join(defs, ",\n"))}}

	for _, cs := range c.TableDef.Constraints {
		if cs.Type == schema.ConstraintPrimaryKey {
			continue
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

func myDefault(v string) string {
	up := strings.ToUpper(v)
	if up == "NULL" || up == "CURRENT_TIMESTAMP" || strings.HasPrefix(up, "CURRENT_TIMESTAMP(") {
		return v
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return v
	}
	if strings.Contains(v, "(") {
		return v
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
