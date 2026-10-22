package dbconn

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

// Store adalah akses data koneksi di platform DB (satu implementasi sqlc).
type Store interface {
	ListConnections(ctx context.Context, limit, offset int32) ([]gen.DatabaseConnection, error)
	CountConnections(ctx context.Context) (int64, error)
	GetConnectionByID(ctx context.Context, id int64) (gen.DatabaseConnection, error)
	InsertConnection(ctx context.Context, arg gen.InsertConnectionParams) (gen.DatabaseConnection, error)
	UpdateConnection(ctx context.Context, arg gen.UpdateConnectionParams) (gen.DatabaseConnection, error)
	DeleteConnection(ctx context.Context, id int64) error

	// GetConnection dipakai Registry untuk membuka koneksi target.
	GetConnection(ctx context.Context, id int64) (TargetCredentials, error)
}

type SQLStore struct{ Q *gen.Queries }

func (s *SQLStore) ListConnections(ctx context.Context, limit, offset int32) ([]gen.DatabaseConnection, error) {
	rows, err := s.Q.ListConnections(ctx, gen.ListConnectionsParams{Limit: limit, Offset: offset})
	return rows, mapErr(err)
}

func (s *SQLStore) CountConnections(ctx context.Context) (int64, error) {
	n, err := s.Q.CountConnections(ctx)
	return n, mapErr(err)
}

func (s *SQLStore) GetConnectionByID(ctx context.Context, id int64) (gen.DatabaseConnection, error) {
	row, err := s.Q.GetConnectionByID(ctx, id)
	return row, mapErr(err)
}

func (s *SQLStore) InsertConnection(ctx context.Context, arg gen.InsertConnectionParams) (gen.DatabaseConnection, error) {
	row, err := s.Q.InsertConnection(ctx, arg)
	return row, mapErr(err)
}

func (s *SQLStore) UpdateConnection(ctx context.Context, arg gen.UpdateConnectionParams) (gen.DatabaseConnection, error) {
	row, err := s.Q.UpdateConnection(ctx, arg)
	return row, mapErr(err)
}

func (s *SQLStore) DeleteConnection(ctx context.Context, id int64) error {
	return mapErr(s.Q.DeleteConnection(ctx, id))
}

func (s *SQLStore) GetConnection(ctx context.Context, id int64) (TargetCredentials, error) {
	row, err := s.GetConnectionByID(ctx, id)
	if err != nil {
		return TargetCredentials{}, err
	}
	return TargetCredentials{
		ID:                 row.ID,
		Name:               row.Name,
		Kind:               string(row.Kind),
		Host:               row.Host,
		Port:               int(row.Port),
		Database:           row.DatabaseName,
		User:               row.Username,
		PasswordCiphertext: row.PasswordCiphertext,
		SSLMode:            row.SslMode,
		IsActive:           row.IsActive,
	}, nil
}

const (
	pgUniqueViolation = "23505"
)

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return apperr.Wrap(apperr.CodeConflict, "nama koneksi sudah dipakai", err)
	}
	return err
}

func nullableUserID(id int64) pgtype.Int8 { return pgtype.Int8{Int64: id, Valid: true} }
