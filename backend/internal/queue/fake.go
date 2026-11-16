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
