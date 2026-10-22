package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
)

func browserFor(t *testing.T) dbms.DataBrowser {
	t.Helper()
	cfg := targetConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	conn, err := postgres.Driver{}.Open(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn.Browser()
}

func booksRows(t *testing.T, q dbms.RowsQuery) *dbms.RowsPage {
	t.Helper()
	if q.Schema == "" {
		q.Schema = "app"
	}
	page, err := browserFor(t).Rows(context.Background(), q)
	require.NoError(t, err)
	return page
}

func TestRowsPagination(t *testing.T) {
	b := browserFor(t)

	first, err := b.Rows(context.Background(), dbms.RowsQuery{Schema: "app", Table: "books", Limit: 5})
	require.NoError(t, err)
	require.Equal(t, []string{"id", "author_id", "title", "price", "published_on", "tags"}, first.Columns)
	require.Len(t, first.Rows, 5)
	require.EqualValues(t, 12, first.TotalEstimate)
	require.Equal(t, 5, first.Limit)
	require.Equal(t, 0, first.Offset)
	require.Equal(t, "1", first.Rows[0][0].Value, "urutan default = kolom pertama (id) menaik")
	require.Equal(t, "5", first.Rows[4][0].Value)

	second, err := b.Rows(context.Background(), dbms.RowsQuery{Schema: "app", Table: "books", Limit: 5, Offset: 5})
	require.NoError(t, err)
	require.Len(t, second.Rows, 5)
	require.Equal(t, "6", second.Rows[0][0].Value, "halaman kedua tidak tumpang tindih")

	beyond, err := b.Rows(context.Background(), dbms.RowsQuery{Schema: "app", Table: "books", Limit: 5, Offset: 20})
	require.NoError(t, err)
	require.Empty(t, beyond.Rows)
	require.EqualValues(t, 12, beyond.TotalEstimate)

	// Limit 0 = default; limit > MaxRows di-clamp.
	all := booksRows(t, dbms.RowsQuery{Table: "books"})
	require.Len(t, all.Rows, 12)
	require.Equal(t, 50, all.Limit)

	clamped := booksRows(t, dbms.RowsQuery{Table: "books", Limit: 999})
	require.Equal(t, dbms.MaxRows, clamped.Limit)
}

func TestRowsOrderBy(t *testing.T) {
	asc := booksRows(t, dbms.RowsQuery{Table: "books", OrderBy: "title", OrderDir: "asc", Limit: 1})
	require.Equal(t, "Analitik Bisnis", asc.Rows[0][2].Value)

	desc := booksRows(t, dbms.RowsQuery{Table: "books", OrderBy: "title", OrderDir: "desc", Limit: 1})
	require.Equal(t, "Query Lanjutan", desc.Rows[0][2].Value)
}

func TestRowsRejectsInjection(t *testing.T) {
	b := browserFor(t)

	// Nama tabel: ditolak sebagai tidak ditemukan, bukan dieksekusi.
	_, err := b.Rows(context.Background(), dbms.RowsQuery{Schema: "app", Table: `books"; DROP TABLE app.books; --`})
	require.Error(t, err)
	require.True(t, errors.Is(err, dbms.ErrNotFound))

	// Kolom urut di luar whitelist: ditolak.
	_, err = b.Rows(context.Background(), dbms.RowsQuery{Schema: "app", Table: "books", OrderBy: `title); DROP TABLE app.books; --`})
	require.Error(t, err)
	require.False(t, errors.Is(err, dbms.ErrNotFound), "orderBy bukan soal tidak ada, tapi ditolak")

	// Arah urut hanya asc/desc.
	_, err = b.Rows(context.Background(), dbms.RowsQuery{Schema: "app", Table: "books", OrderBy: "title", OrderDir: `asc; DROP TABLE app.books`})
	require.Error(t, err)

	// Buktikan tabel masih utuh setelah percobaan di atas.
	page := booksRows(t, dbms.RowsQuery{Table: "books", Limit: 1})
	require.Len(t, page.Rows, 1)
}

func TestRowsNotFound(t *testing.T) {
	b := browserFor(t)

	_, err := b.Rows(context.Background(), dbms.RowsQuery{Schema: "app", Table: "tidak_ada"})
	require.Error(t, err)
	require.True(t, errors.Is(err, dbms.ErrNotFound))

	// Schema sistem tidak bisa ditelusuri.
	_, err = b.Rows(context.Background(), dbms.RowsQuery{Schema: "pg_catalog", Table: "pg_class"})
	require.Error(t, err)
	require.True(t, errors.Is(err, dbms.ErrNotFound))
}

func TestRowsSerializesCellsSafely(t *testing.T) {
	page := booksRows(t, dbms.RowsQuery{Table: "books", Limit: 1})
	require.Equal(t, "120000.00", page.Rows[0][3].Value, "numeric sebagai string, bukan float")

	atts := booksRows(t, dbms.RowsQuery{Table: "attachments"})
	require.Len(t, atts.Rows, 3)

	deadbeef := atts.Rows[0][2] // data bytea
	require.False(t, deadbeef.IsNull)
	require.Equal(t, `\xdeadbeef`, deadbeef.Value, "bytea ditampilkan sebagai hex")

	nullData := atts.Rows[1][2]
	require.True(t, nullData.IsNull)
	require.False(t, nullData.Truncated)

	longNote := atts.Rows[2][3] // note: teks panjang
	require.True(t, longNote.Truncated, "teks panjang dipotong")
	require.LessOrEqual(t, len(longNote.Value), 256)
	require.False(t, atts.Rows[0][3].Truncated, "teks pendek tidak dipotong")
}
