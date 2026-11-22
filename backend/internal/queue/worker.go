package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
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
	Defer(ctx context.Context, jobID int64, delay time.Duration) error
	Retry(ctx context.Context, jobID int64, delay time.Duration, lastError string) error
	Stale(ctx context.Context, grace time.Duration) ([]*Job, error)
	RequeueUnlocked(ctx context.Context, grace time.Duration) (int64, error)
}

type Locks interface {
	Acquire(ctx context.Context, databaseID, jobID int64, owner string, ttl time.Duration) (bool, error)
	Renew(ctx context.Context, databaseID, jobID int64, owner string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, databaseID, jobID int64, owner string) error
}

type Targets interface {
	Migrator(ctx context.Context, databaseID int64) (dbms.Migrator, error)
}

type Worker struct {
	Jobs           JobStore
	Locks          Locks
	Lifecycle      Lifecycle
	Targets        Targets
	Logger         *slog.Logger
	ID             string
	PollInterval   time.Duration
	LockTTL        time.Duration
	LockRetryDelay time.Duration
	RetryBaseDelay time.Duration
	Concurrency    int
}

var (
	errLeaseLost   = errors.New("lease lock database hilang")
	errInterrupted = errors.New("worker berhenti sebelum attempt selesai dan perubahannya tidak ditemukan di database target")
)

var defaultOwner = func() string {
	host, _ := os.Hostname()
	return host + ":" + strconv.Itoa(os.Getpid())
}()

var tracer = otel.Tracer("github.com/bangedaw/IF3110_K03_12/backend/internal/queue")

func (w *Worker) Run(ctx context.Context) {
	n := max(w.Concurrency, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.recoverLoop(ctx)
	}()
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
	ctx, span := w.jobSpan(ctx, job, "queue.job")
	defer span.End()
	log := w.logger()
	log.InfoContext(ctx, "job.start", "event", "job.start", "status", "running")

	if !w.acquire(ctx, job) {
		return
	}
	h := w.hold(ctx, job)

	exec, err := w.Lifecycle.Start(h.execCtx, StartInput{RequestID: job.RequestID, JobID: job.ID, ActorID: job.ActorID, Attempt: job.Attempt})
	if h.lost() {
		return
	}
	if err != nil {
		var rejected *apperr.Error
		if !errors.As(err, &rejected) && job.Attempt < job.MaxAttempts {
			h.retry(fmt.Errorf("revalidasi: %w", err))
			return
		}
		h.end(JobFailed, fmt.Errorf("revalidasi: %w", err))
		return
	}
	h.withRun(exec)

	progress, runErr := w.execute(h.execCtx, job, exec)
	if h.lost() {
		return
	}
	if errors.Is(runErr, dbms.ErrCommitUncertain) {
		// Lock tidak dilepas: lease kedaluwarsa tetapi job tetap running, sehingga migration lain di database
		// ini tertahan sampai Recover memeriksa bukti commit.
		h.stop()
		span.SetStatus(codes.Error, runErr.Error())
		log.ErrorContext(h.ctx, "job.uncertain", "event", "job.uncertain", "status", "uncertain", "error", runErr.Error())
		return
	}
	h.settle(exec, progress, runErr)
}

// Recover memulihkan job yang tertinggal running: worker-nya mati, kehilangan lease, atau meninggalkan
// hasil commit yang belum pasti. Hasil attempt ditentukan dari bukti di database target, bukan ditebak.
func (w *Worker) Recover(ctx context.Context) (int, error) {
	grace := w.lockTTL()
	n, err := w.Jobs.RequeueUnlocked(ctx, grace)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		w.logger().WarnContext(ctx, "job.recover", "event", "job.recover", "status", "requeued", "jobs", n)
	}
	jobs, err := w.Jobs.Stale(ctx, grace)
	if err != nil {
		return int(n), err
	}
	for _, job := range jobs {
		w.recover(context.WithoutCancel(ctx), job)
	}
	return int(n) + len(jobs), nil
}

func (w *Worker) recoverLoop(ctx context.Context) {
	ticker := time.NewTicker(w.lockTTL())
	defer ticker.Stop()
	for {
		if _, err := w.Recover(ctx); err != nil && ctx.Err() == nil {
			w.logger().ErrorContext(ctx, "job.recover", "event", "job.recover", "status", "error", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) recover(ctx context.Context, job *Job) {
	ctx, span := w.jobSpan(ctx, job, "queue.recover")
	defer span.End()
	log := w.logger()

	// Acquire berhasil hanya jika lease job ini sudah kedaluwarsa dan belum diambil worker lain, sehingga
	// satu job dipulihkan oleh satu worker saja.
	ok, err := w.Locks.Acquire(ctx, job.DatabaseID, job.ID, w.owner(), w.lockTTL())
	if err != nil || !ok {
		log.InfoContext(ctx, "job.recover", "event", "job.recover", "status", "skipped", "error", fmt.Sprint(err))
		return
	}
	h := w.hold(ctx, job)
	giveUp := func(err error) {
		// Job tetap running dan lease dilepas, sehingga putaran pemulihan berikutnya mencoba lagi.
		h.stop()
		span.SetStatus(codes.Error, err.Error())
		log.ErrorContext(h.ctx, "job.recover", "event", "job.recover", "status", "error", "error", err.Error())
		h.release()
	}

	exec, err := w.Lifecycle.Interrupted(h.execCtx, job.ID, job.Attempt)
	if err != nil {
		giveUp(fmt.Errorf("baca attempt yang terputus: %w", err))
		return
	}
	if exec == nil {
		log.WarnContext(h.ctx, "job.recover", "event", "job.recover", "status", "no_run")
		if job.Attempt < job.MaxAttempts {
			h.retry(errInterrupted)
		} else {
			h.end(JobFailed, errInterrupted)
		}
		return
	}
	h.withRun(exec)

	progress, done, err := w.evidence(h.execCtx, exec)
	if err != nil {
		giveUp(fmt.Errorf("periksa bukti commit: %w", err))
		return
	}
	log.InfoContext(h.ctx, "job.recover", "event", "job.recover", "status", "checked", "committed", done)
	var runErr error
	if !done {
		runErr = errInterrupted
	}
	h.settle(exec, progress, runErr)
}

// evidence membaca dari database target apakah attempt yang terputus sudah selesai. Mode biasa dilihat dari
// riwayat versi (satu transaksi, jadi ter-commit seluruhnya atau tidak sama sekali), mode batch dari checkpoint.
func (w *Worker) evidence(ctx context.Context, exec *Execution) (*dbms.BatchProgress, bool, error) {
	m, err := w.Targets.Migrator(ctx, exec.DatabaseID)
	if err != nil {
		return nil, false, err
	}
	if exec.Batch == nil {
		_, found, err := m.CommittedVersion(ctx, exec.RunID)
		return nil, found, err
	}
	bm, ok := m.(dbms.BatchMigrator)
	if !ok {
		return nil, false, errors.New("database target tidak mendukung migration per batch")
	}
	p, err := bm.BatchProgress(ctx, exec.CheckpointRunID)
	if errors.Is(err, dbms.ErrBatchRunUnknown) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if exec.Direction == dbms.DirectionDown {
		return p, p.RevertCompleted, nil
	}
	return p, p.Completed, nil
}

func (w *Worker) jobSpan(ctx context.Context, job *Job, name string) (context.Context, trace.Span) {
	ctx, span := tracer.Start(job.Context(ctx), name)
	span.SetAttributes(
		attribute.Int64("job.id", job.ID),
		attribute.Int64("job.request_id", job.RequestID),
		attribute.Int64("job.database_id", job.DatabaseID),
		attribute.Int("job.attempt", job.Attempt),
	)
	ctx = sqltrace.With(ctx,
		slog.Int64("job_id", job.ID), slog.Int64("request_id", job.RequestID),
		slog.Int64("database_id", job.DatabaseID), slog.Int64("actor", job.ActorID), slog.Int("attempt", job.Attempt))
	return ctx, span
}

// held adalah job yang lock database-nya sedang dipegang worker ini. Hanya pemegang lease yang boleh
// menutup attempt (Finish) dan mengubah status job.
type held struct {
	w       *Worker
	job     *Job
	ctx     context.Context
	execCtx context.Context
	stop    func()
	start   time.Time
}

func (w *Worker) hold(ctx context.Context, job *Job) *held {
	execCtx, stop := w.holdLock(ctx, job)
	return &held{w: w, job: job, ctx: ctx, execCtx: execCtx, stop: stop, start: time.Now()}
}

func (h *held) withRun(exec *Execution) {
	h.execCtx = sqltrace.With(h.execCtx, slog.Int64("run_id", exec.RunID), slog.Int64("migration_id", exec.MigrationID))
	h.ctx = sqltrace.With(h.ctx, slog.Int64("run_id", exec.RunID), slog.Int64("migration_id", exec.MigrationID))
}

// lost melaporkan lease yang hilang di tengah jalan. Worker berhenti tanpa menutup attempt karena worker lain
// mungkin sudah mengambil alih; Recover menutupnya setelah memeriksa bukti di database target.
func (h *held) lost() bool {
	if !errors.Is(context.Cause(h.execCtx), errLeaseLost) {
		return false
	}
	h.stop()
	trace.SpanFromContext(h.ctx).SetStatus(codes.Error, errLeaseLost.Error())
	h.w.logger().ErrorContext(h.ctx, "job.abandon", "event", "job.abandon", "status", "lease_lost")
	return true
}

func (h *held) settle(exec *Execution, progress *dbms.BatchProgress, runErr error) {
	willRetry := runErr != nil && h.job.Attempt < h.job.MaxAttempts && retryable(runErr)
	if err := h.w.Lifecycle.Finish(h.ctx, FinishInput{RunID: exec.RunID, Err: runErr, Progress: progress, WillRetry: willRetry}); err != nil {
		h.w.logger().ErrorContext(h.ctx, "job.record", "event", "job.record", "status", "error", "error", err.Error())
	}
	if willRetry {
		h.retry(runErr)
		return
	}
	h.end(outcome(runErr, progress), runErr)
}

func (h *held) release() {
	if err := h.w.Locks.Release(h.ctx, h.job.DatabaseID, h.job.ID, h.w.owner()); err != nil {
		h.w.logger().ErrorContext(h.ctx, "lock.release", "event", "lock.release", "status", "error", "error", err.Error())
	}
}

func (h *held) end(status JobStatus, err error) {
	h.stop()
	h.w.complete(h.ctx, h.job, status, err, h.start)
	h.release()
}

func (h *held) retry(err error) {
	h.stop()
	h.w.retry(h.ctx, h.job, err)
	h.release()
}

// retryable memisahkan kegagalan sementara (koneksi, deadlock, worker mati) dari kegagalan yang akan
// berulang jika dijalankan lagi (SQL salah, constraint dilanggar).
func retryable(err error) bool {
	var se *dbms.StatementError
	if errors.As(err, &se) {
		return se.Retryable()
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code == apperr.CodeDBUnreachable
	}
	return errors.Is(err, errInterrupted) || pgconn.SafeToRetry(err)
}

func (w *Worker) retry(ctx context.Context, job *Job, err error) {
	delay := w.backoff(job.Attempt)
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	if rerr := w.Jobs.Retry(ctx, job.ID, delay, err.Error()); rerr != nil {
		w.logger().ErrorContext(ctx, "job.retry", "event", "job.retry", "status", "error", "error", rerr.Error())
		return
	}
	w.logger().WarnContext(ctx, "job.retry", "event", "job.retry", "status", "scheduled",
		"next_attempt", job.Attempt+1, "delay_ms", delay.Milliseconds(), "error", err.Error())
}

// backoff menggandakan jeda setiap attempt: 5 detik, 10 detik, 20 detik, dan seterusnya, paling lama 5 menit.
func (w *Worker) backoff(attempt int) time.Duration {
	base := w.RetryBaseDelay
	if base <= 0 {
		base = 5 * time.Second
	}
	return min(base<<min(attempt-1, 16), 5*time.Minute)
}

func (w *Worker) acquire(ctx context.Context, job *Job) bool {
	ctx, span := tracer.Start(ctx, "queue.lock")
	defer span.End()
	held, err := w.Locks.Acquire(ctx, job.DatabaseID, job.ID, w.owner(), w.lockTTL())
	span.SetAttributes(attribute.Bool("lock.acquired", held))
	log := w.logger()
	switch {
	case err != nil:
		span.SetStatus(codes.Error, err.Error())
		log.ErrorContext(ctx, "lock.acquire", "event", "lock.acquire", "status", "error", "error", err.Error())
	case held:
		log.InfoContext(ctx, "lock.acquire", "event", "lock.acquire", "status", "acquired")
		return true
	default:
		log.InfoContext(ctx, "lock.acquire", "event", "lock.acquire", "status", "busy")
	}
	if err := w.Jobs.Defer(ctx, job.ID, w.lockRetryDelay()); err != nil {
		log.ErrorContext(ctx, "job.defer", "event", "job.defer", "status", "error", "error", err.Error())
	}
	return false
}

// holdLock memperpanjang lease selama job berjalan. Jika lease tidak bisa diperpanjang, eksekusi dibatalkan
// supaya worker tidak lanjut mengubah database yang lock-nya mungkin sudah dipegang worker lain.
func (w *Worker) holdLock(ctx context.Context, job *Job) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	stopped := make(chan struct{})
	ttl := w.lockTTL()
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(ttl / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// Batas waktu sepertiga TTL: perpanjangan yang menggantung tidak boleh membuat worker terus
				// berjalan setelah lease-nya kedaluwarsa dan diambil alih Recover.
				rctx, cancelRenew := context.WithTimeout(context.WithoutCancel(ctx), ttl/3)
				ok, err := w.Locks.Renew(rctx, job.DatabaseID, job.ID, w.owner(), ttl)
				cancelRenew()
				if err != nil || !ok {
					w.logger().ErrorContext(ctx, "lock.renew", "event", "lock.renew", "status", "lost", "error", fmt.Sprint(err))
					cancel(errLeaseLost)
					return
				}
			}
		}
	}()
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			close(done)
			<-stopped
			cancel(nil)
		})
	}
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
		if err := w.Jobs.SaveProgress(context.WithoutCancel(ctx), job.ID, p); err != nil {
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

func (w *Worker) owner() string {
	if w.ID != "" {
		return w.ID
	}
	return defaultOwner
}

func (w *Worker) lockTTL() time.Duration {
	if w.LockTTL > 0 {
		return w.LockTTL
	}
	return 30 * time.Second
}

func (w *Worker) lockRetryDelay() time.Duration {
	if w.LockRetryDelay > 0 {
		return w.LockRetryDelay
	}
	return 5 * time.Second
}

func (w *Worker) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}
