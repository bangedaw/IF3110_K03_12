package mysql_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/mysql"
)

type browserFixture struct {
	cfg     dbms.ConnConfig
	browser dbms.DataBrowser
}

func newBrowserFixture(t *testing.T) *browserFixture {
	t.Helper()
	cfg := targetConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	conn, err := mysql.Driver{}.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return &browserFixture{cfg: cfg, browser: conn.Browser()}
}

func (f *browserFixture) rows(t *testing.T, q dbms.RowsQuery) *dbms.RowsPage {
	t.Helper()
	if q.Schema == "" {
		q.Schema = f.cfg.Database
	}
	page, err := f.browser.Rows(context.Background(), q)
	require.NoError(t, err)
	return page
}

func TestRowsPagination(t *testing.T) {
	f := newBrowserFixture(t)
	first := f.rows(t, dbms.RowsQuery{Table: "orders", Limit: 5})

	require.Equal(t, []string{"id", "customer_id", "status", "total", "ordered_at"}, first.Columns)
	require.Len(t, first.Rows, 5)
	require.Positive(t, first.TotalEstimate)
	require.Equal(t, 5, first.Limit)
	require.Equal(t, 0, first.Offset)
	require.Equal(t, "1", first.Rows[0][0].Value, "urutan default = kolom pertama (id) menaik")
	require.Equal(t, "5", first.Rows[4][0].Value)

	second := f.rows(t, dbms.RowsQuery{Table: "orders", Limit: 5, Offset: 5})
	require.Len(t, second.Rows, 5)
	require.Equal(t, "6", second.Rows[0][0].Value, "halaman kedua tidak tumpang tindih")

	beyond := f.rows(t, dbms.RowsQuery{Table: "orders", Limit: 5, Offset: 20})
	require.Empty(t, beyond.Rows)
	require.Positive(t, beyond.TotalEstimate)

	def := f.rows(t, dbms.RowsQuery{Table: "orders"})
	require.Equal(t, 50, def.Limit)
	big := f.rows(t, dbms.RowsQuery{Table: "orders", Limit: 10_000})
	require.Equal(t, dbms.MaxRows, big.Limit)
}

func TestRowsOrderBy(t *testing.T) {
	f := newBrowserFixture(t)
	desc := f.rows(t, dbms.RowsQuery{Table: "orders", Limit: 5, OrderBy: "total", OrderDir: "desc"})
	require.Equal(t, "350000.00", desc.Rows[0][3].Value)

	asc := f.rows(t, dbms.RowsQuery{Table: "orders", Limit: 5, OrderBy: "total"})
	require.Equal(t, "21000.00", asc.Rows[0][3].Value, "total terkecil dulu")

	_, err := f.browser.Rows(context.Background(),
		dbms.RowsQuery{Schema: f.cfg.Database, Table: "orders", OrderBy: "tidak_ada"})
	require.Error(t, err)

	_, err = f.browser.Rows(context.Background(),
		dbms.RowsQuery{Schema: f.cfg.Database, Table: "orders", OrderDir: "sideways"})
	require.Error(t, err)
}

func TestRowsIsNullCell(t *testing.T) {
	f := newBrowserFixture(t)
	page := f.rows(t, dbms.RowsQuery{Table: "customers", OrderBy: "id"})
	require.Len(t, page.Rows, 3)

	require.True(t, page.Rows[1][3].IsNull)
	require.False(t, page.Rows[0][3].IsNull)
}

func TestRowsUnknownTableNotFound(t *testing.T) {
	f := newBrowserFixture(t)
	_, err := f.browser.Rows(context.Background(),
		dbms.RowsQuery{Schema: f.cfg.Database, Table: "tidak_ada"})
	require.ErrorIs(t, err, dbms.ErrNotFound)

	_, err = f.browser.Rows(context.Background(),
		dbms.RowsQuery{Schema: "mysql", Table: "user"})
	require.ErrorIs(t, err, dbms.ErrNotFound)
}

func TestRowsIsIdentifierSafe(t *testing.T) {
	f := newBrowserFixture(t)

	_, err := f.browser.Rows(context.Background(),
		dbms.RowsQuery{Schema: f.cfg.Database, Table: "orders`; DROP TABLE customers; --"})
	require.ErrorIs(t, err, dbms.ErrNotFound)

	page := f.rows(t, dbms.RowsQuery{Table: "customers"})
	require.Len(t, page.Rows, 3)
}

func TestRowsLongCellTruncated(t *testing.T) {
	f := newBrowserFixture(t)
	page := f.rows(t, dbms.RowsQuery{Table: "long_cells"})
	require.Len(t, page.Rows, 1)
	cell := page.Rows[0][0]
	require.True(t, cell.Truncated, "nilai > maxCellLen harus dipotong")
	require.Len(t, cell.Value, 256)
}
