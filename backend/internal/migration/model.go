package migration

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

type Target interface {
	Explain(ctx context.Context, sql string, analyze bool) (*dbms.ExplainResult, error)
	CommittedRun(ctx context.Context, runID int64) (version int, found bool, err error)
}

type TargetRegistry interface {
	Get(ctx context.Context, databaseID int64) (Target, error)
}

type Service struct {
	Pool    *pgxpool.Pool
	Targets TargetRegistry
	Auth    authz.Authorizer
	Audit   *audit.Logger
}

type request struct {
	ID              int64
	MigrationID     int64
	Kind            gen.RequestKind
	Status          gen.RequestStatus
	Revision        int32
	RequestedByName string
	CreatedAt       time.Time
	JobID           *int64
	JobFailed       bool
}

func (r request) effective() gen.RequestStatus {
	if r.JobFailed && (r.Status == gen.RequestStatusQueued || r.Status == gen.RequestStatusRunning) {
		return gen.RequestStatusFailed
	}
	return r.Status
}

func (r request) open() bool {
	switch r.effective() {
	case gen.RequestStatusPendingReview, gen.RequestStatusApproved, gen.RequestStatusQueued, gen.RequestStatusRunning:
		return true
	}
	return false
}

func (r request) up() bool { return r.Kind != gen.RequestKindRollback }

func (r request) dto() apigen.MigrationRequest {
	return apigen.MigrationRequest{
		Id:              r.ID,
		Kind:            apigen.MigrationKind(r.Kind),
		Status:          apigen.MigrationStatus(r.effective()),
		Revision:        int(r.Revision),
		RequestedByName: r.RequestedByName,
		CreatedAt:       r.CreatedAt,
		JobId:           r.JobID,
	}
}

func toMigration(row gen.GetMigrationRow, st state, level authz.Level) apigen.Migration {
	m := row.Migration
	out := apigen.Migration{
		Id:                 m.ID,
		DatabaseId:         m.DatabaseID,
		Title:              m.Title,
		Description:        m.Description,
		Status:             st.status(m),
		Position:           apigen.MigrationPosition(m.Status),
		Version:            intPtr(m.Version),
		Revision:           int(m.Revision),
		UpSql:              m.SqlText,
		DownSql:            m.DownSql.String,
		Irreversible:       m.Irreversible,
		IrreversibleReason: m.IrreversibleReason,
		Batch:              batchOf(m),
		SqlLocked:          row.SqlLocked,
		CreatedByName:      row.CreatedByName,
		CreatedAt:          m.CreatedAt.Time.UTC(),
		UpdatedAt:          m.UpdatedAt.Time.UTC(),
		Actions:            st.actions(m, row.SqlLocked, level),
	}
	if latest := st.latest(m.ID); latest != nil {
		dto := latest.dto()
		out.Request = &dto
	}
	if current := st.current(m.ID); current != nil {
		out.JobId = current.JobID
	}
	if r := st.executable(m); r != nil {
		out.ExecutableRequestId = &r.ID
	}
	return out
}

func batchOf(m gen.Migration) *apigen.MigrationBatch {
	if !m.BatchTable.Valid {
		return nil
	}
	return &apigen.MigrationBatch{
		Schema: m.BatchSchema.String,
		Table:  m.BatchTable.String,
		Column: m.BatchColumn.String,
		Size:   int(m.BatchSize.Int32),
	}
}

func toExecution(row gen.ListExecutionsRow) apigen.MigrationExecution {
	r := row.MigrationRun
	return apigen.MigrationExecution{
		Id:                 r.ID,
		MigrationId:        r.MigrationID,
		MigrationTitle:     row.Title,
		Version:            intPtr(r.Version),
		Revision:           int(r.Revision),
		Kind:               apigen.MigrationKind(r.Kind),
		Status:             apigen.MigrationExecutionStatus(r.Status),
		Attempt:            int(r.Attempt),
		ExecutedBy:         r.ExecutedBy,
		ExecutedByName:     row.ExecutedByName,
		ExecutedSql:        r.SqlText,
		RevertsExecutionId: int64Ptr(r.RevertsRunID),
		JobId:              int64Ptr(r.JobID),
		ErrorCode:          textPtr(r.ErrorCode),
		ErrorMessage:       textPtr(r.ErrorMessage),
		StartedAt:          r.StartedAt.Time.UTC(),
		FinishedAt:         timePtr(r.FinishedAt),
	}
}

func intPtr(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int32)
	return &n
}

func int64Ptr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func textPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func timePtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time.UTC()
	return &t
}

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func int8of(n int64) pgtype.Int8 { return pgtype.Int8{Int64: n, Valid: true} }

func int4of(n int32) pgtype.Int4 { return pgtype.Int4{Int32: n, Valid: true} }

func formatID(id int64) string { return strconv.FormatInt(id, 10) }
