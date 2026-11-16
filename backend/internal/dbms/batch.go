package dbms

import (
	"context"
	"errors"
	"time"
)

var (
	ErrBatchScopeMismatch = errors.New("cakupan batch berbeda dengan eksekusi sebelumnya")

	ErrBatchRunReverted = errors.New("eksekusi batch sudah dibatalkan sebagian atau seluruhnya, tidak dapat dilanjutkan")

	ErrBatchRunUnknown = errors.New("eksekusi batch tidak ditemukan di database target")

	ErrBatchSQL = errors.New("SQL batch harus berupa satu statement yang memakai $1 (cursor eksklusif) dan $2 (batas inklusif)")

	ErrBatchKey = errors.New("kolom batch harus bertipe integer dan unik")

	ErrIrreversible = errors.New("migration tidak memiliki SQL down sehingga tidak dapat dibatalkan")
)

type BatchSpec struct {
	Schema    string
	Table     string
	KeyColumn string
	Size      int
}

type BatchApplyInput struct {
	SQL              string
	Batch            BatchSpec
	DatabaseID       int64
	MigrationID      int64
	RunID            int64
	Version          int
	Checksum         []byte
	LockTimeout      time.Duration
	StatementTimeout time.Duration
	OnBatch          func(BatchProgress)
}

type BatchRevertInput struct {
	SQL              string
	DatabaseID       int64
	RunID            int64
	ByRunID          int64
	LockTimeout      time.Duration
	StatementTimeout time.Duration
	OnBatch          func(BatchProgress)
}

type BatchState string

const (
	BatchPending           BatchState = "pending"
	BatchPartial           BatchState = "partial"
	BatchCompleted         BatchState = "completed"
	BatchPartiallyReverted BatchState = "partially_reverted"
	BatchReverted          BatchState = "reverted"
)

type BatchProgress struct {
	RunID            int64
	BatchesCommitted int
	RowsCommitted    int64
	BatchesReverted  int
	RowsReverted     int64
	Cursor           int64
	LowerBound       int64
	UpperBound       int64
	Completed        bool
	RevertCompleted  bool
}

func (p BatchProgress) State() BatchState {
	switch {
	case p.RevertCompleted:
		return BatchReverted
	case p.BatchesReverted > 0:
		return BatchPartiallyReverted
	case p.Completed:
		return BatchCompleted
	case p.BatchesCommitted > 0:
		return BatchPartial
	}
	return BatchPending
}

type BatchMigrator interface {
	ApplyBatches(ctx context.Context, in BatchApplyInput) (*BatchProgress, error)
	RevertBatches(ctx context.Context, in BatchRevertInput) (*BatchProgress, error)
	BatchProgress(ctx context.Context, runID int64) (*BatchProgress, error)
}
