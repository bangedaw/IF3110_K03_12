// Package dbms adalah kontrak antara domain explorer/migration dan driver
// DBMS target (PostgreSQL, MySQL). Interface di sini dibekukan di Fase 0:
// Inspector/Browser milik Edo, Migrator/Explainer milik Rhino.
package dbms

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

const (
	KindPostgres = "postgres"
	KindMySQL    = "mysql"
)

var (
	// ErrNotFound dipakai bila objek yang diminta tidak ada di DBMS target
	// (tabel/schema tidak ditemukan) — dipetakan explorer ke HTTP 404.
	ErrNotFound = errors.New("objek tidak ditemukan di database target")
	// ErrNotImplemented dipakai oleh stub Migrator/Explainer sebelum
	// implementasi pemiliknya (Rhino) masuk.
	ErrNotImplemented = errors.New("belum diimplementasi")
)

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
	// Snapshot mengambil seluruh schema; dipakai schema diff dan disimpan
	// setelah migration sukses.
	Snapshot(ctx context.Context) (*schema.Model, error)
}

type DataBrowser interface {
	Rows(ctx context.Context, q RowsQuery) (*RowsPage, error)
}

type Migrator interface {
	// SupportsTransactionalDDL: PostgreSQL true, MySQL false (DDL implicit commit).
	SupportsTransactionalDDL() bool
	Apply(ctx context.Context, in ApplyInput) (*ApplyResult, error)
}

type Explainer interface {
	Explain(ctx context.Context, in ExplainInput) (*ExplainResult, error)
}

// RowsQuery bersifat read-only. OrderBy hanya boleh berisi nama kolom yang
// ada di tabel; driver wajib memvalidasinya terhadap hasil introspeksi.
type RowsQuery struct {
	Schema   string
	Table    string
	Limit    int
	Offset   int
	OrderBy  string
	OrderDir string // "asc" (default) | "desc"
}

// MaxRows adalah batas limit yang dipakai explorer.
const MaxRows = 200

type RowsPage struct {
	Columns       []string `json:"columns"`
	Rows          [][]Cell `json:"rows"`
	TotalEstimate int64    `json:"totalEstimate"`
	Limit         int      `json:"limit"`
	Offset        int      `json:"offset"`
}

// Cell adalah nilai sel yang aman dikirim ke JSON (sudah dipotong/di-encode).
type Cell struct {
	Value     string `json:"value"`
	IsNull    bool   `json:"isNull"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ApplyInput milik Rhino; field bisa bertambah sesuai kebutuhan engine.
type ApplyInput struct {
	Statements       []string
	RunID            int64
	Version          int
	LockTimeout      time.Duration
	StatementTimeout time.Duration
}

type ApplyResult struct {
	FailedStatementIndex int
	ErrorCode            string
	ErrorMessage         string
	Position             int
}

type ExplainInput struct {
	Statements []string
	Analyze    bool
	Timeout    time.Duration
}

type ExplainResult struct {
	Plans []StatementPlan
}

// StatementPlan milik Rhino: JSON plan apa adanya dari DBMS.
type StatementPlan struct {
	Index       int
	Explainable bool
	JSON        string
	Error       string
}

// UnimplementedMigrator/UnimplementedExplainer dipakai driver sampai
// implementasi milik Rhino masuk, agar pemanggilan tidak panik.
func UnimplementedMigrator(TransactionalDDL bool) Migrator {
	return unimplementedMigrator{transactionalDDL: TransactionalDDL}
}

type unimplementedMigrator struct{ transactionalDDL bool }

func (m unimplementedMigrator) SupportsTransactionalDDL() bool { return m.transactionalDDL }

func (unimplementedMigrator) Apply(context.Context, ApplyInput) (*ApplyResult, error) {
	return nil, ErrNotImplemented
}

func UnimplementedExplainer() Explainer { return unimplementedExplainer{} }

type unimplementedExplainer struct{}

func (unimplementedExplainer) Explain(context.Context, ExplainInput) (*ExplainResult, error) {
	return nil, ErrNotImplemented
}

// Driver didaftarkan oleh package driver masing-masing (init), sehingga
// menambah DBMS baru cukup menambah satu package dan mengimpornya di wiring.
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
