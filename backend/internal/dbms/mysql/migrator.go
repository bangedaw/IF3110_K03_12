package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration/sqlsplit"
	mysqldrv "github.com/go-sql-driver/mysql"
)

type Migrator struct{ DB *sql.DB }

func (m Migrator) SupportsTransactionalDDL() bool { return false }

const errNoSuchTable = 1146

func (m Migrator) CommittedVersion(ctx context.Context, runID int64) (int, bool, error) {
	var version int
	err := m.DB.QueryRowContext(ctx, `SELECT version FROM _dbmp_schema_history WHERE run_id = ?`, runID).Scan(&version)
	var me *mysqldrv.MySQLError
	switch {
	case errors.Is(err, sql.ErrNoRows), errors.As(err, &me) && me.Number == errNoSuchTable:
		return 0, false, nil
	case err != nil:
		return 0, false, err
	}
	return version, true, nil
}

type StatementError struct {
	Index          int
	LastSuccessful int
	Err            error
}

func (e *StatementError) Error() string {
	return fmt.Sprintf("MySQL statement %d failed (last successful statement: %d): %v", e.Index, e.LastSuccessful, e.Err)
}
func (e *StatementError) Unwrap() error { return e.Err }

func (e *StatementError) StatementIndex() int { return e.Index }

func (m Migrator) Apply(ctx context.Context, in dbms.ApplyInput) (*dbms.ApplyResult, error) {
	if m.DB == nil {
		return nil, errors.New("mysql migrator: nil DB")
	}
	stmts, err := sqlsplit.Split(in.SQL, sqlsplit.MySQL)
	if err != nil {
		return nil, err
	}
	if err = sqlsplit.Validate(stmts, sqlsplit.MySQL); err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, errors.New("migration contains no SQL statements")
	}
	conn, err := m.DB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect target: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS _dbmp_schema_history (
      run_id BIGINT PRIMARY KEY, migration_id BIGINT NOT NULL, version INT NOT NULL,
      checksum VARBINARY(32) NOT NULL, applied_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
    )`); err != nil {
		return nil, fmt.Errorf("create target migration history: %w", err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin migration transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	last := 0
	for _, st := range stmts {
		if _, err = tx.ExecContext(ctx, st.SQL); err != nil {
			return nil, &StatementError{Index: st.Index, LastSuccessful: last, Err: err}
		}
		last = st.Index
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO _dbmp_schema_history(run_id,migration_id,version,checksum) VALUES(?,?,?,?)`,
		in.RunID, in.MigrationID, in.Version, in.Checksum); err != nil {
		return nil, fmt.Errorf("write target migration history (schema changes may already exist): %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit migration: %w", err)
	}
	committed = true
	return &dbms.ApplyResult{StatementsExecuted: len(stmts), AppliedAt: time.Now().UTC()}, nil
}
