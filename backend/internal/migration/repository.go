package migration

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

type PGRepository struct {
	pool *pgxpool.Pool
	q    *gen.Queries
}

func NewRepository(pool *pgxpool.Pool) *PGRepository {
	return &PGRepository{pool: pool, q: gen.New(pool)}
}

var _ Repository = (*PGRepository)(nil)

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.ErrNotFound
	}
	return err
}

func (r *PGRepository) DatabaseKind(ctx context.Context, databaseID int64) (string, error) {
	kind, err := r.q.GetDatabaseKind(ctx, databaseID)
	return string(kind), notFound(err)
}

func (r *PGRepository) Head(ctx context.Context, databaseID int64) (int, error) {
	head, err := r.q.MigrationHead(ctx, databaseID)
	return int(head), err
}

func (r *PGRepository) CreateDraft(ctx context.Context, actorID, databaseID int64, in CreateInput, baseVersion int, sum []byte) (*Migration, error) {
	row, err := r.q.CreateMigrationDraft(ctx, gen.CreateMigrationDraftParams{
		DatabaseID:  databaseID,
		Title:       in.Title,
		Description: in.Description,
		SqlText:     in.SQL,
		Checksum:    sum,
		BaseVersion: i32(baseVersion),
		Actor:       actorID,
	})
	if err != nil {
		return nil, err
	}
	return toMigration(row), nil
}

func (r *PGRepository) Get(ctx context.Context, databaseID, migrationID int64) (*Migration, error) {
	row, err := r.q.GetMigration(ctx, gen.GetMigrationParams{ID: migrationID, DatabaseID: databaseID})
	if err != nil {
		return nil, notFound(err)
	}
	return toMigration(row), nil
}

func (r *PGRepository) List(ctx context.Context, databaseID int64, status string, limit, offset int) ([]Migration, int, error) {
	st := gen.NullMigStatus{MigStatus: gen.MigStatus(status), Valid: status != ""}
	rows, err := r.q.ListMigrations(ctx, gen.ListMigrationsParams{DatabaseID: databaseID, Status: st, Lim: i32(limit), Off: i32(offset)})
	if err != nil {
		return nil, 0, err
	}
	total, err := r.q.CountMigrations(ctx, gen.CountMigrationsParams{DatabaseID: databaseID, Status: st})
	if err != nil {
		return nil, 0, err
	}
	out := make([]Migration, len(rows))
	for i, row := range rows {
		out[i] = *toMigration(row)
	}
	return out, int(total), nil
}

func (r *PGRepository) UpdateDraft(ctx context.Context, actorID, databaseID, migrationID int64, in UpdateInput, sum []byte) (*Migration, error) {
	row, err := r.q.UpdateMigrationDraft(ctx, gen.UpdateMigrationDraftParams{
		ID:          migrationID,
		DatabaseID:  databaseID,
		Title:       in.Title,
		Description: in.Description,
		SqlText:     in.SQL,
		Checksum:    sum,
		Actor:       actorID,
		Revision:    i32(in.Revision),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.New(apperr.CodeConflict, "migration sudah diubah atau dijalankan; muat ulang halaman")
	}
	if err != nil {
		return nil, err
	}
	return toMigration(row), nil
}

func (r *PGRepository) DeleteDraft(ctx context.Context, databaseID, migrationID int64, revision int) error {
	n, err := r.q.DeleteMigrationDraft(ctx, gen.DeleteMigrationDraftParams{ID: migrationID, DatabaseID: databaseID, Revision: i32(revision)})
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.New(apperr.CodeConflict, "migration sudah diubah atau dijalankan; muat ulang halaman")
	}
	return nil
}

func (r *PGRepository) StartRun(ctx context.Context, actorID int64, m *Migration) (*Run, error) {
	sum, err := hex.DecodeString(m.Checksum)
	if err != nil {
		return nil, err
	}
	id, err := r.q.StartMigrationRun(ctx, gen.StartMigrationRunParams{
		MigrationID: m.ID,
		DatabaseID:  m.DatabaseID,
		SqlText:     m.SQL,
		Checksum:    sum,
		ExecutedBy:  actorID,
	})
	if err != nil {
		return nil, err
	}
	return r.run(ctx, id)
}

func (r *PGRepository) FinishRun(ctx context.Context, runID int64, status Status, version *int, errCode, errMessage string, failedIndex *int) (*Run, error) {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		row, err := q.FinishMigrationRun(ctx, gen.FinishMigrationRunParams{
			ID:                   runID,
			Status:               gen.RunStatus(status),
			ErrorCode:            pgtype.Text{String: errCode, Valid: errCode != ""},
			ErrorMessage:         pgtype.Text{String: errMessage, Valid: errMessage != ""},
			FailedStatementIndex: int4(failedIndex),
		})
		if err != nil || status != RunSucceeded {
			return err
		}
		return q.MarkMigrationApplied(ctx, gen.MarkMigrationAppliedParams{
			ID:        row.MigrationID,
			Version:   int4(version),
			AppliedBy: pgtype.Int8{Int64: row.ExecutedBy, Valid: true},
		})
	})
	if err != nil {
		return nil, err
	}
	return r.run(ctx, runID)
}

func (r *PGRepository) run(ctx context.Context, id int64) (*Run, error) {
	row, err := r.q.GetMigrationRun(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	run := toRun(gen.ListMigrationHistoryRow(row))
	return &run, nil
}

func (r *PGRepository) Runs(ctx context.Context, databaseID, migrationID int64) ([]Run, error) {
	rows, err := r.q.ListMigrationRuns(ctx, gen.ListMigrationRunsParams{MigrationID: migrationID, DatabaseID: databaseID})
	if err != nil {
		return nil, err
	}
	out := make([]Run, len(rows))
	for i, row := range rows {
		out[i] = toRun(gen.ListMigrationHistoryRow(row))
	}
	return out, nil
}

func (r *PGRepository) History(ctx context.Context, databaseID int64, limit, offset int) ([]Run, int, error) {
	rows, err := r.q.ListMigrationHistory(ctx, gen.ListMigrationHistoryParams{DatabaseID: databaseID, Lim: i32(limit), Off: i32(offset)})
	if err != nil {
		return nil, 0, err
	}
	total, err := r.q.CountMigrationHistory(ctx, databaseID)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Run, len(rows))
	for i, row := range rows {
		out[i] = toRun(row)
	}
	return out, int(total), nil
}

func (r *PGRepository) WithDatabaseLock(ctx context.Context, databaseID int64, fn func(context.Context) error) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1::bigint)`, databaseID).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return apperr.New(apperr.CodeConflict, "migration lain sedang dijalankan pada database ini; coba lagi sebentar lagi")
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1::bigint)`, databaseID)
	}()
	return fn(ctx)
}

func (r *PGRepository) SaveSnapshot(ctx context.Context, databaseID, runID, actorID int64, model any) error {
	raw, err := json.Marshal(model)
	if err != nil {
		return err
	}
	return r.q.CreateRunSnapshot(ctx, gen.CreateRunSnapshotParams{
		DatabaseID:     databaseID,
		MigrationRunID: pgtype.Int8{Int64: runID, Valid: true},
		TakenBy:        pgtype.Int8{Int64: actorID, Valid: true},
		Model:          raw,
	})
}

func int4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: i32(*v), Valid: true}
}

func i32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n)
}

func intPtr(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int32)
	return &n
}

func toMigration(m gen.Migration) *Migration {
	out := &Migration{
		ID:          m.ID,
		DatabaseID:  m.DatabaseID,
		Title:       m.Title,
		Description: m.Description,
		SQL:         m.SqlText,
		Checksum:    hex.EncodeToString(m.Checksum),
		Status:      Status(m.Status),
		Version:     intPtr(m.Version),
		BaseVersion: int(m.BaseVersion),
		Revision:    int(m.Revision),
		CreatedBy:   m.CreatedBy,
		UpdatedBy:   m.UpdatedBy,
		CreatedAt:   m.CreatedAt.Time,
		UpdatedAt:   m.UpdatedAt.Time,
	}
	if m.AppliedBy.Valid {
		out.AppliedBy = &m.AppliedBy.Int64
	}
	if m.AppliedAt.Valid {
		out.AppliedAt = &m.AppliedAt.Time
	}
	return out
}

func toRun(r gen.ListMigrationHistoryRow) Run {
	out := Run{
		ID:                   r.ID,
		MigrationID:          r.MigrationID,
		DatabaseID:           r.DatabaseID,
		Title:                r.Title,
		SQL:                  r.SqlText,
		Checksum:             hex.EncodeToString(r.Checksum),
		ExecutedBy:           r.ExecutedBy,
		ExecutedByName:       r.ExecutedByName,
		Status:               Status(r.Status),
		StartedAt:            r.StartedAt.Time,
		ErrorCode:            r.ErrorCode.String,
		ErrorMessage:         r.ErrorMessage.String,
		FailedStatementIndex: intPtr(r.FailedStatementIndex),
	}
	if out.Status == RunSucceeded {
		out.Version = intPtr(r.Version)
	}
	if r.FinishedAt.Valid {
		out.FinishedAt = &r.FinishedAt.Time
	}
	return out
}

func (r *PGRepository) RunningRuns(ctx context.Context) ([]Run, error) {
	rows, err := r.q.ListRunningMigrationRuns(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Run, len(rows))
	for i, row := range rows {
		out[i] = toRun(gen.ListMigrationHistoryRow(row))
	}
	return out, nil
}
