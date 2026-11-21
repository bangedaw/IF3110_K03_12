package queue_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
)

type completion struct {
	status    queue.JobStatus
	lastError string
}

type memJobs struct {
	mu        sync.Mutex
	pending   []*queue.Job
	progress  map[int64]dbms.BatchProgress
	completed map[int64]completion
	deferred  map[int64]time.Duration
}

func newMemJobs(jobs ...*queue.Job) *memJobs {
	return &memJobs{pending: jobs, progress: map[int64]dbms.BatchProgress{}, completed: map[int64]completion{}, deferred: map[int64]time.Duration{}}
}

func (m *memJobs) Claim(context.Context) (*queue.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) == 0 {
		return nil, nil
	}
	job := m.pending[0]
	m.pending = m.pending[1:]
	return job, nil
}

func (m *memJobs) SaveProgress(_ context.Context, jobID int64, p dbms.BatchProgress) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.progress[jobID] = p
	return nil
}

func (m *memJobs) Complete(_ context.Context, jobID int64, status queue.JobStatus, lastError string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed[jobID] = completion{status, lastError}
	return nil
}

func (m *memJobs) Defer(_ context.Context, jobID int64, delay time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deferred[jobID] = delay
	return nil
}

type memLocks struct {
	mu       sync.Mutex
	busy     bool
	renewErr bool
	held     map[int64]bool
	renewed  int
}

func newMemLocks() *memLocks { return &memLocks{held: map[int64]bool{}} }

func (l *memLocks) Acquire(_ context.Context, databaseID, _ int64, _ string, _ time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.busy || l.held[databaseID] {
		return false, nil
	}
	l.held[databaseID] = true
	return true, nil
}

func (l *memLocks) Renew(context.Context, int64, int64, string, time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.renewed++
	return !l.renewErr, nil
}

func (l *memLocks) Release(_ context.Context, databaseID, _ int64, _ string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, databaseID)
	return nil
}

func (l *memLocks) isHeld(databaseID int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held[databaseID]
}

type targets struct{ m dbms.Migrator }

func (t targets) Migrator(context.Context, int64) (dbms.Migrator, error) { return t.m, nil }

type uncertainMigrator struct {
	*dbms.FakeMigrator
	committed bool
}

func (u uncertainMigrator) Apply(ctx context.Context, in dbms.ApplyInput) (*dbms.ApplyResult, error) {
	if u.committed {
		if _, err := u.FakeMigrator.Apply(ctx, in); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("commit migration: %w", dbms.ErrCommitUncertain)
}

var batchSpec = dbms.BatchSpec{Schema: "loadtest", Table: "transactions", KeyColumn: "id", Size: 1000}

func runOne(t *testing.T, exec queue.Execution, m dbms.Migrator) (*memJobs, *queue.FakeLifecycle) {
	t.Helper()
	jobs, lc, _ := runWithLocks(t, exec, m, newMemLocks())
	return jobs, lc
}

func runWithLocks(t *testing.T, exec queue.Execution, m dbms.Migrator, locks *memLocks) (*memJobs, *queue.FakeLifecycle, *memLocks) {
	t.Helper()
	jobs := newMemJobs(&queue.Job{ID: 10, RequestID: 20, DatabaseID: 1, ActorID: 3, Attempt: 1, MaxAttempts: 3})
	lc := &queue.FakeLifecycle{Execution: exec}
	w := &queue.Worker{Jobs: jobs, Locks: locks, Lifecycle: lc, Targets: targets{m}, LockTTL: 30 * time.Millisecond}
	found, err := w.Next(context.Background())
	require.NoError(t, err)
	require.True(t, found)
	return jobs, lc, locks
}

func TestNextReturnsFalseOnEmptyQueue(t *testing.T) {
	w := &queue.Worker{Jobs: newMemJobs(), Locks: newMemLocks(), Lifecycle: &queue.FakeLifecycle{}, Targets: targets{&dbms.FakeMigrator{}}}
	found, err := w.Next(context.Background())
	require.NoError(t, err)
	assert.False(t, found)
}

func TestWorkerAppliesPlainMigration(t *testing.T) {
	m := &dbms.FakeMigrator{}
	jobs, lc := runOne(t, queue.Execution{MigrationID: 7, DatabaseID: 1, Direction: dbms.DirectionUp, SQL: "CREATE TABLE t ()", Version: 2}, m)

	require.Len(t, lc.Started, 1)
	assert.Equal(t, queue.StartInput{RequestID: 20, JobID: 10, ActorID: 3, Attempt: 1}, lc.Started[0])
	require.Len(t, m.Applied, 1)
	assert.Equal(t, "CREATE TABLE t ()", m.Applied[0].SQL)
	assert.Equal(t, int64(1), m.Applied[0].RunID)
	require.Len(t, lc.Finished, 1)
	assert.NoError(t, lc.Finished[0].Err)
	assert.Equal(t, completion{status: queue.JobSucceeded}, jobs.completed[10])
}

func TestWorkerFailsJobWhenRevalidationRejects(t *testing.T) {
	m := &dbms.FakeMigrator{}
	jobs := newMemJobs(&queue.Job{ID: 10, RequestID: 20, Attempt: 1})
	lc := &queue.FakeLifecycle{StartErr: apperr.New(apperr.CodeConflict, "approval tidak lengkap")}
	w := &queue.Worker{Jobs: jobs, Locks: newMemLocks(), Lifecycle: lc, Targets: targets{m}}

	_, err := w.Next(context.Background())
	require.NoError(t, err)
	assert.Empty(t, m.Applied, "SQL tidak dijalankan")
	assert.Empty(t, lc.Finished, "tidak ada attempt yang perlu ditutup")
	assert.Equal(t, queue.JobFailed, jobs.completed[10].status)
	assert.Contains(t, jobs.completed[10].lastError, "approval tidak lengkap")
}

func TestWorkerRecordsFailedExecution(t *testing.T) {
	m := &dbms.FakeMigrator{ApplyErr: errors.New("relation already exists")}
	jobs, lc := runOne(t, queue.Execution{DatabaseID: 1, Direction: dbms.DirectionUp, SQL: "CREATE TABLE t ()"}, m)

	require.Len(t, lc.Finished, 1)
	assert.EqualError(t, lc.Finished[0].Err, "relation already exists")
	assert.Equal(t, completion{queue.JobFailed, "relation already exists"}, jobs.completed[10])
}

func TestWorkerMarksPartialBatchAndSavesProgress(t *testing.T) {
	m := &dbms.FakeMigrator{
		Progress: dbms.BatchProgress{BatchesCommitted: 2, RowsCommitted: 2000},
		BatchErr: errors.New("batch 3: deadlock detected"),
	}
	spec := batchSpec
	jobs, lc := runOne(t, queue.Execution{DatabaseID: 1, Direction: dbms.DirectionUp, SQL: "UPDATE t SET x = 1 WHERE id > $1 AND id <= $2", CheckpointRunID: 1, Batch: &spec}, m)

	require.Len(t, m.Batched, 1)
	assert.Equal(t, int64(1), m.Batched[0].RunID, "batch memakai run pemilik checkpoint")
	assert.Equal(t, batchSpec, m.Batched[0].Batch)
	assert.Equal(t, int64(2000), jobs.progress[10].RowsCommitted)
	assert.Equal(t, queue.JobPartial, jobs.completed[10].status)
	require.Len(t, lc.Finished, 1)
	assert.Equal(t, 2, lc.Finished[0].Progress.BatchesCommitted)
}

func TestWorkerRevertsBatchesOfTheRolledBackRun(t *testing.T) {
	m := &dbms.FakeMigrator{Progress: dbms.BatchProgress{BatchesCommitted: 3, BatchesReverted: 3, Completed: true, RevertCompleted: true}}
	spec := batchSpec
	jobs, _ := runOne(t, queue.Execution{RunID: 9, CheckpointRunID: 4, DatabaseID: 1, Direction: dbms.DirectionDown, SQL: "UPDATE t SET x = NULL WHERE id > $1 AND id <= $2", Batch: &spec}, m)

	require.Len(t, m.Reverted, 1)
	assert.Equal(t, int64(4), m.Reverted[0].RunID, "run yang dibatalkan")
	assert.Equal(t, int64(9), m.Reverted[0].ByRunID, "run rollback")
	assert.Empty(t, m.Batched)
	assert.Equal(t, queue.JobSucceeded, jobs.completed[10].status)
}

func TestWorkerTreatsUncertainCommitAsSuccessWhenTargetHasEvidence(t *testing.T) {
	m := uncertainMigrator{FakeMigrator: &dbms.FakeMigrator{}, committed: true}
	jobs, lc := runOne(t, queue.Execution{DatabaseID: 1, Direction: dbms.DirectionUp, SQL: "SELECT 1", Version: 5}, m)

	require.Len(t, lc.Finished, 1)
	assert.NoError(t, lc.Finished[0].Err)
	assert.Equal(t, queue.JobSucceeded, jobs.completed[10].status)
}

func TestWorkerLeavesUncertainCommitUnresolvedWithoutEvidence(t *testing.T) {
	m := uncertainMigrator{FakeMigrator: &dbms.FakeMigrator{}}
	jobs, lc := runOne(t, queue.Execution{DatabaseID: 1, Direction: dbms.DirectionUp, SQL: "SELECT 1"}, m)

	assert.Empty(t, lc.Finished, "hasil belum pasti tidak dicatat sebagai gagal")
	assert.NotContains(t, jobs.completed, int64(10), "job tetap running untuk diperiksa pemulihan")
}

type blockingMigrator struct{ *dbms.FakeMigrator }

func (blockingMigrator) Apply(ctx context.Context, _ dbms.ApplyInput) (*dbms.ApplyResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestWorkerDefersJobWhenDatabaseIsLocked(t *testing.T) {
	m := &dbms.FakeMigrator{}
	locks := newMemLocks()
	locks.busy = true
	jobs, lc, _ := runWithLocks(t, queue.Execution{DatabaseID: 1, SQL: "SELECT 1"}, m, locks)

	assert.Empty(t, lc.Started, "revalidasi dan eksekusi menunggu lock")
	assert.Empty(t, m.Applied)
	assert.Equal(t, 5*time.Second, jobs.deferred[10])
	assert.NotContains(t, jobs.completed, int64(10))
}

func TestWorkerReleasesLockAfterJobEnds(t *testing.T) {
	jobs, _, locks := runWithLocks(t, queue.Execution{DatabaseID: 1, SQL: "SELECT 1"}, &dbms.FakeMigrator{}, newMemLocks())

	assert.Equal(t, queue.JobSucceeded, jobs.completed[10].status)
	assert.False(t, locks.isHeld(1))
}

func TestWorkerStopsExecutionWhenLeaseIsLost(t *testing.T) {
	locks := newMemLocks()
	locks.renewErr = true
	jobs, lc, _ := runWithLocks(t, queue.Execution{DatabaseID: 1, SQL: "SELECT pg_sleep(60)"}, blockingMigrator{&dbms.FakeMigrator{}}, locks)

	require.Len(t, lc.Finished, 1)
	assert.ErrorIs(t, lc.Finished[0].Err, context.Canceled)
	assert.Equal(t, queue.JobFailed, jobs.completed[10].status)
}

func TestWorkerRenewsLeaseWhileRunning(t *testing.T) {
	m := &dbms.FakeMigrator{}
	locks := newMemLocks()
	slow := slowMigrator{FakeMigrator: m, d: 100 * time.Millisecond}
	jobs, _, _ := runWithLocks(t, queue.Execution{DatabaseID: 1, SQL: "SELECT 1"}, slow, locks)

	assert.Equal(t, queue.JobSucceeded, jobs.completed[10].status)
	assert.GreaterOrEqual(t, locks.renewed, 2, "lease diperpanjang setiap sepertiga TTL")
}

func TestWorkerKeepsLockWhenCommitIsUncertain(t *testing.T) {
	_, _, locks := runWithLocks(t, queue.Execution{DatabaseID: 1, SQL: "SELECT 1"}, uncertainMigrator{FakeMigrator: &dbms.FakeMigrator{}}, newMemLocks())

	assert.True(t, locks.isHeld(1), "database tertahan sampai hasil commit dipastikan")
}

type slowMigrator struct {
	*dbms.FakeMigrator
	d time.Duration
}

func (s slowMigrator) Apply(ctx context.Context, in dbms.ApplyInput) (*dbms.ApplyResult, error) {
	time.Sleep(s.d)
	return s.FakeMigrator.Apply(ctx, in)
}
