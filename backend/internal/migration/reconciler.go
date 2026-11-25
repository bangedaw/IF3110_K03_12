package migration

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

const reconciledFailure = "server berhenti sebelum eksekusi selesai; perubahan tidak ditemukan di database target sehingga dianggap di-rollback"

type Reconciler struct {
	Pool    *pgxpool.Pool
	Targets TargetRegistry
	Logger  *slog.Logger
}

func (r Reconciler) Reconcile(ctx context.Context) error {
	logger := r.Logger
	if logger == nil {
		logger = slog.Default()
	}
	runs, err := gen.New(r.Pool).ListRunningMigrationRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		target, err := r.Targets.Get(ctx, run.DatabaseID)
		if err != nil {
			logger.WarnContext(ctx, "reconcile: target tidak terjangkau, run dibiarkan running", "run_id", run.ID, "err", err)
			continue
		}
		version, found, err := target.CommittedRun(ctx, run.ID)
		if err != nil {
			logger.WarnContext(ctx, "reconcile: riwayat target tidak terbaca, run dibiarkan running", "run_id", run.ID, "err", err)
			continue
		}
		if err := r.finish(ctx, run, version, found); err != nil {
			return err
		}
		logger.InfoContext(ctx, "reconcile: run diselesaikan", "run_id", run.ID, "committed", found)
	}
	return nil
}

func (r Reconciler) finish(ctx context.Context, run gen.ListRunningMigrationRunsRow, version int, found bool) error {
	return pgx.BeginFunc(ctx, r.Pool, func(tx pgx.Tx) error {
		q := gen.New(tx)
		if !found {
			return q.FinishRun(ctx, gen.FinishRunParams{
				ID: run.ID, Status: gen.RunStatusFailed, ErrorCode: text("RECONCILED"), ErrorMessage: text(reconciledFailure),
			})
		}
		if err := q.FinishRun(ctx, gen.FinishRunParams{ID: run.ID, Status: gen.RunStatusSucceeded}); err != nil {
			return err
		}
		return q.MarkMigrationApplied(ctx, gen.MarkMigrationAppliedParams{
			ID: run.MigrationID, Version: int4of(int32(version)), AppliedBy: int8of(run.ExecutedBy),
		})
	})
}
