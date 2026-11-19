package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
)

// ListHistory mengembalikan semua attempt (berhasil dan gagal), urut waktu.
// Akses: View Only ke database sudah cukup.
func (s *Service) ListHistory(ctx context.Context, actor Actor, databaseID int64, f HistoryFilter) ([]Execution, error) {
	if err := s.requireView(ctx, actor, databaseID); err != nil {
		return nil, err
	}
	items, err := s.Store.ListExecutions(ctx, databaseID, f)
	if err != nil {
		return nil, fmt.Errorf("ListHistory: %w", err)
	}
	sortExecutions(items)
	if f.HideRollback {
		items = HideRollbackPairs(items)
	}
	return items, nil
}

// HideRollbackPairs menyembunyikan setiap pasangan (execution yang dibatalkan,
// rollback sukses yang merujuknya lewat RevertsExecutionID). Attempt gagal dan
// execution yang tidak dibatalkan tetap tampil.
//
// Contoh: E1 apply A ok; E2 apply B ok; E3 rollback B (reverts E2) ok;
// E4 apply B gagal; E5 apply B ok  ->  tampil E1, E4, E5.
func HideRollbackPairs(items []Execution) []Execution {
	hidden := make(map[int64]struct{})
	for _, e := range items {
		if e.Kind != KindRollback || e.Status != StatusSuccess || e.RevertsExecutionID == nil {
			continue
		}
		hidden[e.ID] = struct{}{}
		hidden[*e.RevertsExecutionID] = struct{}{}
	}
	out := make([]Execution, 0, len(items))
	for _, e := range items {
		if _, hide := hidden[e.ID]; !hide {
			out = append(out, e)
		}
	}
	return out
}

// logExecution menulis log aplikasi (slog) untuk satu baris riwayat, dengan
// data yang sama dengan baris di DB (R-4). trace_id/span_id ditambahkan
// handler log milik Migu. SQL tidak dicatat utuh, hanya hash dan panjang.
func logExecution(ctx context.Context, e Execution) {
	attrs := []any{
		"event", "migration.execution",
		"execution_id", e.ID,
		"migration_id", e.MigrationID,
		"revision", e.Revision,
		"user_id", e.UserID,
		"kind", string(e.Kind),
		"status", string(e.Status),
		"started_at", e.StartedAt,
	}
	if !e.FinishedAt.IsZero() {
		attrs = append(attrs, "finished_at", e.FinishedAt, "duration_ms", e.FinishedAt.Sub(e.StartedAt).Milliseconds())
	}
	if e.RevertsExecutionID != nil {
		attrs = append(attrs, "reverts_execution_id", *e.RevertsExecutionID)
	}
	if e.ExecutedSQL != "" {
		sum := sha256.Sum256([]byte(e.ExecutedSQL))
		attrs = append(attrs, "executed_sql_sha256", hex.EncodeToString(sum[:]), "executed_sql_len", len(e.ExecutedSQL))
	}
	if e.Status == StatusFailed {
		attrs = append(attrs, "error", e.ErrorMsg, "sqlstate", e.SQLState)
		slog.WarnContext(ctx, "migration.execution."+string(e.Status), attrs...)
		return
	}
	slog.InfoContext(ctx, "migration.execution."+string(e.Status), attrs...)
}

// sortExecutions mengurutkan kronologis (StartedAt, lalu ID) di tempat.
func sortExecutions(items []Execution) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].StartedAt.Equal(items[j].StartedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].StartedAt.Before(items[j].StartedAt)
	})
}
