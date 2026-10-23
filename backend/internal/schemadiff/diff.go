package schemadiff

import (
	"sort"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

const (
	KindDropTable      = "drop_table"
	KindDropColumn     = "drop_column"
	KindAlterColumn    = "alter_column"
	KindAddColumn      = "add_column"
	KindDropConstraint = "drop_constraint"
	KindAddConstraint  = "add_constraint"
	KindDropIndex      = "drop_index"
	KindAddIndex       = "add_index"
	KindAddTable       = "add_table"
)

var kindRank = map[string]int{
	KindDropTable:      0,
	KindDropColumn:     1,
	KindAlterColumn:    2,
	KindAddColumn:      3,
	KindDropConstraint: 4,
	KindAddConstraint:  5,
	KindDropIndex:      6,
	KindAddIndex:       7,

	KindAddTable: 8,
}

type Change struct {
	Kind        string `json:"kind"`
	Schema      string `json:"schema"`
	Table       string `json:"table"`
	Name        string `json:"name"`
	Destructive bool   `json:"destructive"`

	TableDef *schema.Table `json:"tableDef,omitempty"`

	Column   *schema.Column `json:"column,omitempty"`
	Previous *schema.Column `json:"previous,omitempty"`

	Constraint *schema.Constraint `json:"constraint,omitempty"`
	Index      *schema.Index      `json:"index,omitempty"`
}

func AlignModels(a, b *schema.Model) {
	if a == nil || b == nil || len(a.Schemas) != 1 || len(b.Schemas) != 1 {
		return
	}
	if a.Schemas[0].Name == b.Schemas[0].Name {
		return
	}
	b.Schemas[0].Name = a.Schemas[0].Name
	for i := range b.Schemas[0].Tables {
		b.Schemas[0].Tables[i].Schema = a.Schemas[0].Name
	}
}

func Diff(a, b *schema.Model) []Change {
	var out []Change

	aTables := tablesBySchema(a)
	bTables := tablesBySchema(b)

	for _, sname := range sortedKeys(aTables) {
		for _, t := range aTables[sname] {
			if _, ok := bTables[sname]; !ok || !hasTable(bTables[sname], t.Name) {
				out = append(out, Change{
					Kind: KindDropTable, Schema: sname, Table: t.Name, Name: t.Name,
					Destructive: true, TableDef: &t,
				})
			}
		}
	}
	for _, sname := range sortedKeys(bTables) {
		for _, t := range bTables[sname] {
			if _, ok := aTables[sname]; !ok || !hasTable(aTables[sname], t.Name) {
				out = append(out, Change{
					Kind: KindAddTable, Schema: sname, Table: t.Name, Name: t.Name,
					TableDef: &t,
				})
			}
		}
	}

	for _, sname := range sortedKeys(aTables) {
		oldTables := aTables[sname]
		newTables, ok := bTables[sname]
		if !ok {
			continue
		}
		for _, old := range oldTables {
			cur, ok := findTable(newTables, old.Name)
			if !ok {
				continue
			}
			if old.Kind != cur.Kind {
				out = append(out, Change{
					Kind: KindDropTable, Schema: sname, Table: old.Name, Name: old.Name,
					Destructive: true, TableDef: &old,
				}, Change{
					Kind: KindAddTable, Schema: sname, Table: cur.Name, Name: cur.Name,
					TableDef: &cur,
				})
				continue
			}
			out = append(out, diffTable(sname, old, cur)...)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := kindRank[out[i].Kind], kindRank[out[j].Kind]
		if ri != rj {
			return ri < rj
		}
		if out[i].Schema != out[j].Schema {
			return out[i].Schema < out[j].Schema
		}
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func diffTable(sname string, old, cur schema.Table) []Change {
	var out []Change

	oldCols := indexColumns(old.Columns)
	newCols := indexColumns(cur.Columns)

	for _, name := range sortedKeys(oldCols) {
		if _, ok := newCols[name]; !ok {
			c := oldCols[name]
			out = append(out, Change{
				Kind: KindDropColumn, Schema: sname, Table: old.Name, Name: name,
				Destructive: true, Column: &c,
			})
		}
	}
	for _, name := range sortedKeys(newCols) {
		cur := newCols[name]
		prev, ok := oldCols[name]
		if !ok {
			out = append(out, Change{
				Kind: KindAddColumn, Schema: sname, Table: old.Name, Name: name,
				Column: &cur,
			})
			continue
		}
		if prev.DataType != cur.DataType || prev.Default != cur.Default || prev.Nullable != cur.Nullable {
			destructive := (prev.Nullable && !cur.Nullable) || cur.IsGenerated || cur.IsIdentity
			out = append(out, Change{
				Kind: KindAlterColumn, Schema: sname, Table: old.Name, Name: name,
				Destructive: destructive, Column: &cur, Previous: &prev,
			})
		}
	}

	out = append(out, diffConstraints(sname, old.Name, old.Constraints, cur.Constraints)...)
	out = append(out, diffIndexes(sname, old.Name, old.Indexes, cur.Indexes)...)
	return out
}

func diffConstraints(sname, table string, old, cur []schema.Constraint) []Change {
	var out []Change
	oldM, newM := indexConstraints(old), indexConstraints(cur)

	for _, name := range sortedKeys(oldM) {
		if _, ok := newM[name]; !ok {
			c := oldM[name]
			out = append(out, Change{
				Kind: KindDropConstraint, Schema: sname, Table: table, Name: name,
				Constraint: &c,
			})
		}
	}
	for _, name := range sortedKeys(newM) {
		c := newM[name]
		prev, ok := oldM[name]
		if !ok {
			out = append(out, Change{
				Kind: KindAddConstraint, Schema: sname, Table: table, Name: name,
				Constraint: &c,
			})
			continue
		}
		if !constraintEqual(prev, c) {
			out = append(out, Change{
				Kind: KindDropConstraint, Schema: sname, Table: table, Name: name,
				Constraint: &prev,
			}, Change{
				Kind: KindAddConstraint, Schema: sname, Table: table, Name: name,
				Constraint: &c,
			})
		}
	}
	return out
}

func diffIndexes(sname, table string, old, cur []schema.Index) []Change {
	var out []Change
	oldM, newM := indexIndexes(old), indexIndexes(cur)

	for _, name := range sortedKeys(oldM) {
		if _, ok := newM[name]; !ok {
			i := oldM[name]
			out = append(out, Change{
				Kind: KindDropIndex, Schema: sname, Table: table, Name: name,
				Index: &i,
			})
		}
	}
	for _, name := range sortedKeys(newM) {
		i := newM[name]
		prev, ok := oldM[name]
		if !ok {
			out = append(out, Change{
				Kind: KindAddIndex, Schema: sname, Table: table, Name: name,
				Index: &i,
			})
			continue
		}
		if !indexEqual(prev, i) {
			out = append(out, Change{
				Kind: KindDropIndex, Schema: sname, Table: table, Name: name,
				Index: &prev,
			}, Change{
				Kind: KindAddIndex, Schema: sname, Table: table, Name: name,
				Index: &i,
			})
		}
	}
	return out
}

func constraintEqual(a, b schema.Constraint) bool {
	return a.Type == b.Type && a.Definition == b.Definition &&
		a.RefSchema == b.RefSchema && a.RefTable == b.RefTable &&
		a.OnDelete == b.OnDelete && a.OnUpdate == b.OnUpdate &&
		slicesEqual(a.Columns, b.Columns) && slicesEqual(a.RefColumns, b.RefColumns)
}

func indexEqual(a, b schema.Index) bool {
	return a.Definition == b.Definition && a.Unique == b.Unique &&
		a.Primary == b.Primary && a.Method == b.Method &&
		slicesEqual(a.Columns, b.Columns)
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func tablesBySchema(m *schema.Model) map[string][]schema.Table {
	out := map[string][]schema.Table{}
	if m == nil {
		return out
	}
	for _, s := range m.Schemas {
		out[s.Name] = s.Tables
	}
	return out
}

func indexColumns(cs []schema.Column) map[string]schema.Column {
	out := map[string]schema.Column{}
	for _, c := range cs {
		out[c.Name] = c
	}
	return out
}

func indexConstraints(cs []schema.Constraint) map[string]schema.Constraint {
	out := map[string]schema.Constraint{}
	for _, c := range cs {
		out[c.Name] = c
	}
	return out
}

func indexIndexes(is []schema.Index) map[string]schema.Index {
	out := map[string]schema.Index{}
	for _, i := range is {
		out[i.Name] = i
	}
	return out
}

func findTable(ts []schema.Table, name string) (schema.Table, bool) {
	for _, t := range ts {
		if t.Name == name {
			return t, true
		}
	}
	return schema.Table{}, false
}

func hasTable(ts []schema.Table, name string) bool {
	_, ok := findTable(ts, name)
	return ok
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
