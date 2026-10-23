package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration/sqlsplit"
)

var (
	ErrNotFound  = errors.New("migration not found")
	ErrConflict  = errors.New("migration revision or database head conflict")
	ErrImmutable = errors.New("applied migrations are immutable")
	ErrForbidden = errors.New("forbidden")
)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func checksum(sql string) []byte    { h := sha256.Sum256([]byte(sql)); return h[:] }
func checksumHex(sql string) string { return hex.EncodeToString(checksum(sql)) }
func validateDraft(in CreateInput) error {
	if strings.TrimSpace(in.Title) == "" {
		return errors.New("title is required")
	}
	if strings.TrimSpace(in.SQL) == "" {
		return errors.New("SQL is required")
	}
	return nil
}
func (s *Service) Create(ctx context.Context, actor, databaseID int64, in CreateInput) (*Migration, error) {
	if err := s.Auth.Authorize(ctx, actor, databaseID, "migration:write"); err != nil {
		return nil, err
	}
	if err := validateDraft(in); err != nil {
		return nil, err
	}
	stmts, err := sqlsplit.Split(in.SQL, sqlsplit.PostgreSQL)
	if err != nil {
		return nil, err
	}
	if err = sqlsplit.Validate(stmts, sqlsplit.PostgreSQL); err != nil {
		return nil, err
	}
	head, err := s.Repo.Head(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	return s.Repo.CreateDraft(ctx, actor, databaseID, in, head, checksum(in.SQL))
}
func (s *Service) Update(ctx context.Context, actor, databaseID, migrationID int64, in UpdateInput) (*Migration, error) {
	if err := s.Auth.Authorize(ctx, actor, databaseID, "migration:write"); err != nil {
		return nil, err
	}
	if err := validateDraft(CreateInput{Title: in.Title, SQL: in.SQL}); err != nil {
		return nil, err
	}
	stmts, err := sqlsplit.Split(in.SQL, sqlsplit.PostgreSQL)
	if err != nil {
		return nil, err
	}
	if err = sqlsplit.Validate(stmts, sqlsplit.PostgreSQL); err != nil {
		return nil, err
	}
	return s.Repo.UpdateDraft(ctx, actor, databaseID, migrationID, in, checksum(in.SQL))
}
func (s *Service) Delete(ctx context.Context, actor, databaseID, migrationID int64, revision int) error {
	if err := s.Auth.Authorize(ctx, actor, databaseID, "migration:write"); err != nil {
		return err
	}
	m, err := s.Repo.Get(ctx, databaseID, migrationID)
	if err != nil {
		return err
	}
	if m.Status != Draft {
		return ErrImmutable
	}
	return s.Repo.DeleteDraft(ctx, databaseID, migrationID, revision)
}
func (s *Service) List(ctx context.Context, actor, databaseID int64, status string, limit, offset int) ([]Migration, int, error) {
	if err := s.Auth.Authorize(ctx, actor, databaseID, "migration:read"); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.Repo.List(ctx, databaseID, status, limit, offset)
}
func (s *Service) Get(ctx context.Context, actor, databaseID, migrationID int64) (*Migration, error) {
	if err := s.Auth.Authorize(ctx, actor, databaseID, "migration:read"); err != nil {
		return nil, err
	}
	return s.Repo.Get(ctx, databaseID, migrationID)
}
func (s *Service) Execute(ctx context.Context, actor, databaseID, migrationID int64, in ExecuteInput) (*Run, error) {
	if err := s.Auth.Authorize(ctx, actor, databaseID, "migration:execute"); err != nil {
		return nil, err
	}
	var result *Run
	err := s.Repo.WithDatabaseLock(ctx, databaseID, func(ctx context.Context) error {
		m, err := s.Repo.Get(ctx, databaseID, migrationID)
		if err != nil {
			return err
		}
		if m.Status != Draft {
			return ErrImmutable
		}
		if m.Revision != in.ExpectedRevision {
			return ErrConflict
		}
		head, err := s.Repo.Head(ctx, databaseID)
		if err != nil {
			return err
		}
		if head != m.BaseVersion && !in.AcknowledgeHeadChanged {
			return fmt.Errorf("%w: head changed from %d to %d", ErrConflict, m.BaseVersion, head)
		}
		version := head + 1
		run, err := s.Repo.StartRun(ctx, actor, m)
		if err != nil {
			return err
		}
		result = run
		target, err := s.Targets.Get(ctx, databaseID)
		if err != nil {
			_ = s.Repo.FinishRun(ctx, run.ID, RunFailed, nil, "DB_UNREACHABLE", err.Error(), nil)
			return err
		}
		_, err = target.Apply(ctx, ApplyRequest{RunID: run.ID, MigrationID: m.ID, DatabaseID: databaseID, Version: version, Checksum: checksum(m.SQL), SQL: m.SQL})
		if err != nil {
			var failed *int

			_ = s.Repo.FinishRun(ctx, run.ID, RunFailed, nil, "MIGRATION_FAILED", err.Error(), failed)
			_ = s.Repo.Audit(ctx, actor, "migration.execute", "migration", fmt.Sprint(m.ID), "failure", map[string]any{"runId": run.ID})
			return err
		}
		if err = s.Repo.FinishRun(ctx, run.ID, RunSucceeded, &version, "", "", nil); err != nil {
			return err
		}
		_ = s.Repo.Audit(ctx, actor, "migration.execute", "migration", fmt.Sprint(m.ID), "success", map[string]any{"runId": run.ID, "version": version})
		result.Status = RunSucceeded

		if model, snapErr := target.Snapshot(ctx); snapErr == nil {
			_ = s.Repo.SaveSnapshot(ctx, databaseID, run.ID, actor, model)
		}
		return nil
	})
	return result, err
}
func (s *Service) Runs(ctx context.Context, actor, databaseID, migrationID int64) ([]Run, error) {
	if err := s.Auth.Authorize(ctx, actor, databaseID, "migration:read"); err != nil {
		return nil, err
	}
	return s.Repo.Runs(ctx, databaseID, migrationID)
}
func (s *Service) History(ctx context.Context, actor, databaseID int64, limit, offset int) ([]Run, int, error) {
	if err := s.Auth.Authorize(ctx, actor, databaseID, "migration:read"); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.Repo.History(ctx, databaseID, limit, offset)
}
func (s *Service) Explain(ctx context.Context, actor, databaseID, migrationID int64, analyze bool) (any, error) {
	if err := s.Auth.Authorize(ctx, actor, databaseID, "migration:explain"); err != nil {
		return nil, err
	}
	m, err := s.Repo.Get(ctx, databaseID, migrationID)
	if err != nil {
		return nil, err
	}
	target, err := s.Targets.Get(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	return target.Explain(ctx, m.SQL, analyze)
}
