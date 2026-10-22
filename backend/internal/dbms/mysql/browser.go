// Browser menampilkan baris data target secara read-only: transaksi
// READ ONLY + SET SESSION max_execution_time, identifier selalu di-quote
// backtick, dan kolom ORDER BY harus lolos whitelist introspeksi.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"

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
	db *sql.DB
	// database: tujuan koneksi; satu-satunya "schema" yang bisa dibaca.
	database string
}

var _ dbms.DataBrowser = (*Browser)(nil)

func (b *Browser) Rows(ctx context.Context, q dbms.RowsQuery) (*dbms.RowsPage, error) {
	// Sama seperti Inspector: hanya database tujuan koneksi yang terlihat.
	if q.Schema != b.database || excludedSchema(q.Schema) {
		return nil, dbms.ErrNotFound
	}
	limit, offset, err := normalizePage(q)
	if err != nil {
		return nil, err
	}

	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("mulai transaksi baca: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// READ ONLY: membuktikan niat baca di sisi server (MySQL 8 mendukung
	// START TRANSACTION READ ONLY).
	if _, err := tx.ExecContext(ctx, "START TRANSACTION READ ONLY"); err != nil {
		return nil, fmt.Errorf("mulai transaksi baca: %w", err)
	}
	// Batas waktu eksekusi SELECT (ms); sesi pool dibatasi, jadi dikembalikan
	// ke nol setelah selesai supaya sesi berikutnya tidak ikut kena.
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf("SET SESSION max_execution_time = %d", browserStatementTimeout.Milliseconds())); err != nil {
		return nil, fmt.Errorf("atur batas waktu: %w", err)
	}
	defer func() { _, _ = tx.ExecContext(context.WithoutCancel(ctx), "SET SESSION max_execution_time = 0") }()

	estimate, err := findEstimate(ctx, tx, q.Schema, q.Table)
	if err != nil {
		return nil, err
	}

	cols, err := columnNames(ctx, tx, q.Schema, q.Table)
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

	query := rowsSQL(q.Schema, q.Table, cols, q, limit, offset)
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, rowsError(err)
	}
	defer rows.Close()

	page := &dbms.RowsPage{
		Columns:       cols,
		Rows:          [][]dbms.Cell{},
		TotalEstimate: estimate,
		Limit:         limit,
		Offset:        offset,
	}
	for rows.Next() {
		// Semua kolom sudah CAST CHAR; []byte nil = NULL.
		vals := make([][]byte, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, rowsError(err)
		}
		row := make([]dbms.Cell, len(vals))
		for i, v := range vals {
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
SELECT COALESCE(TABLE_ROWS, 0)
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
  AND TABLE_TYPE IN ('BASE TABLE', 'VIEW')`

func findEstimate(ctx context.Context, tx *sql.Tx, schemaName, table string) (int64, error) {
	var est int64
	err := tx.QueryRowContext(ctx, findEstimateSQL, schemaName, table).Scan(&est)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, dbms.ErrNotFound
	}
	if err != nil {
		return 0, rowsError(err)
	}
	return nonNegative(est), nil
}

const columnNameSQL = `
SELECT COLUMN_NAME
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY ORDINAL_POSITION`

func columnNames(ctx context.Context, tx *sql.Tx, schemaName, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, columnNameSQL, schemaName, table)
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

// rowsSQL membangun SELECT dari whitelist kolom hasil introspeksi. Hanya dua
// sumber nilai tak-terpetakan: nama kolom dari information_schema (sudah
// diverifikasi) dan dir dari whitelist asc/desc di normalizePage.
func rowsSQL(schemaName, table string, cols []string, q dbms.RowsQuery, limit, offset int) string {
	reg := backtickQualified(schemaName, table)
	quoted := make([]string, len(cols))
	for i, c := range cols {
		// CAST ke CHAR: semua nilai sampai ke Go sebagai teks, sama seperti
		// ::text di PostgreSQL (blob/json tidak bisa di-Scan apa adanya).
		quoted[i] = "CAST(" + backtick(c) + " AS CHAR)"
	}
	orderBy := reg + "." + backtick(cols[0])
	if q.OrderBy != "" {
		orderBy = reg + "." + backtick(q.OrderBy)
	}
	dir := "ASC"
	if strings.EqualFold(q.OrderDir, "desc") {
		dir = "DESC"
	}
	return fmt.Sprintf(
		"SELECT %s FROM %s ORDER BY %s %s LIMIT %d OFFSET %d",
		strings.Join(quoted, ", "), reg, orderBy, dir, limit, offset,
	)
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

// cellOf mengubah nilai kolom (sudah CAST CHAR dari SQL) menjadi Cell aman
// JSON: []byte dipotong tepat di batas, NULL menjadi IsNull.
func cellOf(v any) dbms.Cell {
	switch t := v.(type) {
	case nil:
		return dbms.Cell{IsNull: true}
	case []byte:
		if t == nil { // NULL: driver mengirim []byte nil, interface tetap terisi
			return dbms.Cell{IsNull: true}
		}
		s, cut := truncateCell(string(t))
		return dbms.Cell{Value: s, Truncated: cut}
	case string:
		s, cut := truncateCell(t)
		return dbms.Cell{Value: s, Truncated: cut}
	default:
		return dbms.Cell{Value: fmt.Sprint(v)}
	}
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

// rowsError memetakan error MySQL: timeout (1317 query interrupted /
// 3024 max_execution_time) menjadi pesan rapi; sisanya dibungkus konteks.
func rowsError(err error) error {
	if err == nil {
		return nil
	}
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) && (myErr.Number == 1317 || myErr.Number == 3024) {
		return errors.New("query melebihi batas waktu")
	}
	return fmt.Errorf("baca baris: %w", err)
}
