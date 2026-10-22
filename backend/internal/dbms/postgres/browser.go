package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
)

// TODO(edo): implementasi nyata ada di tugas E-6 (data viewer read-only).
type Browser struct {
	pool *pgxpool.Pool
}

var _ dbms.DataBrowser = (*Browser)(nil)

func (*Browser) Rows(context.Context, dbms.RowsQuery) (*dbms.RowsPage, error) {
	return nil, dbms.ErrNotImplemented
}
