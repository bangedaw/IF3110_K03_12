package dbms

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

const (
	KindPostgres = "postgres"
	KindMySQL    = "mysql"
)

var (
	ErrNotFound = errors.New("objek tidak ditemukan di database target")

	ErrNotImplemented = errors.New("belum diimplementasi")

	ErrInvalidQuery = errors.New("query tidak valid")

	ErrCommitUncertain = errors.New("hasil commit tidak pasti, periksa riwayat target sebelum retry")

	ErrInvalidDirection = errors.New("arah eksekusi migration tidak dikenal")
)

type Direction string

const (
	DirectionUp   Direction = "up"
	DirectionDown Direction = "down"
)

func (d Direction) Normalize() (Direction, error) {
	switch d {
	case "", DirectionUp:
		return DirectionUp, nil
	case DirectionDown:
		return DirectionDown, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidDirection, string(d))
}

type StatementError struct {
	Batch    int
	Index    int
	SQL      string
	SQLState string
	Message  string
	Detail   string
	Hint     string
	Position int
	Err      error
}

func (e *StatementError) Error() string {
	prefix := ""
	if e.Batch > 0 {
		prefix = fmt.Sprintf("batch %d: ", e.Batch)
	}
	if e.Index == 0 {
		return fmt.Sprintf("%scommit failed: %s", prefix, e.Message)
	}
	return fmt.Sprintf("%sstatement %d failed: %s", prefix, e.Index, e.Message)
}

func (e *StatementError) Unwrap() error { return e.Err }

func (e *StatementError) StatementIndex() int { return e.Index }

func (e *StatementError) Retryable() bool { return RetryableSQLState(e.SQLState) }

func RetryableSQLState(code string) bool {
	switch code {
	case "40001", "40P01", "55P03", "57P03":
		return true
	}
	return strings.HasPrefix(code, "08") || strings.HasPrefix(code, "53")
}

type ConnConfig struct {
	Kind     string
	Host     string
	Port     int
	Database string
	User     string
	Password string
	SSLMode  string
}

type Driver interface {
	Kind() string
	Open(ctx context.Context, cfg ConnConfig) (Conn, error)
}

type Conn interface {
	Ping(ctx context.Context) error
	Inspector() Inspector
	Browser() DataBrowser
	Migrator() Migrator
	Explainer() Explainer
	Close() error
}

type Inspector interface {
	ListSchemas(ctx context.Context) ([]string, error)
	ListTables(ctx context.Context, schema string) ([]schema.TableSummary, error)
	DescribeTable(ctx context.Context, schemaName, table string) (*schema.Table, error)

	Snapshot(ctx context.Context) (*schema.Model, error)
}

type DataBrowser interface {
	Rows(ctx context.Context, q RowsQuery) (*RowsPage, error)
}

type Migrator interface {
	SupportsTransactionalDDL() bool
	Apply(ctx context.Context, in ApplyInput) (*ApplyResult, error)
	ApplyBatches(ctx context.Context, in BatchInput) (BatchProgress, error)
	CommittedVersion(ctx context.Context, runID int64) (version int, found bool, err error)
}

type Explainer interface {
	Explain(ctx context.Context, in ExplainInput) (*ExplainResult, error)
}

type RowsQuery struct {
	Schema   string
	Table    string
	Limit    int
	Offset   int
	OrderBy  string
	OrderDir string
}

const MaxRows = 200

type RowsPage struct {
	Columns       []string `json:"columns"`
	Rows          [][]Cell `json:"rows"`
	TotalEstimate int64    `json:"totalEstimate"`
	Limit         int      `json:"limit"`
	Offset        int      `json:"offset"`
}

type Cell struct {
	Value     string `json:"value"`
	IsNull    bool   `json:"isNull"`
	Truncated bool   `json:"truncated,omitempty"`
}

type ApplyInput struct {
	SQL              string
	Direction        Direction
	DatabaseID       int64
	MigrationID      int64
	RunID            int64
	Version          int
	Checksum         []byte
	LockTimeout      time.Duration
	StatementTimeout time.Duration
}

type ApplyResult struct {
	StatementsExecuted   int
	Statements           []string
	AppliedAt            time.Time
	FailedStatementIndex int
	ErrorCode            string
	ErrorMessage         string
	Position             int
}

type BatchInput struct {
	SQL         string
	Table       string
	Column      string
	Size        int
	Revert      bool
	DatabaseID  int64
	MigrationID int64
	RunID       int64
	OnBatch     func(BatchProgress)
}

type BatchProgress struct {
	Batches int
	Rows    int64
}

type ExplainInput struct {
	SQL              string
	Analyze          bool
	StatementTimeout time.Duration
}

type ExplainResult struct {
	Statements []ExplainStatement
}

type ExplainStatement struct {
	Index       int
	SQL         string
	Explainable bool
	Notice      string
	Plan        any
}

func UnimplementedMigrator(TransactionalDDL bool) Migrator {
	return unimplementedMigrator{transactionalDDL: TransactionalDDL}
}

type unimplementedMigrator struct{ transactionalDDL bool }

func (m unimplementedMigrator) SupportsTransactionalDDL() bool { return m.transactionalDDL }

func (unimplementedMigrator) Apply(context.Context, ApplyInput) (*ApplyResult, error) {
	return nil, ErrNotImplemented
}

func (unimplementedMigrator) ApplyBatches(context.Context, BatchInput) (BatchProgress, error) {
	return BatchProgress{}, ErrNotImplemented
}

func (unimplementedMigrator) CommittedVersion(context.Context, int64) (int, bool, error) {
	return 0, false, ErrNotImplemented
}

func UnimplementedExplainer() Explainer { return unimplementedExplainer{} }

type unimplementedExplainer struct{}

func (unimplementedExplainer) Explain(context.Context, ExplainInput) (*ExplainResult, error) {
	return nil, ErrNotImplemented
}

var (
	driverMu     sync.RWMutex
	driverByKind = map[string]Driver{}
)

func Register(d Driver) {
	driverMu.Lock()
	defer driverMu.Unlock()
	driverByKind[d.Kind()] = d
}

func DriverFor(kind string) (Driver, bool) {
	driverMu.RLock()
	defer driverMu.RUnlock()
	d, ok := driverByKind[kind]
	return d, ok
}
