package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel/propagation"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

type Postgres struct{ q *gen.Queries }

var _ Publisher = (*Postgres)(nil)

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
