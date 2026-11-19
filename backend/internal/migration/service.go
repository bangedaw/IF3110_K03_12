package migration

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/sqlsplit"
)

const (
	maxTitleLen       = 200
	maxDescriptionLen = 2000
)

// Service adalah use case migration M2 (R-1 sampai R-6). Eksekusi SQL tidak
// ada di sini; dilakukan worker lewat ExecutionLifecycle.
type Service struct {
	Store   Store
	Publish Publisher
	Auth    Authorizer
	Audit   *audit.Logger // opsional
	Now     func() time.Time
}

func NewService(s Store, p Publisher, a Authorizer) *Service {
	return &Service{Store: s, Publish: p, Auth: a, Now: time.Now}
}

// Create membuat migration draft baru.
func (s *Service) Create(ctx context.Context, actor Actor, databaseID int64, d Definition) (Migration, error) {
	if err := s.requireEdit(ctx, actor, databaseID); err != nil {
		return Migration{}, err
	}
	if err := s.validateDefinition(ctx, databaseID, d); err != nil {
		return Migration{}, err
	}
	now := s.now()
	m := Migration{
		DatabaseID:         databaseID,
		Title:              strings.TrimSpace(d.Title),
		Description:        d.Description,
		Status:             StatusDraft,
		Revision:           1,
		UpSQL:              d.UpSQL,
		DownSQL:            downPtr(d),
		Irreversible:       d.Irreversible,
		IrreversibleReason: d.IrreversibleReason,
		Batch:              d.Batch,
		CreatedBy:          actor.UserID,
		UpdatedBy:          actor.UserID,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	saved, err := s.Store.CreateMigration(ctx, m) // Store mengisi ID dan Seq
	if err != nil {
		return Migration{}, fmt.Errorf("Create: %w", err)
	}
	s.record(ctx, actor.UserID, "migration.create", saved.ID, nil)
	return saved, nil
}

// UpdateDraft mengubah migration berstatus draft; revisi naik satu.
func (s *Service) UpdateDraft(ctx context.Context, actor Actor, id int64, d Definition) (Migration, error) {
	m, err := s.Store.GetMigrationByID(ctx, id)
	if err != nil {
		return Migration{}, err
	}
	if err := s.requireEdit(ctx, actor, m.DatabaseID); err != nil {
		return Migration{}, err
	}
	if err := EnsureSQLEditable(*m); err != nil {
		return Migration{}, apperr.New(apperr.CodeConflict, "migration sudah pernah dieksekusi; buat migration baru")
	}
	if m.Status != StatusDraft {
		return Migration{}, apperr.New(apperr.CodeConflict,
			fmt.Sprintf("migration berstatus %s tidak bisa diubah; hanya draft", m.Status))
	}
	if err := s.validateDefinition(ctx, m.DatabaseID, d); err != nil {
		return Migration{}, err
	}

	updated := *m
	updated.Title = strings.TrimSpace(d.Title)
	updated.Description = d.Description
	updated.UpSQL = d.UpSQL
	updated.DownSQL = downPtr(d)
	updated.Irreversible = d.Irreversible
	updated.IrreversibleReason = d.IrreversibleReason
	updated.Batch = d.Batch
	updated.Revision = m.Revision + 1
	updated.UpdatedBy = actor.UserID
	updated.UpdatedAt = s.now()

	saved, err := s.Store.UpdateMigration(ctx, updated) // compare-and-set pada revisi lama
	if err != nil {
		return Migration{}, err
	}
	s.record(ctx, actor.UserID, "migration.update", saved.ID, map[string]any{"revision": saved.Revision})
	return saved, nil
}

// SubmitForReview: draft -> pending_review, dan membuka approval kind apply.
func (s *Service) SubmitForReview(ctx context.Context, actor Actor, id int64) error {
	m, err := s.Store.GetMigrationByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.requireEdit(ctx, actor, m.DatabaseID); err != nil {
		return err
	}
	if m.Status != StatusDraft {
		return apperr.New(apperr.CodeConflict,
			fmt.Sprintf("migration berstatus %s tidak bisa diajukan; hanya draft", m.Status))
	}
	if err := s.validateDefinition(ctx, m.DatabaseID, definitionFromMigration(*m)); err != nil {
		return err
	}
	// Baris approval dibuat lebih dulu agar tidak ada migration pending_review
	// tanpa approver.
	if err := s.requestApproval(ctx, actor, m, KindApply); err != nil {
		return err
	}
	if err := moveStatus(ctx, s.Store, m, StatusPendingReview); err != nil {
		return conflictErr(err)
	}
	s.record(ctx, actor.UserID, "migration.submit", m.ID, map[string]any{"revision": m.Revision})
	return nil
}

// Get memuat migration. Akses: View Only.
func (s *Service) Get(ctx context.Context, actor Actor, id int64) (Migration, error) {
	m, err := s.Store.GetMigrationByID(ctx, id)
	if err != nil {
		return Migration{}, err
	}
	if err := s.requireView(ctx, actor, m.DatabaseID); err != nil {
		return Migration{}, err
	}
	return *m, nil
}

// List mengembalikan semua migration database, urut Seq.
func (s *Service) List(ctx context.Context, actor Actor, databaseID int64) ([]Migration, error) {
	if err := s.requireView(ctx, actor, databaseID); err != nil {
		return nil, err
	}
	return s.Store.ListMigrations(ctx, databaseID)
}

// RequestRollback mengajukan approval rollback (kind rollback).
func (s *Service) RequestRollback(ctx context.Context, actor Actor, migrationID int64) error {
	return s.requestExecution(ctx, actor, migrationID, KindRollback, CheckRollback, "migration.request_rollback")
}

// RequestForward mengajukan approval forward (kind forward).
func (s *Service) RequestForward(ctx context.Context, actor Actor, migrationID int64) error {
	return s.requestExecution(ctx, actor, migrationID, KindForward, CheckForward, "migration.request_forward")
}

type ruleCheck func(m Migration, history []Execution, all []Migration) error

func (s *Service) requestExecution(ctx context.Context, actor Actor, migrationID int64, kind Kind, check ruleCheck, action string) error {
	m, err := s.Store.GetMigrationByID(ctx, migrationID)
	if err != nil {
		return err
	}
	if err := s.requireEdit(ctx, actor, m.DatabaseID); err != nil {
		return err
	}
	all, err := s.Store.ListMigrations(ctx, m.DatabaseID)
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	hist, err := s.Store.ListExecutions(ctx, m.DatabaseID, HistoryFilter{})
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	if err := check(*m, hist, all); err != nil {
		return apperr.New(apperr.CodeConflict, err.Error())
	}
	if err := s.requestApproval(ctx, actor, m, kind); err != nil {
		return err
	}
	s.record(ctx, actor.UserID, action, m.ID, map[string]any{"revision": m.Revision})
	return nil
}

// --- helper internal ---

func (s *Service) requireView(ctx context.Context, actor Actor, databaseID int64) error {
	if s.Auth == nil {
		return errAuthNotWired
	}
	return s.Auth.CanView(ctx, actor, databaseID)
}

func (s *Service) requireEdit(ctx context.Context, actor Actor, databaseID int64) error {
	if s.Auth == nil {
		return errAuthNotWired
	}
	return s.Auth.CanEdit(ctx, actor, databaseID)
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// record menulis audit log (tabel DB), bukan log aplikasi.
func (s *Service) record(ctx context.Context, actorID int64, action string, migrationID int64, meta map[string]any) {
	if s.Audit == nil {
		return
	}
	s.Audit.Record(ctx, audit.Entry{
		ActorID:      actorID,
		Action:       action,
		ResourceType: "migration",
		ResourceID:   strconv.FormatInt(migrationID, 10),
		Outcome:      audit.OutcomeSuccess,
		Metadata:     meta,
	})
}

// validateDefinition: validasi domain (ValidateDefinition) + judul + SQL.
func (s *Service) validateDefinition(ctx context.Context, databaseID int64, d Definition) error {
	fields := map[string]string{}
	title := strings.TrimSpace(d.Title)
	if title == "" || len(title) > maxTitleLen {
		fields["title"] = fmt.Sprintf("wajib diisi, maksimal %d karakter", maxTitleLen)
	}
	if len(d.Description) > maxDescriptionLen {
		fields["description"] = fmt.Sprintf("maksimal %d karakter", maxDescriptionLen)
	}
	if err := ValidateDefinition(d); err != nil {
		fields["definition"] = err.Error()
	}

	kind, err := s.Store.DatabaseKind(ctx, databaseID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(d.UpSQL) != "" {
		if err := validateSQLText(kind, d.UpSQL); err != nil {
			fields["upSql"] = err.Error()
		}
	}
	if !d.Irreversible && strings.TrimSpace(d.DownSQL) != "" {
		if err := validateSQLText(kind, d.DownSQL); err != nil {
			fields["downSql"] = err.Error()
		}
	}
	if len(fields) > 0 {
		return apperr.Validation(fields)
	}
	return nil
}

// validateSQLText memakai paket sqlsplit (satu-satunya titik kontak). Bila
// tanda tangan sqlsplit berbeda, cukup ubah fungsi ini.
func validateSQLText(dbKind, sql string) error {
	dialect := sqlsplit.Dialect(dbKind)
	stmts, err := sqlsplit.Split(sql, dialect)
	if err != nil {
		return err
	}
	return sqlsplit.Validate(stmts, dialect)
}

// moveStatus memindahkan status di memori (Transition) lalu menyimpannya
// dengan compare-and-set. Bila penyimpanan gagal, m dikembalikan seperti semula.
func moveStatus(ctx context.Context, st Store, m *Migration, to Status) error {
	from := m.Status
	if err := Transition(m, to); err != nil {
		return err
	}
	if from == to {
		return nil
	}
	if err := st.SetStatus(ctx, m.ID, from, to); err != nil {
		m.Status = from
		return err
	}
	return nil
}

// conflictErr memetakan error status/aturan domain menjadi apperr conflict.
func conflictErr(err error) error {
	if err == nil {
		return nil
	}
	return apperr.New(apperr.CodeConflict, err.Error())
}

func downPtr(d Definition) *string {
	if d.Irreversible {
		return nil
	}
	s := d.DownSQL
	return &s
}

func definitionFromMigration(m Migration) Definition {
	d := Definition{
		Title:              m.Title,
		Description:        m.Description,
		UpSQL:              m.UpSQL,
		Irreversible:       m.Irreversible,
		IrreversibleReason: m.IrreversibleReason,
		Batch:              m.Batch,
	}
	if m.DownSQL != nil {
		d.DownSQL = *m.DownSQL
	}
	return d
}
