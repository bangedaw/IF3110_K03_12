package audit

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
	OutcomeDenied  = "denied"
)

type Entry struct {
	ActorID      int64
	Action       string
	ResourceType string
	ResourceID   string
	Outcome      string
	Metadata     map[string]any
}

type Logger struct{ q *gen.Queries }

func New(db gen.DBTX) *Logger { return &Logger{q: gen.New(db)} }

func (l *Logger) Record(ctx context.Context, e Entry) {
	meta, err := json.Marshal(e.Metadata)
	if err != nil || e.Metadata == nil {
		meta = []byte("{}")
	}
	err = l.q.InsertAuditLog(ctx, gen.InsertAuditLogParams{
		ActorUserID:  pgtype.Int8{Int64: e.ActorID, Valid: e.ActorID != 0},
		Action:       e.Action,
		ResourceType: e.ResourceType,
		ResourceID:   pgtype.Text{String: e.ResourceID, Valid: e.ResourceID != ""},
		Outcome:      e.Outcome,
		Metadata:     meta,
	})
	if err != nil {
		slog.ErrorContext(ctx, "audit log gagal ditulis", "action", e.Action, "err", err)
	}
}

type deniedRecorder struct {
	authz.Authorizer
	log *Logger
}

func RecordDenied(inner authz.Authorizer, l *Logger) authz.Authorizer {
	return deniedRecorder{Authorizer: inner, log: l}
}

func (d deniedRecorder) Authorize(ctx context.Context, p authz.Principal, a authz.Action, r authz.Resource) error {
	err := d.Authorizer.Authorize(ctx, p, a, r)
	if errors.Is(err, authz.ErrForbidden) {
		e := Entry{ActorID: p.UserID, Action: string(a), ResourceType: "platform", Outcome: OutcomeDenied}
		if r.DatabaseID != 0 {
			e.ResourceType, e.ResourceID = "database", strconv.FormatInt(r.DatabaseID, 10)
		}
		d.log.Record(ctx, e)
	}
	return err
}
