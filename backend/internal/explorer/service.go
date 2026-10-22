// Package explorer menyajikan introspeksi database target kepada user
// ber-grant: daftar database, schema, tabel, dan baris data (read-only).
package explorer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schema"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/schemadiff"
)

// listLimit: explorer memuat seluruh koneksi dalam satu halaman.
// ponytail: 500 baris lebih dari cukup untuk Milestone 1; ganti ke
// pagination bila jumlah koneksi mendekati itu.
const listLimit = 500

// Store adalah subset akses data dari dbconn.Store yang dipakai explorer.
type Store interface {
	ListConnections(ctx context.Context, limit, offset int32) ([]gen.DatabaseConnection, error)
	GetConnectionByID(ctx context.Context, id int64) (gen.DatabaseConnection, error)

	CreateSnapshot(ctx context.Context, arg gen.CreateSnapshotParams) (gen.SchemaSnapshot, error)
	ListSnapshots(ctx context.Context, databaseID int64) ([]gen.SchemaSnapshot, error)
	GetSnapshotByID(ctx context.Context, id, databaseID int64) (gen.SchemaSnapshot, error)
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

// Snapshot adalah snapshot schema tersimpan (hasil Inspector.Snapshot saat
// diambil); dipakai sebagai sumber pembanding pada schema diff.
type Snapshot struct {
	ID             int64         `json:"id"`
	DatabaseID     int64         `json:"databaseId"`
	MigrationRunID *int64        `json:"migrationRunId,omitempty"`
	TakenBy        *int64        `json:"takenBy,omitempty"`
	TakenAt        time.Time     `json:"takenAt"`
	Model          *schema.Model `json:"model"`
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

// CreateSnapshot mengambil schema terkini dari target hidup lalu menyimpannya.
func (s *Service) CreateSnapshot(ctx context.Context, p authz.Principal, databaseID int64) (*Snapshot, error) {
	conn, err := s.target(ctx, p, databaseID)
	if err != nil {
		return nil, err
	}
	model, err := conn.Inspector().Snapshot(ctx)
	if err != nil {
		return nil, mapTargetErr(err)
	}
	raw, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	row, err := s.store.CreateSnapshot(ctx, gen.CreateSnapshotParams{
		DatabaseID: databaseID,
		TakenBy:    pgtype.Int8{Int64: p.UserID, Valid: true},
		Model:      raw,
	})
	if err != nil {
		return nil, err
	}
	return toSnapshot(row)
}

// Snapshots: daftar snapshot tersimpan; koneksi hidup tidak diperlukan.
func (s *Service) Snapshots(ctx context.Context, p authz.Principal, databaseID int64) ([]Snapshot, error) {
	if err := s.az.Authorize(ctx, p, authz.ActionDBView, authz.Resource{DatabaseID: databaseID}); err != nil {
		return nil, err
	}
	rows, err := s.store.ListSnapshots(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	out := []Snapshot{}
	for _, row := range rows {
		snap, err := toSnapshot(row)
		if err != nil {
			return nil, err
		}
		out = append(out, *snap)
	}
	return out, nil
}

// Snapshot mengambil satu snapshot; terkunci pada databaseID agar snapshot
// database lain tidak bisa dibaca lewat id (cross-database -> 404).
func (s *Service) Snapshot(ctx context.Context, p authz.Principal, databaseID, snapshotID int64) (*Snapshot, error) {
	if err := s.az.Authorize(ctx, p, authz.ActionDBView, authz.Resource{DatabaseID: databaseID}); err != nil {
		return nil, err
	}
	row, err := s.store.GetSnapshotByID(ctx, snapshotID, databaseID)
	if err != nil {
		return nil, err
	}
	return toSnapshot(row)
}

// SchemaDiffRequest adalah body POST .../schema-diff:
// against = pembanding (koneksi hidup lain atau snapshot tersimpan),
// direction = arah SQL yang dihasilkan (hanya "toTarget": buat {databaseId}
// menyamai against).
type SchemaDiffRequest struct {
	Against struct {
		Type string `json:"type"` // "database" | "snapshot"
		ID   int64  `json:"id"`
	} `json:"against"`
	Direction string `json:"direction"`
}

// SchemaDiffResult: perubahan skema, SQL gabungan, dan peringatan generator.
type SchemaDiffResult struct {
	Changes  []schemadiff.Change `json:"changes"`
	SQL      string              `json:"sql"`
	Warnings []string            `json:"warnings"`
}

// SchemaDiff membandingkan skema hidup {databaseId} dengan against lalu
// menghasilkan SQL pengubah (arah toTarget). Butuh view di kedua sisi;
// snapshot terkunci pada databaseId (cross-database -> 404).
func (s *Service) SchemaDiff(ctx context.Context, p authz.Principal, databaseID int64, req SchemaDiffRequest) (*SchemaDiffResult, error) {
	if fields := validateDiffRequest(req); len(fields) > 0 {
		return nil, apperr.Validation(fields)
	}
	if err := s.az.Authorize(ctx, p, authz.ActionSchemaDiff, authz.Resource{DatabaseID: databaseID}); err != nil {
		return nil, err
	}

	current, err := s.liveModel(ctx, p, databaseID)
	if err != nil {
		return nil, err
	}

	var target *schema.Model
	switch req.Against.Type {
	case "database":
		target, err = s.liveModel(ctx, p, req.Against.ID)
	case "snapshot":
		var snap *Snapshot
		snap, err = s.Snapshot(ctx, p, databaseID, req.Against.ID)
		if err == nil {
			target = snap.Model
		}
	}
	if err != nil {
		return nil, err
	}

	changes := schemadiff.Diff(current, target)
	if changes == nil {
		changes = []schemadiff.Change{}
	}

	row, err := s.store.GetConnectionByID(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	res, err := schemadiff.Generate(string(row.Kind), changes)
	if err != nil {
		if errors.Is(err, schemadiff.ErrUnsupportedDialect) {
			return nil, apperr.Validation(map[string]string{"against": err.Error()})
		}
		return nil, err
	}
	return &SchemaDiffResult{
		Changes:  changes,
		SQL:      strings.Join(res.Statements, ";\n"),
		Warnings: res.Warnings,
	}, nil
}

func validateDiffRequest(req SchemaDiffRequest) map[string]string {
	fields := map[string]string{}
	if req.Direction != "toTarget" {
		fields["direction"] = `hanya "toTarget" yang didukung`
	}
	if req.Against.Type != "database" && req.Against.Type != "snapshot" {
		fields["against.type"] = `harus "database" atau "snapshot"`
	}
	if req.Against.ID <= 0 {
		fields["against.id"] = "harus positif"
	}
	return fields
}

// liveModel: authorize view lalu ambil snapshot skema dari koneksi hidup.
func (s *Service) liveModel(ctx context.Context, p authz.Principal, id int64) (*schema.Model, error) {
	conn, err := s.target(ctx, p, id)
	if err != nil {
		return nil, err
	}
	model, err := conn.Inspector().Snapshot(ctx)
	if err != nil {
		return nil, mapTargetErr(err)
	}
	return model, nil
}

func toSnapshot(row gen.SchemaSnapshot) (*Snapshot, error) {
	m := &schema.Model{}
	if err := json.Unmarshal(row.Model, m); err != nil {
		return nil, fmt.Errorf("model snapshot id %d tidak terbaca: %w", row.ID, err)
	}
	out := &Snapshot{
		ID:         row.ID,
		DatabaseID: row.DatabaseID,
		TakenAt:    row.TakenAt.Time,
		Model:      m,
	}
	if row.MigrationRunID.Valid {
		v := row.MigrationRunID.Int64
		out.MigrationRunID = &v
	}
	if row.TakenBy.Valid {
		v := row.TakenBy.Int64
		out.TakenBy = &v
	}
	return out, nil
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
