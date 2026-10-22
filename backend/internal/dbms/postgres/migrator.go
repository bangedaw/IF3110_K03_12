package postgres

import (
    "context"
    "errors"
    "fmt"
    "strings"
    "time"

    "example.com/dbmp/backend/internal/dbms"
    "example.com/dbmp/backend/internal/migration/sqlsplit"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgconn"
)

type Connector interface { Connect(context.Context) (*pgx.Conn, error) }
type Migrator struct { Connector Connector }

type StatementError struct {
    Index int
    SQLState string
    Message string
    Detail string
    Hint string
    Position int
    Err error
}
func (e *StatementError) Error() string { return fmt.Sprintf("statement %d failed: %s", e.Index, e.Message) }
func (e *StatementError) Unwrap() error { return e.Err }

func (m Migrator) SupportsTransactionalDDL() bool { return true }

func (m Migrator) Apply(ctx context.Context, in dbms.ApplyInput) (*dbms.ApplyResult, error) {
    if m.Connector == nil { return nil, errors.New("postgres migrator: nil connector") }
    conn, err := m.Connector.Connect(ctx)
    if err != nil { return nil, fmt.Errorf("connect target: %w", err) }
    defer conn.Close(context.Background())

    var locked bool
    if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1, $2)`, int32(in.DatabaseID>>32), int32(in.DatabaseID)).Scan(&locked); err != nil {
        return nil, fmt.Errorf("acquire target advisory lock: %w", err)
    }
    if !locked { return nil, ErrLockBusy }
    defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1, $2)`, int32(in.DatabaseID>>32), int32(in.DatabaseID))

    stmts, err := sqlsplit.Split(in.SQL, sqlsplit.PostgreSQL)
    if err != nil { return nil, err }
    if err = sqlsplit.Validate(stmts, sqlsplit.PostgreSQL); err != nil { return nil, err }
    if len(stmts)==0 { return nil, errors.New("migration contains no SQL statements") }

    tx, err := conn.Begin(ctx)
    if err != nil { return nil, fmt.Errorf("begin migration transaction: %w", err) }
    committed := false
    defer func() { if !committed { _ = tx.Rollback(context.Background()) } }()
    lockTimeout := in.LockTimeout; if lockTimeout <= 0 { lockTimeout=5*time.Second }
    statementTimeout := in.StatementTimeout; if statementTimeout <= 0 { statementTimeout=30*time.Second }
    if _, err = tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`, lockTimeout.String()); err != nil { return nil, err }
    if _, err = tx.Exec(ctx, `SELECT set_config('statement_timeout', $1, true)`, statementTimeout.String()); err != nil { return nil, err }

    for _, st := range stmts {
        if _, err = tx.Exec(ctx, st.SQL); err != nil {
            se := &StatementError{Index:st.Index, Err:err, Message:err.Error()}
            var pe *pgconn.PgError
            if errors.As(err, &pe) {
                se.SQLState=pe.Code; se.Message=pe.Message; se.Detail=pe.Detail; se.Hint=pe.Hint
                if pe.Position!="" { fmt.Sscan(pe.Position, &se.Position) }
            }
            return nil, se
        }
    }
    _, err = tx.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS _dbmp_schema_history (
          run_id BIGINT PRIMARY KEY, migration_id BIGINT NOT NULL, version INTEGER NOT NULL,
          checksum BYTEA NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
        )`)
    if err != nil { return nil, fmt.Errorf("create target migration history: %w", err) }
    _, err = tx.Exec(ctx, `INSERT INTO _dbmp_schema_history(run_id,migration_id,version,checksum) VALUES($1,$2,$3,$4)`,
        in.RunID, in.MigrationID, in.Version, in.Checksum)
    if err != nil { return nil, fmt.Errorf("write target migration history: %w", err) }
    if err = tx.Commit(ctx); err != nil { return nil, fmt.Errorf("commit migration: %w", err) }
    committed=true
    return &dbms.ApplyResult{StatementsExecuted:len(stmts), AppliedAt:time.Now().UTC()}, nil
}

var ErrLockBusy = errors.New("target database already has a migration running")

type DSNConnector struct { DSN string }
func (d DSNConnector) Connect(ctx context.Context) (*pgx.Conn, error) { return pgx.Connect(ctx, strings.TrimSpace(d.DSN)) }
