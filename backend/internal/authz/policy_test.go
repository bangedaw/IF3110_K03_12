package authz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPolicyMatrix(t *testing.T) {
	type subject struct {
		name  string
		p     Principal
		level Level
	}
	admin := subject{"admin tanpa group", Principal{UserID: 1, IsPlatformAdmin: true}, LevelNone}
	edit := subject{"user edit", Principal{UserID: 2}, LevelEdit}
	view := subject{"user view", Principal{UserID: 3}, LevelView}
	none := subject{"user tanpa akses", Principal{UserID: 4}, LevelNone}
	subjects := []subject{admin, edit, view, none}

	allowed := map[Action][]subject{
		ActionManageUsers:       {admin},
		ActionManageGroups:      {admin},
		ActionManageConnections: {admin},
		ActionManageGrants:      {admin},
		ActionDBView:            {edit, view},
		ActionMigrationRead:     {edit, view},
		ActionSchemaDiff:        {edit, view},
		ActionMigrationWrite:    {edit},
		ActionMigrationExecute:  {edit},
		ActionMigrationExplain:  {edit},
	}

	for action, ok := range allowed {
		for _, s := range subjects {
			want := false
			for _, a := range ok {
				want = want || a.name == s.name
			}
			err := Check(s.p, action, s.level)
			if want {
				assert.NoError(t, err, "%s harus boleh %s", s.name, action)
			} else {
				assert.ErrorIs(t, err, ErrForbidden, "%s tidak boleh %s", s.name, action)
			}
		}
	}
}

func TestAdminWithGrantGetsDatabaseAccess(t *testing.T) {
	assert.NoError(t, Check(Principal{UserID: 1, IsPlatformAdmin: true}, ActionDBView, LevelView))
}

func TestUnknownActionDenied(t *testing.T) {
	assert.ErrorIs(t, Check(Principal{IsPlatformAdmin: true}, "unknown", LevelEdit), ErrForbidden)
}

func TestFake(t *testing.T) {
	az := NewFake().Grant(2, 1, LevelView)
	ctx := context.Background()
	p := Principal{UserID: 2}

	assert.NoError(t, az.Authorize(ctx, p, ActionDBView, Resource{DatabaseID: 1}))
	assert.ErrorIs(t, az.Authorize(ctx, p, ActionMigrationExecute, Resource{DatabaseID: 1}), ErrForbidden)
	assert.ErrorIs(t, az.Authorize(ctx, p, ActionDBView, Resource{DatabaseID: 2}), ErrForbidden)
}
