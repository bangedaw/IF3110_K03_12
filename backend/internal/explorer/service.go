// Package explorer menyajikan introspeksi database target kepada user
// ber-grant: daftar database, schema, tabel, dan baris data (read-only).
package explorer

import (
	"context"
	"errors"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
)

// listLimit: explorer memuat seluruh koneksi dalam satu halaman.
// ponytail: 500 baris lebih dari cukup untuk Milestone 1; ganti ke
// pagination bila jumlah koneksi mendekati itu.
const listLimit = 500

// Store adalah subset akses koneksi dari dbconn.Store yang dipakai explorer.
type Store interface {
	ListConnections(ctx context.Context, limit, offset int32) ([]gen.DatabaseConnection, error)
	GetConnectionByID(ctx context.Context, id int64) (gen.DatabaseConnection, error)
}

type Service struct {
	store    Store
	registry *dbconn.Registry
	az       authz.Authorizer
}

func NewService(store Store, registry *dbconn.Registry, az authz.Authorizer) *Service {
	return &Service{store: store, registry: registry, az: az}
}

// Database adalah ringkasan database target untuk daftar explorer.
type Database struct {
	ID       int64       `json:"id"`
	Name     string      `json:"name"`
	Kind     string      `json:"kind"`
	IsActive bool        `json:"isActive"`
	Level    authz.Level `json:"level"`
}

type DatabaseDetail struct {
	Database
	Host         string `json:"host"`
	Port         int    `json:"port"`
	DatabaseName string `json:"databaseName"`
	SSLMode      string `json:"sslMode"`
}

// Databases: hanya koneksi aktif yang EffectiveLevel user bukan none.
func (s *Service) Databases(ctx context.Context, p authz.Principal) ([]Database, error) {
	rows, err := s.store.ListConnections(ctx, listLimit, 0)
	if err != nil {
		return nil, err
	}
	out := []Database{}
	for _, row := range rows {
		if !row.IsActive {
			continue
		}
		level, err := s.az.EffectiveLevel(ctx, p.UserID, row.ID)
		if err != nil {
			return nil, err
		}
		if level == authz.LevelNone {
			continue
		}
		out = append(out, Database{
			ID:       row.ID,
			Name:     row.Name,
			Kind:     string(row.Kind),
			IsActive: row.IsActive,
			Level:    level,
		})
	}
	return out, nil
}

func (s *Service) Database(ctx context.Context, p authz.Principal, id int64) (*DatabaseDetail, error) {
	// Authorize dulu: keberadaan koneksi tidak dibocorkan ke user tanpa akses.
	if err := s.az.Authorize(ctx, p, authz.ActionDBView, authz.Resource{DatabaseID: id}); err != nil {
		return nil, err
	}
	row, err := s.store.GetConnectionByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !row.IsActive {
		return nil, apperr.ErrNotFound
	}
	level, err := s.az.EffectiveLevel(ctx, p.UserID, id)
	if err != nil {
		return nil, err
	}
	return &DatabaseDetail{
		Database: Database{
			ID:       row.ID,
			Name:     row.Name,
			Kind:     string(row.Kind),
			IsActive: row.IsActive,
			Level:    level,
		},
		Host:         row.Host,
		Port:         int(row.Port),
		DatabaseName: row.DatabaseName,
		SSLMode:      row.SslMode,
	}, nil
}

// target: authorize lalu ambil koneksi terbuka dari registry.
func (s *Service) target(ctx context.Context, p authz.Principal, id int64) (dbms.Conn, error) {
	if err := s.az.Authorize(ctx, p, authz.ActionDBView, authz.Resource{DatabaseID: id}); err != nil {
		return nil, err
	}
	conn, err := s.registry.Get(ctx, id)
	if err != nil {
		return nil, mapTargetErr(err)
	}
	return conn, nil
}

func (s *Service) Schemas(ctx context.Context, p authz.Principal, id int64) ([]string, error) {
	conn, err := s.target(ctx, p, id)
	if err != nil {
		return nil, err
	}
	out, err := conn.Inspector().ListSchemas(ctx)
	if err != nil {
		return nil, mapTargetErr(err)
	}
	return out, nil
}

func (s *Service) Tables(ctx context.Context, p authz.Principal, id int64, schemaName string) ([]schema.TableSummary, error) {
	conn, err := s.target(ctx, p, id)
	if err != nil {
		return nil, err
	}
	out, err := conn.Inspector().ListTables(ctx, schemaName)
	if err != nil {
		return nil, mapTargetErr(err)
	}
	return out, nil
}

func (s *Service) Table(ctx context.Context, p authz.Principal, id int64, schemaName, table string) (*schema.Table, error) {
	conn, err := s.target(ctx, p, id)
	if err != nil {
		return nil, err
	}
	out, err := conn.Inspector().DescribeTable(ctx, schemaName, table)
	if err != nil {
		return nil, mapTargetErr(err)
	}
	return out, nil
}

func (s *Service) Rows(ctx context.Context, p authz.Principal, id int64, q dbms.RowsQuery) (*dbms.RowsPage, error) {
	conn, err := s.target(ctx, p, id)
	if err != nil {
		return nil, err
	}
	page, err := conn.Browser().Rows(ctx, q)
	if err != nil {
		return nil, mapTargetErr(err)
	}
	return page, nil
}

// mapTargetErr memetakan kegagalan sisi database target:
//   - objek tidak ada di target        -> 404
//   - error ber-code apperr (mis.      -> kode aslinya (403/404/422)
//   - sisanya (dial/timeout/driver)    -> 503 DB_UNREACHABLE
//
// ponytail: error validasi Browser (orderBy tak dikenal) juga kena 503;
// bila UI perlu 422, tambahkan sentinel dbms.ErrInvalidQuery dan petakan di sini.
func mapTargetErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, dbms.ErrNotFound) {
		return apperr.ErrNotFound
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae
	}
	return apperr.Wrap(apperr.CodeDBUnreachable, "database target tidak terjangkau", err)
}
