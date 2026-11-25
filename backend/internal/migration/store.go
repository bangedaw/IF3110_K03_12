package migration

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
)

// Store adalah satu-satunya akses data M2 (migration, approval, riwayat
// eksekusi). Dipakai Service dan lifecycle. Aturan:
//   - Get* yang tidak menemukan baris mengembalikan apperr.ErrNotFound
//     (tidak pernah (nil, nil)).
//   - SetStatus dan UpdateMigration bersifat compare-and-set.
//   - Baris eksekusi tidak pernah dihapus; hanya difinalisasi satu kali.
type Store interface {
	DatabaseKind(ctx context.Context, databaseID int64) (string, error)

	// CreateMigration mengisi ID dan Seq (urutan linear per database).
	CreateMigration(ctx context.Context, m Migration) (Migration, error)
	GetMigrationByID(ctx context.Context, id int64) (*Migration, error)
	ListMigrations(ctx context.Context, databaseID int64) ([]Migration, error) // urut Seq
	// UpdateMigration menyimpan draft dengan m.Revision = revisi lama + 1;
	// gagal (apperr conflict) bila revisi lama sudah berubah, bukan draft, atau SQL terkunci.
	UpdateMigration(ctx context.Context, m Migration) (Migration, error)
	// SetStatus: ErrStatusConflict bila status saat ini bukan from.
	SetStatus(ctx context.Context, id int64, from, to Status) error
	LockSQL(ctx context.Context, id int64) error

	ListApprovers(ctx context.Context, databaseID int64) ([]Approver, error)
	// UpsertApprovals membuat baris pending, atau mengatur ulang baris yang ada ke pending.
	UpsertApprovals(ctx context.Context, rows []Approval) error
	ListApprovals(ctx context.Context, migrationID int64, revision int, kind Kind) ([]Approval, error)
	// DecideApproval mengembalikan (nil, nil) bila tidak ada baris pending milik leader itu.
	DecideApproval(ctx context.Context, migrationID int64, revision int, kind Kind, leaderID int64,
		d ApprovalDecision, comment string, at time.Time) (*Approval, error)

	InsertExecution(ctx context.Context, e Execution) (Execution, error)
	GetExecution(ctx context.Context, id int64) (*Execution, error)
	// FinalizeExecution hanya berlaku untuk attempt yang masih running.
	FinalizeExecution(ctx context.Context, id int64, res ExecutionResult, at time.Time) error
	// ListExecutions: semua attempt database (opsional per migration), urut StartedAt lalu ID.
	ListExecutions(ctx context.Context, databaseID int64, f HistoryFilter) ([]Execution, error)
}

// pgStore: implementasi Postgres dengan SQL langsung (tabel dan kolom ada di
// 00005_migration_workflow.sql). Tidak memakai kode sqlc agar tidak bentrok
// dengan query M1 (GetMigration, ListMigrations) di paket gen.
type pgStore struct {
	pool    *pgxpool.Pool
	leaders LeaderSource
}

// NewPgStore membuat Store. leaders disediakan modul admin (Migu, M-5).
func NewPgStore(pool *pgxpool.Pool, leaders LeaderSource) Store {
	return &pgStore{pool: pool, leaders: leaders}
}

type rowScanner interface{ Scan(dest ...any) error }

func missing(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.ErrNotFound
	}
	return err
}

// --- migration ---

const migrationCols = `id, database_id, title, description, status::text, version, base_version, revision, seq,
	up_sql, down_sql, irreversible, irreversible_reason, batch_column, batch_size, sql_locked,
	created_by, updated_by, created_at, updated_at`

func scanMigration(r rowScanner) (*Migration, error) {
	var (
		m      Migration
		status string
		col    *string
		size   *int
	)
	if err := r.Scan(&m.ID, &m.DatabaseID, &m.Title, &m.Description, &status, &m.Version, &m.BaseVersion,
		&m.Revision, &m.Seq, &m.UpSQL, &m.DownSQL, &m.Irreversible, &m.IrreversibleReason, &col, &size,
		&m.SQLLocked, &m.CreatedBy, &m.UpdatedBy, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	m.Status = Status(status)
	m.SQL = m.UpSQL // cermin field legacy
	if col != nil && size != nil {
		m.Batch = &BatchConfig{Column: *col, Size: *size}
	}
	return &m, nil
}

func batchParts(b *BatchConfig) (*string, *int) {
	if b == nil {
		return nil, nil
	}
	return &b.Column, &b.Size
}

func (s *pgStore) DatabaseKind(ctx context.Context, databaseID int64) (string, error) {
	var kind string
	err := s.pool.QueryRow(ctx, `SELECT kind::text FROM database_connections WHERE id = $1`, databaseID).Scan(&kind)
	return kind, missing(err)
}

func (s *pgStore) CreateMigration(ctx context.Context, m Migration) (Migration, error) {
	col, size := batchParts(m.Batch)
	sum := sha256.Sum256([]byte(m.UpSQL))
	row := s.pool.QueryRow(ctx, `
		INSERT INTO migrations (database_id, title, description, sql_text, checksum, status, revision, base_version,
			up_sql, down_sql, irreversible, irreversible_reason, batch_column, batch_size, sql_locked, seq,
			created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, 'draft', 1, 0, $4, $6, $7, $8, $9, $10, false,
			(SELECT COALESCE(MAX(seq), 0) + 1 FROM migrations WHERE database_id = $1), $11, $11)
		RETURNING `+migrationCols,
		m.DatabaseID, m.Title, m.Description, m.UpSQL, sum[:], m.DownSQL, m.Irreversible, m.IrreversibleReason,
		col, size, m.CreatedBy)
	out, err := scanMigration(row)
	if err != nil {
		return Migration{}, err
	}
	return *out, nil
}

func (s *pgStore) GetMigrationByID(ctx context.Context, id int64) (*Migration, error) {
	m, err := scanMigration(s.pool.QueryRow(ctx, `SELECT `+migrationCols+` FROM migrations WHERE id = $1`, id))
	if err != nil {
		return nil, missing(err)
	}
	return m, nil
}

func (s *pgStore) ListMigrations(ctx context.Context, databaseID int64) ([]Migration, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+migrationCols+` FROM migrations WHERE database_id = $1 ORDER BY seq`, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Migration
	for rows.Next() {
		m, err := scanMigration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *pgStore) UpdateMigration(ctx context.Context, m Migration) (Migration, error) {
	col, size := batchParts(m.Batch)
	sum := sha256.Sum256([]byte(m.UpSQL))
	row := s.pool.QueryRow(ctx, `
		UPDATE migrations
		   SET title = $2, description = $3, sql_text = $4, up_sql = $4, checksum = $5, down_sql = $6,
		       irreversible = $7, irreversible_reason = $8, batch_column = $9, batch_size = $10,
		       revision = $11, updated_by = $12, updated_at = now()
		 WHERE id = $1 AND revision = $11 - 1 AND status = 'draft' AND NOT sql_locked
		RETURNING `+migrationCols,
		m.ID, m.Title, m.Description, m.UpSQL, sum[:], m.DownSQL, m.Irreversible, m.IrreversibleReason,
		col, size, m.Revision, m.UpdatedBy)
	out, err := scanMigration(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Migration{}, apperr.New(apperr.CodeConflict, "migration sudah diubah atau tidak lagi draft; muat ulang halaman")
	}
	if err != nil {
		return Migration{}, err
	}
	return *out, nil
}

func (s *pgStore) SetStatus(ctx context.Context, id int64, from, to Status) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE migrations SET status = $3::mig_status, updated_at = now() WHERE id = $1 AND status = $2::mig_status`,
		id, string(from), string(to))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: id=%d diharapkan %s", ErrStatusConflict, id, from)
	}
	return nil
}

func (s *pgStore) LockSQL(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE migrations SET sql_locked = true WHERE id = $1`, id)
	return err
}

// --- approval ---

func (s *pgStore) ListApprovers(ctx context.Context, databaseID int64) ([]Approver, error) {
	return s.leaders.ListLeaders(ctx, databaseID)
}

func (s *pgStore) UpsertApprovals(ctx context.Context, rows []Approval) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, a := range rows {
			if _, err := tx.Exec(ctx, `
				INSERT INTO migration_approvals (migration_id, revision, kind, leader_id, group_id, decision, comment, decided_at)
				VALUES ($1, $2, $3, $4, $5, 'pending', '', NULL)
				ON CONFLICT (migration_id, revision, kind, leader_id)
				DO UPDATE SET group_id = EXCLUDED.group_id, decision = 'pending', comment = '', decided_at = NULL`,
				a.MigrationID, a.Revision, string(a.Kind), a.LeaderID, a.GroupID); err != nil {
				return err
			}
		}
		return nil
	})
}

const approvalCols = `migration_id, revision, group_id, leader_id, kind, decision, comment, decided_at`

func scanApproval(r rowScanner) (*Approval, error) {
	var (
		a         Approval
		kind, dec string
		decidedAt *time.Time
	)
	if err := r.Scan(&a.MigrationID, &a.Revision, &a.GroupID, &a.LeaderID, &kind, &dec, &a.Comment, &decidedAt); err != nil {
		return nil, err
	}
	a.Kind, a.Decision, a.DecidedAt = Kind(kind), ApprovalDecision(dec), decidedAt
	return &a, nil
}

func (s *pgStore) ListApprovals(ctx context.Context, migrationID int64, revision int, kind Kind) ([]Approval, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+approvalCols+` FROM migration_approvals
		WHERE migration_id = $1 AND revision = $2 AND kind = $3 ORDER BY leader_id`,
		migrationID, revision, string(kind))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Approval
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (s *pgStore) DecideApproval(ctx context.Context, migrationID int64, revision int, kind Kind, leaderID int64,
	d ApprovalDecision, comment string, at time.Time) (*Approval, error) {
	a, err := scanApproval(s.pool.QueryRow(ctx, `
		UPDATE migration_approvals SET decision = $5, comment = $6, decided_at = $7
		 WHERE migration_id = $1 AND revision = $2 AND kind = $3 AND leader_id = $4 AND decision = 'pending'
		RETURNING `+approvalCols,
		migrationID, revision, string(kind), leaderID, string(d), comment, at))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// --- execution ---

const executionCols = `id, migration_id, revision, user_id, kind, status, executed_sql, reverts_execution_id,
	error_msg, sql_state, started_at, finished_at`

func scanExecution(r rowScanner) (*Execution, error) {
	var (
		e          Execution
		kind, stat string
		finished   *time.Time
	)
	if err := r.Scan(&e.ID, &e.MigrationID, &e.Revision, &e.UserID, &kind, &stat, &e.ExecutedSQL,
		&e.RevertsExecutionID, &e.ErrorMsg, &e.SQLState, &e.StartedAt, &finished); err != nil {
		return nil, err
	}
	e.Kind, e.Status = Kind(kind), Status(stat)
	if finished != nil {
		e.FinishedAt = *finished
	}
	return &e, nil
}

func (s *pgStore) InsertExecution(ctx context.Context, e Execution) (Execution, error) {
	out, err := scanExecution(s.pool.QueryRow(ctx, `
		INSERT INTO migration_executions (migration_id, revision, user_id, kind, status, executed_sql, reverts_execution_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+executionCols,
		e.MigrationID, e.Revision, e.UserID, string(e.Kind), string(e.Status), e.ExecutedSQL, e.RevertsExecutionID))
	if err != nil {
		return Execution{}, err
	}
	return *out, nil
}

func (s *pgStore) GetExecution(ctx context.Context, id int64) (*Execution, error) {
	e, err := scanExecution(s.pool.QueryRow(ctx, `SELECT `+executionCols+` FROM migration_executions WHERE id = $1`, id))
	if err != nil {
		return nil, missing(err)
	}
	return e, nil
}

func (s *pgStore) FinalizeExecution(ctx context.Context, id int64, res ExecutionResult, at time.Time) error {
	status := StatusFailed
	if res.Success {
		status = StatusSuccess
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE migration_executions
		   SET status = $2, executed_sql = COALESCE(NULLIF($3, ''), executed_sql),
		       error_msg = $4, sql_state = $5, finished_at = $6
		 WHERE id = $1 AND status = 'running'`,
		id, string(status), res.ExecutedSQL, res.ErrorMsg, res.SQLState, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("attempt %d tidak berstatus running", id)
	}
	return nil
}

func (s *pgStore) ListExecutions(ctx context.Context, databaseID int64, f HistoryFilter) ([]Execution, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+executionCols+` FROM migration_executions
		WHERE migration_id IN (SELECT id FROM migrations WHERE database_id = $1)
		  AND ($2::bigint IS NULL OR migration_id = $2)
		ORDER BY started_at, id`, databaseID, f.MigrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Execution
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}
