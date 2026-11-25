package queue

import (
	"context"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
)

type PublishInput struct {
	RequestID  int64
	DatabaseID int64
	ActorID    int64
}

type Publisher interface {
	Publish(ctx context.Context, in PublishInput) (jobID int64, err error)
}

type StartInput struct {
	RequestID int64
	JobID     int64
	ActorID   int64
	Attempt   int
}

type Execution struct {
	RunID           int64
	CheckpointRunID int64
	MigrationID     int64
	DatabaseID      int64
	Direction       dbms.Direction
	SQL             string
	Version         int
	Checksum        []byte
	Batch           *dbms.BatchSpec
}

type FinishInput struct {
	RunID     int64
	Err       error
	Progress  *dbms.BatchProgress
	WillRetry bool
}

type Lifecycle interface {
	Start(ctx context.Context, in StartInput) (*Execution, error)
	Finish(ctx context.Context, in FinishInput) error
	// Interrupted mengembalikan eksekusi attempt ke-attempt dari job yang run-nya masih running, dipakai
	// pemulihan setelah worker mati. Hasilnya nil jika attempt itu belum sempat membuat run.
	Interrupted(ctx context.Context, jobID int64, attempt int) (*Execution, error)
}
