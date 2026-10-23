package schema

import "strings"

var pgTypeAlias = map[string]string{
	"int2": "smallint", "int4": "integer", "int8": "bigint", "int": "integer",
	"bool": "boolean", "float4": "real", "float8": "double precision",
	"decimal": "numeric",
	"varchar": "character varying", "char": "character", "bpchar": "character",
	"timestamptz": "timestamp with time zone", "timetz": "time with time zone",
	"serial": "integer", "bigserial": "bigint", "smallserial": "smallint",
	"varbit": "varbit", "bit varying": "varbit",
}

var mysqlTypeAlias = map[string]string{
	"decimal": "numeric", "numeric": "numeric",
	"varchar": "character varying", "char": "character",
	"int": "integer", "integer": "integer",
	"tinyint": "tinyint", "smallint": "smallint", "mediumint": "mediumint", "bigint": "bigint",
	"datetime": "datetime", "timestamp": "timestamp", "text": "text",
	"longtext": "longtext", "mediumtext": "mediumtext", "tinytext": "tinytext",
	"blob": "blob", "longblob": "longblob", "mediumblob": "mediumblob", "tinyblob": "tinyblob",
	"varbinary": "varbinary", "binary": "binary", "json": "json", "date": "date",
	"time": "time", "year": "year", "bit": "bit", "enum": "enum", "set": "set",
	"float": "float", "double": "double precision", "double precision": "double precision",
}

var mysqlIntWidth = map[string]bool{
	"tinyint": true, "smallint": true, "mediumint": true, "int": true, "integer": true, "bigint": true,
}

func NormalizeType(kind, raw string) string {
	name, args := splitType(collapseSpace(strings.ToLower(strings.TrimSpace(raw))))
	if name == "" {
		return ""
	}
	switch kind {
	case KindMySQL:

		if name == "tinyint" && args == "1" {
			return "boolean"
		}

		if mysqlIntWidth[name] {
			args = ""
		}
		if alias, ok := mysqlTypeAlias[name]; ok {
			name = alias
		}
	case KindPostgres:
		if alias, ok := pgTypeAlias[name]; ok {
			name = alias
		}
	}
	if args == "" {
		return name
	}
	return name + "(" + args + ")"
}

func NormalizeDefault(kind, raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	switch kind {
	case KindPostgres:
		s = stripPGCast(s)
		if strings.EqualFold(s, "now()") || strings.EqualFold(s, "current_timestamp") {
			return "CURRENT_TIMESTAMP"
		}
	case KindMySQL:
		if strings.EqualFold(s, "current_timestamp()") || strings.EqualFold(s, "current_timestamp") {
			return "CURRENT_TIMESTAMP"
		}
	}
	switch strings.ToUpper(s) {
	case "TRUE", "FALSE", "NULL":
		return strings.ToUpper(s)
	}
	return s
}

func stripPGCast(s string) string {
	var b strings.Builder
	inQuote := false
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			if inQuote && i+1 < len(s) && s[i+1] == '\'' {
				b.WriteString("''")
				i++
				continue
			}
			inQuote = !inQuote
		case inQuote:
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ':' && i+1 < len(s) && s[i+1] == ':':
			if depth == 0 {
				return strings.TrimSpace(b.String())
			}
			i = skipCastType(s, i+2) - 1
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func skipCastType(s string, i int) int {
	nested := 0
	for i < len(s) {
		switch s[i] {
		case '(':
			nested++
		case ')':
			if nested == 0 {
				return i
			}
			nested--
		case ',':
			if nested == 0 {
				return i
			}
		}
		i++
	}
	return i
}

func splitType(s string) (name, args string) {
	open := strings.IndexByte(s, '(')
	if open < 0 || !strings.HasSuffix(s, ")") {
		return strings.TrimSpace(s), ""
	}
	return strings.TrimSpace(s[:open]), strings.TrimSpace(s[open+1 : len(s)-1])
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
