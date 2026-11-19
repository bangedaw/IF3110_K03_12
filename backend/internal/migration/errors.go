package migration

import "errors"

// Semua sentinel error paket migration ada di sini (tidak boleh dideklarasikan
// ulang di file lain). Not found dan forbidden memakai apperr.ErrNotFound dan
// authz.ErrForbidden dari paket lain, bukan sentinel baru.
var (
	// Definisi dan SQL (definition.go).
	ErrInvalidDefinition  = errors.New("migration: definisi tidak valid")
	ErrInvalidBatchConfig = errors.New("migration: konfigurasi batch tidak valid")
	ErrInvalidBatchSQL    = errors.New("migration: SQL batch tidak valid")
	ErrSQLNotEditable     = errors.New("migration: SQL tidak dapat diubah")
	ErrIrreversible       = errors.New("migration: migration irreversible tidak dapat di-rollback")

	// Status (status.go, store.go).
	ErrInvalidTransition = errors.New("migration: perpindahan status tidak sah")
	ErrStatusConflict    = errors.New("migration: status sudah berubah oleh proses lain")

	// Approval (approval.go, lifecycle.go).
	ErrNotApprover        = errors.New("migration: actor bukan leader yang wajib menyetujui")
	ErrApprovalNotPending = errors.New("migration: approval tidak dalam status pending")
	ErrAlreadyDecided     = errors.New("migration: actor sudah memutuskan approval ini")
	ErrApprovalIncomplete = errors.New("migration: belum semua leader menyetujui")

	// Aturan apply, rollback, forward (rules.go).
	ErrApplyBlocked       = errors.New("migration: apply tidak diizinkan")
	ErrRollbackBlocked    = errors.New("migration: rollback tidak diizinkan")
	ErrForwardBlocked     = errors.New("migration: forward tidak diizinkan")
	ErrNoPendingMigration = errors.New("migration: tidak ada migration yang menunggu")
)
