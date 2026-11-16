package dbms

import (
	"context"
	"sync"
	"time"
)

type FakeMigrator struct {
	mu        sync.Mutex
	committed map[int64]int

	ApplyErr error
	BatchErr error
	Progress BatchProgress
	Applied  []ApplyInput
	Batched  []BatchInput
}

var _ Migrator = (*FakeMigrator)(nil)

func (f *FakeMigrator) SupportsTransactionalDDL() bool { return true }

func (f *FakeMigrator) Apply(_ context.Context, in ApplyInput) (*ApplyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Applied = append(f.Applied, in)
	if f.ApplyErr != nil {
		return nil, f.ApplyErr
	}
	if f.committed == nil {
		f.committed = map[int64]int{}
	}
	f.committed[in.RunID] = in.Version
	return &ApplyResult{StatementsExecuted: 1, AppliedAt: time.Now().UTC()}, nil
}

func (f *FakeMigrator) ApplyBatches(_ context.Context, in BatchInput) (BatchProgress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Batched = append(f.Batched, in)
	if in.OnBatch != nil {
		in.OnBatch(f.Progress)
	}
	return f.Progress, f.BatchErr
}

func (f *FakeMigrator) CommittedVersion(_ context.Context, runID int64) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	version, found := f.committed[runID]
	return version, found, nil
}
