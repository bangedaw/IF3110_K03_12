package migration

import (
	"slices"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

type positions struct {
	active []int64
	redo   []int64
}

func replay(runs []gen.ListSucceededRunsRow) positions {
	var p positions
	for _, r := range runs {
		switch r.Kind {
		case gen.RequestKindApply:
			p.active = append(p.active, r.MigrationID)
			p.redo = nil
		case gen.RequestKindRollback:
			p.active = without(p.active, r.MigrationID)
			p.redo = append(without(p.redo, r.MigrationID), r.MigrationID)
		case gen.RequestKindForward:
			p.redo = without(p.redo, r.MigrationID)
			p.active = append(p.active, r.MigrationID)
		}
	}
	return p
}

func without(ids []int64, id int64) []int64 {
	return slices.DeleteFunc(ids, func(v int64) bool { return v == id })
}

func isLast(ids []int64, id int64) bool { return len(ids) > 0 && ids[len(ids)-1] == id }

type state struct {
	positions
	requests []request
}

func (s state) latest(migrationID int64) *request {
	return s.find(migrationID, func(request) bool { return true })
}

func (s state) current(migrationID int64) *request {
	return s.find(migrationID, func(r request) bool { return r.Status != gen.RequestStatusRejected })
}

func (s state) open(migrationID int64) *request {
	return s.find(migrationID, request.open)
}

func (s state) find(migrationID int64, match func(request) bool) *request {
	for i := len(s.requests) - 1; i >= 0; i-- {
		if r := s.requests[i]; r.MigrationID == migrationID && match(r) {
			return &r
		}
	}
	return nil
}

func (s state) status(m gen.Migration) apigen.MigrationStatus {
	latest := s.latest(m.ID)
	if latest == nil {
		if m.Status == gen.MigStatusDraft {
			return apigen.MigrationStatusDraft
		}
		return apigen.MigrationStatusSucceeded
	}
	if cur := s.current(m.ID); cur != nil && cur.Status == gen.RequestStatusPartial && !latest.open() {
		return apigen.MigrationStatusPartial
	}
	if m.Status == gen.MigStatusDraft && latest.Revision != m.Revision {
		return apigen.MigrationStatusDraft
	}
	return apigen.MigrationStatus(latest.effective())
}

func conflict(detail string) error { return apperr.New(apperr.CodeConflict, detail) }

func (s state) check(kind gen.RequestKind, m gen.Migration, self int64) error {
	var partialUp, partialDown bool
	for _, r := range s.requests {
		if r.Status != gen.RequestStatusPartial || r.ID == self {
			continue
		}
		if r.MigrationID != m.ID {
			return conflict("migration lain di database ini masih partial; lanjutkan atau batalkan dulu")
		}
		if r.up() {
			partialUp = true
		} else {
			partialDown = true
		}
	}
	if partialDown {
		return conflict("rollback migration ini masih partial; lanjutkan dulu sampai selesai")
	}
	switch kind {
	case gen.RequestKindApply, gen.RequestKindForward:
		if partialUp {
			return conflict("migration ini masih partial; lanjutkan atau rollback dulu")
		}
		if kind == gen.RequestKindApply && m.Status != gen.MigStatusDraft {
			return conflict("migration sudah pernah diterapkan; gunakan rollback atau forward")
		}
		if kind == gen.RequestKindForward && !isLast(s.redo, m.ID) {
			return conflict("forward hanya untuk migration yang paling akhir di-rollback, selama belum ada migration baru yang diterapkan")
		}
	case gen.RequestKindRollback:
		if m.Irreversible || !m.DownSql.Valid {
			return conflict("migration irreversible tidak bisa di-rollback: " + m.IrreversibleReason)
		}
		if !partialUp && !isLast(s.active, m.ID) {
			return conflict("rollback hanya untuk migration aktif terakhir")
		}
	}
	return nil
}

func (s state) checkNew(kind gen.RequestKind, m gen.Migration) error {
	if s.open(m.ID) != nil {
		return conflict("masih ada request yang belum selesai untuk migration ini")
	}
	return s.check(kind, m, 0)
}

func (s state) executable(m gen.Migration) *request {
	cur := s.current(m.ID)
	if cur == nil || cur.Revision != m.Revision {
		return nil
	}
	switch cur.effective() {
	case gen.RequestStatusApproved, gen.RequestStatusPartial:
	case gen.RequestStatusFailed:
		if s.latest(m.ID).ID != cur.ID {
			return nil
		}
	default:
		return nil
	}
	if s.check(cur.Kind, m, cur.ID) != nil {
		return nil
	}
	return cur
}

func (s state) editable(m gen.Migration, sqlLocked bool) error {
	switch {
	case sqlLocked:
		return conflict("SQL migration yang pernah dieksekusi tidak bisa diubah; buat migration baru")
	case m.Status != gen.MigStatusDraft:
		return conflict("hanya draft yang bisa diubah")
	case s.open(m.ID) != nil:
		return conflict("draft tidak bisa diubah selama masih ada request yang belum selesai")
	}
	return nil
}

func (s state) actions(m gen.Migration, sqlLocked bool, level authz.Level) []apigen.MigrationAction {
	out := []apigen.MigrationAction{}
	if level != authz.LevelEdit {
		return out
	}
	if s.editable(m, sqlLocked) == nil {
		out = append(out, apigen.MigrationActionEdit, apigen.MigrationActionDelete)
	}
	if s.executable(m) != nil {
		out = append(out, apigen.MigrationActionExecute)
	}
	for kind, action := range map[gen.RequestKind]apigen.MigrationAction{
		gen.RequestKindApply:    apigen.MigrationActionSubmit,
		gen.RequestKindRollback: apigen.MigrationActionRollback,
		gen.RequestKindForward:  apigen.MigrationActionForward,
	} {
		if s.checkNew(kind, m) == nil {
			out = append(out, action)
		}
	}
	if !m.BatchTable.Valid {
		out = append(out, apigen.MigrationActionExplain)
	}
	slices.Sort(out)
	return out
}
