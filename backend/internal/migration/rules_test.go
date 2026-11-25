package migration

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
)

const (
	a = int64(1)
	b = int64(2)
	c = int64(3)
)

func runs(steps ...any) []gen.ListSucceededRunsRow {
	var out []gen.ListSucceededRunsRow
	for i := 0; i < len(steps); i += 2 {
		out = append(out, gen.ListSucceededRunsRow{ID: int64(i), Kind: steps[i].(gen.RequestKind), MigrationID: steps[i+1].(int64)})
	}
	return out
}

func TestReplayPositions(t *testing.T) {
	const apply, rollback, forward = gen.RequestKindApply, gen.RequestKindRollback, gen.RequestKindForward
	cases := map[string]struct {
		runs         []gen.ListSucceededRunsRow
		active, redo []int64
	}{
		"kosong":                     {nil, nil, nil},
		"linear":                     {runs(apply, a, apply, b, apply, c), []int64{a, b, c}, nil},
		"rollback terakhir":          {runs(apply, a, apply, b, rollback, b), []int64{a}, []int64{b}},
		"rollback berurutan":         {runs(apply, a, apply, b, apply, c, rollback, c, rollback, b), []int64{a}, []int64{c, b}},
		"forward tidak lanjut":       {runs(apply, a, apply, b, apply, c, rollback, c, rollback, b, forward, b), []int64{a, b}, []int64{c}},
		"apply baru menghapus redo":  {runs(apply, a, apply, b, rollback, b, apply, c), []int64{a, c}, nil},
		"batal batch partial":        {runs(apply, a, rollback, b), []int64{a}, []int64{b}},
		"forward lalu rollback lagi": {runs(apply, a, rollback, a, forward, a, rollback, a), nil, []int64{a}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := replay(tc.runs)
			assert.ElementsMatch(t, tc.active, p.active)
			assert.Equal(t, len(tc.redo), len(p.redo))
			for i := range tc.redo {
				assert.Equal(t, tc.redo[i], p.redo[i])
			}
			for i := range tc.active {
				assert.Equal(t, tc.active[i], p.active[i])
			}
		})
	}
}

func mig(id int64, status gen.MigStatus) gen.Migration {
	return gen.Migration{ID: id, Status: status, Revision: 1, DownSql: pgtype.Text{String: "down", Valid: true}}
}

func TestCheckRules(t *testing.T) {
	st := state{positions: positions{active: []int64{a, b}, redo: []int64{c}}}
	irreversible := mig(b, gen.MigStatusApplied)
	irreversible.Irreversible = true

	assert.NoError(t, st.check(gen.RequestKindApply, mig(9, gen.MigStatusDraft), 0))
	assert.Error(t, st.check(gen.RequestKindApply, mig(b, gen.MigStatusApplied), 0))
	assert.NoError(t, st.check(gen.RequestKindRollback, mig(b, gen.MigStatusApplied), 0))
	assert.ErrorIs(t, st.check(gen.RequestKindRollback, mig(a, gen.MigStatusApplied), 0), apperr.ErrConflict, "hanya aktif terakhir")
	assert.Error(t, st.check(gen.RequestKindRollback, irreversible, 0))
	assert.NoError(t, st.check(gen.RequestKindForward, mig(c, gen.MigStatusRolledBack), 0))
	assert.Error(t, st.check(gen.RequestKindForward, mig(a, gen.MigStatusApplied), 0))
	assert.Error(t, st.check(gen.RequestKindForward, mig(9, gen.MigStatusRolledBack), 0))
}

func TestPartialHoldsOtherMigrations(t *testing.T) {
	st := state{
		positions: positions{active: []int64{a}},
		requests:  []request{{ID: 7, MigrationID: b, Kind: gen.RequestKindApply, Status: gen.RequestStatusPartial, Revision: 1}},
	}
	partial := mig(b, gen.MigStatusDraft)
	assert.Error(t, st.check(gen.RequestKindApply, mig(c, gen.MigStatusDraft), 0))
	assert.Error(t, st.check(gen.RequestKindRollback, mig(a, gen.MigStatusApplied), 0))
	assert.Error(t, st.check(gen.RequestKindApply, partial, 0), "tidak boleh request apply baru")
	assert.NoError(t, st.check(gen.RequestKindApply, partial, 7), "melanjutkan request yang sama")
	assert.NoError(t, st.check(gen.RequestKindRollback, partial, 0), "membatalkan batch yang sudah commit")
	assert.Equal(t, apigen.MigrationStatusPartial, st.status(partial))

	st.requests = append(st.requests, request{ID: 8, MigrationID: b, Kind: gen.RequestKindRollback, Status: gen.RequestStatusPartial, Revision: 1})
	assert.NoError(t, st.check(gen.RequestKindRollback, partial, 8), "melanjutkan rollback")
	assert.Error(t, st.check(gen.RequestKindApply, partial, 7), "apply tidak bisa dilanjutkan setelah sebagian dibatalkan")
	assert.Equal(t, int64(8), st.executable(partial).ID)
}

func TestRejectedRollbackOfPartialKeepsResume(t *testing.T) {
	st := state{requests: []request{
		{ID: 7, MigrationID: b, Kind: gen.RequestKindApply, Status: gen.RequestStatusPartial, Revision: 1},
		{ID: 8, MigrationID: b, Kind: gen.RequestKindRollback, Status: gen.RequestStatusRejected, Revision: 1},
	}}
	m := mig(b, gen.MigStatusDraft)
	require.NotNil(t, st.executable(m))
	assert.Equal(t, int64(7), st.executable(m).ID)
	assert.Equal(t, apigen.MigrationStatusPartial, st.status(m))
}

func TestActionsFollowLevelAndState(t *testing.T) {
	st := state{}
	m := mig(b, gen.MigStatusDraft)
	assert.Empty(t, st.actions(m, false, authz.LevelView))
	assert.ElementsMatch(t, []apigen.MigrationAction{"edit", "delete", "submit", "explain"}, st.actions(m, false, authz.LevelEdit))
	assert.ElementsMatch(t, []apigen.MigrationAction{"submit", "explain"}, st.actions(m, true, authz.LevelEdit), "SQL terkunci")

	st.requests = []request{{ID: 1, MigrationID: b, Kind: gen.RequestKindApply, Status: gen.RequestStatusPendingReview, Revision: 1}}
	assert.ElementsMatch(t, []apigen.MigrationAction{"explain"}, st.actions(m, false, authz.LevelEdit))
	st.requests[0].Status = gen.RequestStatusApproved
	assert.ElementsMatch(t, []apigen.MigrationAction{"execute", "explain"}, st.actions(m, false, authz.LevelEdit))
	st.requests[0].Status, st.requests[0].JobFailed = gen.RequestStatusRunning, true
	assert.ElementsMatch(t, []apigen.MigrationAction{"edit", "delete", "execute", "submit", "explain"}, st.actions(m, false, authz.LevelEdit), "job gagal sebelum run tercatat")
}

func TestHideRollbackUsesExecutionIDs(t *testing.T) {
	id := func(n int64) *int64 { return &n }
	ex := func(n, m int64, kind apigen.MigrationKind, status apigen.MigrationExecutionStatus, reverts *int64) apigen.MigrationExecution {
		return apigen.MigrationExecution{Id: n, MigrationId: m, Kind: kind, Status: status, RevertsExecutionId: reverts}
	}
	visible := func(items ...apigen.MigrationExecution) []int64 {
		var out []int64
		for _, e := range HideRollbackPairs(items) {
			out = append(out, e.Id)
		}
		return out
	}
	assert.Equal(t, []int64{1, 4, 5}, visible(
		ex(1, a, apigen.Apply, apigen.Succeeded, nil), ex(2, b, apigen.Apply, apigen.Succeeded, nil),
		ex(3, b, apigen.Rollback, apigen.Succeeded, id(2)), ex(4, b, apigen.Forward, apigen.Succeeded, nil),
		ex(5, c, apigen.Apply, apigen.Succeeded, nil)), "contoh di spesifikasi")
	assert.Equal(t, []int64{1, 2}, visible(
		ex(1, a, apigen.Apply, apigen.Succeeded, nil), ex(2, a, apigen.Rollback, apigen.Failed, id(1))), "rollback gagal tidak menyembunyikan")
	assert.Equal(t, []int64{1, 2}, visible(
		ex(1, a, apigen.Apply, apigen.Failed, nil), ex(2, a, apigen.Rollback, apigen.Succeeded, id(1))), "attempt gagal tetap terlihat")
}

func TestValidateDefinition(t *testing.T) {
	batch := &apigen.MigrationBatch{Schema: "app", Table: "t", Column: "id", Size: 100}
	valid := apigen.MigrationDefinition{Title: "t", UpSql: "SELECT 1", DownSql: "SELECT 2"}
	cases := map[string]struct {
		kind  string
		edit  func(*apigen.MigrationDefinition)
		field string
	}{
		"lengkap":                   {"postgres", func(*apigen.MigrationDefinition) {}, ""},
		"judul kosong":              {"postgres", func(d *apigen.MigrationDefinition) { d.Title = " " }, "title"},
		"up kosong":                 {"postgres", func(d *apigen.MigrationDefinition) { d.UpSql = "" }, "upSql"},
		"up hanya komentar":         {"postgres", func(d *apigen.MigrationDefinition) { d.UpSql = "-- x" }, "upSql"},
		"transaksi manual":          {"postgres", func(d *apigen.MigrationDefinition) { d.UpSql = "BEGIN; SELECT 1; COMMIT;" }, "upSql"},
		"down kosong":               {"postgres", func(d *apigen.MigrationDefinition) { d.DownSql = "" }, "downSql"},
		"irreversible tanpa alasan": {"postgres", func(d *apigen.MigrationDefinition) { d.DownSql, d.Irreversible = "", true }, "irreversibleReason"},
		"irreversible dengan down":  {"postgres", func(d *apigen.MigrationDefinition) { d.Irreversible, d.IrreversibleReason = true, "x" }, "downSql"},
		"irreversible sah":          {"postgres", func(d *apigen.MigrationDefinition) { d.DownSql, d.Irreversible, d.IrreversibleReason = "", true, "x" }, ""},
		"alasan tanpa irreversible": {"postgres", func(d *apigen.MigrationDefinition) { d.IrreversibleReason = "x" }, "irreversibleReason"},
		"batch tanpa placeholder":   {"postgres", func(d *apigen.MigrationDefinition) { d.Batch = batch }, "upSql"},
		"batch sah": {"postgres", func(d *apigen.MigrationDefinition) {
			d.Batch, d.UpSql, d.DownSql = batch, "UPDATE t SET x=1 WHERE id>$1 AND id<=$2", "UPDATE t SET x=0 WHERE id>$1 AND id<=$2"
		}, ""},
		"batch irreversible": {"postgres", func(d *apigen.MigrationDefinition) {
			d.Batch, d.UpSql, d.DownSql, d.Irreversible, d.IrreversibleReason = batch, "UPDATE t SET x=1 WHERE id>$1 AND id<=$2", "", true, "x"
		}, ""},
		"batch tanpa tabel": {"postgres", func(d *apigen.MigrationDefinition) {
			d.Batch = &apigen.MigrationBatch{Schema: "app", Column: "id", Size: 1}
		}, "batch"},
		"ukuran batch nol": {"postgres", func(d *apigen.MigrationDefinition) {
			d.Batch = &apigen.MigrationBatch{Schema: "app", Table: "t", Column: "id"}
		}, "batch"},
		"batch hanya untuk postgres": {"mysql", func(d *apigen.MigrationDefinition) { d.Batch = batch }, "batch"},
		"batch dengan placeholder lebih": {"postgres", func(d *apigen.MigrationDefinition) {
			d.Batch, d.UpSql = batch, "UPDATE t SET x=$3 WHERE id>$1 AND id<=$2"
		}, "upSql"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d := valid
			tc.edit(&d)
			err := validateDefinition(tc.kind, d)
			if tc.field == "" {
				require.NoError(t, err)
				return
			}
			var ae *apperr.Error
			require.ErrorAs(t, err, &ae)
			assert.Contains(t, ae.Fields, tc.field)
		})
	}
}
