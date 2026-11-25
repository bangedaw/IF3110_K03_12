package migration

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
)

func (s *Service) authorize(ctx context.Context, p authz.Principal, a authz.Action, databaseID int64) error {
	return s.Auth.Authorize(ctx, p, a, authz.Resource{DatabaseID: databaseID})
}

func (s *Service) record(ctx context.Context, p authz.Principal, action string, migrationID int64, meta map[string]any) {
	if s.Audit == nil {
		return
	}
	s.Audit.Record(ctx, audit.Entry{
		ActorID: p.UserID, Action: action, ResourceType: "migration",
		ResourceID: formatID(migrationID), Outcome: audit.OutcomeSuccess, Metadata: meta,
	})
}

func missing(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.ErrNotFound
	}
	return err
}

func loadState(ctx context.Context, q *gen.Queries, databaseID int64) (state, error) {
	runs, err := q.ListSucceededRuns(ctx, databaseID)
	if err != nil {
		return state{}, err
	}
	rows, err := q.ListDatabaseRequests(ctx, databaseID)
	if err != nil {
		return state{}, err
	}
	st := state{positions: replay(runs), requests: make([]request, len(rows))}
	for i, r := range rows {
		st.requests[i] = request{
			ID: r.ID, MigrationID: r.MigrationID, Kind: r.Kind, Status: r.Status, Revision: r.Revision,
			RequestedByName: r.RequestedByName, CreatedAt: r.CreatedAt.Time.UTC(),
			JobID: int64Ptr(r.JobID), JobFailed: r.JobStatus == string(gen.JobStatusFailed),
		}
	}
	return st, nil
}

func (s *Service) load(ctx context.Context, q *gen.Queries, p authz.Principal, a authz.Action, id int64) (gen.GetMigrationRow, error) {
	row, err := q.GetMigration(ctx, id)
	if err != nil {
		return row, missing(err)
	}
	return row, s.authorize(ctx, p, a, row.Migration.DatabaseID)
}

func (s *Service) Get(ctx context.Context, p authz.Principal, id int64) (apigen.Migration, error) {
	q := gen.New(s.Pool)
	row, err := s.load(ctx, q, p, authz.ActionMigrationRead, id)
	if err != nil {
		return apigen.Migration{}, err
	}
	st, err := loadState(ctx, q, row.Migration.DatabaseID)
	if err != nil {
		return apigen.Migration{}, err
	}
	level, err := s.Auth.EffectiveLevel(ctx, p.UserID, row.Migration.DatabaseID)
	if err != nil {
		return apigen.Migration{}, err
	}
	return toMigration(row, st, level), nil
}

func (s *Service) List(ctx context.Context, p authz.Principal, databaseID int64) ([]apigen.Migration, error) {
	if err := s.authorize(ctx, p, authz.ActionMigrationRead, databaseID); err != nil {
		return nil, err
	}
	q := gen.New(s.Pool)
	rows, err := q.ListMigrations(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	st, err := loadState(ctx, q, databaseID)
	if err != nil {
		return nil, err
	}
	level, err := s.Auth.EffectiveLevel(ctx, p.UserID, databaseID)
	if err != nil {
		return nil, err
	}
	out := make([]apigen.Migration, len(rows))
	for i, row := range rows {
		out[i] = toMigration(gen.GetMigrationRow(row), st, level)
	}
	return out, nil
}

func (s *Service) validate(ctx context.Context, databaseID int64, d apigen.MigrationDefinition) error {
	kind, err := gen.New(s.Pool).GetDatabaseKind(ctx, databaseID)
	if err != nil {
		return missing(err)
	}
	return validateDefinition(string(kind), d)
}

func batchParams(b *apigen.MigrationBatch) (schema, table, column pgtype.Text, size pgtype.Int4) {
	if b == nil {
		return
	}
	return text(b.Schema), text(b.Table), text(b.Column), int4of(int32(b.Size))
}

func description(d apigen.MigrationDefinition) string {
	if d.Description == nil {
		return ""
	}
	return *d.Description
}

func (s *Service) Create(ctx context.Context, p authz.Principal, databaseID int64, d apigen.MigrationDefinition) (apigen.Migration, error) {
	if err := s.authorize(ctx, p, authz.ActionMigrationWrite, databaseID); err != nil {
		return apigen.Migration{}, err
	}
	if err := s.validate(ctx, databaseID, d); err != nil {
		return apigen.Migration{}, err
	}
	q := gen.New(s.Pool)
	head, err := q.MigrationHead(ctx, databaseID)
	if err != nil {
		return apigen.Migration{}, err
	}
	schema, table, column, size := batchParams(d.Batch)
	id, err := q.CreateMigration(ctx, gen.CreateMigrationParams{
		DatabaseID: databaseID, Title: strings.TrimSpace(d.Title), Description: description(d),
		SqlText: d.UpSql, DownSql: text(strings.TrimSpace(d.DownSql)), Checksum: checksum(d.UpSql),
		Irreversible: d.Irreversible, IrreversibleReason: strings.TrimSpace(d.IrreversibleReason),
		BatchSchema: schema, BatchTable: table, BatchColumn: column, BatchSize: size,
		BaseVersion: head, Actor: p.UserID,
	})
	if err != nil {
		return apigen.Migration{}, err
	}
	s.record(ctx, p, "migration.create", id, nil)
	return s.Get(ctx, p, id)
}

func (s *Service) inMigration(ctx context.Context, p authz.Principal, a authz.Action, id int64, fn func(q *gen.Queries, tx pgx.Tx, row gen.GetMigrationRow, st state) error) error {
	if _, err := s.load(ctx, gen.New(s.Pool), p, a, id); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := gen.New(tx)
		if _, err := q.LockMigration(ctx, id); err != nil {
			return missing(err)
		}
		row, err := q.GetMigration(ctx, id)
		if err != nil {
			return missing(err)
		}
		st, err := loadState(ctx, q, row.Migration.DatabaseID)
		if err != nil {
			return err
		}
		return fn(q, tx, row, st)
	})
}

func (s *Service) Update(ctx context.Context, p authz.Principal, id int64, d apigen.MigrationDefinition, revision int) (apigen.Migration, error) {
	row, err := s.load(ctx, gen.New(s.Pool), p, authz.ActionMigrationWrite, id)
	if err != nil {
		return apigen.Migration{}, err
	}
	if err := s.validate(ctx, row.Migration.DatabaseID, d); err != nil {
		return apigen.Migration{}, err
	}
	err = s.inMigration(ctx, p, authz.ActionMigrationWrite, id, func(q *gen.Queries, _ pgx.Tx, row gen.GetMigrationRow, st state) error {
		if err := st.editable(row.Migration, row.SqlLocked); err != nil {
			return err
		}
		if int(row.Migration.Revision) != revision {
			return conflict("migration sudah diubah orang lain; muat ulang halaman lalu coba lagi")
		}
		schema, table, column, size := batchParams(d.Batch)
		return q.UpdateMigration(ctx, gen.UpdateMigrationParams{
			ID: id, Title: strings.TrimSpace(d.Title), Description: description(d),
			SqlText: d.UpSql, DownSql: text(strings.TrimSpace(d.DownSql)), Checksum: checksum(d.UpSql),
			Irreversible: d.Irreversible, IrreversibleReason: strings.TrimSpace(d.IrreversibleReason),
			BatchSchema: schema, BatchTable: table, BatchColumn: column, BatchSize: size, Actor: p.UserID,
		})
	})
	if err != nil {
		return apigen.Migration{}, err
	}
	s.record(ctx, p, "migration.update", id, map[string]any{"revision": revision + 1})
	return s.Get(ctx, p, id)
}

func (s *Service) Delete(ctx context.Context, p authz.Principal, id int64) error {
	err := s.inMigration(ctx, p, authz.ActionMigrationWrite, id, func(q *gen.Queries, _ pgx.Tx, row gen.GetMigrationRow, st state) error {
		if err := st.editable(row.Migration, row.SqlLocked); err != nil {
			return err
		}
		return q.DeleteMigration(ctx, id)
	})
	if err != nil {
		return err
	}
	s.record(ctx, p, "migration.delete", id, nil)
	return nil
}

func (s *Service) Request(ctx context.Context, p authz.Principal, id int64, kind apigen.MigrationKind) (apigen.Migration, error) {
	var requestID int64
	err := s.inMigration(ctx, p, authz.ActionMigrationExecute, id, func(q *gen.Queries, _ pgx.Tx, row gen.GetMigrationRow, st state) error {
		m := row.Migration
		if err := st.checkNew(gen.RequestKind(kind), m); err != nil {
			return err
		}
		for _, r := range st.requests {
			if r.MigrationID == id && r.Status != r.effective() {
				if err := q.SetMigrationRequestStatus(ctx, gen.SetMigrationRequestStatusParams{ID: r.ID, Status: r.effective()}); err != nil {
					return err
				}
			}
		}
		var err error
		requestID, err = q.CreateMigrationRequest(ctx, gen.CreateMigrationRequestParams{
			MigrationID: id, DatabaseID: m.DatabaseID, Kind: gen.RequestKind(kind), Revision: m.Revision, RequestedBy: p.UserID,
		})
		return err
	})
	if err != nil {
		return apigen.Migration{}, err
	}
	s.record(ctx, p, "migration.request", id, map[string]any{"requestId": requestID, "kind": kind})
	return s.Get(ctx, p, id)
}

func (s *Service) Explain(ctx context.Context, p authz.Principal, id int64, analyze bool) ([]apigen.MigrationPlan, error) {
	row, err := s.load(ctx, gen.New(s.Pool), p, authz.ActionMigrationExplain, id)
	if err != nil {
		return nil, err
	}
	if row.Migration.BatchTable.Valid {
		return nil, conflict("EXPLAIN tidak tersedia untuk migration per batch karena SQL memakai placeholder")
	}
	target, err := s.Targets.Get(ctx, row.Migration.DatabaseID)
	if err != nil {
		return nil, err
	}
	res, err := target.Explain(ctx, row.Migration.SqlText, analyze)
	if err != nil {
		return nil, err
	}
	out := make([]apigen.MigrationPlan, len(res.Statements))
	for i, st := range res.Statements {
		out[i] = apigen.MigrationPlan{Index: st.Index, Sql: st.SQL, Explainable: st.Explainable, Notice: textPtr(text(st.Notice))}
		if st.Plan != nil {
			out[i].Plan = &st.Plan
		}
	}
	return out, nil
}

func (s *Service) History(ctx context.Context, p authz.Principal, databaseID int64, migrationID *int64, hideRollback bool) ([]apigen.MigrationExecution, error) {
	if err := s.authorize(ctx, p, authz.ActionMigrationRead, databaseID); err != nil {
		return nil, err
	}
	rows, err := gen.New(s.Pool).ListExecutions(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	out := make([]apigen.MigrationExecution, 0, len(rows))
	for _, row := range rows {
		if migrationID == nil || row.MigrationRun.MigrationID == *migrationID {
			out = append(out, toExecution(row))
		}
	}
	if hideRollback {
		out = HideRollbackPairs(out)
	}
	return out, nil
}

func (s *Service) Execute(ctx context.Context, p authz.Principal, id, requestID int64) (int64, error) {
	var jobID int64
	err := s.inMigration(ctx, p, authz.ActionMigrationExecute, id, func(q *gen.Queries, tx pgx.Tx, row gen.GetMigrationRow, st state) error {
		m := row.Migration
		cur := st.current(id)
		if cur == nil || cur.ID != requestID {
			return conflict("request ini bukan lagi request terakhir; muat ulang halaman")
		}
		if cur.JobID != nil && cur.open() {
			jobID = *cur.JobID
			return nil
		}
		groups, err := q.ListRequestApprovals(ctx, gen.ListRequestApprovalsParams{DatabaseID: m.DatabaseID, RequestID: requestID})
		if err != nil {
			return err
		}
		if !approved(groups) {
			return conflict("approval belum lengkap dari leader setiap group yang punya akses")
		}
		if cur.Revision != m.Revision {
			return conflict("migration berubah setelah disetujui; ajukan ulang")
		}
		if err := st.check(cur.Kind, m, cur.ID); err != nil {
			return err
		}
		if cur.Status != gen.RequestStatusPendingReview && st.executable(m) == nil {
			return conflict("request ini tidak bisa dijalankan; ajukan request baru")
		}
		if err := q.SetMigrationRequestStatus(ctx, gen.SetMigrationRequestStatusParams{ID: requestID, Status: gen.RequestStatusQueued}); err != nil {
			return err
		}
		jobID, err = queue.NewPostgres(tx).Publish(ctx, queue.PublishInput{RequestID: requestID, DatabaseID: m.DatabaseID, ActorID: p.UserID})
		return err
	})
	if err != nil {
		return 0, err
	}
	s.record(ctx, p, "migration.execute", id, map[string]any{"requestId": requestID, "jobId": jobID})
	return jobID, nil
}
