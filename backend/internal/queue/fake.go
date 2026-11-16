package queue

import (
	"context"
	"sync"
)

type Fake struct {
	mu   sync.Mutex
	jobs map[int64]int64

	Err       error
	Published []PublishInput
}

var _ Publisher = (*Fake)(nil)

func (f *Fake) Publish(_ context.Context, in PublishInput) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return 0, f.Err
	}
	f.Published = append(f.Published, in)
	if id, ok := f.jobs[in.RequestID]; ok {
		return id, nil
	}
	if f.jobs == nil {
		f.jobs = map[int64]int64{}
	}
	id := int64(len(f.jobs) + 1)
	f.jobs[in.RequestID] = id
	return id, nil
}

type FakeLifecycle struct {
	mu sync.Mutex

	Execution Execution
	StartErr  error
	FinishErr error
	Started   []StartInput
	Finished  []FinishInput
}

var _ Lifecycle = (*FakeLifecycle)(nil)

func (f *FakeLifecycle) Start(_ context.Context, in StartInput) (*Execution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Started = append(f.Started, in)
	if f.StartErr != nil {
		return nil, f.StartErr
	}
	e := f.Execution
	if e.RunID == 0 {
		e.RunID = int64(len(f.Started))
	}
	if e.CheckpointRunID == 0 {
		e.CheckpointRunID = 1
	}
	return &e, nil
}

func (f *FakeLifecycle) Finish(_ context.Context, in FinishInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Finished = append(f.Finished, in)
	return f.FinishErr
}
