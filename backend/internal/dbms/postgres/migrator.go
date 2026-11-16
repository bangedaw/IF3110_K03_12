package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqlsplit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Connector interface {
	Connect(context.Context) (*pgx.Conn, error)
}
type Migrator struct{ Connector Connector }

type StatementError = dbms.StatementError

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (m Migrator) SupportsTransactionalDDL() bool { return true }

func (m Migrator) connectLocked(ctx context.Context, databaseID int64) (*pgx.Conn, func(), error) {
	if m.Connector == nil {
		return nil, nil, errors.New("postgres migrator: nil connector")
	}
	conn, err := m.Connector.Connect(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("connect target: %w", err)
	}
	closeConn := func() { _ = conn.Close(context.Background()) }
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1::bigint)`, databaseID).Scan(&locked); err != nil {
		closeConn()
		return nil, nil, fmt.Errorf("acquire target advisory lock: %w", err)
	}
	if !locked {
		closeConn()
		return nil, nil, ErrLockBusy
	}
	release := func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1::bigint)`, databaseID)
		closeConn()
	}
	return conn, release, nil
}

func setTimeouts(ctx context.Context, tx execer, lockTimeout, statementTimeout time.Duration) error {
	if lockTimeout <= 0 {
		lockTimeout = 5 * time.Second
	}
	if statementTimeout <= 0 {
		statementTimeout = 30 * time.Second
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`, lockTimeout.String()); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT set_config('statement_timeout', $1, true)`, statementTimeout.String())
	return err
}

func ensureHistory(ctx context.Context, q execer) error {
	_, err := q.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS _dbmp_schema_history (
          run_id BIGINT PRIMARY KEY, migration_id BIGINT NOT NULL, version INTEGER NOT NULL,
          checksum BYTEA NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
          direction TEXT NOT NULL DEFAULT 'up'
        )`)
	if err != nil {
		return fmt.Errorf("create target migration history: %w", err)
	}
	_, err = q.Exec(ctx, `ALTER TABLE _dbmp_schema_history ADD COLUMN IF NOT EXISTS direction TEXT NOT NULL DEFAULT 'up'`)
	if err != nil {
		return fmt.Errorf("upgrade target migration history: %w", err)
	}
	return nil
}

func recordHistory(ctx context.Context, q execer, runID, migrationID int64, version int, checksum []byte, direction dbms.Direction) error {
	_, err := q.Exec(ctx, `INSERT INTO _dbmp_schema_history(run_id,migration_id,version,checksum,direction) VALUES($1,$2,$3,$4,$5)`,
		runID, migrationID, version, checksum, string(direction))
	if err != nil {
		return fmt.Errorf("write target migration history: %w", err)
	}
	return nil
}

func (m Migrator) Apply(ctx context.Context, in dbms.ApplyInput) (*dbms.ApplyResult, error) {
	if m.Connector == nil {
		return nil, errors.New("postgres migrator: nil connector")
	}
	direction, err := in.Direction.Normalize()
	if err != nil {
		return nil, err
	}
	conn, release, err := m.connectLocked(ctx, in.DatabaseID)
	if err != nil {
		return nil, err
	}
	defer release()

	stmts, err := sqlsplit.Split(in.SQL, sqlsplit.PostgreSQL)
	if err != nil {
		return nil, err
	}
	if err = sqlsplit.Validate(stmts, sqlsplit.PostgreSQL); err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, errors.New("migration contains no SQL statements")
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin migration transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	if err = setTimeouts(ctx, tx, in.LockTimeout, in.StatementTimeout); err != nil {
		return nil, err
	}

	executed := make([]string, 0, len(stmts))
	for _, st := range stmts {
		if _, err = tx.Exec(ctx, st.SQL); err != nil {
			return nil, statementError(st.Index, st.SQL, err)
		}
		executed = append(executed, st.SQL)
	}
	if err = ensureHistory(ctx, tx); err != nil {
		return nil, err
	}
	if err = recordHistory(ctx, tx, in.RunID, in.MigrationID, in.Version, in.Checksum, direction); err != nil {
		return nil, err
	}
	committed = true
	if err = tx.Commit(ctx); err != nil {
		return nil, commitError(err)
	}
	return &dbms.ApplyResult{StatementsExecuted: len(stmts), Statements: executed, AppliedAt: time.Now().UTC()}, nil
}

func statementError(index int, sql string, err error) *StatementError {
	se := &StatementError{Index: index, SQL: sql, Err: err, Message: err.Error()}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		se.SQLState = pe.Code
		se.Message = pe.Message
		se.Detail = pe.Detail
		se.Hint = pe.Hint
		se.Position = int(pe.Position)
	}
	return se
}

func commitError(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return statementError(0, "COMMIT", err)
	}
	return fmt.Errorf("%w: %w", dbms.ErrCommitUncertain, err)
}

var ErrLockBusy = errors.New("target database already has a migration running")

func (m Migrator) CommittedVersion(ctx context.Context, runID int64) (int, bool, error) {
	conn, err := m.Connector.Connect(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("connect target: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var version int
	err = conn.QueryRow(ctx, `SELECT version FROM _dbmp_schema_history WHERE run_id = $1`, runID).Scan(&version)
	var pe *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows), errors.As(err, &pe) && pe.Code == "42P01":
		return 0, false, nil
	case err != nil:
		return 0, false, err
	}
	return version, true, nil
}

type DSNConnector struct{ DSN string }

func (d DSNConnector) Connect(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, strings.TrimSpace(d.DSN))
}
