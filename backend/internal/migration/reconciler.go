package migration

import (
	"context"
	"log/slog"
)

const reconciledFailure = "server berhenti sebelum eksekusi selesai; perubahan tidak ditemukan di database target sehingga dianggap di-rollback"

type ReconcileRepository interface {
	RunningRuns(ctx context.Context) ([]Run, error)
	FinishRun(ctx context.Context, runID int64, status Status, version *int, errCode, errMessage string, failedIndex *int) (*Run, error)
}

type Reconciler struct {
	Repo    ReconcileRepository
	Targets TargetRegistry
	Logger  *slog.Logger
}

func (r Reconciler) Reconcile(ctx context.Context) error {
	logger := r.Logger
	if logger == nil {
		logger = slog.Default()
	}
	runs, err := r.Repo.RunningRuns(ctx)
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
		if found {
			_, err = r.Repo.FinishRun(ctx, run.ID, RunSucceeded, &version, "", "", nil)
		} else {
			_, err = r.Repo.FinishRun(ctx, run.ID, RunFailed, nil, "RECONCILED", reconciledFailure, nil)
		}
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "reconcile: run diselesaikan", "run_id", run.ID, "committed", found)
	}
	return nil
}
