package migration

import "fmt"

// transitions adalah tabel perpindahan status yang sah.
// Kunci = status asal, nilai = himpunan status tujuan yang diizinkan.
//
// Hanya eksekusi apply yang mengubah Migration.Status. Rollback dan forward
// tidak memindahkan status migration (posisi aktif dihitung dari riwayat,
// lihat ActiveMigrations).
var transitions = map[Status]map[Status]struct{}{
	StatusDraft: {
		StatusPendingReview: {}, // Submit
	},
	StatusPendingReview: {
		StatusApproved: {}, // Approve (semua approval approved)
		StatusDraft:    {}, // Reject / revisi
	},
	StatusApproved: {
		StatusPendingQueue: {}, // Enqueue
		StatusDraft:        {}, // Revoke (mis. publish job gagal)
	},
	StatusPendingQueue: {
		StatusRunning: {}, // MarkRunning (job diambil worker)
		StatusFailed:  {}, // gagal sebelum mulai
		StatusDraft:   {}, // dibatalkan sebelum dijalankan
	},
	StatusRunning: {
		StatusSuccess: {},
		StatusFailed:  {},
	},
	StatusSuccess: {}, // terminal
	StatusFailed: {
		StatusDraft: {}, // revisi setelah gagal
	},
}

// CanTransition melaporkan apakah perpindahan from -> to sah.
func CanTransition(from, to Status) bool {
	dst, ok := transitions[from]
	if !ok {
		return false
	}
	_, ok = dst[to]
	return ok
}

// Transition memindahkan m ke status `to` bila sah; jika tidak, mengembalikan
// ErrInvalidTransition dan tidak mengubah m. Hanya mengubah objek di memori;
// untuk menyimpan ke DB pakai moveStatus (service.go).
func Transition(m *Migration, to Status) error {
	if m == nil {
		return fmt.Errorf("%w: migration nil", ErrInvalidTransition)
	}
	if m.Status == to {
		return nil // idempoten
	}
	if !CanTransition(m.Status, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, m.Status, to)
	}
	m.Status = to
	return nil
}

// Pemicu bernama, agar pemanggil tidak menghardcode status tujuan.

func Submit(m *Migration) error  { return Transition(m, StatusPendingReview) }
func Approve(m *Migration) error { return Transition(m, StatusApproved) }
func Enqueue(m *Migration) error { return Transition(m, StatusPendingQueue) }
func Reject(m *Migration) error  { return Transition(m, StatusDraft) }
func Revise(m *Migration) error  { return Transition(m, StatusDraft) }
func Revoke(m *Migration) error  { return Transition(m, StatusDraft) }

// StartRunning: pending_queue -> running.
func StartRunning(m *Migration) error { return Transition(m, StatusRunning) }

// Finish: running -> success/failed. Hanya dua status itu yang diterima.
func Finish(m *Migration, final Status) error {
	if final != StatusSuccess && final != StatusFailed {
		return fmt.Errorf("%w: Finish hanya menerima success/failed, didapat %s", ErrInvalidTransition, final)
	}
	return Transition(m, final)
}
