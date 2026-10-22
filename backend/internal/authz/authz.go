package authz

import (
	"context"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
)

type Action string

const (
	ActionManageUsers       Action = "platform:users:manage"
	ActionManageGroups      Action = "platform:groups:manage"
	ActionManageConnections Action = "platform:connections:manage"
	ActionManageGrants      Action = "platform:grants:manage"

	ActionDBView           Action = "db:view"
	ActionMigrationRead    Action = "migration:read"
	ActionMigrationWrite   Action = "migration:write"
	ActionMigrationExecute Action = "migration:execute"
	ActionMigrationExplain Action = "migration:explain"
	ActionSchemaDiff       Action = "schema:diff"
)

type Level string

const (
	LevelNone Level = ""
	LevelView Level = "view"
	LevelEdit Level = "edit"
)

func (l Level) rank() int {
	switch l {
	case LevelView:
		return 1
	case LevelEdit:
		return 2
	default:
		return 0
	}
}

type Principal struct {
	UserID          int64
	IsPlatformAdmin bool
}

type Resource struct {
	DatabaseID int64
}

type Authorizer interface {
	Authorize(ctx context.Context, p Principal, a Action, r Resource) error
	EffectiveLevel(ctx context.Context, userID, databaseID int64) (Level, error)
}

var ErrForbidden = apperr.ErrForbidden
