package dbconn

import (
	"context"
	"errors"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
)

type Targets struct{ Registry *Registry }

var (
	_ migration.TargetRegistry = Targets{}
	_ queue.Targets            = Targets{}
)

func (t Targets) Get(ctx context.Context, databaseID int64) (migration.Target, error) {
	conn, err := t.Registry.Get(ctx, databaseID)
	if err != nil {
		return nil, mapOpenError(err)
	}
	return target{conn: conn}, nil
}

func (t Targets) Migrator(ctx context.Context, databaseID int64) (dbms.Migrator, error) {
	t.Registry.Invalidate(databaseID)
	conn, err := t.Registry.Get(ctx, databaseID)
	if err != nil {
		return nil, mapOpenError(err)
	}
	return conn.Migrator(), nil
}

func mapOpenError(err error) error {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	return apperr.Wrap(apperr.CodeDBUnreachable, "database target tidak terjangkau", err)
}

type target struct{ conn dbms.Conn }

func (t target) Explain(ctx context.Context, sql string, analyze bool) (*dbms.ExplainResult, error) {
	res, err := t.conn.Explainer().Explain(ctx, dbms.ExplainInput{SQL: sql, Analyze: analyze})
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeMigrationFailed, err.Error(), err)
	}
	return res, nil
}

func (t target) CommittedRun(ctx context.Context, runID int64) (int, bool, error) {
	version, found, err := t.conn.Migrator().CommittedVersion(ctx, runID)
	if err != nil {
		return 0, false, mapOpenError(err)
	}
	return version, found, nil
}
