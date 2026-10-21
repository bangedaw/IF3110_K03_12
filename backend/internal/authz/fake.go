package authz

import (
	"context"
	"sync"
)

type Fake struct {
	mu     sync.RWMutex
	levels map[[2]int64]Level
}

func NewFake() *Fake { return &Fake{levels: map[[2]int64]Level{}} }

func (f *Fake) Grant(userID, databaseID int64, l Level) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.levels[[2]int64{userID, databaseID}] = l
	return f
}

func (f *Fake) Authorize(ctx context.Context, p Principal, a Action, r Resource) error {
	l, _ := f.EffectiveLevel(ctx, p.UserID, r.DatabaseID)
	return Check(p, a, l)
}

func (f *Fake) EffectiveLevel(_ context.Context, userID, databaseID int64) (Level, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.levels[[2]int64{userID, databaseID}], nil
}
