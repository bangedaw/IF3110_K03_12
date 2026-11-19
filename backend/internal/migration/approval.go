package migration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
)

// RequiredApprovers menghitung leader setiap group yang punya akses (View Only
// maupun View + Edit) ke database.
//
// Keputusan sementara (belum dijawab di Q&A):
//   - Leader pengaju tetap dihitung (tanpa pengecualian self-approval).
//   - Group tanpa leader aktif tidak menghasilkan approver.
func (s *Service) RequiredApprovers(ctx context.Context, databaseID int64) ([]Approver, error) {
	return requiredApprovers(ctx, s.Store, databaseID)
}

// requiredApprovers dipakai Service dan lifecycle. Satu leader yang memimpin
// beberapa group dihitung satu suara.
func requiredApprovers(ctx context.Context, st Store, databaseID int64) ([]Approver, error) {
	all, err := st.ListApprovers(ctx, databaseID)
	if err != nil {
		return nil, fmt.Errorf("required approvers: %w", err)
	}
	seen := make(map[int64]struct{}, len(all))
	out := make([]Approver, 0, len(all))
	for _, a := range all {
		if a.LeaderID == 0 {
			continue
		}
		if _, ok := seen[a.LeaderID]; ok {
			continue
		}
		seen[a.LeaderID] = struct{}{}
		out = append(out, a)
	}
	return out, nil
}

// RequestApproval membuka putaran approval baru untuk migration/kind.
func (s *Service) RequestApproval(ctx context.Context, actor Actor, migrationID int64, kind Kind) error {
	if !kind.IsValid() {
		return fmt.Errorf("RequestApproval: kind %q tidak sah", kind)
	}
	m, err := s.Store.GetMigrationByID(ctx, migrationID)
	if err != nil {
		return err
	}
	if err := s.requireEdit(ctx, actor, m.DatabaseID); err != nil {
		return err
	}
	return s.requestApproval(ctx, actor, m, kind)
}

// requestApproval membuat baris pending untuk setiap leader pada revisi
// migration saat ini. Menolak bila putaran sebelumnya masih berjalan; bila
// putaran sebelumnya sudah selesai (ditolak atau semua setuju), baris lama
// diatur ulang ke pending.
func (s *Service) requestApproval(ctx context.Context, actor Actor, m *Migration, kind Kind) error {
	required, err := requiredApprovers(ctx, s.Store, m.DatabaseID)
	if err != nil {
		return err
	}
	if len(required) == 0 {
		return apperr.New(apperr.CodeConflict,
			fmt.Sprintf("tidak ada leader aktif yang dapat menyetujui database %d", m.DatabaseID))
	}
	current, err := s.Store.ListApprovals(ctx, m.ID, m.Revision, kind)
	if err != nil {
		return fmt.Errorf("requestApproval: %w", err)
	}
	if roundOpen(current) {
		return apperr.New(apperr.CodeConflict, "pengajuan approval untuk revisi ini masih berjalan")
	}
	rows := make([]Approval, 0, len(required))
	for _, ap := range required {
		rows = append(rows, Approval{
			MigrationID: m.ID,
			Revision:    m.Revision,
			GroupID:     ap.GroupID,
			LeaderID:    ap.LeaderID,
			Kind:        kind,
			Decision:    ApprovalPending,
		})
	}
	if err := s.Store.UpsertApprovals(ctx, rows); err != nil {
		return fmt.Errorf("requestApproval: %w", err)
	}
	s.record(ctx, actor.UserID, "migration.approval.request", m.ID, map[string]any{
		"revision": m.Revision, "kind": kind, "required": len(required),
	})
	return nil
}

// Decide mencatat setuju/tolak dari seorang leader.
//
//   - apply: migration harus pending_review. Semua setuju -> approved, publish
//     job, lalu pending_queue. Ada yang menolak -> kembali ke draft.
//   - rollback/forward: migration harus success; status migration tidak
//     berubah. Semua setuju -> publish job.
func (s *Service) Decide(ctx context.Context, actor Actor, migrationID int64, kind Kind, d ApprovalDecision, comment string) error {
	if !kind.IsValid() {
		return fmt.Errorf("Decide: kind %q tidak sah", kind)
	}
	if !d.IsDecided() {
		return fmt.Errorf("Decide: keputusan %q tidak sah", d)
	}
	if s.Publish == nil {
		return errors.New("Decide: Publisher belum di-wire")
	}

	m, err := s.Store.GetMigrationByID(ctx, migrationID)
	if err != nil {
		return err
	}
	want := StatusSuccess
	if kind == KindApply {
		want = StatusPendingReview
	}
	if m.Status != want {
		return fmt.Errorf("%w: kind=%s butuh status %s, migration berstatus %s", ErrInvalidTransition, kind, want, m.Status)
	}

	required, err := requiredApprovers(ctx, s.Store, m.DatabaseID)
	if err != nil {
		return err
	}
	isApprover := false
	for _, a := range required {
		if a.LeaderID == actor.UserID {
			isApprover = true
			break
		}
	}
	if !isApprover {
		return fmt.Errorf("%w: user %d untuk database %d", ErrNotApprover, actor.UserID, m.DatabaseID)
	}

	existing, err := s.Store.ListApprovals(ctx, m.ID, m.Revision, kind)
	if err != nil {
		return fmt.Errorf("Decide: %w", err)
	}
	for _, ap := range existing {
		if ap.Decision == ApprovalRejected {
			return fmt.Errorf("%w: pengajuan ini sudah ditolak", ErrApprovalNotPending)
		}
		if ap.LeaderID == actor.UserID && ap.Decision.IsDecided() {
			return fmt.Errorf("%w: keputusan=%s", ErrAlreadyDecided, ap.Decision)
		}
	}

	updated, err := s.Store.DecideApproval(ctx, m.ID, m.Revision, kind, actor.UserID, d, comment, s.now())
	if err != nil {
		return fmt.Errorf("Decide: %w", err)
	}
	if updated == nil {
		return ErrApprovalNotPending
	}
	s.record(ctx, actor.UserID, "migration.approval.decide", m.ID, map[string]any{
		"revision": m.Revision, "kind": kind, "decision": d, "comment": comment,
	})

	all, err := s.Store.ListApprovals(ctx, m.ID, m.Revision, kind)
	if err != nil {
		return fmt.Errorf("Decide: %w", err)
	}

	if d == ApprovalRejected {
		if kind == KindApply {
			return moveStatus(ctx, s.Store, m, StatusDraft)
		}
		return nil
	}
	if !ApprovalsComplete(required, all) {
		return nil
	}

	if kind == KindApply {
		if err := moveStatus(ctx, s.Store, m, StatusApproved); err != nil {
			return fmt.Errorf("Decide: approve: %w", err)
		}
	}
	req := ExecutionRequest{MigrationID: m.ID, RequestedBy: actor.UserID, Kind: kind}
	if err := s.Publish.Publish(ctx, req); err != nil {
		if kind == KindApply {
			// Tidak ada job: kembalikan ke draft agar bisa diajukan ulang.
			_ = moveStatus(ctx, s.Store, m, StatusDraft)
		}
		return fmt.Errorf("Decide: publish: %w", err)
	}
	if kind == KindApply {
		// Worker bisa saja sudah memajukan status; konflik di sini bukan kegagalan.
		if err := moveStatus(ctx, s.Store, m, StatusPendingQueue); err != nil && !errors.Is(err, ErrStatusConflict) {
			slog.WarnContext(ctx, "migration.enqueue.status", "migration_id", m.ID, "error", err)
		}
	}
	return nil
}

// ApprovalsComplete: true jika setiap approver di `required` punya keputusan
// Approved di `got`. Ada Rejected atau approver tanpa baris -> false. Tanpa
// approver -> false.
func ApprovalsComplete(required []Approver, got []Approval) bool {
	if len(required) == 0 {
		return false
	}
	approved := make(map[int64]struct{}, len(got))
	rejected := make(map[int64]struct{}, len(got))
	for _, a := range got {
		switch a.Decision {
		case ApprovalApproved:
			approved[a.LeaderID] = struct{}{}
		case ApprovalRejected:
			rejected[a.LeaderID] = struct{}{}
		}
	}
	for _, r := range required {
		if _, ok := rejected[r.LeaderID]; ok {
			return false
		}
		if _, ok := approved[r.LeaderID]; !ok {
			return false
		}
	}
	return true
}

// ListApprovals untuk halaman review: approval revisi migration saat ini
// untuk kind tertentu. Akses: View Only.
func (s *Service) ListApprovals(ctx context.Context, actor Actor, migrationID int64, kind Kind) ([]Approval, error) {
	if !kind.IsValid() {
		return nil, fmt.Errorf("ListApprovals: kind %q tidak sah", kind)
	}
	m, err := s.Store.GetMigrationByID(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	if err := s.requireView(ctx, actor, m.DatabaseID); err != nil {
		return nil, err
	}
	return s.Store.ListApprovals(ctx, m.ID, m.Revision, kind)
}

// roundOpen: putaran approval masih berjalan bila ada baris pending dan belum
// ada penolakan.
func roundOpen(rows []Approval) bool {
	pending := false
	for _, a := range rows {
		if a.Decision == ApprovalRejected {
			return false
		}
		if a.Decision == ApprovalPending {
			pending = true
		}
	}
	return pending
}
