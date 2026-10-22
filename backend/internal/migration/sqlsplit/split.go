package sqlsplit

import (
    "fmt"
    "strings"
    "unicode"
)

type Dialect string
const (
    PostgreSQL Dialect = "postgres"
    MySQL Dialect = "mysql"
)

type Statement struct {
    Index int    `json:"index"`
    SQL   string `json:"sql"`
}

type ValidationError struct { StatementIndex int; Message string }
func (e *ValidationError) Error() string {
    if e.StatementIndex > 0 { return fmt.Sprintf("statement %d: %s", e.StatementIndex, e.Message) }
    return e.Message
}

func Split(script string, dialect Dialect) ([]Statement, error) {
    var out []Statement
    start := 0
    i, n := 0, len(script)
    single, double, backtick := false, false, false
    lineComment, blockDepth := false, 0
    dollarTag := ""
    for i < n {
        c := script[i]
        if lineComment {
            if c == '\n' { lineComment = false }
            i++; continue
        }
        if blockDepth > 0 {
            if i+1 < n && script[i:i+2] == "/*" { blockDepth++; i+=2; continue }
            if i+1 < n && script[i:i+2] == "*/" { blockDepth--; i+=2; continue }
            i++; continue
        }
        if dollarTag != "" {
            if strings.HasPrefix(script[i:], dollarTag) { i += len(dollarTag); dollarTag = ""; continue }
            i++; continue
        }
        if single {
            if c == '\\' && dialect == MySQL && i+1 < n { i+=2; continue }
            if c == '\'' {
                if i+1 < n && script[i+1] == '\'' { i+=2; continue }
                single = false
            }
            i++; continue
        }
        if double {
            if c == '"' {
                if i+1 < n && script[i+1] == '"' { i+=2; continue }
                double = false
            }
            i++; continue
        }
        if backtick {
            if c == '`' {
                if i+1 < n && script[i+1] == '`' { i+=2; continue }
                backtick = false
            }
            i++; continue
        }
        if i+1 < n && script[i:i+2] == "--" { lineComment = true; i+=2; continue }
        if i+1 < n && script[i:i+2] == "/*" { blockDepth = 1; i+=2; continue }
        if c == '\'' { single = true; i++; continue }
        if c == '"' { double = true; i++; continue }
        if c == '`' && dialect == MySQL { backtick = true; i++; continue }
        if dialect == PostgreSQL && c == '$' {
            if tag, ok := readDollarTag(script[i:]); ok { dollarTag = tag; i += len(tag); continue }
        }
        if c == ';' {
            if s := strings.TrimSpace(script[start:i]); s != "" {
                out = append(out, Statement{Index: len(out)+1, SQL: s})
            }
            start = i+1
        }
        i++
    }
    if single || double || backtick || dollarTag != "" || blockDepth > 0 {
        return nil, fmt.Errorf("unterminated quoted string, identifier, dollar-quote, or block comment")
    }
    if s := strings.TrimSpace(script[start:]); s != "" {
        out = append(out, Statement{Index: len(out)+1, SQL: s})
    }
    return out, nil
}

func readDollarTag(s string) (string, bool) {
    if len(s) < 2 || s[0] != '$' { return "", false }
    for i:=1; i<len(s); i++ {
        if s[i] == '$' { return s[:i+1], true }
        r := rune(s[i])
        if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') { return "", false }
    }
    return "", false
}

func Validate(stmts []Statement, dialect Dialect) error {
    for _, st := range stmts {
        normalized := strings.ToUpper(strings.TrimSpace(stripLeadingComments(st.SQL)))
        fields := strings.Fields(normalized)
        if len(fields) == 0 { continue }
        first := strings.Trim(fields[0], ";")
        if first == "BEGIN" || first == "START" && len(fields)>1 && fields[1]=="TRANSACTION" ||
           first == "COMMIT" || first == "END" || first == "ROLLBACK" || first == "SAVEPOINT" ||
           first == "RELEASE" && len(fields)>1 && fields[1]=="SAVEPOINT" {
            return &ValidationError{StatementIndex: st.Index, Message: "transaction-control statements are managed by the platform"}
        }
        if dialect == PostgreSQL {
            joined := strings.Join(fields, " ")
            if first == "VACUUM" || first == "CREATE DATABASE" || first == "CREATE" && containsSequence(fields, "INDEX", "CONCURRENTLY") ||
               strings.HasPrefix(joined, "CREATE INDEX CONCURRENTLY") || strings.HasPrefix(joined, "CREATE UNIQUE INDEX CONCURRENTLY") {
                return &ValidationError{StatementIndex: st.Index, Message: "statement is not allowed inside a managed PostgreSQL migration transaction"}
            }
        }
    }
    return nil
}

func containsSequence(fields []string, a, b string) bool {
    for i:=0; i+1<len(fields); i++ { if fields[i]==a && fields[i+1]==b { return true } }
    return false
}

func stripLeadingComments(s string) string {
    for {
        s = strings.TrimSpace(s)
        if strings.HasPrefix(s, "--") {
            if i := strings.IndexByte(s, '\n'); i >= 0 { s=s[i+1:]; continue }
            return ""
        }
        if strings.HasPrefix(s, "/*") {
            if i := strings.Index(s, "*/"); i >= 0 { s=s[i+2:]; continue }
        }
        return s
    }
}
