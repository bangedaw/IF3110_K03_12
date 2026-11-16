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
	Batched  []BatchApplyInput
	Reverted []BatchRevertInput
}

var (
	_ Migrator      = (*FakeMigrator)(nil)
	_ BatchMigrator = (*FakeMigrator)(nil)
)

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

func (f *FakeMigrator) CommittedVersion(_ context.Context, runID int64) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	version, found := f.committed[runID]
	return version, found, nil
}

func (f *FakeMigrator) ApplyBatches(_ context.Context, in BatchApplyInput) (*BatchProgress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Batched = append(f.Batched, in)
	return f.report(in.OnBatch)
}

func (f *FakeMigrator) RevertBatches(_ context.Context, in BatchRevertInput) (*BatchProgress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reverted = append(f.Reverted, in)
	return f.report(in.OnBatch)
}

func (f *FakeMigrator) BatchProgress(context.Context, int64) (*BatchProgress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.Progress
	return &p, nil
}

func (f *FakeMigrator) report(onBatch func(BatchProgress)) (*BatchProgress, error) {
	p := f.Progress
	if onBatch != nil {
		onBatch(p)
	}
	return &p, f.BatchErr
}
