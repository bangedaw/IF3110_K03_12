// Browser menampilkan baris data target secara read-only: transaksi
// READ ONLY + SET LOCAL statement_timeout, identifier selalu di-quote lewat
// pgx.Identifier, dan kolom ORDER BY harus lolos whitelist introspeksi.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
)

const (
	browserStatementTimeout = 5 * time.Second
	defaultRowsLimit        = 50
	// maxCellLen: nilai sel lebih panjang dari ini dipotong (byte, tepi rune).
	// ponytail: 256 cukup untuk Milestone 1; naikkan bila UI butuh pratinjau lebih.
	maxCellLen = 256
)

type Browser struct {
	pool *pgxpool.Pool
}

var _ dbms.DataBrowser = (*Browser)(nil)

func (b *Browser) Rows(ctx context.Context, q dbms.RowsQuery) (*dbms.RowsPage, error) {
	if excludedSchema(q.Schema) {
		return nil, dbms.ErrNotFound
	}
	limit, offset, err := normalizePage(q)
	if err != nil {
		return nil, err
	}
	reg := pgx.Identifier{q.Schema, q.Table}.Sanitize()

	tx, err := b.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("mulai transaksi baca: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		fmt.Sprintf("SET LOCAL statement_timeout = %d", browserStatementTimeout.Milliseconds())); err != nil {
		return nil, fmt.Errorf("atur batas waktu: %w", err)
	}

	estimate, err := findEstimate(ctx, tx, reg)
	if err != nil {
		return nil, err
	}

	cols, err := attributeNames(ctx, tx, reg)
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		// Tabel tanpa kolom tidak bisa ditampilkan; cegah indeks kosong.
		return nil, dbms.ErrNotFound
	}
	if q.OrderBy != "" && !slices.Contains(cols, q.OrderBy) {
		return nil, errors.New("kolom urut tidak valid")
	}

	query := rowsSQL(reg, cols, q, limit, offset)
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, rowsError(err)
	}
	defer rows.Close()

	page := &dbms.RowsPage{
		Columns:       cols,
		TotalEstimate: estimate,
		Limit:         limit,
		Offset:        offset,
	}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, rowsError(err)
		}
		row := make([]dbms.Cell, len(values))
		for i, v := range values {
			row[i] = cellOf(v)
		}
		page.Rows = append(page.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, rowsError(err)
	}
	return page, nil
}

const findEstimateSQL = `
SELECT c.reltuples::bigint
FROM pg_class c
WHERE c.oid = to_regclass($1) AND c.relkind IN ('r', 'p', 'v')`

func findEstimate(ctx context.Context, tx pgx.Tx, reg string) (int64, error) {
	var est int64
	err := tx.QueryRow(ctx, findEstimateSQL, reg).Scan(&est)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, dbms.ErrNotFound
	}
	if err != nil {
		return 0, rowsError(err)
	}
	return nonNegative(est), nil
}

const attributeNamesSQL = `
SELECT a.attname
FROM pg_attribute a
WHERE a.attrelid = to_regclass($1) AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`

func attributeNames(ctx context.Context, tx pgx.Tx, reg string) ([]string, error) {
	rows, err := tx.Query(ctx, attributeNamesSQL, reg)
	if err != nil {
		return nil, rowsError(err)
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, rowsError(err)
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

// rowsSQL membangun SELECT dari whitelist kolom hasil introspeksi.
// Hanya dua sumber nilai tak-terpetakan: nama kolom dari atribut tabel
// (sudah diverifikasi) dan dir dari whitelist asc/desc di normalizePage.
func rowsSQL(reg string, cols []string, q dbms.RowsQuery, limit, offset int) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = pgx.Identifier{c}.Sanitize() + "::text"
	}
	// ORDER BY memakai nama kolom yang memenuhi tabel ("app"."books"."id"):
	// nama berkualifikasi tidak pernah merujuk output column (::text), jadi
	// pengurutan tetap pada tipe asli (numerik), bukan teks. Default = kolom
	// pertama. (Kurung tidak membantu: tanda kurung dihapus parser saat
	// parse, sehingga (id) tetap dianggap nama output yang polos.)
	orderBy := reg + "." + pgx.Identifier{cols[0]}.Sanitize()
	if q.OrderBy != "" {
		orderBy = reg + "." + pgx.Identifier{q.OrderBy}.Sanitize()
	}
	dir := "ASC"
	if strings.EqualFold(q.OrderDir, "desc") {
		dir = "DESC"
	}
	query := fmt.Sprintf(
		"SELECT %s FROM %s ORDER BY %s %s LIMIT %d OFFSET %d",
		strings.Join(quoted, ", "), reg, orderBy, dir, limit, offset,
	)
	return query
}

func normalizePage(q dbms.RowsQuery) (limit, offset int, err error) {
	limit = q.Limit
	if limit <= 0 {
		limit = defaultRowsLimit
	}
	if limit > dbms.MaxRows {
		limit = dbms.MaxRows
	}
	offset = q.Offset
	if offset < 0 {
		offset = 0
	}
	switch strings.ToLower(q.OrderDir) {
	case "", "asc", "desc":
	default:
		return 0, 0, errors.New("arah urut tidak valid")
	}
	return limit, offset, nil
}

// cellOf mengubah nilai kolom (sudah ::text dari SQL) menjadi Cell aman JSON.
func cellOf(v any) dbms.Cell {
	if v == nil {
		return dbms.Cell{IsNull: true}
	}
	s, ok := v.(string)
	if !ok {
		s = fmt.Sprint(v) // seharusnya tidak terjadi: semua kolom di-cast ::text
	}
	s, cut := truncateCell(s)
	return dbms.Cell{Value: s, Truncated: cut}
}

func truncateCell(s string) (string, bool) {
	if len(s) <= maxCellLen {
		return s, false
	}
	cut := maxCellLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// rowsError memetakan error PG: statement timeout (57014) menjadi pesan
// rapi tanpa membawa teks mentah PG; sisanya dibungkus sebagai konteks.
func rowsError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "57014" {
		return errors.New("query melebihi batas waktu")
	}
	return fmt.Errorf("baca baris: %w", err)
}
