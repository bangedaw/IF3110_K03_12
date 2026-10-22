package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

// TODO(edo): implementasi nyata ada di tugas E-5 (introspeksi pg_catalog).
type Inspector struct {
	pool *pgxpool.Pool
}

var _ dbms.Inspector = (*Inspector)(nil)

func (*Inspector) ListSchemas(context.Context) ([]string, error) {
	return nil, dbms.ErrNotImplemented
}

func (*Inspector) ListTables(context.Context, string) ([]schema.TableSummary, error) {
	return nil, dbms.ErrNotImplemented
}

func (*Inspector) DescribeTable(context.Context, string, string) (*schema.Table, error) {
	return nil, dbms.ErrNotImplemented
}

func (*Inspector) Snapshot(context.Context) (*schema.Model, error) {
	return nil, dbms.ErrNotImplemented
}
