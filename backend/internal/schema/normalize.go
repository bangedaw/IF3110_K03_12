package schema

import "strings"

// Normalisasi membuat tipe/default dari DBMS berbeda bisa dibandingkan.
// Batasan yang disengaja: perbandingan bersifat teks setelah normalisasi,
// jadi ekspresi default yang rumit bisa menghasilkan false positive pada diff.

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

// NormalizeType mengubah tipe mentah DBMS menjadi bentuk kanonik.
func NormalizeType(kind, raw string) string {
	name, args := splitType(collapseSpace(strings.ToLower(strings.TrimSpace(raw))))
	if name == "" {
		return ""
	}
	switch kind {
	case KindMySQL:
		// tinyint(1) adalah boolean de facto di MySQL.
		if name == "tinyint" && args == "1" {
			return "boolean"
		}
		// Display width pada tipe bilangan tidak mengubah arti; buang.
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

// NormalizeDefault membandingkan default expression secara lebih setara:
// membuang cast ::tipe khas PostgreSQL dan menyeragamkan CURRENT_TIMESTAMP.
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

// stripPGCast membuang suffix `::tipe` di luar literal string.
func stripPGCast(s string) string {
	inQuote := false
	for i := 0; i+1 < len(s); i++ {
		switch s[i] {
		case '\'':
			// '' di dalam string adalah quote yang di-escape.
			if inQuote && s[i+1] == '\'' {
				i++
				continue
			}
			inQuote = !inQuote
		case ':':
			if !inQuote && s[i+1] == ':' {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return s
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
