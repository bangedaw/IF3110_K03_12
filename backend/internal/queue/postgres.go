package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/propagation"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

type Postgres struct{ q *gen.Queries }

var (
	_ Publisher = (*Postgres)(nil)
	_ JobStore  = (*Postgres)(nil)
)

func NewPostgres(db gen.DBTX) *Postgres { return &Postgres{q: gen.New(db)} }

func (p *Postgres) Publish(ctx context.Context, in PublishInput) (int64, error) {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	traceContext, err := json.Marshal(carrier)
	if err != nil {
		return 0, err
	}
	id, err := p.q.UpsertJob(ctx, gen.UpsertJobParams{
		RequestID:    in.RequestID,
		DatabaseID:   in.DatabaseID,
		CreatedBy:    in.ActorID,
		TraceContext: traceContext,
	})
	if err != nil {
		return 0, fmt.Errorf("publish job: %w", err)
	}
	if err := p.q.RequeueJob(ctx, gen.RequeueJobParams{ID: id, TraceContext: traceContext}); err != nil {
		return 0, fmt.Errorf("requeue job: %w", err)
	}
	return id, nil
}

type Job struct {
	ID          int64
	RequestID   int64
	DatabaseID  int64
	ActorID     int64
	Attempt     int
	MaxAttempts int
	carrier     propagation.MapCarrier
}

func (j Job) Context(ctx context.Context) context.Context {
	return propagation.TraceContext{}.Extract(ctx, j.carrier)
}

func (p *Postgres) Claim(ctx context.Context) (*Job, error) {
	row, err := p.q.ClaimJob(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim job: %w", err)
	}
	carrier := propagation.MapCarrier{}
	if err := json.Unmarshal(row.TraceContext, &carrier); err != nil {
		return nil, fmt.Errorf("decode trace context of job %d: %w", row.ID, err)
	}
	return &Job{
		ID:          row.ID,
		RequestID:   row.RequestID,
		DatabaseID:  row.DatabaseID,
		ActorID:     row.CreatedBy,
		Attempt:     int(row.Attempts),
		MaxAttempts: int(row.MaxAttempts),
		carrier:     carrier,
	}, nil
}

func (p *Postgres) SaveProgress(ctx context.Context, jobID int64, progress dbms.BatchProgress) error {
	return p.q.SaveJobProgress(ctx, gen.SaveJobProgressParams{
		ID:               jobID,
		BatchesCommitted: int32(progress.BatchesCommitted - progress.BatchesReverted),
		RowsCommitted:    progress.RowsCommitted - progress.RowsReverted,
	})
}

func (p *Postgres) Complete(ctx context.Context, jobID int64, status JobStatus, lastError string) error {
	return p.q.CompleteJob(ctx, gen.CompleteJobParams{
		ID:        jobID,
		Status:    gen.JobStatus(status),
		LastError: pgtype.Text{String: lastError, Valid: lastError != ""},
	})
}

func (p *Postgres) Get(ctx context.Context, databaseID, jobID int64) (apigen.Job, error) {
	j, err := p.q.GetJob(ctx, gen.GetJobParams{ID: jobID, DatabaseID: databaseID})
	if errors.Is(err, pgx.ErrNoRows) {
		return apigen.Job{}, apperr.ErrNotFound
	}
	if err != nil {
		return apigen.Job{}, err
	}
	out := apigen.Job{
		Id:               j.ID,
		RequestId:        j.RequestID,
		DatabaseId:       j.DatabaseID,
		Status:           apigen.JobStatus(j.Status),
		Attempts:         int(j.Attempts),
		MaxAttempts:      int(j.MaxAttempts),
		BatchesCommitted: int(j.BatchesCommitted),
		RowsCommitted:    j.RowsCommitted,
		CreatedAt:        j.CreatedAt.Time.UTC(),
		StartedAt:        utc(j.StartedAt),
		FinishedAt:       utc(j.FinishedAt),
	}
	if j.LastError.Valid {
		out.LastError = &j.LastError.String
	}
	return out, nil
}

func utc(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}
