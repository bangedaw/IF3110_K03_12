package migration

import (
	"context"
	"errors"
)

// Semua interface di batas antar-ruas/antar-paket ada di sini.

// Publisher: sambungan ke Migu (M-1). Rhino hanya memanggil, setelah semua
// approval lengkap. Implementasi asli: internal/queue. Bila kontrak M-1
// mengembalikan job ID, ubah di sini saja (satu tempat).
type Publisher interface {
	Publish(ctx context.Context, req ExecutionRequest) error
}

// ExecutionLifecycle: diimplementasikan Rhino (lifecycle.go), dipanggil
// worker Migu (cmd/worker).
type ExecutionLifecycle interface {
	// Revalidate mengulang aturan, approval, dan status sebelum eksekusi.
	Revalidate(ctx context.Context, req ExecutionRequest) error
	// MarkRunning membuat baris attempt (running) dan mengembalikan ID-nya.
	MarkRunning(ctx context.Context, req ExecutionRequest) (attemptID int64, err error)
	// RecordResult menutup attempt, berhasil maupun gagal.
	RecordResult(ctx context.Context, attemptID int64, res ExecutionResult) error
}

// Authorizer adalah kebutuhan akses paket ini, dibuat sempit supaya tidak
// bergantung pada bentuk paket authz. Wiring di cmd/server/main.go memakai
// AuthorizerFuncs yang membungkus authz (View Only -> CanView, View + Edit ->
// CanEdit). CanEdit juga dipakai untuk mengajukan rollback/forward.
type Authorizer interface {
	CanView(ctx context.Context, actor Actor, databaseID int64) error
	CanEdit(ctx context.Context, actor Actor, databaseID int64) error
}

// AuthorizerFuncs mengadaptasi dua fungsi menjadi Authorizer.
type AuthorizerFuncs struct {
	View func(ctx context.Context, actor Actor, databaseID int64) error
	Edit func(ctx context.Context, actor Actor, databaseID int64) error
}

var errAuthNotWired = errors.New("migration: Authorizer belum di-wire")

func (a AuthorizerFuncs) CanView(ctx context.Context, actor Actor, databaseID int64) error {
	if a.View == nil {
		return errAuthNotWired
	}
	return a.View(ctx, actor, databaseID)
}

func (a AuthorizerFuncs) CanEdit(ctx context.Context, actor Actor, databaseID int64) error {
	if a.Edit == nil {
		return errAuthNotWired
	}
	return a.Edit(ctx, actor, databaseID)
}

// LeaderSource memberi daftar leader group yang punya akses ke database.
// Diimplementasikan di internal/admin (Migu, M-5) karena leader dimiliki
// modul group. Group tanpa leader aktif tidak boleh dikembalikan.
type LeaderSource interface {
	ListLeaders(ctx context.Context, databaseID int64) ([]Approver, error)
}
