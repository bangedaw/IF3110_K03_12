package migration

import "time"

// Status adalah status workflow migration (M2) sekaligus status satu attempt
// eksekusi (hanya running/success/failed yang dipakai di Execution).
type Status string

const (
	StatusDraft         Status = "draft"
	StatusPendingReview Status = "pending_review"
	StatusApproved      Status = "approved"
	StatusPendingQueue  Status = "pending_queue"
	StatusRunning       Status = "running"
	StatusSuccess       Status = "success"
	StatusFailed        Status = "failed"
)

func (s Status) IsTerminal() bool { return s == StatusSuccess || s == StatusFailed }

func (s Status) IsValid() bool {
	switch s {
	case StatusDraft, StatusPendingReview, StatusApproved,
		StatusPendingQueue, StatusRunning, StatusSuccess, StatusFailed:
		return true
	}
	return false
}

// Kind adalah jenis eksekusi.
type Kind string

const (
	KindApply    Kind = "apply"
	KindRollback Kind = "rollback"
	KindForward  Kind = "forward"
)

func (k Kind) IsValid() bool {
	switch k {
	case KindApply, KindRollback, KindForward:
		return true
	}
	return false
}

// BatchConfig: nil pada Migration/Definition berarti mode biasa.
type BatchConfig struct {
	Column string `json:"column"`
	Size   int    `json:"size"`
}

func (b *BatchConfig) IsValid() bool {
	if b == nil {
		return true
	}
	return b.Column != "" && b.Size > 0
}

// Definition adalah input pembuatan/perubahan draft (R-1). Satu-satunya tipe
// input untuk Create dan UpdateDraft.
type Definition struct {
	Title              string       `json:"title"`
	Description        string       `json:"description"`
	UpSQL              string       `json:"upSql"`
	DownSQL            string       `json:"downSql"`
	Irreversible       bool         `json:"irreversible"`
	IrreversibleReason string       `json:"irreversibleReason"`
	Batch              *BatchConfig `json:"batch,omitempty"`
}

// Migration adalah satu-satunya representasi migration. Kolom M2 ada di
// bagian atas; field di bagian "legacy M1" hanya diisi oleh PGRepository
// (repository.go) dan dipakai Reconciler.
type Migration struct {
	ID          int64  `json:"id"`
	DatabaseID  int64  `json:"databaseId"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      Status `json:"status"`
	Revision    int    `json:"revision"`
	// Seq adalah urutan linear per database (1, 2, 3, ...), diberikan Store
	// saat dibuat. Dipakai rules.go untuk urutan apply/rollback/forward.
	// Berbeda dari Version (legacy) yang baru terisi setelah apply.
	Seq                int          `json:"seq"`
	UpSQL              string       `json:"upSql"`
	DownSQL            *string      `json:"downSql,omitempty"` // nil jika irreversible
	Irreversible       bool         `json:"irreversible"`
	IrreversibleReason string       `json:"irreversibleReason,omitempty"`
	Batch              *BatchConfig `json:"batch,omitempty"`
	SQLLocked          bool         `json:"sqlLocked"` // true setelah pernah dieksekusi
	CreatedBy          int64        `json:"createdBy"`
	UpdatedBy          int64        `json:"updatedBy"`
	CreatedAt          time.Time    `json:"createdAt"`
	UpdatedAt          time.Time    `json:"updatedAt"`

	// --- legacy M1 (jangan dipakai kode M2) ---
	SQL         string     `json:"sql"`
	Checksum    string     `json:"checksum"`
	Version     *int       `json:"version,omitempty"`
	BaseVersion int        `json:"baseVersion"`
	AppliedBy   *int64     `json:"appliedBy,omitempty"`
	AppliedAt   *time.Time `json:"appliedAt,omitempty"`
}

// Execution adalah satu baris riwayat per attempt (R-4). Tidak pernah dihapus;
// hanya boleh difinalisasi satu kali (running -> success/failed).
type Execution struct {
	ID                 int64
	MigrationID        int64
	Revision           int
	UserID             int64
	Kind               Kind
	Status             Status // running/success/failed
	ExecutedSQL        string // salinan SQL yang benar-benar dijalankan
	RevertsExecutionID *int64 // diisi pada rollback: execution yang dibatalkan
	ErrorMsg           string
	SQLState           string
	StartedAt          time.Time
	FinishedAt         time.Time // zero selama masih running
}

// ExecutionResult dilaporkan worker saat attempt selesai.
type ExecutionResult struct {
	Success     bool
	ExecutedSQL string
	ErrorMsg    string
	SQLState    string
}

// ExecutionRequest dikirim ke publish job (kontrak Migu).
type ExecutionRequest struct {
	MigrationID int64
	ExecutionID int64 // 0 sampai worker membuat attempt
	RequestedBy int64
	Kind        Kind
}

// Approver adalah leader group yang wajib menyetujui.
type Approver struct {
	GroupID  int64
	LeaderID int64
	Name     string // opsional, untuk UI
}

type ApprovalDecision string

const (
	ApprovalPending  ApprovalDecision = "pending"
	ApprovalApproved ApprovalDecision = "approved"
	ApprovalRejected ApprovalDecision = "rejected"
)

func (d ApprovalDecision) IsValid() bool {
	switch d {
	case ApprovalPending, ApprovalApproved, ApprovalRejected:
		return true
	}
	return false
}

func (d ApprovalDecision) IsDecided() bool { return d == ApprovalApproved || d == ApprovalRejected }

// Approval: satu baris per (migration, revision, kind, leader).
type Approval struct {
	MigrationID int64
	Revision    int
	GroupID     int64
	LeaderID    int64
	Kind        Kind
	Decision    ApprovalDecision
	Comment     string
	DecidedAt   *time.Time
}

// Actor adalah pengguna yang memanggil Service.
type Actor struct {
	UserID int64
}

// HistoryFilter dipakai ListHistory dan Store.ListExecutions. Store hanya
// memakai MigrationID; HideRollback diterapkan Service (HideRollbackPairs).
type HistoryFilter struct {
	HideRollback bool
	MigrationID  *int64
}
