package migration

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration/sqlsplit"
)

const (
	maxTitleLen       = 200
	maxDescriptionLen = 2000
)

func checksum(sql string) []byte { h := sha256.Sum256([]byte(sql)); return h[:] }

func (s *Service) authorize(ctx context.Context, p authz.Principal, a authz.Action, databaseID int64) error {
	return s.Auth.Authorize(ctx, p, a, authz.Resource{DatabaseID: databaseID})
}

func (s *Service) validate(ctx context.Context, databaseID int64, title, description, sql string) error {
	fields := map[string]string{}
	if strings.TrimSpace(title) == "" || len(title) > maxTitleLen {
		fields["title"] = "wajib diisi, maksimal 200 karakter"
	}
	if len(description) > maxDescriptionLen {
		fields["description"] = "maksimal 2000 karakter"
	}
	if strings.TrimSpace(sql) == "" {
		fields["sql"] = "wajib diisi"
	} else {
		kind, err := s.Repo.DatabaseKind(ctx, databaseID)
		if err != nil {
			return err
		}
		stmts, err := sqlsplit.Split(sql, sqlsplit.Dialect(kind))
		if err == nil {
			err = sqlsplit.Validate(stmts, sqlsplit.Dialect(kind))
		}
		if err != nil {
			fields["sql"] = err.Error()
		}
	}
	if len(fields) > 0 {
		return apperr.Validation(fields)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, p authz.Principal, databaseID int64, in CreateInput) (*Migration, error) {
	if err := s.authorize(ctx, p, authz.ActionMigrationWrite, databaseID); err != nil {
		return nil, err
	}
	if err := s.validate(ctx, databaseID, in.Title, in.Description, in.SQL); err != nil {
		return nil, err
	}
	head, err := s.Repo.Head(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	m, err := s.Repo.CreateDraft(ctx, p.UserID, databaseID, in, head, checksum(in.SQL))
	if err != nil {
		return nil, err
	}
	s.record(ctx, p, "migration.create", m.ID, audit.OutcomeSuccess, nil)
	return m, nil
}

func (s *Service) Update(ctx context.Context, p authz.Principal, databaseID, migrationID int64, in UpdateInput) (*Migration, error) {
	if err := s.authorize(ctx, p, authz.ActionMigrationWrite, databaseID); err != nil {
		return nil, err
	}
	if err := s.validate(ctx, databaseID, in.Title, in.Description, in.SQL); err != nil {
		return nil, err
	}
	if _, err := s.editableDraft(ctx, databaseID, migrationID, in.Revision); err != nil {
		return nil, err
	}
	m, err := s.Repo.UpdateDraft(ctx, p.UserID, databaseID, migrationID, in, checksum(in.SQL))
	if err != nil {
		return nil, err
	}
	s.record(ctx, p, "migration.update", m.ID, audit.OutcomeSuccess, nil)
	return m, nil
}

func (s *Service) Delete(ctx context.Context, p authz.Principal, databaseID, migrationID int64, revision int) error {
	if err := s.authorize(ctx, p, authz.ActionMigrationWrite, databaseID); err != nil {
		return err
	}
	if _, err := s.editableDraft(ctx, databaseID, migrationID, revision); err != nil {
		return err
	}
	if err := s.Repo.DeleteDraft(ctx, databaseID, migrationID, revision); err != nil {
		return err
	}
	s.record(ctx, p, "migration.delete", migrationID, audit.OutcomeSuccess, nil)
	return nil
}

func (s *Service) editableDraft(ctx context.Context, databaseID, migrationID int64, revision int) (*Migration, error) {
	m, err := s.Repo.Get(ctx, databaseID, migrationID)
	if err != nil {
		return nil, err
	}
	if m.Status != Draft {
		return nil, apperr.New(apperr.CodeConflict, "migration yang sudah dijalankan tidak bisa diubah; buat migration baru")
	}
	if m.Revision != revision {
		return nil, apperr.New(apperr.CodeConflict, "migration sudah diubah orang lain; muat ulang halaman lalu coba lagi")
	}
	return m, nil
}

func (s *Service) List(ctx context.Context, p authz.Principal, databaseID int64, status string, limit, offset int) ([]Migration, int, error) {
	if err := s.authorize(ctx, p, authz.ActionMigrationRead, databaseID); err != nil {
		return nil, 0, err
	}
	if status != string(Draft) && status != string(Applied) {
		status = ""
	}
	limit, offset = page(limit, offset)
	return s.Repo.List(ctx, databaseID, status, limit, offset)
}

func (s *Service) Get(ctx context.Context, p authz.Principal, databaseID, migrationID int64) (*Migration, error) {
	if err := s.authorize(ctx, p, authz.ActionMigrationRead, databaseID); err != nil {
		return nil, err
	}
	return s.Repo.Get(ctx, databaseID, migrationID)
}

func (s *Service) Head(ctx context.Context, p authz.Principal, databaseID int64) (int, error) {
	if err := s.authorize(ctx, p, authz.ActionMigrationRead, databaseID); err != nil {
		return 0, err
	}
	return s.Repo.Head(ctx, databaseID)
}

type statementIndexer interface{ StatementIndex() int }

func (s *Service) Execute(ctx context.Context, p authz.Principal, databaseID, migrationID int64, in ExecuteInput) (*Run, error) {
	if err := s.authorize(ctx, p, authz.ActionMigrationExecute, databaseID); err != nil {
		return nil, err
	}
	var result *Run
	err := s.Repo.WithDatabaseLock(ctx, databaseID, func(ctx context.Context) error {
		m, err := s.editableDraft(ctx, databaseID, migrationID, in.ExpectedRevision)
		if err != nil {
			return err
		}
		head, err := s.Repo.Head(ctx, databaseID)
		if err != nil {
			return err
		}
		if head != m.BaseVersion && !in.AcknowledgeHeadChanged {
			return apperr.New(apperr.CodeConflict, fmt.Sprintf(
				"versi database berubah dari %d menjadi %d sejak draft ini dibuat; tinjau ulang SQL lalu konfirmasi", m.BaseVersion, head))
		}
		version := head + 1
		run, err := s.Repo.StartRun(ctx, p.UserID, m)
		if err != nil {
			return err
		}
		target, err := s.Targets.Get(ctx, databaseID)
		if err != nil {
			_, _ = s.Repo.FinishRun(ctx, run.ID, RunFailed, nil, string(apperr.CodeDBUnreachable), "database target tidak terjangkau", nil)
			s.record(ctx, p, "migration.execute", m.ID, audit.OutcomeFailure, map[string]any{"runId": run.ID})
			return err
		}
		_, applyErr := target.Apply(ctx, ApplyRequest{RunID: run.ID, MigrationID: m.ID, DatabaseID: databaseID, Version: version, Checksum: checksum(m.SQL), SQL: m.SQL})
		if applyErr != nil {
			var failedIndex *int
			var si statementIndexer
			if errors.As(applyErr, &si) {
				idx := si.StatementIndex()
				failedIndex = &idx
			}
			result, err = s.Repo.FinishRun(ctx, run.ID, RunFailed, nil, string(apperr.CodeMigrationFailed), failureMessage(applyErr), failedIndex)
			s.record(ctx, p, "migration.execute", m.ID, audit.OutcomeFailure, map[string]any{"runId": run.ID})
			return err
		}
		result, err = s.Repo.FinishRun(ctx, run.ID, RunSucceeded, &version, "", "", nil)
		if err != nil {
			return err
		}
		s.record(ctx, p, "migration.execute", m.ID, audit.OutcomeSuccess, map[string]any{"runId": run.ID, "version": version})
		if model, snapErr := target.Snapshot(ctx); snapErr == nil {
			_ = s.Repo.SaveSnapshot(ctx, databaseID, run.ID, p.UserID, model)
		}
		return nil
	})
	return result, err
}

func failureMessage(err error) string {
	var ae *apperr.Error
	if errors.As(err, &ae) && ae.Detail != "" {
		return ae.Detail
	}
	return err.Error()
}

func (s *Service) Runs(ctx context.Context, p authz.Principal, databaseID, migrationID int64) ([]Run, error) {
	if err := s.authorize(ctx, p, authz.ActionMigrationRead, databaseID); err != nil {
		return nil, err
	}
	return s.Repo.Runs(ctx, databaseID, migrationID)
}

func (s *Service) History(ctx context.Context, p authz.Principal, databaseID int64, limit, offset int) ([]Run, int, error) {
	if err := s.authorize(ctx, p, authz.ActionMigrationRead, databaseID); err != nil {
		return nil, 0, err
	}
	limit, offset = page(limit, offset)
	return s.Repo.History(ctx, databaseID, limit, offset)
}

func (s *Service) Explain(ctx context.Context, p authz.Principal, databaseID, migrationID int64, analyze bool) (any, error) {
	if err := s.authorize(ctx, p, authz.ActionMigrationExplain, databaseID); err != nil {
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

func (s *Service) record(ctx context.Context, p authz.Principal, action string, migrationID int64, outcome string, meta map[string]any) {
	if s.Audit == nil {
		return
	}
	s.Audit.Record(ctx, audit.Entry{
		ActorID:      p.UserID,
		Action:       action,
		ResourceType: "migration",
		ResourceID:   strconv.FormatInt(migrationID, 10),
		Outcome:      outcome,
		Metadata:     meta,
	})
}

func page(limit, offset int) (int, int) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
