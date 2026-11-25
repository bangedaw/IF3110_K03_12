package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

const (
	browserStatementTimeout = 5 * time.Second
	defaultRowsLimit        = 50

	maxCellLen = 256
)

type Browser struct {
	pool *pgxpool.Pool
}

var _ dbms.DataBrowser = (*Browser)(nil)

func (b *Browser) Rows(ctx context.Context, q dbms.RowsQuery) (*dbms.RowsPage, error) {
	if excludedSchema(q.Schema) || schema.IsInternalTable(q.Table) {
		return nil, dbms.ErrNotFound
	}
	limit, offset := normalizePage(q)
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
		return nil, dbms.ErrNotFound
	}

	query := rowsSQL(reg, cols, limit, offset)
	rows, err := tx.Query(ctx, query)
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

func rowsSQL(reg string, cols []string, limit, offset int) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = pgx.Identifier{c}.Sanitize() + "::text"
	}

	orderBy := reg + "." + pgx.Identifier{cols[0]}.Sanitize()
	query := fmt.Sprintf(
		"SELECT %s FROM %s ORDER BY %s LIMIT %d OFFSET %d",
		strings.Join(quoted, ", "), reg, orderBy, limit, offset,
	)
	return query
}

func normalizePage(q dbms.RowsQuery) (limit, offset int) {
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
	return limit, offset
}

func cellOf(v any) dbms.Cell {
	if v == nil {
		return dbms.Cell{IsNull: true}
	}
	s, ok := v.(string)
	if !ok {
		s = fmt.Sprint(v)
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
