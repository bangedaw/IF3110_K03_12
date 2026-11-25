package migration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/audit"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/queue"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"

	_ "github.com/bangedaw/IF3110_K03_12/backend/internal/dbms/postgres"
)

const cipherKey = "REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8="

type env struct {
	t        *testing.T
	ctx      context.Context
	platform *pgxpool.Pool
	target   *pgxpool.Pool
	svc      *migration.Service
	worker   *queue.Worker
	dbID     int64
	deadID   int64
	editors  int64
	viewers  int64
	lead     authz.Principal
	editor   authz.Principal
	viewLead authz.Principal
	nobody   authz.Principal
}

func setup(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, ctx: context.Background(), platform: testutil.NewPlatformDB(t), target: testutil.NewPostgresTarget(t)}
	t.Cleanup(e.platform.Close)
	t.Cleanup(e.target.Close)
	user := func(name string) authz.Principal {
		return authz.Principal{UserID: e.scan(`INSERT INTO users (username, password_hash) VALUES ($1, 'x') RETURNING id`, name)}
	}
	e.lead, e.editor, e.viewLead, e.nobody = user("lead"), user("editor"), user("viewlead"), user("nobody")

	cipher, err := secret.New(cipherKey)
	require.NoError(t, err)
	cfg := e.target.Config().ConnConfig
	connection := func(name, host string, port int) int64 {
		return e.scan(`INSERT INTO database_connections (name, kind, host, port, database_name, username, password_ciphertext)
			VALUES ($1, 'postgres', $2, $3, $4, $5, $6) RETURNING id`,
			name, host, port, cfg.Database, cfg.User, cipher.Encrypt([]byte(cfg.Password)))
	}
	e.dbID, e.deadID = connection("live", cfg.Host, int(cfg.Port)), connection("dead", "127.0.0.1", 1)
	e.editors = e.group("editors", e.lead, e.editor)
	e.viewers = e.group("viewers", e.viewLead)
	e.grant(e.editors, e.dbID, "edit")
	e.grant(e.editors, e.deadID, "edit")

	registry := dbconn.NewRegistry(&dbconn.SQLStore{Q: gen.New(e.platform)}, cipher)
	t.Cleanup(registry.Close)
	targets := dbconn.Targets{Registry: registry}
	az, auditLog, jobs := authz.NewDBAuthorizer(e.platform), audit.New(e.platform), queue.NewPostgres(e.platform)
	e.svc = &migration.Service{Pool: e.platform, Targets: targets, Auth: audit.RecordDenied(az, auditLog), Audit: auditLog}
	e.worker = &queue.Worker{
		Jobs: jobs, Locks: jobs, Lifecycle: migration.Lifecycle{Pool: e.platform, Auth: az, Audit: auditLog}, Targets: targets,
		Logger: slog.New(slog.DiscardHandler), LockRetryDelay: time.Millisecond, RetryBaseDelay: time.Millisecond,
	}
	return e
}

func (e *env) scan(sql string, args ...any) int64 {
	e.t.Helper()
	var id int64
	require.NoError(e.t, e.platform.QueryRow(e.ctx, sql, args...).Scan(&id))
	return id
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	_, err := e.platform.Exec(e.ctx, sql, args...)
	require.NoError(e.t, err)
}

func (e *env) onTarget(sql string) {
	e.t.Helper()
	_, err := e.target.Exec(e.ctx, sql)
	require.NoError(e.t, err)
}

func (e *env) group(name string, members ...authz.Principal) int64 {
	e.t.Helper()
	id := e.scan(`INSERT INTO groups (name) VALUES ($1) RETURNING id`, name)
	for _, m := range members {
		e.exec(`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, id, m.UserID)
	}
	if len(members) > 0 {
		e.exec(`UPDATE groups SET leader_user_id = $2 WHERE id = $1`, id, members[0].UserID)
	}
	return id
}

func (e *env) grant(group, database int64, level string) {
	e.t.Helper()
	e.exec(`INSERT INTO group_database_grants (group_id, database_id, level) VALUES ($1, $2, $3)`, group, database, level)
}

func (e *env) draft(title, up, down string) apigen.Migration {
	e.t.Helper()
	d := apigen.MigrationDefinition{Title: title, UpSql: up, DownSql: down}
	if down == "" {
		d.Irreversible, d.IrreversibleReason = true, "data hilang"
	}
	m, err := e.svc.Create(e.ctx, e.editor, e.dbID, d)
	require.NoError(e.t, err)
	return m
}

func (e *env) table(name string) apigen.Migration {
	return e.draft(name, "CREATE TABLE app."+name+" (id int)", "DROP TABLE app."+name)
}

func (e *env) get(id int64) apigen.Migration {
	e.t.Helper()
	m, err := e.svc.Get(e.ctx, e.editor, id)
	require.NoError(e.t, err)
	return m
}

func (e *env) approve(id int64, kind apigen.MigrationKind) int64 {
	e.t.Helper()
	m, err := e.svc.Request(e.ctx, e.editor, id, kind)
	require.NoError(e.t, err)
	m, err = e.svc.Decide(e.ctx, e.lead, id, m.Request.Id, true, "")
	require.NoError(e.t, err)
	require.Equal(e.t, apigen.MigrationStatusApproved, m.Status)
	return m.Request.Id
}

func (e *env) execute(id, requestID int64) int64 {
	e.t.Helper()
	jobID, err := e.svc.Execute(e.ctx, e.editor, id, requestID)
	require.NoError(e.t, err)
	return jobID
}

func (e *env) work() {
	e.t.Helper()
	for range 20 {
		time.Sleep(5 * time.Millisecond)
		found, err := e.worker.Next(e.ctx)
		require.NoError(e.t, err)
		if !found {
			return
		}
	}
	e.t.Fatal("antrean tidak pernah kosong")
}

func (e *env) run(id int64, kind apigen.MigrationKind) apigen.Migration {
	e.t.Helper()
	e.execute(id, e.approve(id, kind))
	e.work()
	return e.get(id)
}

func (e *env) has(table string) bool {
	e.t.Helper()
	var exists bool
	require.NoError(e.t, e.target.QueryRow(e.ctx, `SELECT to_regclass('app.' || $1) IS NOT NULL`, table).Scan(&exists))
	return exists
}

func (e *env) history(hide bool) []apigen.MigrationExecution {
	e.t.Helper()
	items, err := e.svc.History(e.ctx, e.editor, e.dbID, nil, hide)
	require.NoError(e.t, err)
	return items
}

func (e *env) jobs() int64 { return e.scan(`SELECT count(*) FROM jobs`) }

func (e *env) jobStatus(id int64) string {
	e.t.Helper()
	var s string
	require.NoError(e.t, e.platform.QueryRow(e.ctx, `SELECT status::text FROM jobs WHERE id = $1`, id).Scan(&s))
	return s
}

func TestApplyWaitsForApprovalAndExecute(t *testing.T) {
	e := setup(t)
	m := e.table("a")
	assert.Equal(t, apigen.MigrationStatusDraft, m.Status)

	m, err := e.svc.Request(e.ctx, e.editor, m.Id, apigen.Apply)
	require.NoError(t, err)
	assert.Equal(t, apigen.MigrationStatusPendingReview, m.Status)
	_, err = e.svc.Execute(e.ctx, e.editor, m.Id, m.Request.Id)
	assert.ErrorIs(t, err, apperr.ErrConflict, "belum disetujui")
	_, err = e.svc.Request(e.ctx, e.editor, m.Id, apigen.Apply)
	assert.ErrorIs(t, err, apperr.ErrConflict, "satu request terbuka")

	m, err = e.svc.Decide(e.ctx, e.lead, m.Id, m.Request.Id, true, "ok")
	require.NoError(t, err)
	assert.Equal(t, apigen.MigrationStatusApproved, m.Status)
	assert.Zero(t, e.jobs(), "job baru dibuat saat Execute")

	jobID := e.execute(m.Id, m.Request.Id)
	assert.Equal(t, jobID, e.execute(m.Id, m.Request.Id), "Execute ganda memakai job yang sama")
	assert.Equal(t, int64(1), e.jobs())
	assert.Equal(t, apigen.MigrationStatusQueued, e.get(m.Id).Status)
	assert.False(t, e.has("a"), "API tidak menjalankan SQL")

	e.work()
	m = e.get(m.Id)
	assert.Equal(t, apigen.MigrationStatusSucceeded, m.Status)
	assert.Equal(t, apigen.MigrationPosition("applied"), m.Position)
	assert.Equal(t, 1, *m.Version)
	assert.True(t, e.has("a"))
	assert.Equal(t, "succeeded", e.jobStatus(jobID))
	h := e.history(false)
	require.Len(t, h, 1)
	assert.Equal(t, "CREATE TABLE app.a (id int)", h[0].ExecutedSql)
	assert.Equal(t, e.editor.UserID, h[0].ExecutedBy)
	assert.Equal(t, 1, h[0].Revision)
}

func TestConcurrentExecuteCreatesOneJob(t *testing.T) {
	e := setup(t)
	m := e.table("a")
	requestID := e.approve(m.Id, apigen.Apply)
	var wg sync.WaitGroup
	ids := make([]int64, 8)
	for i := range ids {
		wg.Go(func() { ids[i], _ = e.svc.Execute(e.ctx, e.editor, m.Id, requestID) })
	}
	wg.Wait()
	for _, id := range ids {
		assert.Equal(t, ids[0], id)
	}
	assert.Equal(t, int64(1), e.jobs())
	e.work()
	assert.Len(t, e.history(false), 1, "tidak ada commit ganda")
}

func TestEveryGroupLeaderMustApprove(t *testing.T) {
	e := setup(t)
	e.grant(e.viewers, e.dbID, "view")
	m := e.table("a")
	m, err := e.svc.Request(e.ctx, e.editor, m.Id, apigen.Apply)
	require.NoError(t, err)
	requestID := m.Request.Id

	_, err = e.svc.Decide(e.ctx, e.editor, m.Id, requestID, true, "")
	assert.ErrorIs(t, err, apperr.ErrForbidden, "anggota biasa bukan leader")
	_, err = e.svc.Decide(e.ctx, e.nobody, m.Id, requestID, true, "")
	assert.ErrorIs(t, err, apperr.ErrForbidden)

	m, err = e.svc.Decide(e.ctx, e.lead, m.Id, requestID, true, "")
	require.NoError(t, err)
	assert.Equal(t, apigen.MigrationStatusPendingReview, m.Status, "leader group view juga wajib")
	_, err = e.svc.Decide(e.ctx, e.lead, m.Id, requestID, true, "")
	assert.ErrorIs(t, err, apperr.ErrConflict, "satu keputusan per group")

	list, err := e.svc.Approvals(e.ctx, e.viewLead, m.Id)
	require.NoError(t, err)
	assert.True(t, list.CanDecide)
	assert.Len(t, list.Groups, 2)

	m, err = e.svc.Decide(e.ctx, e.viewLead, m.Id, requestID, true, "")
	require.NoError(t, err)
	assert.Equal(t, apigen.MigrationStatusApproved, m.Status)
}

func TestLeaderOfSeveralGroupsDecidesOnce(t *testing.T) {
	e := setup(t)
	e.grant(e.group("lain", e.lead), e.dbID, "view")
	assert.NotZero(t, e.approve(e.table("a").Id, apigen.Apply))
}

func TestGroupWithoutLeaderHoldsTheRequest(t *testing.T) {
	e := setup(t)
	orphan := e.group("tanpa-leader")
	e.grant(orphan, e.dbID, "view")
	m := e.table("a")
	m, err := e.svc.Request(e.ctx, e.editor, m.Id, apigen.Apply)
	require.NoError(t, err)
	m, err = e.svc.Decide(e.ctx, e.lead, m.Id, m.Request.Id, true, "")
	require.NoError(t, err)
	assert.Equal(t, apigen.MigrationStatusPendingReview, m.Status)
	_, err = e.svc.Execute(e.ctx, e.editor, m.Id, m.Request.Id)
	assert.ErrorIs(t, err, apperr.ErrConflict)

	e.exec(`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, orphan, e.nobody.UserID)
	e.exec(`UPDATE groups SET leader_user_id = $2 WHERE id = $1`, orphan, e.nobody.UserID)
	m, err = e.svc.Decide(e.ctx, e.nobody, m.Id, m.Request.Id, true, "")
	require.NoError(t, err)
	assert.Equal(t, apigen.MigrationStatusApproved, m.Status)
}

func TestRejectionReopensTheDraft(t *testing.T) {
	e := setup(t)
	m := e.table("a")
	m, err := e.svc.Request(e.ctx, e.editor, m.Id, apigen.Apply)
	require.NoError(t, err)
	def := apigen.MigrationDefinition{Title: "a2", UpSql: "CREATE TABLE app.a2 (id int)", DownSql: "DROP TABLE app.a2"}
	_, err = e.svc.Update(e.ctx, e.editor, m.Id, def, 1)
	assert.ErrorIs(t, err, apperr.ErrConflict, "terkunci selama review")

	rejected, err := e.svc.Decide(e.ctx, e.lead, m.Id, m.Request.Id, false, "kurang index")
	require.NoError(t, err)
	assert.Equal(t, apigen.MigrationStatusRejected, rejected.Status)
	_, err = e.svc.Execute(e.ctx, e.editor, m.Id, m.Request.Id)
	assert.ErrorIs(t, err, apperr.ErrConflict)

	updated, err := e.svc.Update(e.ctx, e.editor, m.Id, def, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, updated.Revision)
	assert.Equal(t, apigen.MigrationStatusDraft, updated.Status)
	_, err = e.svc.Update(e.ctx, e.editor, m.Id, def, 1)
	assert.ErrorIs(t, err, apperr.ErrConflict, "revisi basi")
	assert.Equal(t, apigen.MigrationStatusSucceeded, e.run(m.Id, apigen.Apply).Status)
	assert.True(t, e.has("a2"))
}

func TestRollbackForwardAndHideRollback(t *testing.T) {
	e := setup(t)
	a, b := e.table("a"), e.table("b")
	e.run(a.Id, apigen.Apply)
	e.run(b.Id, apigen.Apply)

	_, err := e.svc.Request(e.ctx, e.editor, a.Id, apigen.Rollback)
	assert.ErrorIs(t, err, apperr.ErrConflict, "hanya migration aktif terakhir")
	_, err = e.svc.Request(e.ctx, e.editor, b.Id, apigen.Forward)
	assert.ErrorIs(t, err, apperr.ErrConflict, "forward hanya setelah rollback")

	m := e.run(b.Id, apigen.Rollback)
	assert.Equal(t, apigen.MigrationPosition("rolled_back"), m.Position)
	assert.Nil(t, m.Version)
	assert.False(t, e.has("b"))
	m = e.run(b.Id, apigen.Forward)
	assert.Equal(t, 2, *m.Version)
	assert.True(t, e.has("b"))
	e.run(e.table("c").Id, apigen.Apply)

	full := e.history(false)
	require.Len(t, full, 5)
	rollback := full[2]
	assert.Equal(t, full[3].Id, *rollback.RevertsExecutionId)
	assert.Equal(t, "DROP TABLE app.b", rollback.ExecutedSql)
	var visible []int64
	for _, x := range e.history(true) {
		visible = append(visible, x.Id)
	}
	assert.Equal(t, []int64{full[0].Id, full[1].Id, full[4].Id}, visible, "C, B forward, A")
	assert.Len(t, e.history(false), 5, "filter tidak menghapus riwayat")
}

func TestForwardFollowsLinearOrder(t *testing.T) {
	e := setup(t)
	a, b, c := e.table("a"), e.table("b"), e.table("c")
	for _, m := range []apigen.Migration{a, b, c} {
		e.run(m.Id, apigen.Apply)
	}
	e.run(c.Id, apigen.Rollback)
	e.run(b.Id, apigen.Rollback)

	_, err := e.svc.Request(e.ctx, e.editor, c.Id, apigen.Forward)
	assert.ErrorIs(t, err, apperr.ErrConflict, "B harus lebih dulu")
	e.run(b.Id, apigen.Forward)
	assert.False(t, e.has("c"), "forward tidak menjalankan migration berikutnya")

	e.run(e.table("d").Id, apigen.Apply)
	_, err = e.svc.Request(e.ctx, e.editor, c.Id, apigen.Forward)
	assert.ErrorIs(t, err, apperr.ErrConflict, "sudah ada migration baru")
}

func TestIrreversibleCannotRollBack(t *testing.T) {
	e := setup(t)
	m := e.draft("drop", "DROP TABLE app.attachments", "")
	assert.Equal(t, apigen.MigrationPosition("applied"), e.run(m.Id, apigen.Apply).Position)
	_, err := e.svc.Request(e.ctx, e.editor, m.Id, apigen.Rollback)
	assert.ErrorIs(t, err, apperr.ErrConflict)
	assert.NotContains(t, e.get(m.Id).Actions, apigen.MigrationActionRollback)
}

func TestFailedApplyIsRecordedLockedAndRetriable(t *testing.T) {
	e := setup(t)
	m := e.draft("gagal", "CREATE TABLE app.x (id int); INSERT INTO app.perlu VALUES (1)", "DROP TABLE app.x")
	m = e.run(m.Id, apigen.Apply)
	assert.Equal(t, apigen.MigrationStatusFailed, m.Status)
	assert.True(t, m.SqlLocked)
	assert.False(t, e.has("x"), "seluruh transaction dibatalkan")
	_, err := e.svc.Update(e.ctx, e.editor, m.Id, apigen.MigrationDefinition{Title: "x", UpSql: "SELECT 1", DownSql: "SELECT 1"}, 1)
	assert.ErrorIs(t, err, apperr.ErrConflict)
	assert.ErrorIs(t, e.svc.Delete(e.ctx, e.editor, m.Id), apperr.ErrConflict)
	h := e.history(true)
	require.Len(t, h, 1, "attempt gagal tetap terlihat")
	assert.Equal(t, "42P01", *h[0].ErrorCode)

	other := e.run(e.table("lain").Id, apigen.Apply)
	assert.Equal(t, 1, *other.Version, "migration lain boleh dilanjutkan")

	e.onTarget(`CREATE TABLE app.perlu (id int)`)
	require.NotNil(t, e.get(m.Id).ExecutableRequestId)
	e.execute(m.Id, *e.get(m.Id).ExecutableRequestId)
	e.work()
	m = e.get(m.Id)
	assert.Equal(t, apigen.MigrationStatusSucceeded, m.Status, "retry tanpa approval ulang")
	assert.Equal(t, 2, *m.Version)
	assert.Len(t, e.history(false), 3)
}

func TestFailedRollbackKeepsPosition(t *testing.T) {
	e := setup(t)
	m := e.draft("a", "CREATE TABLE app.a (id int)", "DROP TABLE app.tidak_ada")
	e.run(m.Id, apigen.Apply)
	m = e.run(m.Id, apigen.Rollback)
	assert.Equal(t, apigen.MigrationPosition("applied"), m.Position)
	assert.Equal(t, 1, *m.Version)
	assert.Equal(t, apigen.MigrationStatusFailed, m.Status)
	assert.Len(t, e.history(true), 2)
}

func TestUnreachableTargetIsRetriedThenFails(t *testing.T) {
	e := setup(t)
	m, err := e.svc.Create(e.ctx, e.editor, e.deadID, apigen.MigrationDefinition{Title: "x", UpSql: "SELECT 1", DownSql: "SELECT 1"})
	require.NoError(t, err)
	m = e.run(m.Id, apigen.Apply)
	assert.Equal(t, apigen.MigrationStatusFailed, m.Status)
	h, err := e.svc.History(e.ctx, e.editor, e.deadID, &m.Id, false)
	require.NoError(t, err)
	assert.Len(t, h, 3, "setiap attempt tercatat")
}

func TestRevalidationRejectsChangedAccess(t *testing.T) {
	e := setup(t)
	m := e.table("a")
	requestID := e.approve(m.Id, apigen.Apply)
	jobID := e.execute(m.Id, requestID)
	e.grant(e.viewers, e.dbID, "view")
	e.work()
	assert.Equal(t, "failed", e.jobStatus(jobID))
	assert.False(t, e.has("a"))
	assert.Empty(t, e.history(false), "SQL tidak pernah dijalankan")

	_, err := e.svc.Execute(e.ctx, e.editor, m.Id, requestID)
	assert.ErrorIs(t, err, apperr.ErrConflict)
	_, err = e.svc.Decide(e.ctx, e.viewLead, m.Id, requestID, true, "")
	require.NoError(t, err, "group baru menyetujui request yang sama")
	assert.Equal(t, jobID, e.execute(m.Id, requestID))
	e.work()
	assert.Equal(t, apigen.MigrationStatusSucceeded, e.get(m.Id).Status)
}

func TestRevalidationChecksActorAndOrder(t *testing.T) {
	e := setup(t)
	a, b := e.table("a"), e.table("b")
	e.run(a.Id, apigen.Apply)
	rollback := e.approve(a.Id, apigen.Rollback)
	e.run(b.Id, apigen.Apply)
	_, err := e.svc.Execute(e.ctx, e.editor, a.Id, rollback)
	assert.ErrorIs(t, err, apperr.ErrConflict, "a bukan lagi aktif terakhir")

	x := e.table("x")
	apply, undo := e.approve(x.Id, apigen.Apply), e.approve(b.Id, apigen.Rollback)
	e.execute(x.Id, apply)
	stale := e.execute(b.Id, undo)
	e.work()
	assert.Equal(t, "failed", e.jobStatus(stale), "x diterapkan lebih dulu, jadi b bukan lagi aktif terakhir")
	assert.True(t, e.has("b"))

	c := e.table("c")
	jobID := e.execute(c.Id, e.approve(c.Id, apigen.Apply))
	e.exec(`DELETE FROM group_members WHERE user_id = $1`, e.editor.UserID)
	e.work()
	assert.Equal(t, "failed", e.jobStatus(jobID), "akses dicabut sebelum worker berjalan")
	assert.False(t, e.has("c"))
}

func TestAccessIsEnforced(t *testing.T) {
	e := setup(t)
	e.grant(e.viewers, e.dbID, "view")
	m := e.table("a")
	viewed, err := e.svc.Get(e.ctx, e.viewLead, m.Id)
	require.NoError(t, err)
	assert.Empty(t, viewed.Actions)

	for name, call := range map[string]func(p authz.Principal) error{
		"create": func(p authz.Principal) error {
			_, err := e.svc.Create(e.ctx, p, e.dbID, apigen.MigrationDefinition{})
			return err
		},
		"update": func(p authz.Principal) error {
			_, err := e.svc.Update(e.ctx, p, m.Id, apigen.MigrationDefinition{}, 1)
			return err
		},
		"delete":  func(p authz.Principal) error { return e.svc.Delete(e.ctx, p, m.Id) },
		"request": func(p authz.Principal) error { _, err := e.svc.Request(e.ctx, p, m.Id, apigen.Apply); return err },
		"execute": func(p authz.Principal) error { _, err := e.svc.Execute(e.ctx, p, m.Id, 1); return err },
		"explain": func(p authz.Principal) error { _, err := e.svc.Explain(e.ctx, p, m.Id, false); return err },
	} {
		assert.ErrorIs(t, call(e.viewLead), apperr.ErrForbidden, "%s oleh View Only", name)
		assert.ErrorIs(t, call(e.nobody), apperr.ErrForbidden, "%s tanpa akses", name)
	}
	for name, call := range map[string]func() error{
		"get":       func() error { _, err := e.svc.Get(e.ctx, e.nobody, m.Id); return err },
		"list":      func() error { _, err := e.svc.List(e.ctx, e.nobody, e.dbID); return err },
		"history":   func() error { _, err := e.svc.History(e.ctx, e.nobody, e.dbID, nil, false); return err },
		"approvals": func() error { _, err := e.svc.Approvals(e.ctx, e.nobody, m.Id); return err },
		"decide":    func() error { _, err := e.svc.Decide(e.ctx, e.nobody, m.Id, 1, true, ""); return err },
	} {
		assert.ErrorIs(t, call(), apperr.ErrForbidden, name)
	}
	_, err = e.svc.Get(e.ctx, e.editor, 9999)
	assert.ErrorIs(t, err, apperr.ErrNotFound)
	assert.NoError(t, e.svc.Delete(e.ctx, e.editor, m.Id))
	_, err = e.svc.Get(e.ctx, e.editor, m.Id)
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

func (e *env) batch(up, down string) apigen.Migration {
	e.t.Helper()
	e.onTarget(`CREATE TABLE app.big (id int PRIMARY KEY, v int NOT NULL DEFAULT 0 CHECK (v < 100));
		INSERT INTO app.big SELECT g FROM generate_series(1, 100) g`)
	m, err := e.svc.Create(e.ctx, e.editor, e.dbID, apigen.MigrationDefinition{
		Title: "batch", UpSql: up, DownSql: down,
		Batch: &apigen.MigrationBatch{Schema: "app", Table: "big", Column: "id", Size: 10},
	})
	require.NoError(e.t, err)
	return m
}

func (e *env) sum() int64 {
	e.t.Helper()
	var n int64
	require.NoError(e.t, e.target.QueryRow(e.ctx, `SELECT sum(v) FROM app.big`).Scan(&n))
	return n
}

const (
	batchUp   = "UPDATE app.big SET v = v + 1 WHERE id > $1 AND id <= $2"
	batchDown = "UPDATE app.big SET v = v - 1 WHERE id > $1 AND id <= $2"
)

func TestBatchApplyRollbackForward(t *testing.T) {
	e := setup(t)
	m := e.batch(batchUp, batchDown)
	assert.Equal(t, apigen.MigrationStatusSucceeded, e.run(m.Id, apigen.Apply).Status)
	assert.Equal(t, int64(100), e.sum())
	m = e.run(m.Id, apigen.Rollback)
	assert.Equal(t, apigen.MigrationPosition("rolled_back"), m.Position)
	assert.Equal(t, int64(0), e.sum())
	e.run(m.Id, apigen.Forward)
	assert.Equal(t, int64(100), e.sum(), "forward memakai checkpoint baru")
	e.run(m.Id, apigen.Rollback)
	assert.Equal(t, int64(0), e.sum(), "rollback kedua membatalkan forward")
}

func TestPartialBatchHoldsThenResumes(t *testing.T) {
	e := setup(t)
	m := e.batch(batchUp, batchDown)
	e.onTarget(`UPDATE app.big SET v = 99 WHERE id = 55`)
	m = e.run(m.Id, apigen.Apply)
	assert.Equal(t, apigen.MigrationStatusPartial, m.Status)
	assert.Equal(t, "partial", e.jobStatus(*m.JobId))
	assert.Equal(t, int64(50+99), e.sum(), "lima batch tetap committed")

	other := e.table("lain")
	_, err := e.svc.Request(e.ctx, e.editor, other.Id, apigen.Apply)
	assert.ErrorIs(t, err, apperr.ErrConflict, "migration lain tertahan")
	assert.Subset(t, m.Actions, []apigen.MigrationAction{"execute", "rollback"})

	e.onTarget(`UPDATE app.big SET v = 0 WHERE id = 55`)
	e.execute(m.Id, *m.ExecutableRequestId)
	e.work()
	assert.Equal(t, apigen.MigrationStatusSucceeded, e.get(m.Id).Status)
	assert.Equal(t, int64(100), e.sum(), "batch yang sudah commit tidak diulang")
	assert.Equal(t, apigen.MigrationStatusSucceeded, e.run(other.Id, apigen.Apply).Status)

	e.run(other.Id, apigen.Rollback)
	e.run(m.Id, apigen.Rollback)
	assert.Equal(t, int64(0), e.sum(), "rollback mencakup batch dari kedua attempt")
}

func TestPartialBatchCanBeCancelled(t *testing.T) {
	e := setup(t)
	m := e.batch(batchUp, batchDown)
	e.onTarget(`UPDATE app.big SET v = 99 WHERE id = 55`)
	e.run(m.Id, apigen.Apply)

	m = e.run(m.Id, apigen.Rollback)
	assert.Equal(t, apigen.MigrationStatusSucceeded, m.Status)
	assert.Equal(t, apigen.MigrationPosition("rolled_back"), m.Position)
	assert.Equal(t, int64(99), e.sum())
	assert.Equal(t, apigen.MigrationStatusSucceeded, e.run(e.table("lain").Id, apigen.Apply).Status, "database tidak lagi tertahan")
}

func TestPartiallyRevertedBatchResumes(t *testing.T) {
	e := setup(t)
	e.onTarget(`CREATE TABLE app.blocked (id int)`)
	m := e.batch(batchUp, "UPDATE app.big SET v = v - 1 - (SELECT count(*)::int FROM app.blocked b WHERE b.id = big.id) * 1000 WHERE id > $1 AND id <= $2")
	e.onTarget(`ALTER TABLE app.big ADD CHECK (v > -10)`)
	e.run(m.Id, apigen.Apply)
	e.onTarget(`INSERT INTO app.blocked VALUES (45)`)
	m = e.run(m.Id, apigen.Rollback)
	assert.Equal(t, apigen.MigrationStatusPartial, m.Status)
	assert.Equal(t, int64(50), e.sum(), "lima batch terakhir sudah dibatalkan")
	assert.Equal(t, apigen.MigrationPosition("applied"), m.Position)

	e.onTarget(`TRUNCATE app.blocked`)
	e.execute(m.Id, *m.ExecutableRequestId)
	e.work()
	m = e.get(m.Id)
	assert.Equal(t, apigen.MigrationPosition("rolled_back"), m.Position)
	assert.Equal(t, int64(0), e.sum())
}

func TestInterruptedAttemptCanBeClosed(t *testing.T) {
	e := setup(t)
	m := e.table("a")
	jobID := e.execute(m.Id, e.approve(m.Id, apigen.Apply))
	requestID := e.get(m.Id).Request.Id
	lc := migration.Lifecycle{Pool: e.platform, Auth: authz.NewDBAuthorizer(e.platform)}

	none, err := lc.Interrupted(e.ctx, jobID, 1)
	require.NoError(t, err)
	assert.Nil(t, none)
	started, err := lc.Start(e.ctx, queue.StartInput{RequestID: requestID, JobID: jobID, ActorID: e.editor.UserID, Attempt: 1})
	require.NoError(t, err)
	again, err := lc.Interrupted(e.ctx, jobID, 1)
	require.NoError(t, err)
	assert.Equal(t, started, again)

	require.NoError(t, lc.Finish(e.ctx, queue.FinishInput{RunID: started.RunID, Err: errors.New("putus"), WillRetry: true}))
	assert.Equal(t, apigen.MigrationStatusQueued, e.get(m.Id).Status)
	assert.Error(t, lc.Finish(e.ctx, queue.FinishInput{RunID: started.RunID}), "attempt hanya ditutup sekali")
}

func TestHistoryIsImmutable(t *testing.T) {
	e := setup(t)
	m := e.table("a")
	e.run(m.Id, apigen.Apply)
	for _, set := range []string{"sql_text = 'x'", "status = 'failed'", "error_message = 'x'", "reverts_run_id = NULL"} {
		_, err := e.platform.Exec(e.ctx, `UPDATE migration_runs SET `+set)
		assert.ErrorContains(t, err, "final", set)
	}
	e.exec(`DELETE FROM database_connections WHERE id = $1`, e.dbID)
	assert.Zero(t, e.scan(`SELECT count(*) FROM migration_runs`), "menghapus koneksi tetap bisa")
}

func TestExecutionLogCarriesFullSQL(t *testing.T) {
	e := setup(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	e.run(e.draft("a", "CREATE TABLE app.a (id int); CREATE ROLE app_log_test PASSWORD 'LEAK-role-pw'; DROP ROLE app_log_test", "DROP TABLE app.a").Id, apigen.Apply)
	assert.NotContains(t, buf.String(), "LEAK-role-pw")
	seen := 0
	for line := range strings.Lines(buf.String()) {
		if strings.Contains(line, `"msg":"migration.execution.start"`) || strings.Contains(line, `"msg":"migration.execution.end"`) {
			seen++
			assert.Contains(t, line, `"query":"CREATE TABLE app.a (id int); CREATE ROLE app_log_test PASSWORD`)
		}
	}
	assert.Equal(t, 2, seen)
}

func TestExplain(t *testing.T) {
	e := setup(t)
	m := e.draft("q", "SELECT * FROM app.books WHERE id = 1", "SELECT 1")
	plan, err := e.svc.Explain(e.ctx, e.editor, m.Id, false)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	assert.True(t, plan[0].Explainable)
	assert.NotNil(t, plan[0].Plan)
}

func TestReconcilerResolvesMilestoneOneRuns(t *testing.T) {
	e := setup(t)
	orphan := func(committed bool) (int64, int64) {
		m := e.table(fmt.Sprintf("o%v", committed))
		run := e.scan(`INSERT INTO migration_runs (migration_id, database_id, sql_text, checksum, executed_by, status)
			VALUES ($1, $2, 'SELECT 1', '\x00', $3, 'running') RETURNING id`, m.Id, e.dbID, e.editor.UserID)
		if committed {
			mg, err := e.worker.Targets.Migrator(e.ctx, e.dbID)
			require.NoError(t, err)
			_, err = mg.Apply(e.ctx, dbms.ApplyInput{SQL: "SELECT 1", DatabaseID: e.dbID, MigrationID: m.Id, RunID: run, Version: 1, Checksum: []byte{0}})
			require.NoError(t, err)
		}
		return m.Id, run
	}
	done, _ := orphan(true)
	lost, _ := orphan(false)
	require.NoError(t, migration.Reconciler{Pool: e.platform, Targets: e.svc.Targets}.Reconcile(e.ctx))
	assert.Equal(t, apigen.MigrationPosition("applied"), e.get(done).Position)
	assert.Equal(t, apigen.MigrationPosition("draft"), e.get(lost).Position)
	assert.Zero(t, e.scan(`SELECT count(*) FROM migration_runs WHERE status = 'running'`))
}
