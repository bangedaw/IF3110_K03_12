package migration_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"

	_ "github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
)

const cipherKey = "REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8="

var (
	editor = authz.Principal{UserID: 10}
	viewer = authz.Principal{UserID: 11}
	nobody = authz.Principal{UserID: 12}
)

type env struct {
	t        *testing.T
	svc      *migration.Service
	platform *pgxpool.Pool
	target   *pgxpool.Pool
	dbID     int64
	deadID   int64
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	platform := testutil.NewPlatformDB(t)
	t.Cleanup(platform.Close)
	target := testutil.NewPostgresTarget(t)
	t.Cleanup(target.Close)

	for _, p := range []authz.Principal{editor, viewer, nobody} {
		_, err := platform.Exec(ctx, `INSERT INTO users (id, username, password_hash) VALUES ($1, $2, 'x')`, p.UserID, fmt.Sprintf("user%d", p.UserID))
		require.NoError(t, err)
	}
	cipher, err := secret.New(cipherKey)
	require.NoError(t, err)
	cfg := target.Config().ConnConfig
	insert := func(name, host string, port uint16, password string) int64 {
		var id int64
		require.NoError(t, platform.QueryRow(ctx, `
			INSERT INTO database_connections (name, kind, host, port, database_name, username, password_ciphertext)
			VALUES ($1, 'postgres', $2, $3, $4, $5, $6) RETURNING id`,
			name, host, int32(port), cfg.Database, cfg.User, cipher.Encrypt([]byte(password))).Scan(&id))
		return id
	}
	e := &env{t: t, platform: platform, target: target}
	e.dbID = insert("live", cfg.Host, cfg.Port, cfg.Password)
	e.deadID = insert("dead", "127.0.0.1", 1, "x")

	store := &dbconn.SQLStore{Q: gen.New(platform)}
	registry := dbconn.NewRegistry(store, cipher)
	t.Cleanup(registry.Close)
	az := authz.NewFake().
		Grant(editor.UserID, e.dbID, authz.LevelEdit).
		Grant(editor.UserID, e.deadID, authz.LevelEdit).
		Grant(viewer.UserID, e.dbID, authz.LevelView)
	e.svc = &migration.Service{
		Repo:    migration.NewRepository(platform),
		Targets: dbconn.Targets{Registry: registry},
		Auth:    az,
		Audit:   audit.New(platform),
	}
	return e
}

func (e *env) draft(title, sql string) *migration.Migration {
	e.t.Helper()
	m, err := e.svc.Create(context.Background(), editor, e.dbID, migration.CreateInput{Title: title, SQL: sql})
	require.NoError(e.t, err)
	return m
}

func (e *env) targetHas(table string) bool {
	e.t.Helper()
	var exists bool
	require.NoError(e.t, e.target.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
	return exists
}

func fields(t *testing.T, err error) map[string]string {
	t.Helper()
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, apperr.CodeValidation, ae.Code, err.Error())
	return ae.Fields
}

func TestDraftLifecycle(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	_, err := e.svc.Create(ctx, editor, e.dbID, migration.CreateInput{})
	assert.Contains(t, fields(t, err), "title")
	assert.Contains(t, fields(t, err), "sql")
	_, err = e.svc.Create(ctx, editor, e.dbID, migration.CreateInput{Title: "x", SQL: "BEGIN; CREATE TABLE app.x (id int); COMMIT;"})
	assert.Contains(t, fields(t, err)["sql"], "transaction-control")
	_, err = e.svc.Create(ctx, viewer, e.dbID, migration.CreateInput{Title: "x", SQL: "SELECT 1"})
	assert.ErrorIs(t, err, authz.ErrForbidden)

	m := e.draft("Tambah tabel", "CREATE TABLE app.t1 (id int)")
	assert.Equal(t, migration.Draft, m.Status)
	assert.Equal(t, 1, m.Revision)
	assert.Equal(t, 0, m.BaseVersion)

	got, err := e.svc.Get(ctx, viewer, e.dbID, m.ID)
	require.NoError(t, err)
	assert.Equal(t, "CREATE TABLE app.t1 (id int)", got.SQL)
	_, err = e.svc.Get(ctx, nobody, e.dbID, m.ID)
	assert.ErrorIs(t, err, authz.ErrForbidden)
	_, err = e.svc.Get(ctx, viewer, e.dbID, 9999)
	assert.ErrorIs(t, err, apperr.ErrNotFound)

	_, err = e.svc.Update(ctx, viewer, e.dbID, m.ID, migration.UpdateInput{Title: "y", SQL: "SELECT 1", Revision: 1})
	assert.ErrorIs(t, err, authz.ErrForbidden)
	updated, err := e.svc.Update(ctx, editor, e.dbID, m.ID, migration.UpdateInput{Title: "Ubah", SQL: "CREATE TABLE app.t1 (id bigint)", Revision: 1})
	require.NoError(t, err)
	assert.Equal(t, 2, updated.Revision)
	_, err = e.svc.Update(ctx, editor, e.dbID, m.ID, migration.UpdateInput{Title: "Basi", SQL: "SELECT 1", Revision: 1})
	assert.ErrorIs(t, err, apperr.ErrConflict)

	items, total, err := e.svc.List(ctx, viewer, e.dbID, "draft", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, "Ubah", items[0].Title)

	assert.ErrorIs(t, e.svc.Delete(ctx, viewer, e.dbID, m.ID, 2), authz.ErrForbidden)
	assert.ErrorIs(t, e.svc.Delete(ctx, editor, e.dbID, m.ID, 1), apperr.ErrConflict)
	require.NoError(t, e.svc.Delete(ctx, editor, e.dbID, m.ID, 2))
	_, err = e.svc.Get(ctx, editor, e.dbID, m.ID)
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

func TestExecuteCommitsAndRecordsLinearHistory(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	m := e.draft("Buat t1", "CREATE TABLE app.t1 (id int); INSERT INTO app.t1 VALUES (1);")
	run, err := e.svc.Execute(ctx, editor, e.dbID, m.ID, migration.ExecuteInput{ExpectedRevision: 1})
	require.NoError(t, err)
	assert.Equal(t, migration.RunSucceeded, run.Status)
	require.NotNil(t, run.Version)
	assert.Equal(t, 1, *run.Version)
	assert.True(t, e.targetHas("app.t1"))

	applied, err := e.svc.Get(ctx, viewer, e.dbID, m.ID)
	require.NoError(t, err)
	assert.Equal(t, migration.Applied, applied.Status)
	assert.Equal(t, 1, *applied.Version)
	require.NotNil(t, applied.AppliedBy)
	assert.Equal(t, editor.UserID, *applied.AppliedBy)

	_, err = e.svc.Update(ctx, editor, e.dbID, m.ID, migration.UpdateInput{Title: "x", SQL: "SELECT 1", Revision: 1})
	assert.ErrorIs(t, err, apperr.ErrConflict)
	_, err = e.svc.Execute(ctx, editor, e.dbID, m.ID, migration.ExecuteInput{ExpectedRevision: 1})
	assert.ErrorIs(t, err, apperr.ErrConflict)

	second := e.draft("Buat t2", "CREATE TABLE app.t2 (id int)")
	assert.Equal(t, 1, second.BaseVersion)
	run, err = e.svc.Execute(ctx, editor, e.dbID, second.ID, migration.ExecuteInput{ExpectedRevision: 1})
	require.NoError(t, err)
	assert.Equal(t, 2, *run.Version)

	history, total, err := e.svc.History(ctx, viewer, e.dbID, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Equal(t, "Buat t2", history[0].Title)
	assert.Equal(t, 2, *history[0].Version)
	assert.Equal(t, 1, *history[1].Version)
	assert.Equal(t, "user10", history[1].ExecutedByName)
	assert.Contains(t, history[1].SQL, "CREATE TABLE app.t1")
	assert.NotNil(t, history[1].FinishedAt)

	var snapshots int
	require.NoError(t, e.platform.QueryRow(ctx, `SELECT count(*) FROM schema_snapshots WHERE migration_run_id IS NOT NULL`).Scan(&snapshots))
	assert.Equal(t, 2, snapshots)
}

func TestExecuteFailureRollsBackEveryStatement(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	m := e.draft("Gagal di statement 3", "CREATE TABLE app.t3 (id int); INSERT INTO app.t3 VALUES (1); INSERT INTO app.tidak_ada VALUES (1);")
	run, err := e.svc.Execute(ctx, editor, e.dbID, m.ID, migration.ExecuteInput{ExpectedRevision: 1})
	require.NoError(t, err)
	assert.Equal(t, migration.RunFailed, run.Status)
	require.NotNil(t, run.FailedStatementIndex)
	assert.Equal(t, 3, *run.FailedStatementIndex)
	assert.Equal(t, string(apperr.CodeMigrationFailed), run.ErrorCode)
	assert.Contains(t, run.ErrorMessage, "tidak_ada")
	assert.Nil(t, run.Version)

	assert.False(t, e.targetHas("app.t3"), "statement 1-2 harus ikut ter-rollback")
	still, err := e.svc.Get(ctx, editor, e.dbID, m.ID)
	require.NoError(t, err)
	assert.Equal(t, migration.Draft, still.Status)
	head, err := e.svc.Head(ctx, viewer, e.dbID)
	require.NoError(t, err)
	assert.Equal(t, 0, head)

	runs, err := e.svc.Runs(ctx, viewer, e.dbID, m.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, migration.RunFailed, runs[0].Status)
}

func TestViewOnlyCanReadButNotChange(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.draft("Draft", "CREATE TABLE app.t4 (id int)")

	_, err := e.svc.Execute(ctx, viewer, e.dbID, m.ID, migration.ExecuteInput{ExpectedRevision: 1})
	assert.ErrorIs(t, err, authz.ErrForbidden)
	_, err = e.svc.Explain(ctx, viewer, e.dbID, m.ID, false)
	assert.ErrorIs(t, err, authz.ErrForbidden)
	assert.False(t, e.targetHas("app.t4"))

	_, _, err = e.svc.History(ctx, viewer, e.dbID, 0, 0)
	assert.NoError(t, err)
	_, err = e.svc.Runs(ctx, viewer, e.dbID, m.ID)
	assert.NoError(t, err)
	_, _, err = e.svc.History(ctx, nobody, e.dbID, 0, 0)
	assert.ErrorIs(t, err, authz.ErrForbidden)
}

func TestExecuteDetectsHeadChange(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.draft("A", "CREATE TABLE app.ta (id int)")
	b := e.draft("B", "CREATE TABLE app.tb (id int)")

	_, err := e.svc.Execute(ctx, editor, e.dbID, a.ID, migration.ExecuteInput{ExpectedRevision: 1})
	require.NoError(t, err)

	_, err = e.svc.Execute(ctx, editor, e.dbID, b.ID, migration.ExecuteInput{ExpectedRevision: 1})
	assert.ErrorIs(t, err, apperr.ErrConflict)
	assert.False(t, e.targetHas("app.tb"))

	run, err := e.svc.Execute(ctx, editor, e.dbID, b.ID, migration.ExecuteInput{ExpectedRevision: 1, AcknowledgeHeadChanged: true})
	require.NoError(t, err)
	assert.Equal(t, 2, *run.Version)
}

func TestConcurrentExecutionsStayLinear(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	drafts := []*migration.Migration{
		e.draft("P1", "CREATE TABLE app.p1 (id int); SELECT pg_sleep(0.5);"),
		e.draft("P2", "CREATE TABLE app.p2 (id int); SELECT pg_sleep(0.5);"),
	}

	var wg sync.WaitGroup
	errs := make([]error, len(drafts))
	for i, m := range drafts {
		wg.Go(func() {
			_, errs[i] = e.svc.Execute(ctx, editor, e.dbID, m.ID, migration.ExecuteInput{ExpectedRevision: 1})
		})
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		} else {
			assert.ErrorIs(t, err, apperr.ErrConflict)
		}
	}
	assert.Equal(t, 1, succeeded)
	head, err := e.svc.Head(ctx, editor, e.dbID)
	require.NoError(t, err)
	assert.Equal(t, 1, head)
}

func TestExecuteOnUnreachableTargetRecordsFailedRun(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m, err := e.svc.Create(ctx, editor, e.deadID, migration.CreateInput{Title: "x", SQL: "CREATE TABLE t (id int)"})
	require.NoError(t, err)

	_, err = e.svc.Execute(ctx, editor, e.deadID, m.ID, migration.ExecuteInput{ExpectedRevision: 1})
	assert.ErrorIs(t, err, apperr.ErrDBUnreachable)

	runs, err := e.svc.Runs(ctx, editor, e.deadID, m.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, migration.RunFailed, runs[0].Status)
	assert.Equal(t, string(apperr.CodeDBUnreachable), runs[0].ErrorCode)
}

func TestMigrationActionsAreAudited(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m := e.draft("Audit", "CREATE TABLE app.t5 (id int)")
	_, err := e.svc.Execute(ctx, editor, e.dbID, m.ID, migration.ExecuteInput{ExpectedRevision: 1})
	require.NoError(t, err)

	var actions []string
	rows, err := e.platform.Query(ctx, `SELECT action || ':' || outcome FROM audit_logs WHERE resource_type = 'migration' ORDER BY id`)
	require.NoError(t, err)
	for rows.Next() {
		var a string
		require.NoError(t, rows.Scan(&a))
		actions = append(actions, a)
	}
	assert.Equal(t, []string{"migration.create:success", "migration.execute:success"}, actions)
}

func (e *env) orphanRun(databaseID int64, m *migration.Migration, commitOnTarget bool) *migration.Run {
	e.t.Helper()
	ctx := context.Background()
	repo := e.svc.Repo
	run, err := repo.StartRun(ctx, editor.UserID, m)
	require.NoError(e.t, err)
	if commitOnTarget {
		target, err := e.svc.Targets.Get(ctx, databaseID)
		require.NoError(e.t, err)
		sum, err := hex.DecodeString(run.Checksum)
		require.NoError(e.t, err)
		_, err = target.Apply(ctx, migration.ApplyRequest{RunID: run.ID, MigrationID: m.ID, DatabaseID: databaseID, Version: 1, Checksum: sum, SQL: m.SQL})
		require.NoError(e.t, err)
	}
	return run
}

func TestReconcilerResolvesRunsLeftRunning(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	committed := e.draft("Ter-commit sebelum crash", "CREATE TABLE app.r1 (id int)")
	lost := e.draft("Belum ter-commit", "CREATE TABLE app.r2 (id int)")
	dead, err := e.svc.Create(ctx, editor, e.deadID, migration.CreateInput{Title: "Target mati", SQL: "CREATE TABLE r3 (id int)"})
	require.NoError(t, err)

	committedRun := e.orphanRun(e.dbID, committed, true)
	lostRun := e.orphanRun(e.dbID, lost, false)
	deadRun := e.orphanRun(e.deadID, dead, false)

	rec := migration.Reconciler{Repo: e.svc.Repo.(*migration.PGRepository), Targets: e.svc.Targets}
	require.NoError(t, rec.Reconcile(ctx))

	runs, err := e.svc.Runs(ctx, editor, e.dbID, committed.ID)
	require.NoError(t, err)
	assert.Equal(t, committedRun.ID, runs[0].ID)
	assert.Equal(t, migration.RunSucceeded, runs[0].Status)
	m, err := e.svc.Get(ctx, editor, e.dbID, committed.ID)
	require.NoError(t, err)
	assert.Equal(t, migration.Applied, m.Status)
	assert.Equal(t, 1, *m.Version)

	runs, err = e.svc.Runs(ctx, editor, e.dbID, lost.ID)
	require.NoError(t, err)
	assert.Equal(t, lostRun.ID, runs[0].ID)
	assert.Equal(t, migration.RunFailed, runs[0].Status)
	assert.Equal(t, "RECONCILED", runs[0].ErrorCode)
	m, err = e.svc.Get(ctx, editor, e.dbID, lost.ID)
	require.NoError(t, err)
	assert.Equal(t, migration.Draft, m.Status)

	runs, err = e.svc.Runs(ctx, editor, e.deadID, dead.ID)
	require.NoError(t, err)
	assert.Equal(t, deadRun.ID, runs[0].ID)
	assert.Equal(t, migration.RunRunning, runs[0].Status, "target mati: run dibiarkan agar bisa direkonsiliasi nanti")

	head, err := e.svc.Head(ctx, editor, e.dbID)
	require.NoError(t, err)
	assert.Equal(t, 1, head)
}
