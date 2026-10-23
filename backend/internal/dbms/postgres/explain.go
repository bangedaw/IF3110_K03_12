package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration/sqlsplit"
	"github.com/jackc/pgx/v5"
)

type Explainer struct{ Connector Connector }

func (e Explainer) Explain(ctx context.Context, in dbms.ExplainInput) (*dbms.ExplainResult, error) {
	if e.Connector == nil {
		return nil, errors.New("postgres explainer: nil connector")
	}
	stmts, err := sqlsplit.Split(in.SQL, sqlsplit.PostgreSQL)
	if err != nil {
		return nil, err
	}
	if err = sqlsplit.Validate(stmts, sqlsplit.PostgreSQL); err != nil {
		return nil, err
	}
	conn, err := e.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	timeout := in.StatementTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('statement_timeout',$1,true)`, timeout.String()); err != nil {
		return nil, err
	}

	result := &dbms.ExplainResult{Statements: make([]dbms.ExplainStatement, 0, len(stmts))}
	for _, st := range stmts {
		typ := firstKeyword(st.SQL)
		explainable := isExplainablePG(typ)
		item := dbms.ExplainStatement{Index: st.Index, SQL: st.SQL, Explainable: explainable}
		if !explainable {
			if _, err = tx.Exec(ctx, st.SQL); err != nil {
				return nil, fmt.Errorf("statement %d (context DDL): %w", st.Index, err)
			}
			item.Notice = "DDL executed only to establish context; not explainable"
			result.Statements = append(result.Statements, item)
			continue
		}
		opts := "FORMAT JSON, VERBOSE, COSTS"
		if in.Analyze {
			opts += ", ANALYZE, BUFFERS"
		}
		var raw []byte
		err = tx.QueryRow(ctx, "EXPLAIN ("+opts+") "+st.SQL).Scan(&raw)
		if err != nil {
			return nil, fmt.Errorf("explain statement %d: %w", st.Index, err)
		}
		var plan any
		if err = json.Unmarshal(raw, &plan); err != nil {
			return nil, fmt.Errorf("decode plan for statement %d: %w", st.Index, err)
		}
		item.Plan = plan
		result.Statements = append(result.Statements, item)
	}
	return result, nil
}
func firstKeyword(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "--") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = strings.TrimSpace(s[i+1:])
		} else {
			return ""
		}
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(strings.Trim(fields[0], "("))
}
func isExplainablePG(k string) bool {
	switch k {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "WITH":
		return true
	default:
		return false
	}
}

var _ interface {
	Connect(context.Context) (*pgx.Conn, error)
} = DSNConnector{}
