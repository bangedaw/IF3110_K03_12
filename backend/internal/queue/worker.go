package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqltrace"
)

type JobStatus string

const (
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobPartial   JobStatus = "partial"
)

type JobStore interface {
	Claim(ctx context.Context) (*Job, error)
	SaveProgress(ctx context.Context, jobID int64, progress dbms.BatchProgress) error
	Complete(ctx context.Context, jobID int64, status JobStatus, lastError string) error
}

type Targets interface {
	Migrator(ctx context.Context, databaseID int64) (dbms.Migrator, error)
}

type Worker struct {
	Jobs         JobStore
	Lifecycle    Lifecycle
	Targets      Targets
	Logger       *slog.Logger
	PollInterval time.Duration
	Concurrency  int
}

var tracer = otel.Tracer("github.com/bangedaw/IF3110_K03_12/backend/internal/queue")

func (w *Worker) Run(ctx context.Context) {
	n := max(w.Concurrency, 1)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.loop(ctx)
		}()
	}
	wg.Wait()
}

func (w *Worker) loop(ctx context.Context) {
	poll := w.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	for ctx.Err() == nil {
		found, err := w.Next(ctx)
		if err != nil {
			w.logger().ErrorContext(ctx, "queue.claim", "event", "queue.claim", "status", "error", "error", err.Error())
		}
		if found && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(poll):
		}
	}
}

func (w *Worker) Next(ctx context.Context) (bool, error) {
	job, err := w.Jobs.Claim(ctx)
	if err != nil || job == nil {
		return false, err
	}
	// Job yang sudah diambil diselesaikan walaupun worker diminta berhenti, supaya tidak terputus di tengah SQL.
	w.process(context.WithoutCancel(ctx), job)
	return true, nil
}

func (w *Worker) process(ctx context.Context, job *Job) {
	start := time.Now()
	ctx, span := tracer.Start(job.Context(ctx), "queue.job")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("job.id", job.ID),
		attribute.Int64("job.request_id", job.RequestID),
		attribute.Int64("job.database_id", job.DatabaseID),
		attribute.Int("job.attempt", job.Attempt),
	)
	ctx = sqltrace.With(ctx,
		slog.Int64("job_id", job.ID), slog.Int64("request_id", job.RequestID),
		slog.Int64("database_id", job.DatabaseID), slog.Int64("actor", job.ActorID), slog.Int("attempt", job.Attempt))
	log := w.logger()
	log.InfoContext(ctx, "job.start", "event", "job.start", "status", "running")

	exec, err := w.Lifecycle.Start(ctx, StartInput{RequestID: job.RequestID, JobID: job.ID, ActorID: job.ActorID, Attempt: job.Attempt})
	if err != nil {
		w.complete(ctx, job, JobFailed, fmt.Errorf("revalidasi: %w", err), start)
		return
	}
	ctx = sqltrace.With(ctx, slog.Int64("run_id", exec.RunID), slog.Int64("migration_id", exec.MigrationID))

	progress, runErr := w.execute(ctx, job, exec)
	if errors.Is(runErr, dbms.ErrCommitUncertain) {
		// shortcut: job dibiarkan running tanpa Finish; pemulihan di M-3 memeriksa ulang bukti commit sebelum memutuskan.
		span.SetStatus(codes.Error, runErr.Error())
		log.ErrorContext(ctx, "job.uncertain", "event", "job.uncertain", "status", "uncertain", "error", runErr.Error())
		return
	}
	if err := w.Lifecycle.Finish(ctx, FinishInput{RunID: exec.RunID, Err: runErr, Progress: progress}); err != nil {
		log.ErrorContext(ctx, "job.record", "event", "job.record", "status", "error", "error", err.Error())
	}
	w.complete(ctx, job, outcome(runErr, progress), runErr, start)
}

func (w *Worker) execute(ctx context.Context, job *Job, exec *Execution) (*dbms.BatchProgress, error) {
	m, err := w.Targets.Migrator(ctx, exec.DatabaseID)
	if err != nil {
		return nil, err
	}
	if exec.Batch == nil {
		_, err = m.Apply(ctx, dbms.ApplyInput{
			SQL: exec.SQL, Direction: exec.Direction, DatabaseID: exec.DatabaseID, MigrationID: exec.MigrationID,
			RunID: exec.RunID, Version: exec.Version, Checksum: exec.Checksum,
		})
		if errors.Is(err, dbms.ErrCommitUncertain) {
			if _, found, checkErr := m.CommittedVersion(ctx, exec.RunID); checkErr == nil && found {
				return nil, nil
			}
		}
		return nil, err
	}

	bm, ok := m.(dbms.BatchMigrator)
	if !ok {
		return nil, errors.New("database target tidak mendukung migration per batch")
	}
	onBatch := func(p dbms.BatchProgress) {
		if err := w.Jobs.SaveProgress(ctx, job.ID, p); err != nil {
			w.logger().WarnContext(ctx, "job.progress", "event", "job.progress", "status", "error", "error", err.Error())
		}
	}
	var progress *dbms.BatchProgress
	if exec.Direction == dbms.DirectionDown {
		progress, err = bm.RevertBatches(ctx, dbms.BatchRevertInput{
			SQL: exec.SQL, DatabaseID: exec.DatabaseID, RunID: exec.CheckpointRunID, ByRunID: exec.RunID, OnBatch: onBatch,
		})
	} else {
		progress, err = bm.ApplyBatches(ctx, dbms.BatchApplyInput{
			SQL: exec.SQL, Batch: *exec.Batch, DatabaseID: exec.DatabaseID, MigrationID: exec.MigrationID,
			RunID: exec.CheckpointRunID, Version: exec.Version, Checksum: exec.Checksum, OnBatch: onBatch,
		})
	}
	if progress != nil {
		onBatch(*progress)
	}
	return progress, err
}

func outcome(err error, progress *dbms.BatchProgress) JobStatus {
	if err == nil {
		return JobSucceeded
	}
	if progress != nil {
		if s := progress.State(); s == dbms.BatchPartial || s == dbms.BatchPartiallyReverted {
			return JobPartial
		}
	}
	return JobFailed
}

func (w *Worker) complete(ctx context.Context, job *Job, status JobStatus, runErr error, start time.Time) {
	lastError := ""
	level := slog.LevelInfo
	if runErr != nil {
		lastError = runErr.Error()
		level = slog.LevelError
		span := trace.SpanFromContext(ctx)
		span.RecordError(runErr)
		span.SetStatus(codes.Error, lastError)
	}
	if err := w.Jobs.Complete(ctx, job.ID, status, lastError); err != nil {
		w.logger().ErrorContext(ctx, "job.complete", "event", "job.complete", "status", "error", "error", err.Error())
	}
	attrs := []any{"event", "job.end", "status", string(status), "duration_ms", time.Since(start).Milliseconds()}
	if lastError != "" {
		attrs = append(attrs, "error", lastError)
	}
	w.logger().Log(ctx, level, "job.end", attrs...)
}

func (w *Worker) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}
