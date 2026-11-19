package migration

import (
	"fmt"
	"slices"
	"sort"
)

// CheckApply memvalidasi apakah `m` boleh di-apply mengingat riwayat dan
// daftar migration.
//
// Aturan:
//   - m belum pernah dieksekusi (SQLLocked=false).
//   - Tidak ada eksekusi lain yang masih running.
//   - Urutan linear: m harus migration pertama yang belum aktif menurut Seq.
func CheckApply(m Migration, history []Execution, all []Migration) error {
	if m.SQLLocked {
		return fmt.Errorf("%w: migration %d sudah pernah dieksekusi", ErrApplyBlocked, m.ID)
	}
	if id, busy := runningExecution(history); busy {
		return fmt.Errorf("%w: ada eksekusi lain yang belum selesai (execution %d)", ErrApplyBlocked, id)
	}

	active := ActiveMigrations(history, all)
	next, ok := nextPending(all, active)
	if !ok {
		return fmt.Errorf("%w: tidak ada migration pending", ErrNoPendingMigration)
	}
	if next.ID != m.ID {
		return fmt.Errorf("%w: bukan giliran migration %d (berikutnya: %d %q)",
			ErrApplyBlocked, m.ID, next.ID, next.Title)
	}
	return nil
}

// CheckRollback memvalidasi rollback `m`.
//
// Aturan: bukan irreversible, punya DownSQL, tidak ada eksekusi yang masih
// running, m sedang aktif, dan m adalah migration aktif terakhir.
func CheckRollback(m Migration, history []Execution, all []Migration) error {
	if m.Irreversible {
		reason := m.IrreversibleReason
		if reason == "" {
			reason = "(tanpa alasan)"
		}
		return fmt.Errorf("%w: migration %d: %s", ErrIrreversible, m.ID, reason)
	}
	if m.DownSQL == nil || *m.DownSQL == "" {
		return fmt.Errorf("%w: migration %d tidak punya DownSQL", ErrRollbackBlocked, m.ID)
	}
	if id, busy := runningExecution(history); busy {
		return fmt.Errorf("%w: ada eksekusi lain yang belum selesai (execution %d)", ErrRollbackBlocked, id)
	}
	if !wasApplied(m, history) {
		return fmt.Errorf("%w: migration %d belum pernah sukses di-apply", ErrRollbackBlocked, m.ID)
	}

	active := ActiveMigrations(history, all)
	if len(active) == 0 {
		return fmt.Errorf("%w: tidak ada migration aktif", ErrRollbackBlocked)
	}
	head := active[len(active)-1]
	if head.ID != m.ID {
		return fmt.Errorf("%w: hanya migration aktif terakhir (%d %q) yang bisa di-rollback, bukan %d %q",
			ErrRollbackBlocked, head.ID, head.Title, m.ID, m.Title)
	}
	return nil
}

// CheckForward memvalidasi forward `m`.
//
// Aturan saat ini (belum diubah dari versi sebelumnya): m irreversible
// ditolak, tidak ada eksekusi running, m pernah sukses, dan m bukan head.
func CheckForward(m Migration, history []Execution, all []Migration) error {
	if m.Irreversible {
		return fmt.Errorf("%w: migration %d irreversible, tidak bisa forward", ErrIrreversible, m.ID)
	}
	if id, busy := runningExecution(history); busy {
		return fmt.Errorf("%w: ada eksekusi lain yang belum selesai (execution %d)", ErrForwardBlocked, id)
	}
	if !wasApplied(m, history) {
		return fmt.Errorf("%w: migration %d belum pernah sukses di-apply", ErrForwardBlocked, m.ID)
	}

	active := ActiveMigrations(history, all)
	if len(active) == 0 {
		return fmt.Errorf("%w: tidak ada migration aktif", ErrForwardBlocked)
	}
	head := active[len(active)-1]
	if head.ID == m.ID {
		return fmt.Errorf("%w: migration %d adalah head, tidak ada yang bisa di-forward", ErrForwardBlocked, m.ID)
	}
	return nil
}

// ActiveMigrations menghitung posisi aktif dari riwayat; hasil urut menaik
// menurut Seq. Rollback gagal dan attempt yang belum selesai tidak mengubah
// posisi. Sumber kebenaran adalah riwayat, bukan Migration.Status.
func ActiveMigrations(history []Execution, all []Migration) []Migration {
	byID := make(map[int64]Migration, len(all))
	for _, m := range all {
		byID[m.ID] = m
	}

	state := make(map[int64]bool, len(all))
	for _, e := range sortedExecutions(history) {
		if e.Status != StatusSuccess {
			continue
		}
		switch e.Kind {
		case KindApply:
			state[e.MigrationID] = true
		case KindRollback:
			state[e.MigrationID] = false
		case KindForward:
			state[e.MigrationID] = false
			if next, ok := nextBySeq(byID, e.MigrationID); ok {
				state[next.ID] = true
			}
		}
	}

	out := make([]Migration, 0, len(state))
	for id, applied := range state {
		if !applied {
			continue
		}
		if m, ok := byID[id]; ok {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// lastAppliedExecutionID mengembalikan ID execution sukses (apply/forward)
// terakhir milik migrationID yang belum dibatalkan rollback sukses. Dipakai
// sebagai Execution.RevertsExecutionID saat rollback.
func lastAppliedExecutionID(history []Execution, migrationID int64) *int64 {
	var id *int64
	for _, e := range sortedExecutions(history) {
		if e.MigrationID != migrationID || e.Status != StatusSuccess {
			continue
		}
		switch e.Kind {
		case KindApply, KindForward:
			v := e.ID
			id = &v
		case KindRollback:
			id = nil
		}
	}
	return id
}

// --- helper internal ---

func runningExecution(history []Execution) (int64, bool) {
	for _, e := range history {
		if e.Status == StatusRunning {
			return e.ID, true
		}
	}
	return 0, false
}

func sortedExecutions(history []Execution) []Execution {
	hs := slices.Clone(history)
	sortExecutions(hs)
	return hs
}

// wasApplied: apakah m sedang dalam kondisi applied menurut riwayat.
func wasApplied(m Migration, history []Execution) bool {
	applied := false
	for _, e := range sortedExecutions(history) {
		if e.Status != StatusSuccess || e.MigrationID != m.ID {
			continue
		}
		switch e.Kind {
		case KindApply, KindForward:
			applied = true
		case KindRollback:
			applied = false
		}
	}
	return applied
}

// nextPending: migration berikutnya (urut Seq) yang belum aktif.
func nextPending(all []Migration, active []Migration) (Migration, bool) {
	applied := make(map[int64]struct{}, len(active))
	for _, m := range active {
		applied[m.ID] = struct{}{}
	}
	candidates := make([]Migration, 0, len(all))
	for _, m := range all {
		if _, ok := applied[m.ID]; !ok {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) == 0 {
		return Migration{}, false
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Seq < candidates[j].Seq })
	return candidates[0], true
}

// nextBySeq: migration dengan Seq terkecil di atas migration `id`.
func nextBySeq(byID map[int64]Migration, id int64) (Migration, bool) {
	cur, ok := byID[id]
	if !ok {
		return Migration{}, false
	}
	var best *Migration
	for _, m := range byID {
		if m.Seq <= cur.Seq {
			continue
		}
		if best == nil || m.Seq < best.Seq {
			mm := m
			best = &mm
		}
	}
	if best == nil {
		return Migration{}, false
	}
	return *best, true
}
