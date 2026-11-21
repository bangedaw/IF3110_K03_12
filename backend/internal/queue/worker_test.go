package queue_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

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
}

func newMemJobs(jobs ...*queue.Job) *memJobs {
	return &memJobs{pending: jobs, progress: map[int64]dbms.BatchProgress{}, completed: map[int64]completion{}}
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
	jobs := newMemJobs(&queue.Job{ID: 10, RequestID: 20, DatabaseID: 1, ActorID: 3, Attempt: 1, MaxAttempts: 3})
	lc := &queue.FakeLifecycle{Execution: exec}
	w := &queue.Worker{Jobs: jobs, Lifecycle: lc, Targets: targets{m}}
	found, err := w.Next(context.Background())
	require.NoError(t, err)
	require.True(t, found)
	return jobs, lc
}

func TestNextReturnsFalseOnEmptyQueue(t *testing.T) {
	w := &queue.Worker{Jobs: newMemJobs(), Lifecycle: &queue.FakeLifecycle{}, Targets: targets{&dbms.FakeMigrator{}}}
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
	w := &queue.Worker{Jobs: jobs, Lifecycle: lc, Targets: targets{m}}

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
