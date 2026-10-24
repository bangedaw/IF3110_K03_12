package dbconn

import (
	"context"
	"errors"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration"
)

type Targets struct{ Registry *Registry }

var _ migration.TargetRegistry = Targets{}

func (t Targets) Get(ctx context.Context, databaseID int64) (migration.Target, error) {
	conn, err := t.Registry.Get(ctx, databaseID)
	if err != nil {
		return nil, mapOpenError(err)
	}
	return target{conn: conn}, nil
}

func mapOpenError(err error) error {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	return apperr.Wrap(apperr.CodeDBUnreachable, "database target tidak terjangkau", err)
}

type target struct{ conn dbms.Conn }

func (t target) Apply(ctx context.Context, in migration.ApplyRequest) (*migration.ApplyResult, error) {
	res, err := t.conn.Migrator().Apply(ctx, dbms.ApplyInput{
		SQL:         in.SQL,
		DatabaseID:  in.DatabaseID,
		MigrationID: in.MigrationID,
		RunID:       in.RunID,
		Version:     in.Version,
		Checksum:    in.Checksum,
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeMigrationFailed, err.Error(), err)
	}
	return &migration.ApplyResult{StatementsExecuted: res.StatementsExecuted, AppliedAt: res.AppliedAt}, nil
}

func (t target) Explain(ctx context.Context, sql string, analyze bool) (any, error) {
	res, err := t.conn.Explainer().Explain(ctx, dbms.ExplainInput{SQL: sql, Analyze: analyze})
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeMigrationFailed, err.Error(), err)
	}
	return res, nil
}

func (t target) Snapshot(ctx context.Context) (any, error) {
	model, err := t.conn.Inspector().Snapshot(ctx)
	if err != nil {
		return nil, mapOpenError(err)
	}
	return model, nil
}
