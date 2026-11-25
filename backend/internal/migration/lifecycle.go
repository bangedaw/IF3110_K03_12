package migration

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqltrace"
)

type Lifecycle struct {
	Pool  *pgxpool.Pool
	Auth  authz.Authorizer
	Audit *audit.Logger
}

var _ queue.Lifecycle = Lifecycle{}

func (l Lifecycle) Start(ctx context.Context, in queue.StartInput) (*queue.Execution, error) {
	var (
		exec     *queue.Execution
		run      gen.MigrationRun
		rejected error
	)
	err := pgx.BeginFunc(ctx, l.Pool, func(tx pgx.Tx) error {
		q := gen.New(tx)
		req, err := q.LockMigrationRequest(ctx, in.RequestID)
		if err != nil {
			return missing(err)
		}
		row, err := q.GetMigration(ctx, req.MigrationID)
		if err != nil {
			return missing(err)
		}
		m := row.Migration
		if req.Status != gen.RequestStatusQueued && req.Status != gen.RequestStatusRunning {
			return conflict("request tidak sedang menunggu eksekusi")
		}
		rejected, err = l.revalidate(ctx, q, in, req, m)
		if err != nil {
			return err
		}
		if rejected != nil {
			status := gen.RequestStatusFailed
			committed, err := l.committed(ctx, q, in.JobID, m)
			if err != nil {
				return err
			}
			if committed {
				status = gen.RequestStatusPartial
			}
			return q.SetMigrationRequestStatus(ctx, gen.SetMigrationRequestStatusParams{ID: req.ID, Status: status})
		}
		if run, err = l.insertRun(ctx, q, in, req, m); err != nil {
			return err
		}
		exec = execution(run, m)
		return q.SetMigrationRequestStatus(ctx, gen.SetMigrationRequestStatusParams{ID: req.ID, Status: gen.RequestStatusRunning})
	})
	if err != nil {
		return nil, err
	}
	if rejected != nil {
		slog.WarnContext(ctx, "migration.revalidate", "event", "migration.revalidate", "status", "rejected", "error", rejected.Error())
		return nil, rejected
	}
	slog.InfoContext(ctx, "migration.execution.start", append(runAttrs("migration.execution.start", run), "migration_id", run.MigrationID)...)
	return exec, nil
}

func (l Lifecycle) revalidate(ctx context.Context, q *gen.Queries, in queue.StartInput, req gen.MigrationRequest, m gen.Migration) (rejected, err error) {
	level, err := l.Auth.EffectiveLevel(ctx, in.ActorID, m.DatabaseID)
	if err != nil {
		return nil, err
	}
	if level != authz.LevelEdit {
		return apperr.New(apperr.CodeForbidden, "pengguna yang menjalankan tidak lagi punya akses View + Edit"), nil
	}
	if req.Revision != m.Revision {
		return conflict("migration berubah setelah disetujui; ajukan ulang"), nil
	}
	groups, err := q.ListRequestApprovals(ctx, gen.ListRequestApprovalsParams{DatabaseID: m.DatabaseID, RequestID: req.ID})
	if err != nil {
		return nil, err
	}
	if !approved(groups) {
		return conflict("approval belum lengkap dari leader setiap group yang punya akses"), nil
	}
	st, err := loadState(ctx, q, m.DatabaseID)
	if err != nil {
		return nil, err
	}
	return st.check(req.Kind, m, req.ID), nil
}

func (l Lifecycle) committed(ctx context.Context, q *gen.Queries, jobID int64, m gen.Migration) (bool, error) {
	if !m.BatchTable.Valid {
		return false, nil
	}
	n, err := q.GetJobProgress(ctx, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return n > 0, err
}

func (l Lifecycle) insertRun(ctx context.Context, q *gen.Queries, in queue.StartInput, req gen.MigrationRequest, m gen.Migration) (gen.MigrationRun, error) {
	p := gen.InsertRunParams{
		MigrationID: m.ID, DatabaseID: m.DatabaseID, ExecutedBy: in.ActorID,
		RequestID: int8of(req.ID), JobID: int8of(in.JobID), Kind: req.Kind, Revision: m.Revision, Attempt: int32(in.Attempt),
		SqlText: m.SqlText, Checksum: m.Checksum,
	}
	if req.Kind == gen.RequestKindRollback {
		last, err := q.LastUpRun(ctx, m.ID)
		if err != nil {
			return gen.MigrationRun{}, missing(err)
		}
		p.SqlText, p.Checksum = m.DownSql.String, checksum(m.DownSql.String)
		p.RevertsRunID, p.CheckpointRunID, p.Version = int8of(last.ID), int8of(last.CheckpointRunID), last.Version
		if m.Version.Valid {
			p.Version = m.Version
		}
		return q.InsertRun(ctx, p)
	}
	head, err := q.MigrationHead(ctx, m.DatabaseID)
	if err != nil {
		return gen.MigrationRun{}, err
	}
	p.Version = int4of(head + 1)
	if m.BatchTable.Valid {
		prev, err := q.LastRequestRun(ctx, int8of(req.ID))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return gen.MigrationRun{}, err
		case prev.Version == p.Version:
			p.CheckpointRunID = int8of(prev.CheckpointRunID)
		}
	}
	return q.InsertRun(ctx, p)
}

func execution(run gen.MigrationRun, m gen.Migration) *queue.Execution {
	e := &queue.Execution{
		RunID: run.ID, CheckpointRunID: run.ID, MigrationID: run.MigrationID, DatabaseID: run.DatabaseID,
		Direction: dbms.DirectionUp, SQL: run.SqlText, Version: int(run.Version.Int32), Checksum: run.Checksum,
	}
	if run.CheckpointRunID.Valid {
		e.CheckpointRunID = run.CheckpointRunID.Int64
	}
	if run.Kind == gen.RequestKindRollback {
		e.Direction = dbms.DirectionDown
	}
	if b := batchOf(m); b != nil {
		e.Batch = &dbms.BatchSpec{Schema: b.Schema, Table: b.Table, KeyColumn: b.Column, Size: b.Size}
	}
	return e
}

func (l Lifecycle) Finish(ctx context.Context, in queue.FinishInput) error {
	var run gen.MigrationRun
	params := gen.FinishRunParams{ID: in.RunID, Status: gen.RunStatusSucceeded}
	if in.Err != nil {
		params.Status = gen.RunStatusFailed
		params.ErrorMessage = text(in.Err.Error())
		var se *dbms.StatementError
		if errors.As(in.Err, &se) {
			params.ErrorCode = text(se.SQLState)
			params.FailedStatementIndex = pgtype.Int4{Int32: int32(se.Index), Valid: se.Index > 0}
		}
	}
	err := pgx.BeginFunc(ctx, l.Pool, func(tx pgx.Tx) error {
		q := gen.New(tx)
		var err error
		if run, err = q.LockRun(ctx, in.RunID); err != nil {
			return missing(err)
		}
		if run.Status != gen.RunStatusRunning {
			return conflict("attempt ini sudah ditutup")
		}
		if err := q.FinishRun(ctx, params); err != nil {
			return err
		}
		status, err := l.settle(ctx, q, run, in)
		if err != nil {
			return err
		}
		return q.SetMigrationRequestStatus(ctx, gen.SetMigrationRequestStatusParams{ID: run.RequestID.Int64, Status: status})
	})
	if err != nil {
		return err
	}
	attrs := runAttrs("migration.execution.end", run)
	outcome := audit.OutcomeSuccess
	if in.Err != nil {
		outcome = audit.OutcomeFailure
		slog.ErrorContext(ctx, "migration.execution.end", append(attrs, "status", string(params.Status),
			"error", in.Err.Error(), "sqlstate", params.ErrorCode.String, "will_retry", in.WillRetry)...)
	} else {
		slog.InfoContext(ctx, "migration.execution.end", append(attrs, "status", string(params.Status))...)
	}
	if l.Audit != nil {
		l.Audit.Record(ctx, audit.Entry{
			ActorID: run.ExecutedBy, Action: "migration." + string(run.Kind), ResourceType: "migration",
			ResourceID: formatID(run.MigrationID), Outcome: outcome, Metadata: map[string]any{"executionId": run.ID},
		})
	}
	return nil
}

func (l Lifecycle) settle(ctx context.Context, q *gen.Queries, run gen.MigrationRun, in queue.FinishInput) (gen.RequestStatus, error) {
	if in.Err == nil {
		if run.Kind != gen.RequestKindRollback {
			return gen.RequestStatusSucceeded, q.MarkMigrationApplied(ctx, gen.MarkMigrationAppliedParams{
				ID: run.MigrationID, Version: run.Version, AppliedBy: int8of(run.ExecutedBy),
			})
		}
		if err := q.ClosePartialRequests(ctx, run.MigrationID); err != nil {
			return "", err
		}
		return gen.RequestStatusSucceeded, q.MarkMigrationRolledBack(ctx, run.MigrationID)
	}
	if in.WillRetry {
		return gen.RequestStatusQueued, nil
	}
	if in.Progress != nil {
		if s := in.Progress.State(); s == dbms.BatchPartial || s == dbms.BatchPartiallyReverted {
			return gen.RequestStatusPartial, nil
		}
		return gen.RequestStatusFailed, nil
	}
	row, err := q.GetMigration(ctx, run.MigrationID)
	if err != nil {
		return "", err
	}
	committed, err := l.committed(ctx, q, run.JobID.Int64, row.Migration)
	if err != nil {
		return "", err
	}
	if committed {
		return gen.RequestStatusPartial, nil
	}
	return gen.RequestStatusFailed, nil
}

func (l Lifecycle) Interrupted(ctx context.Context, jobID int64, attempt int) (*queue.Execution, error) {
	q := gen.New(l.Pool)
	run, err := q.InterruptedRun(ctx, gen.InterruptedRunParams{JobID: int8of(jobID), Attempt: int32(attempt)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	row, err := q.GetMigration(ctx, run.MigrationID)
	if err != nil {
		return nil, missing(err)
	}
	return execution(run, row.Migration), nil
}

func runAttrs(event string, run gen.MigrationRun) []any {
	attrs := []any{
		"event", event, "execution_id", run.ID, "kind", string(run.Kind), "revision", run.Revision,
		"version", run.Version.Int32, "executed_by", run.ExecutedBy, "query", sqltrace.Mask(run.SqlText),
	}
	if run.RevertsRunID.Valid {
		attrs = append(attrs, "reverts_execution_id", run.RevertsRunID.Int64)
	}
	return attrs
}
