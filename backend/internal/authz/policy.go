package authz

var adminOnly = map[Action]bool{
	ActionManageUsers:       true,
	ActionManageGroups:      true,
	ActionManageConnections: true,
	ActionManageGrants:      true,
}

var minLevel = map[Action]Level{
	ActionDBView:           LevelView,
	ActionMigrationRead:    LevelView,
	ActionSchemaDiff:       LevelView,
	ActionMigrationWrite:   LevelEdit,
	ActionMigrationExecute: LevelEdit,
	ActionMigrationExplain: LevelEdit,
}

func Check(p Principal, a Action, level Level) error {
	if adminOnly[a] {
		if p.IsPlatformAdmin {
			return nil
		}
		return ErrForbidden
	}
	need, ok := minLevel[a]
	if ok && level.rank() >= need.rank() {
		return nil
	}
	return ErrForbidden
}
