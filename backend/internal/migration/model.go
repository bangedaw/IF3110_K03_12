package migration

import (
	"context"
	"time"
)

type Status string

const (
	Draft        Status = "draft"
	Applied      Status = "applied"
	RunRunning   Status = "running"
	RunSucceeded Status = "succeeded"
	RunFailed    Status = "failed"
)

type Migration struct {
	ID          int64      `json:"id"`
	DatabaseID  int64      `json:"databaseId"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	SQL         string     `json:"sql"`
	Checksum    string     `json:"checksum"`
	Status      Status     `json:"status"`
	Version     *int       `json:"version,omitempty"`
	BaseVersion int        `json:"baseVersion"`
	Revision    int        `json:"revision"`
	CreatedBy   int64      `json:"createdBy"`
	UpdatedBy   int64      `json:"updatedBy"`
	AppliedBy   *int64     `json:"appliedBy,omitempty"`
	AppliedAt   *time.Time `json:"appliedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}
type Run struct {
	ID                   int64      `json:"id"`
	MigrationID          int64      `json:"migrationId"`
	DatabaseID           int64      `json:"databaseId"`
	SQL                  string     `json:"sql"`
	Checksum             string     `json:"checksum"`
	ExecutedBy           int64      `json:"executedBy"`
	Status               Status     `json:"status"`
	StartedAt            time.Time  `json:"startedAt"`
	FinishedAt           *time.Time `json:"finishedAt,omitempty"`
	ErrorCode            string     `json:"errorCode,omitempty"`
	ErrorMessage         string     `json:"errorMessage,omitempty"`
	FailedStatementIndex *int       `json:"failedStatementIndex,omitempty"`
}
type CreateInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	SQL         string `json:"sql"`
}
type UpdateInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	SQL         string `json:"sql"`
	Revision    int    `json:"revision"`
}
type ExecuteInput struct {
	ExpectedRevision       int  `json:"expectedRevision"`
	AcknowledgeHeadChanged bool `json:"acknowledgeHeadChanged"`
}

type Repository interface {
	Head(ctx context.Context, databaseID int64) (int, error)
	CreateDraft(ctx context.Context, actorID, databaseID int64, in CreateInput, baseVersion int, checksum []byte) (*Migration, error)
	Get(ctx context.Context, databaseID, migrationID int64) (*Migration, error)
	List(ctx context.Context, databaseID int64, status string, limit, offset int) ([]Migration, int, error)
	UpdateDraft(ctx context.Context, actorID, databaseID, migrationID int64, in UpdateInput, checksum []byte) (*Migration, error)
	DeleteDraft(ctx context.Context, databaseID, migrationID int64, revision int) error
	StartRun(ctx context.Context, actorID int64, m *Migration) (*Run, error)
	FinishRun(ctx context.Context, runID int64, status Status, version *int, errCode, errMessage string, failedIndex *int) error
	Runs(ctx context.Context, databaseID, migrationID int64) ([]Run, error)
	History(ctx context.Context, databaseID int64, limit, offset int) ([]Run, int, error)
	WithDatabaseLock(ctx context.Context, databaseID int64, fn func(context.Context) error) error
	SaveSnapshot(ctx context.Context, databaseID, runID, actorID int64, model any) error
	Audit(ctx context.Context, actorID int64, action, resourceType, resourceID, outcome string, metadata map[string]any) error
}
type TargetRegistry interface {
	Get(ctx context.Context, databaseID int64) (Target, error)
}
type Target interface {
	Apply(ctx context.Context, in ApplyRequest) (*ApplyResult, error)
	Explain(ctx context.Context, sql string, analyze bool) (any, error)
	Snapshot(ctx context.Context) (any, error)
}
type ApplyRequest struct {
	RunID, MigrationID, DatabaseID int64
	Version                        int
	Checksum                       []byte
	SQL                            string
}
type ApplyResult struct {
	StatementsExecuted int
	AppliedAt          time.Time
}
type Authorizer interface {
	Authorize(ctx context.Context, userID, databaseID int64, action string) error
}
type Service struct {
	Repo    Repository
	Targets TargetRegistry
	Auth    Authorizer
	Now     func() time.Time
}
