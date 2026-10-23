package authz

import (
	"context"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

type DBAuthorizer struct{ q *gen.Queries }

func NewDBAuthorizer(db gen.DBTX) *DBAuthorizer { return &DBAuthorizer{q: gen.New(db)} }

func (a *DBAuthorizer) Authorize(ctx context.Context, p Principal, action Action, r Resource) error {
	if adminOnly[action] {
		return Check(p, action, LevelNone)
	}
	level, err := a.EffectiveLevel(ctx, p.UserID, r.DatabaseID)
	if err != nil {
		return err
	}
	return Check(p, action, level)
}

func (a *DBAuthorizer) EffectiveLevel(ctx context.Context, userID, databaseID int64) (Level, error) {
	level, err := a.q.EffectiveAccessLevel(ctx, gen.EffectiveAccessLevelParams{UserID: userID, DatabaseID: databaseID})
	return Level(level), err
}
