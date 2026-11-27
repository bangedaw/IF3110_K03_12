package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqlsplit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqltrace"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var batchTracer = otel.Tracer("github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres")

// startBatchSpan membuka span untuk satu transaction batch. Query range, statement batch, checkpoint, dan
// commit menjadi anak span ini, sehingga trace memperlihatkan query mana milik batch ke berapa.
func startBatchSpan(ctx context.Context, direction dbms.Direction, runID int64) (context.Context, trace.Span) {
	return batchTracer.Start(ctx, "migration.batch", trace.WithAttributes(
		attribute.String("migration.direction", string(direction)),
		attribute.Int64("migration.run_id", runID),
	))
}

func endBatchSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

var _ dbms.BatchMigrator = Migrator{}

type batchRun struct {
	RunID       int64
	MigrationID int64
	Version     int
	Checksum    []byte
	Schema      string
	Table       string
	KeyColumn   string
	Size        int
	LowerBound  int64
	UpperBound  int64
}

type rowQuerier interface {
	execer
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func ensureBatchTables(ctx context.Context, q execer) error {
	_, err := q.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS _dbmp_batch_run (
          run_id BIGINT PRIMARY KEY, migration_id BIGINT NOT NULL, version INTEGER NOT NULL,
          checksum BYTEA NOT NULL, schema_name TEXT NOT NULL, table_name TEXT NOT NULL,
          key_column TEXT NOT NULL, batch_size INTEGER NOT NULL,
          lower_bound BIGINT NOT NULL, upper_bound BIGINT NOT NULL,
          created_at TIMESTAMPTZ NOT NULL DEFAULT now(), completed_at TIMESTAMPTZ,
          reverted_at TIMESTAMPTZ, reverted_by BIGINT
        )`)
	if err != nil {
		return fmt.Errorf("create target batch run table: %w", err)
	}
	_, err = q.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS _dbmp_batch_checkpoint (
          run_id BIGINT NOT NULL REFERENCES _dbmp_batch_run (run_id), direction TEXT NOT NULL,
          batch_no INTEGER NOT NULL, cursor_from BIGINT NOT NULL, cursor_to BIGINT NOT NULL,
          rows_affected BIGINT NOT NULL, by_run_id BIGINT NOT NULL,
          committed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
          PRIMARY KEY (run_id, direction, batch_no)
        )`)
	if err != nil {
		return fmt.Errorf("create target batch checkpoint table: %w", err)
	}
	return ensureHistory(ctx, q)
}

func batchStatement(sql string) (string, error) {
	stmts, err := sqlsplit.Split(sql, sqlsplit.PostgreSQL)
	if err != nil {
		return "", err
	}
	if err = sqlsplit.Validate(stmts, sqlsplit.PostgreSQL); err != nil {
		return "", err
	}
	if len(stmts) != 1 {
		return "", fmt.Errorf("%w: ditemukan %d statement", dbms.ErrBatchSQL, len(stmts))
	}
	st := stmts[0].SQL
	if !strings.Contains(st, "$1") || !strings.Contains(st, "$2") || strings.Contains(st, "$3") {
		return "", dbms.ErrBatchSQL
	}
	return st, nil
}

func checkBatchKey(ctx context.Context, q rowQuerier, spec dbms.BatchSpec) error {
	var typ string
	var unique bool
	err := q.QueryRow(ctx, `
        SELECT format_type(a.atttypid, NULL),
               EXISTS (SELECT 1 FROM pg_index i
                       WHERE i.indrelid = c.oid AND i.indisunique AND i.indpred IS NULL
                         AND i.indnkeyatts = 1 AND i.indkey[0] = a.attnum)
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        JOIN pg_attribute a ON a.attrelid = c.oid
        WHERE n.nspname = $1 AND c.relname = $2 AND a.attname = $3 AND NOT a.attisdropped AND a.attnum > 0`,
		spec.Schema, spec.Table, spec.KeyColumn).Scan(&typ, &unique)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: kolom %s.%s.%s tidak ditemukan", dbms.ErrBatchKey, spec.Schema, spec.Table, spec.KeyColumn)
	}
	if err != nil {
		return err
	}
	switch typ {
	case "smallint", "integer", "bigint":
	default:
		return fmt.Errorf("%w: kolom %s bertipe %s", dbms.ErrBatchKey, spec.KeyColumn, typ)
	}
	if !unique {
		return fmt.Errorf("%w: kolom %s tidak memiliki unique index tunggal", dbms.ErrBatchKey, spec.KeyColumn)
	}
	return nil
}

func loadBatchRun(ctx context.Context, q rowQuerier, runID int64) (*batchRun, error) {
	r := batchRun{RunID: runID}
	err := q.QueryRow(ctx, `
        SELECT migration_id, version, checksum, schema_name, table_name, key_column, batch_size, lower_bound, upper_bound
        FROM _dbmp_batch_run WHERE run_id = $1`, runID).
		Scan(&r.MigrationID, &r.Version, &r.Checksum, &r.Schema, &r.Table, &r.KeyColumn, &r.Size, &r.LowerBound, &r.UpperBound)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, dbms.ErrBatchRunUnknown
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func prepareBatchRun(ctx context.Context, q rowQuerier, in dbms.BatchApplyInput) (*batchRun, error) {
	r, err := loadBatchRun(ctx, q, in.RunID)
	if err == nil {
		if r.MigrationID != in.MigrationID || r.Version != in.Version || r.Schema != in.Batch.Schema ||
			r.Table != in.Batch.Table || r.KeyColumn != in.Batch.KeyColumn {
			return nil, dbms.ErrBatchScopeMismatch
		}
		return r, nil
	}
	if !errors.Is(err, dbms.ErrBatchRunUnknown) {
		return nil, err
	}
	if err = checkBatchKey(ctx, q, in.Batch); err != nil {
		return nil, err
	}
	table := pgx.Identifier{in.Batch.Schema, in.Batch.Table}.Sanitize()
	key := pgx.Identifier{in.Batch.KeyColumn}.Sanitize()
	r = &batchRun{
		RunID: in.RunID, MigrationID: in.MigrationID, Version: in.Version, Checksum: in.Checksum,
		Schema: in.Batch.Schema, Table: in.Batch.Table, KeyColumn: in.Batch.KeyColumn, Size: in.Batch.Size,
	}
	err = q.QueryRow(ctx, fmt.Sprintf(`SELECT coalesce(min(%[1]s)::bigint - 1, 0), coalesce(max(%[1]s)::bigint, 0) FROM %[2]s`, key, table)).
		Scan(&r.LowerBound, &r.UpperBound)
	if err != nil {
		return nil, fmt.Errorf("determine batch scope: %w", err)
	}
	_, err = q.Exec(ctx, `
        INSERT INTO _dbmp_batch_run (run_id, migration_id, version, checksum, schema_name, table_name, key_column, batch_size, lower_bound, upper_bound)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		r.RunID, r.MigrationID, r.Version, r.Checksum, r.Schema, r.Table, r.KeyColumn, r.Size, r.LowerBound, r.UpperBound)
	if err != nil {
		return nil, fmt.Errorf("record batch scope: %w", err)
	}
	return r, nil
}

func readBatchProgress(ctx context.Context, q rowQuerier, runID int64) (*dbms.BatchProgress, error) {
	p := dbms.BatchProgress{RunID: runID}
	err := q.QueryRow(ctx, `
        SELECT r.lower_bound, r.upper_bound, r.completed_at IS NOT NULL, r.reverted_at IS NOT NULL,
               count(c.*) FILTER (WHERE c.direction = 'up'),
               coalesce(sum(c.rows_affected) FILTER (WHERE c.direction = 'up'), 0)::bigint,
               coalesce(max(c.cursor_to) FILTER (WHERE c.direction = 'up'), r.lower_bound),
               count(c.*) FILTER (WHERE c.direction = 'down'),
               coalesce(sum(c.rows_affected) FILTER (WHERE c.direction = 'down'), 0)::bigint
        FROM _dbmp_batch_run r
        LEFT JOIN _dbmp_batch_checkpoint c ON c.run_id = r.run_id
        WHERE r.run_id = $1
        GROUP BY r.run_id`, runID).
		Scan(&p.LowerBound, &p.UpperBound, &p.Completed, &p.RevertCompleted,
			&p.BatchesCommitted, &p.RowsCommitted, &p.Cursor, &p.BatchesReverted, &p.RowsReverted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, dbms.ErrBatchRunUnknown
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (m Migrator) BatchProgress(ctx context.Context, runID int64) (*dbms.BatchProgress, error) {
	if m.Connector == nil {
		return nil, errors.New("postgres migrator: nil connector")
	}
	conn, err := m.Connector.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect target: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var exists bool
	if err = conn.QueryRow(ctx, `SELECT to_regclass('_dbmp_batch_run') IS NOT NULL`).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, dbms.ErrBatchRunUnknown
	}
	return readBatchProgress(ctx, conn, runID)
}

func (m Migrator) ApplyBatches(ctx context.Context, in dbms.BatchApplyInput) (*dbms.BatchProgress, error) {
	stmt, err := batchStatement(in.SQL)
	if err != nil {
		return nil, err
	}
	if in.Batch.Size <= 0 {
		return nil, errors.New("ukuran batch harus lebih dari 0")
	}
	ctx = sqltrace.With(ctx,
		slog.Int64("database_id", in.DatabaseID), slog.Int64("migration_id", in.MigrationID),
		slog.Int64("run_id", in.RunID), slog.Int("version", in.Version), slog.String("direction", string(dbms.DirectionUp)),
		slog.String("mode", "batch"), slog.Int("batch_size", in.Batch.Size))
	conn, release, err := m.connectLocked(ctx, in.DatabaseID)
	if err != nil {
		return nil, err
	}
	defer release()
	if err = ensureBatchTables(ctx, conn); err != nil {
		return nil, err
	}
	run, err := prepareBatchRun(ctx, conn, in)
	if err != nil {
		return nil, err
	}
	progress, err := readBatchProgress(ctx, conn, run.RunID)
	if err != nil {
		return nil, err
	}
	if progress.BatchesReverted > 0 || progress.RevertCompleted {
		return progress, dbms.ErrBatchRunReverted
	}
	if progress.Completed {
		return progress, nil
	}
	if progress.BatchesCommitted > 0 {
		slog.InfoContext(ctx, "migration.batch.resume", "event", "migration.batch.resume",
			"batches_committed", progress.BatchesCommitted, "rows_committed", progress.RowsCommitted, "cursor", progress.Cursor)
	}

	key := pgx.Identifier{run.KeyColumn}.Sanitize()
	bound := fmt.Sprintf(`SELECT max(%[1]s)::bigint FROM (SELECT %[1]s FROM %[2]s WHERE %[1]s > $1 AND %[1]s <= $2 ORDER BY %[1]s LIMIT $3) b`,
		key, pgx.Identifier{run.Schema, run.Table}.Sanitize())
	for {
		if err = ctx.Err(); err != nil {
			return progress, err
		}
		done, err := applyOneBatch(ctx, conn, in, run, progress, stmt, bound)
		if err != nil {
			return progress, err
		}
		if done {
			break
		}
		if in.OnBatch != nil {
			in.OnBatch(*progress)
		}
	}
	if err = finishBatchRun(ctx, conn, run); err != nil {
		return progress, err
	}
	progress.Completed = true
	return progress, nil
}

func applyOneBatch(ctx context.Context, conn *pgx.Conn, in dbms.BatchApplyInput, run *batchRun, p *dbms.BatchProgress, stmt, bound string) (_ bool, err error) {
	batchNo := p.BatchesCommitted + 1
	ctx, span := startBatchSpan(ctx, dbms.DirectionUp, run.RunID)
	defer func() { endBatchSpan(span, err) }()
	span.SetAttributes(attribute.Int("migration.batch.number", batchNo), attribute.Int64("migration.batch.cursor_from", p.Cursor))
	ctx = sqltrace.With(ctx, slog.Int("batch", batchNo))
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin batch %d: %w", batchNo, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	if err = setTimeouts(ctx, tx, in.LockTimeout, in.StatementTimeout); err != nil {
		return false, err
	}
	var end pgtype.Int8
	if err = tx.QueryRow(ctx, bound, p.Cursor, run.UpperBound, run.Size).Scan(&end); err != nil {
		return false, fmt.Errorf("determine batch %d range: %w", batchNo, err)
	}
	if !end.Valid {
		// Tidak ada row tersisa: transaction ini hanya memastikan cakupan sudah habis.
		span.SetAttributes(attribute.Bool("migration.batch.empty", true))
		return true, nil
	}
	tag, err := tx.Exec(ctx, stmt, p.Cursor, end.Int64)
	if err != nil {
		se := statementError(1, stmt, err)
		se.Batch = batchNo
		return false, se
	}
	_, err = tx.Exec(ctx, `
        INSERT INTO _dbmp_batch_checkpoint (run_id, direction, batch_no, cursor_from, cursor_to, rows_affected, by_run_id)
        VALUES ($1, 'up', $2, $3, $4, $5, $1)`, run.RunID, batchNo, p.Cursor, end.Int64, tag.RowsAffected())
	if err != nil {
		return false, fmt.Errorf("write checkpoint batch %d: %w", batchNo, err)
	}
	committed = true
	if err = tx.Commit(ctx); err != nil {
		return false, batchCommitError(batchNo, err)
	}
	from := p.Cursor
	span.SetAttributes(attribute.Int64("migration.batch.cursor_to", end.Int64), attribute.Int64("migration.batch.rows", tag.RowsAffected()))
	p.BatchesCommitted = batchNo
	p.RowsCommitted += tag.RowsAffected()
	p.Cursor = end.Int64
	slog.InfoContext(ctx, "migration.batch.committed", "event", "migration.batch.committed", "status", "committed",
		"rows", tag.RowsAffected(), "cursor_from", from, "cursor_to", end.Int64, "rows_committed", p.RowsCommitted)
	return false, nil
}

func finishBatchRun(ctx context.Context, conn *pgx.Conn, run *batchRun) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	if _, err = tx.Exec(ctx, `UPDATE _dbmp_batch_run SET completed_at = now() WHERE run_id = $1`, run.RunID); err != nil {
		return err
	}
	if err = recordHistory(ctx, tx, run.RunID, run.MigrationID, run.Version, run.Checksum, dbms.DirectionUp); err != nil {
		return err
	}
	committed = true
	if err = tx.Commit(ctx); err != nil {
		return commitError(err)
	}
	slog.InfoContext(ctx, "migration.batch.completed", "event", "migration.batch.completed", "status", "completed")
	return nil
}

func (m Migrator) RevertBatches(ctx context.Context, in dbms.BatchRevertInput) (*dbms.BatchProgress, error) {
	if strings.TrimSpace(in.SQL) == "" {
		return nil, dbms.ErrIrreversible
	}
	stmt, err := batchStatement(in.SQL)
	if err != nil {
		return nil, err
	}
	ctx = sqltrace.With(ctx,
		slog.Int64("database_id", in.DatabaseID), slog.Int64("run_id", in.ByRunID), slog.Int64("reverts_run_id", in.RunID),
		slog.String("direction", string(dbms.DirectionDown)), slog.String("mode", "batch"))
	conn, release, err := m.connectLocked(ctx, in.DatabaseID)
	if err != nil {
		return nil, err
	}
	defer release()
	if err = ensureBatchTables(ctx, conn); err != nil {
		return nil, err
	}
	run, err := loadBatchRun(ctx, conn, in.RunID)
	if err != nil {
		return nil, err
	}
	progress, err := readBatchProgress(ctx, conn, run.RunID)
	if err != nil {
		return nil, err
	}
	if progress.RevertCompleted {
		return progress, nil
	}
	for {
		if err = ctx.Err(); err != nil {
			return progress, err
		}
		done, err := revertOneBatch(ctx, conn, in, run, progress, stmt)
		if err != nil {
			return progress, err
		}
		if done {
			break
		}
		if in.OnBatch != nil {
			in.OnBatch(*progress)
		}
	}
	progress.RevertCompleted = true
	return progress, nil
}

func revertOneBatch(ctx context.Context, conn *pgx.Conn, in dbms.BatchRevertInput, run *batchRun, p *dbms.BatchProgress, stmt string) (_ bool, err error) {
	ctx, span := startBatchSpan(ctx, dbms.DirectionDown, in.ByRunID)
	defer func() { endBatchSpan(span, err) }()
	span.SetAttributes(attribute.Int64("migration.reverts_run_id", run.RunID))
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	if err = setTimeouts(ctx, tx, in.LockTimeout, in.StatementTimeout); err != nil {
		return false, err
	}
	var batchNo int
	var from, to int64
	err = tx.QueryRow(ctx, `
        SELECT u.batch_no, u.cursor_from, u.cursor_to
        FROM _dbmp_batch_checkpoint u
        WHERE u.run_id = $1 AND u.direction = 'up'
          AND NOT EXISTS (SELECT 1 FROM _dbmp_batch_checkpoint d
                          WHERE d.run_id = u.run_id AND d.direction = 'down' AND d.batch_no = u.batch_no)
        ORDER BY u.batch_no DESC
        LIMIT 1`, run.RunID).Scan(&batchNo, &from, &to)
	if errors.Is(err, pgx.ErrNoRows) {
		// Semua batch sudah dibatalkan: transaction ini menandai run selesai dibatalkan dan mencatat riwayat.
		span.SetAttributes(attribute.Bool("migration.batch.empty", true))
		if _, err = tx.Exec(ctx, `UPDATE _dbmp_batch_run SET reverted_at = now(), reverted_by = $2 WHERE run_id = $1`, run.RunID, in.ByRunID); err != nil {
			return false, err
		}
		if err = recordHistory(ctx, tx, in.ByRunID, run.MigrationID, run.Version, run.Checksum, dbms.DirectionDown); err != nil {
			return false, err
		}
		committed = true
		if err = tx.Commit(ctx); err != nil {
			return false, commitError(err)
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	span.SetAttributes(attribute.Int("migration.batch.number", batchNo),
		attribute.Int64("migration.batch.cursor_from", from), attribute.Int64("migration.batch.cursor_to", to))
	ctx = sqltrace.With(ctx, slog.Int("batch", batchNo))
	tag, err := tx.Exec(ctx, stmt, from, to)
	if err != nil {
		se := statementError(1, stmt, err)
		se.Batch = batchNo
		return false, se
	}
	_, err = tx.Exec(ctx, `
        INSERT INTO _dbmp_batch_checkpoint (run_id, direction, batch_no, cursor_from, cursor_to, rows_affected, by_run_id)
        VALUES ($1, 'down', $2, $3, $4, $5, $6)`, run.RunID, batchNo, from, to, tag.RowsAffected(), in.ByRunID)
	if err != nil {
		return false, fmt.Errorf("write revert checkpoint batch %d: %w", batchNo, err)
	}
	committed = true
	if err = tx.Commit(ctx); err != nil {
		return false, batchCommitError(batchNo, err)
	}
	span.SetAttributes(attribute.Int64("migration.batch.rows", tag.RowsAffected()))
	p.BatchesReverted++
	p.RowsReverted += tag.RowsAffected()
	slog.InfoContext(ctx, "migration.batch.reverted", "event", "migration.batch.reverted", "status", "reverted",
		"rows", tag.RowsAffected(), "cursor_from", from, "cursor_to", to, "rows_reverted", p.RowsReverted)
	return false, nil
}

func batchCommitError(batchNo int, err error) error {
	err = commitError(err)
	var se *StatementError
	if errors.As(err, &se) {
		se.Batch = batchNo
	}
	return err
}
