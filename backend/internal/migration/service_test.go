package migration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/migration"
)

// Test unit memakai Store in-memory; tidak butuh Docker. Test integrasi untuk
// pgStore ditulis terpisah (testcontainers) setelah 00003 masuk.

const (
	editorID = int64(10)
	viewerID = int64(11)
	dbID     = int64(1)
)

var errForbidden = errors.New("forbidden")

type fakeStore struct {
	migs      map[int64]*migration.Migration
	approvals []migration.Approval
	execs     []migration.Execution
	approvers []migration.Approver
	nextMig   int64
	nextExec  int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		migs: map[int64]*migration.Migration{},
		approvers: []migration.Approver{
			{GroupID: 1, LeaderID: 1},
			{GroupID: 2, LeaderID: 2},
			{GroupID: 3, LeaderID: 2}, // leader yang sama memimpin dua group: satu suara
			{GroupID: 4, LeaderID: 0}, // tanpa leader aktif: diabaikan
		},
	}
}

func (f *fakeStore) DatabaseKind(context.Context, int64) (string, error) { return "postgres", nil }

func (f *fakeStore) CreateMigration(_ context.Context, m migration.Migration) (migration.Migration, error) {
	f.nextMig++
	m.ID = f.nextMig
	m.Seq = 0
	for _, o := range f.migs {
		if o.DatabaseID == m.DatabaseID {
			m.Seq++
		}
	}
	m.Seq++
	c := m
	f.migs[m.ID] = &c
	return m, nil
}

func (f *fakeStore) GetMigrationByID(_ context.Context, id int64) (*migration.Migration, error) {
	m, ok := f.migs[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	c := *m
	return &c, nil
}

func (f *fakeStore) ListMigrations(_ context.Context, databaseID int64) ([]migration.Migration, error) {
	var out []migration.Migration
	for _, m := range f.migs {
		if m.DatabaseID == databaseID {
			out = append(out, *m)
		}
	}
	for i := range out { // urut Seq
		for j := i + 1; j < len(out); j++ {
			if out[j].Seq < out[i].Seq {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateMigration(_ context.Context, m migration.Migration) (migration.Migration, error) {
	cur := f.migs[m.ID]
	if cur == nil || cur.Revision != m.Revision-1 || cur.Status != migration.StatusDraft || cur.SQLLocked {
		return migration.Migration{}, apperr.New(apperr.CodeConflict, "konflik")
	}
	c := m
	c.SQLLocked = cur.SQLLocked
	f.migs[m.ID] = &c
	return c, nil
}

func (f *fakeStore) SetStatus(_ context.Context, id int64, from, to migration.Status) error {
	m := f.migs[id]
	if m == nil || m.Status != from {
		return migration.ErrStatusConflict
	}
	m.Status = to
	return nil
}

func (f *fakeStore) LockSQL(_ context.Context, id int64) error {
	f.migs[id].SQLLocked = true
	return nil
}

func (f *fakeStore) ListApprovers(context.Context, int64) ([]migration.Approver, error) {
	return f.approvers, nil
}

func (f *fakeStore) UpsertApprovals(_ context.Context, rows []migration.Approval) error {
	for _, r := range rows {
		replaced := false
		for i, a := range f.approvals {
			if a.MigrationID == r.MigrationID && a.Revision == r.Revision && a.Kind == r.Kind && a.LeaderID == r.LeaderID {
				f.approvals[i], replaced = r, true
			}
		}
		if !replaced {
			f.approvals = append(f.approvals, r)
		}
	}
	return nil
}

func (f *fakeStore) ListApprovals(_ context.Context, id int64, rev int, kind migration.Kind) ([]migration.Approval, error) {
	var out []migration.Approval
	for _, a := range f.approvals {
		if a.MigrationID == id && a.Revision == rev && a.Kind == kind {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) DecideApproval(_ context.Context, id int64, rev int, kind migration.Kind, leader int64,
	d migration.ApprovalDecision, comment string, at time.Time) (*migration.Approval, error) {
	for i, a := range f.approvals {
		if a.MigrationID == id && a.Revision == rev && a.Kind == kind && a.LeaderID == leader && a.Decision == migration.ApprovalPending {
			f.approvals[i].Decision, f.approvals[i].Comment, f.approvals[i].DecidedAt = d, comment, &at
			c := f.approvals[i]
			return &c, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) InsertExecution(_ context.Context, e migration.Execution) (migration.Execution, error) {
	f.nextExec++
	e.ID = f.nextExec
	f.execs = append(f.execs, e)
	return e, nil
}

func (f *fakeStore) GetExecution(_ context.Context, id int64) (*migration.Execution, error) {
	for _, e := range f.execs {
		if e.ID == id {
			c := e
			return &c, nil
		}
	}
	return nil, apperr.ErrNotFound
}

func (f *fakeStore) FinalizeExecution(_ context.Context, id int64, res migration.ExecutionResult, at time.Time) error {
	for i, e := range f.execs {
		if e.ID == id && e.Status == migration.StatusRunning {
			f.execs[i].Status = migration.StatusFailed
			if res.Success {
				f.execs[i].Status = migration.StatusSuccess
			}
			f.execs[i].ErrorMsg, f.execs[i].SQLState, f.execs[i].FinishedAt = res.ErrorMsg, res.SQLState, at
			return nil
		}
	}
	return errors.New("bukan attempt running")
}

func (f *fakeStore) ListExecutions(_ context.Context, databaseID int64, flt migration.HistoryFilter) ([]migration.Execution, error) {
	var out []migration.Execution
	for _, e := range f.execs {
		if f.migs[e.MigrationID].DatabaseID != databaseID {
			continue
		}
		if flt.MigrationID != nil && *flt.MigrationID != e.MigrationID {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

type fakePub struct{ reqs []migration.ExecutionRequest }

func (p *fakePub) Publish(_ context.Context, r migration.ExecutionRequest) error {
	p.reqs = append(p.reqs, r)
	return nil
}

type env struct {
	svc   *migration.Service
	store *fakeStore
	pub   *fakePub
	lc    migration.ExecutionLifecycle
}

func newEnv() *env {
	st, pub := newFakeStore(), &fakePub{}
	auth := migration.AuthorizerFuncs{
		View: func(context.Context, migration.Actor, int64) error { return nil },
		Edit: func(_ context.Context, a migration.Actor, _ int64) error {
			if a.UserID == editorID {
				return nil
			}
			return errForbidden
		},
	}
	return &env{svc: migration.NewService(st, pub, auth), store: st, pub: pub, lc: migration.NewLifecycle(st)}
}

var (
	editor = migration.Actor{UserID: editorID}
	viewer = migration.Actor{UserID: viewerID}
)

func def(title string) migration.Definition {
	return migration.Definition{Title: title, UpSQL: "CREATE TABLE app.t1 (id int)", DownSQL: "DROP TABLE app.t1"}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (e *env) decide(t *testing.T, id int64, kind migration.Kind, leaders ...int64) {
	t.Helper()
	for _, l := range leaders {
		must(t, e.svc.Decide(context.Background(), migration.Actor{UserID: l}, id, kind, migration.ApprovalApproved, "ok"))
	}
}

func TestApplyRollbackFlow(t *testing.T) {
	ctx := context.Background()
	e := newEnv()

	if _, err := e.svc.Create(ctx, viewer, dbID, def("x")); !errors.Is(err, errForbidden) {
		t.Fatalf("view-only harus ditolak, dapat %v", err)
	}
	m, err := e.svc.Create(ctx, editor, dbID, def("Buat t1"))
	must(t, err)
	must(t, e.svc.SubmitForReview(ctx, editor, m.ID))

	err = e.svc.Decide(ctx, migration.Actor{UserID: 99}, m.ID, migration.KindApply, migration.ApprovalApproved, "")
	if !errors.Is(err, migration.ErrNotApprover) {
		t.Fatalf("bukan leader harus ditolak, dapat %v", err)
	}
	e.decide(t, m.ID, migration.KindApply, 1)
	if len(e.pub.reqs) != 0 {
		t.Fatal("job tidak boleh dibuat sebelum semua leader setuju")
	}
	err = e.svc.Decide(ctx, migration.Actor{UserID: 1}, m.ID, migration.KindApply, migration.ApprovalApproved, "")
	if !errors.Is(err, migration.ErrAlreadyDecided) {
		t.Fatalf("vote ganda harus ditolak, dapat %v", err)
	}
	e.decide(t, m.ID, migration.KindApply, 2)
	if len(e.pub.reqs) != 1 || e.pub.reqs[0].Kind != migration.KindApply {
		t.Fatalf("harus tepat satu job apply, dapat %+v", e.pub.reqs)
	}
	if got, _ := e.svc.Get(ctx, viewer, m.ID); got.Status != migration.StatusPendingQueue {
		t.Fatalf("status = %s", got.Status)
	}

	req := e.pub.reqs[0]
	must(t, e.lc.Revalidate(ctx, req))
	attempt, err := e.lc.MarkRunning(ctx, req)
	must(t, err)
	must(t, e.lc.RecordResult(ctx, attempt, migration.ExecutionResult{Success: true}))
	if err := e.lc.RecordResult(ctx, attempt, migration.ExecutionResult{Success: true}); err == nil {
		t.Fatal("riwayat tidak boleh diubah setelah selesai")
	}
	got, _ := e.svc.Get(ctx, viewer, m.ID)
	if got.Status != migration.StatusSuccess || !got.SQLLocked {
		t.Fatalf("setelah apply: status=%s locked=%v", got.Status, got.SQLLocked)
	}
	if _, err := e.svc.UpdateDraft(ctx, editor, m.ID, def("ubah")); err == nil {
		t.Fatal("SQL migration yang pernah dieksekusi tidak boleh diubah")
	}

	// Rollback: approval baru, job baru, merujuk execution apply.
	must(t, e.svc.RequestRollback(ctx, editor, m.ID))
	e.decide(t, m.ID, migration.KindRollback, 1, 2)
	if len(e.pub.reqs) != 2 || e.pub.reqs[1].Kind != migration.KindRollback {
		t.Fatalf("harus ada job rollback, dapat %+v", e.pub.reqs)
	}
	rb := e.pub.reqs[1]
	must(t, e.lc.Revalidate(ctx, rb))
	rbAttempt, err := e.lc.MarkRunning(ctx, rb)
	must(t, err)
	must(t, e.lc.RecordResult(ctx, rbAttempt, migration.ExecutionResult{Success: true}))

	all, err := e.svc.ListHistory(ctx, viewer, dbID, migration.HistoryFilter{})
	must(t, err)
	if len(all) != 2 || all[1].RevertsExecutionID == nil || *all[1].RevertsExecutionID != attempt {
		t.Fatalf("rollback harus merujuk execution %d: %+v", attempt, all)
	}
	hidden, err := e.svc.ListHistory(ctx, viewer, dbID, migration.HistoryFilter{HideRollback: true})
	must(t, err)
	if len(hidden) != 0 {
		t.Fatalf("pasangan apply+rollback harus tersembunyi: %+v", hidden)
	}
}

func TestRejectReturnsToDraft(t *testing.T) {
	ctx := context.Background()
	e := newEnv()
	m, err := e.svc.Create(ctx, editor, dbID, def("Ditolak"))
	must(t, err)
	must(t, e.svc.SubmitForReview(ctx, editor, m.ID))
	must(t, e.svc.Decide(ctx, migration.Actor{UserID: 1}, m.ID, migration.KindApply, migration.ApprovalRejected, "no"))

	got, _ := e.svc.Get(ctx, viewer, m.ID)
	if got.Status != migration.StatusDraft {
		t.Fatalf("status = %s", got.Status)
	}
	if len(e.pub.reqs) != 0 {
		t.Fatal("tidak boleh ada job setelah penolakan")
	}
	// Diajukan ulang: putaran approval diatur ulang.
	must(t, e.svc.SubmitForReview(ctx, editor, m.ID))
	e.decide(t, m.ID, migration.KindApply, 1, 2)
	if len(e.pub.reqs) != 1 {
		t.Fatalf("pengajuan ulang harus bisa disetujui, job=%d", len(e.pub.reqs))
	}
}

func TestHideRollbackPairs(t *testing.T) {
	r := func(id, reverts int64) migration.Execution {
		e := migration.Execution{ID: id, Kind: migration.KindRollback, Status: migration.StatusSuccess}
		e.RevertsExecutionID = &reverts
		return e
	}
	apply := func(id int64, st migration.Status) migration.Execution {
		return migration.Execution{ID: id, Kind: migration.KindApply, Status: st}
	}
	items := []migration.Execution{
		apply(1, migration.StatusSuccess),
		apply(2, migration.StatusSuccess),
		r(3, 2),
		apply(4, migration.StatusFailed),
		apply(5, migration.StatusSuccess),
	}
	var ids []int64
	for _, e := range migration.HideRollbackPairs(items) {
		ids = append(ids, e.ID)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 4 || ids[2] != 5 {
		t.Fatalf("harus E1, E4, E5; dapat %v", ids)
	}
}

func TestValidateDefinition(t *testing.T) {
	batchUp := "UPDATE t SET a = 1 WHERE id > $1 AND id <= $2"
	batchDown := "UPDATE t SET a = NULL WHERE id > $1 AND id <= $2"
	cases := []struct {
		name string
		d    migration.Definition
		ok   bool
	}{
		{"biasa", def("x"), true},
		{"tanpa up", migration.Definition{DownSQL: "x"}, false},
		{"reversible tanpa down", migration.Definition{UpSQL: "x"}, false},
		{"irreversible tanpa alasan", migration.Definition{UpSQL: "x", Irreversible: true}, false},
		{"irreversible dengan alasan", migration.Definition{UpSQL: "x", Irreversible: true, IrreversibleReason: "drop data"}, true},
		{"batch benar", migration.Definition{UpSQL: batchUp, DownSQL: batchDown, Batch: &migration.BatchConfig{Column: "id", Size: 100}}, true},
		{"batch tanpa $2", migration.Definition{UpSQL: "UPDATE t SET a = 1 WHERE id > $1", DownSQL: batchDown, Batch: &migration.BatchConfig{Column: "id", Size: 100}}, false},
		{"batch dua statement", migration.Definition{UpSQL: batchUp + "; " + batchUp, DownSQL: batchDown, Batch: &migration.BatchConfig{Column: "id", Size: 100}}, false},
		{"batch $2 di dalam string", migration.Definition{UpSQL: "UPDATE t SET a = '$1 $2' WHERE id > 0", DownSQL: batchDown, Batch: &migration.BatchConfig{Column: "id", Size: 100}}, false},
		{"batch ukuran nol", migration.Definition{UpSQL: batchUp, DownSQL: batchDown, Batch: &migration.BatchConfig{Column: "id"}}, false},
	}
	for _, c := range cases {
		err := migration.ValidateDefinition(c.d)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, ok=%v", c.name, err, c.ok)
		}
	}
}

func TestStatusTransitions(t *testing.T) {
	ok := [][2]migration.Status{
		{migration.StatusDraft, migration.StatusPendingReview},
		{migration.StatusPendingReview, migration.StatusApproved},
		{migration.StatusApproved, migration.StatusPendingQueue},
		{migration.StatusPendingQueue, migration.StatusRunning},
		{migration.StatusRunning, migration.StatusSuccess},
	}
	for _, p := range ok {
		if !migration.CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s harus sah", p[0], p[1])
		}
	}
	m := migration.Migration{Status: migration.StatusDraft}
	if err := migration.Transition(&m, migration.StatusRunning); !errors.Is(err, migration.ErrInvalidTransition) {
		t.Fatalf("draft -> running harus ditolak, dapat %v", err)
	}
}
