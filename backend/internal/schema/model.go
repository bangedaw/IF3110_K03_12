package schema

import "sort"

const (
	KindPostgres = "postgres"
	KindMySQL    = "mysql"
)

const (
	TableKindTable = "table"
	TableKindView  = "view"
)

const (
	ConstraintPrimaryKey = "primary_key"
	ConstraintForeignKey = "foreign_key"
	ConstraintUnique     = "unique"
	ConstraintCheck      = "check"
)

type Model struct {
	Schemas []Schema `json:"schemas"`
}

type Schema struct {
	Name   string  `json:"name"`
	Tables []Table `json:"tables"`
}

type TableSummary struct {
	Schema      string `json:"schema"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	RowEstimate int64  `json:"rowEstimate"`
}

type Table struct {
	Schema      string       `json:"schema"`
	Name        string       `json:"name"`
	Kind        string       `json:"kind"`
	RowEstimate int64        `json:"rowEstimate"`
	Columns     []Column     `json:"columns"`
	Constraints []Constraint `json:"constraints"`
	Indexes     []Index      `json:"indexes"`
}

type Column struct {
	Name        string `json:"name"`
	DataType    string `json:"dataType"`
	RawType     string `json:"rawType,omitempty"`
	Nullable    bool   `json:"nullable"`
	Default     string `json:"default,omitempty"`
	IsIdentity  bool   `json:"isIdentity,omitempty"`
	IsGenerated bool   `json:"isGenerated,omitempty"`
	Ordinal     int    `json:"ordinal"`
}

type Constraint struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Columns    []string `json:"columns,omitempty"`
	Definition string   `json:"definition,omitempty"`
	RefSchema  string   `json:"refSchema,omitempty"`
	RefTable   string   `json:"refTable,omitempty"`
	RefColumns []string `json:"refColumns,omitempty"`
	OnUpdate   string   `json:"onUpdate,omitempty"`
	OnDelete   string   `json:"onDelete,omitempty"`
}

type Index struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns,omitempty"`
	Unique     bool     `json:"unique"`
	Primary    bool     `json:"primary"`
	Method     string   `json:"method,omitempty"`
	Definition string   `json:"definition,omitempty"`
}

func (m *Model) Sort() {
	if m == nil {
		return
	}
	sort.Slice(m.Schemas, func(i, j int) bool { return m.Schemas[i].Name < m.Schemas[j].Name })
	for si := range m.Schemas {
		s := &m.Schemas[si]
		sort.Slice(s.Tables, func(i, j int) bool { return s.Tables[i].Name < s.Tables[j].Name })
		for ti := range s.Tables {
			t := &s.Tables[ti]
			sort.Slice(t.Columns, func(i, j int) bool {
				if t.Columns[i].Ordinal != t.Columns[j].Ordinal {
					return t.Columns[i].Ordinal < t.Columns[j].Ordinal
				}
				return t.Columns[i].Name < t.Columns[j].Name
			})
			sort.Slice(t.Constraints, func(i, j int) bool {
				if t.Constraints[i].Type != t.Constraints[j].Type {
					return t.Constraints[i].Type < t.Constraints[j].Type
				}
				return t.Constraints[i].Name < t.Constraints[j].Name
			})
			sort.Slice(t.Indexes, func(i, j int) bool { return t.Indexes[i].Name < t.Indexes[j].Name })
		}
	}
}

func (m *Model) Find(schemaName, table string) (*Table, bool) {
	if m == nil {
		return nil, false
	}
	for si := range m.Schemas {
		if m.Schemas[si].Name != schemaName {
			continue
		}
		for ti := range m.Schemas[si].Tables {
			if m.Schemas[si].Tables[ti].Name == table {
				return &m.Schemas[si].Tables[ti], true
			}
		}
	}
	return nil, false
}
