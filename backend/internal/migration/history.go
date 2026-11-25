package migration

import "github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"

func HideRollbackPairs(items []apigen.MigrationExecution) []apigen.MigrationExecution {
	byID := make(map[int64]apigen.MigrationExecution, len(items))
	for _, e := range items {
		byID[e.Id] = e
	}
	hidden := map[int64]bool{}
	for _, e := range items {
		if e.Kind != apigen.Rollback || e.Status != apigen.Succeeded || e.RevertsExecutionId == nil {
			continue
		}
		if reverted, ok := byID[*e.RevertsExecutionId]; ok && reverted.Status == apigen.Succeeded {
			hidden[e.Id], hidden[reverted.Id] = true, true
		}
	}
	out := make([]apigen.MigrationExecution, 0, len(items))
	for _, e := range items {
		if !hidden[e.Id] {
			out = append(out, e)
		}
	}
	return out
}
