package migration

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// lifecycle adalah implementasi ExecutionLifecycle (contracts.go) di atas Store.
type lifecycle struct {
	store Store
	now   func() time.Time
}

// NewLifecycle membuat ExecutionLifecycle untuk dipakai worker.
func NewLifecycle(s Store) ExecutionLifecycle {
	return &lifecycle{store: s, now: time.Now}
}

// Revalidate mengulang pemeriksaan terhadap keadaan TERKINI saat worker
// mengambil job:
//  1. status migration sesuai jenis eksekusi,
//  2. CheckApply/CheckRollback/CheckForward masih lolos,
//  3. approval revisi saat ini masih lengkap.
func (l *lifecycle) Revalidate(ctx context.Context, req ExecutionRequest) error {
	m, err := l.load(ctx, req)
	if err != nil {
		return err
	}

	switch req.Kind {
	case KindApply:
		if m.Status != StatusApproved && m.Status != StatusPendingQueue {
			return fmt.Errorf("%w: apply butuh status approved/pending_queue, didapat %s", ErrInvalidTransition, m.Status)
		}
	case KindRollback, KindForward:
		if m.Status != StatusSuccess {
			return fmt.Errorf("%w: %s butuh status success, didapat %s", ErrInvalidTransition, req.Kind, m.Status)
		}
		if !m.SQLLocked {
			return fmt.Errorf("%w: migration %d belum pernah dieksekusi", ErrInvalidTransition, m.ID)
		}
	}

	all, err := l.store.ListMigrations(ctx, m.DatabaseID)
	if err != nil {
		return fmt.Errorf("lifecycle: list migrations: %w", err)
	}
	history, err := l.store.ListExecutions(ctx, m.DatabaseID, HistoryFilter{})
	if err != nil {
		return fmt.Errorf("lifecycle: history: %w", err)
	}
	switch req.Kind {
	case KindApply:
		err = CheckApply(*m, history, all)
	case KindRollback:
		err = CheckRollback(*m, history, all)
	case KindForward:
		err = CheckForward(*m, history, all)
	}
	if err != nil {
		return err
	}

	required, err := requiredApprovers(ctx, l.store, m.DatabaseID)
	if err != nil {
		return err
	}
	got, err := l.store.ListApprovals(ctx, m.ID, m.Revision, req.Kind)
	if err != nil {
		return fmt.Errorf("lifecycle: approvals: %w", err)
	}
	if !ApprovalsComplete(required, got) {
		return fmt.Errorf("%w: revisi %d, kind=%s", ErrApprovalIncomplete, m.Revision, req.Kind)
	}
	return nil
}

// MarkRunning membuat baris attempt (running). Untuk apply, status migration
// juga dipindah ke running. SQL yang dijalankan disalin ke attempt: UpSQL untuk
// apply/forward, DownSQL untuk rollback.
func (l *lifecycle) MarkRunning(ctx context.Context, req ExecutionRequest) (int64, error) {
	m, err := l.load(ctx, req)
	if err != nil {
		return 0, err
	}

	if req.Kind == KindApply && m.Status != StatusRunning { // running = retry idempoten
		if m.Status == StatusApproved { // worker mendahului Service.Decide
			if err := moveStatus(ctx, l.store, m, StatusPendingQueue); err != nil {
				return 0, fmt.Errorf("lifecycle: enqueue: %w", err)
			}
		}
		if err := moveStatus(ctx, l.store, m, StatusRunning); err != nil {
			return 0, fmt.Errorf("lifecycle: mark running: %w", err)
		}
	}

	sql := m.UpSQL
	var reverts *int64
	if req.Kind == KindRollback {
		if m.DownSQL == nil {
			return 0, fmt.Errorf("%w: migration %d tidak punya DownSQL", ErrRollbackBlocked, m.ID)
		}
		sql = *m.DownSQL
		history, err := l.store.ListExecutions(ctx, m.DatabaseID, HistoryFilter{MigrationID: &m.ID})
		if err != nil {
			return 0, fmt.Errorf("lifecycle: history: %w", err)
		}
		reverts = lastAppliedExecutionID(history, m.ID)
	}

	att, err := l.store.InsertExecution(ctx, Execution{
		MigrationID:        m.ID,
		Revision:           m.Revision,
		UserID:             req.RequestedBy,
		Kind:               req.Kind,
		Status:             StatusRunning,
		ExecutedSQL:        sql,
		RevertsExecutionID: reverts,
		StartedAt:          l.now(),
	})
	if err != nil {
		return 0, fmt.Errorf("lifecycle: insert attempt: %w", err)
	}

	if !m.SQLLocked {
		if err := l.store.LockSQL(ctx, m.ID); err != nil {
			_ = l.store.FinalizeExecution(ctx, att.ID, ExecutionResult{ErrorMsg: "gagal mengunci SQL: " + err.Error()}, l.now())
			return 0, fmt.Errorf("lifecycle: lock sql: %w", err)
		}
	}
	logExecution(ctx, att)
	return att.ID, nil
}

// RecordResult menutup attempt (satu kali; riwayat tidak berubah setelahnya).
// Untuk apply, status migration ikut menjadi success/failed.
func (l *lifecycle) RecordResult(ctx context.Context, attemptID int64, res ExecutionResult) error {
	if attemptID == 0 {
		return errors.New("lifecycle: attemptID kosong")
	}
	if res.Success && (res.ErrorMsg != "" || res.SQLState != "") {
		return errors.New("lifecycle: Success=true tetapi ErrorMsg/SQLState terisi")
	}
	cur, err := l.store.GetExecution(ctx, attemptID)
	if err != nil {
		return err
	}
	if !cur.FinishedAt.IsZero() || cur.Status != StatusRunning {
		return fmt.Errorf("lifecycle: attempt %d sudah selesai (riwayat tidak dapat diubah)", attemptID)
	}

	now := l.now()
	if err := l.store.FinalizeExecution(ctx, attemptID, res, now); err != nil {
		return fmt.Errorf("lifecycle: finalize: %w", err)
	}

	final := StatusFailed
	if res.Success {
		final = StatusSuccess
	}
	done := *cur
	done.Status = final
	done.FinishedAt = now
	done.ErrorMsg, done.SQLState = res.ErrorMsg, res.SQLState
	if res.ExecutedSQL != "" {
		done.ExecutedSQL = res.ExecutedSQL
	}
	logExecution(ctx, done)

	if cur.Kind != KindApply {
		return nil // rollback/forward tidak mengubah status migration
	}
	m, err := l.store.GetMigrationByID(ctx, cur.MigrationID)
	if err != nil {
		return fmt.Errorf("lifecycle: get migration: %w", err)
	}
	if err := moveStatus(ctx, l.store, m, final); err != nil {
		return fmt.Errorf("lifecycle: attempt %d tercatat, status migration gagal diperbarui: %w", attemptID, err)
	}
	return nil
}

func (l *lifecycle) load(ctx context.Context, req ExecutionRequest) (*Migration, error) {
	if req.MigrationID == 0 {
		return nil, errors.New("lifecycle: MigrationID kosong")
	}
	if !req.Kind.IsValid() {
		return nil, fmt.Errorf("lifecycle: kind %q tidak dikenal", req.Kind)
	}
	return l.store.GetMigrationByID(ctx, req.MigrationID)
}
