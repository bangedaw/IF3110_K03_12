package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (e *env) targetHas(table string) bool {
	e.t.Helper()
	var exists bool
	require.NoError(e.t, e.target.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
	return exists
}

var migrationIDPattern = regexp.MustCompile(`/migrations/(\d+)$`)

func (e *env) createDraft(title, sql string) int64 {
	e.t.Helper()
	rec := e.post(dbPath(e.liveID, "/migrations/new"), editorID, url.Values{"title": {title}, "sql": {sql}})
	require.Equal(e.t, http.StatusSeeOther, rec.Code, body(rec))
	m := migrationIDPattern.FindStringSubmatch(rec.Header().Get("Location"))
	require.Len(e.t, m, 2, rec.Header().Get("Location"))
	id, err := strconv.ParseInt(m[1], 10, 64)
	require.NoError(e.t, err)
	return id
}

func migPath(e *env, id int64, rest string) string {
	return dbPath(e.liveID, fmt.Sprintf("/migrations/%d%s", id, rest))
}

func TestMigrationCreateExecuteAndHistory(t *testing.T) {
	e := newEnvWith(t, false, true)

	list := e.get(dbPath(e.liveID, "/migrations"), editorID)
	require.Equal(t, http.StatusOK, list.Code)
	assert.Contains(t, body(list), "Buat migration")
	assert.Contains(t, body(list), `href="/databases/`+strconv.FormatInt(e.liveID, 10)+`/history"`)

	invalid := e.post(dbPath(e.liveID, "/migrations/new"), editorID, url.Values{"title": {""}, "sql": {"BEGIN; SELECT 1;"}})
	assert.Equal(t, http.StatusUnprocessableEntity, invalid.Code)
	assert.Contains(t, body(invalid), "wajib diisi")
	assert.Contains(t, body(invalid), "transaction-control")

	id := e.createDraft("Tambah tabel audit", "CREATE TABLE app.audit_trail (id int); INSERT INTO app.audit_trail VALUES (1);")
	detail := e.get(migPath(e, id, ""), editorID)
	require.Equal(t, http.StatusOK, detail.Code)
	assert.Contains(t, body(detail), "CREATE TABLE app.audit_trail")
	assert.Contains(t, body(detail), "Eksekusi")

	confirm := e.get(migPath(e, id, "/execute"), editorID)
	require.Equal(t, http.StatusOK, confirm.Code)
	assert.Contains(t, body(confirm), "CREATE TABLE app.audit_trail")
	assert.Contains(t, body(confirm), `name="revision" value="1"`)

	done := e.follow(e.post(migPath(e, id, "/execute"), editorID, url.Values{"revision": {"1"}}), editorID)
	assert.Contains(t, body(done), "berhasil diterapkan sebagai versi 1")
	assert.Contains(t, body(done), "applied")
	assert.NotContains(t, body(done), "Simpan perubahan")
	assert.True(t, e.targetHas("app.audit_trail"))

	history := e.get(dbPath(e.liveID, "/history"), viewerID)
	require.Equal(t, http.StatusOK, history.Code)
	for _, want := range []string{"Tambah tabel audit", "user44", "berhasil", "CREATE TABLE app.audit_trail"} {
		assert.Contains(t, body(history), want)
	}
}

func TestMigrationFailureIsRolledBack(t *testing.T) {
	e := newEnvWith(t, false, true)
	id := e.createDraft("Gagal", "CREATE TABLE app.half (id int); INSERT INTO app.half VALUES (1); INSERT INTO app.tidak_ada VALUES (1);")

	page := e.follow(e.post(migPath(e, id, "/execute"), editorID, url.Values{"revision": {"1"}}), editorID)
	assert.Contains(t, body(page), "Eksekusi gagal pada statement 3")
	assert.Contains(t, body(page), "di-rollback")
	assert.Contains(t, body(page), "gagal · rollback")
	assert.Contains(t, body(page), "Simpan perubahan", "migration tetap draft dan bisa diperbaiki")
	assert.False(t, e.targetHas("app.half"))
}

func TestMigrationViewOnlyIsRejectedByBackend(t *testing.T) {
	e := newEnvWith(t, false, true)
	id := e.createDraft("Draft", "CREATE TABLE app.blocked (id int)")

	list := e.get(dbPath(e.liveID, "/migrations"), viewerID)
	require.Equal(t, http.StatusOK, list.Code)
	assert.NotContains(t, body(list), "Buat migration")
	detail := e.get(migPath(e, id, ""), viewerID)
	require.Equal(t, http.StatusOK, detail.Code)
	assert.NotContains(t, body(detail), "/execute")
	assert.NotContains(t, body(detail), "Simpan perubahan")
	assert.Equal(t, http.StatusOK, e.get(dbPath(e.liveID, "/history"), viewerID).Code)

	for _, path := range []string{dbPath(e.liveID, "/migrations/new"), migPath(e, id, "/execute"), migPath(e, id, "/delete"), migPath(e, id, "/explain")} {
		assert.Equal(t, http.StatusForbidden, e.get(path, viewerID).Code, "GET %s", path)
	}
	posts := map[string]url.Values{
		dbPath(e.liveID, "/migrations/new"): {"title": {"x"}, "sql": {"SELECT 1"}},
		migPath(e, id, ""):                  {"title": {"x"}, "sql": {"SELECT 1"}, "revision": {"1"}},
		migPath(e, id, "/execute"):          {"revision": {"1"}},
		migPath(e, id, "/delete"):           {"revision": {"1"}},
		migPath(e, id, "/explain"):          {},
	}
	for path, form := range posts {
		assert.Equal(t, http.StatusForbidden, e.post(path, viewerID, form).Code, "POST %s", path)
	}
	assert.False(t, e.targetHas("app.blocked"))

	assert.Equal(t, http.StatusForbidden, e.get(dbPath(e.liveID, "/migrations"), nobodyID).Code)
	assert.Equal(t, http.StatusForbidden, e.get(dbPath(e.liveID, "/history"), nobodyID).Code)
}

func TestMigrationEditConflictAndDelete(t *testing.T) {
	e := newEnvWith(t, false, true)
	id := e.createDraft("Awal", "CREATE TABLE app.c1 (id int)")

	updated := e.follow(e.post(migPath(e, id, ""), editorID, url.Values{"title": {"Ubah"}, "sql": {"CREATE TABLE app.c1 (id bigint)"}, "revision": {"1"}}), editorID)
	assert.Contains(t, body(updated), "Draft migration diperbarui")
	assert.Contains(t, body(updated), "revisi 2")

	stale := e.post(migPath(e, id, ""), editorID, url.Values{"title": {"Basi"}, "sql": {"SELECT 1"}, "revision": {"1"}})
	assert.Equal(t, http.StatusConflict, stale.Code)
	assert.Contains(t, body(stale), "sudah diubah orang lain")

	assert.Equal(t, http.StatusOK, e.get(migPath(e, id, "/delete"), editorID).Code)
	gone := e.follow(e.post(migPath(e, id, "/delete"), editorID, url.Values{"revision": {"2"}}), editorID)
	assert.Contains(t, body(gone), "dihapus")
	assert.Equal(t, http.StatusNotFound, e.get(migPath(e, id, ""), editorID).Code)
}

func TestSchemaDiffSavesRealDraftMigration(t *testing.T) {
	e := newEnvWith(t, false, true)
	rec := e.post(dbPath(e.liveID, "/diff/save"), editorID, url.Values{"title": {"Sinkron"}, "sql": {"ALTER TABLE app.books ADD COLUMN extra TEXT"}})
	require.Equal(t, http.StatusSeeOther, rec.Code, body(rec))
	assert.Regexp(t, migrationIDPattern, rec.Header().Get("Location"))

	page := e.follow(rec, editorID)
	assert.Contains(t, body(page), "Sinkron")
	assert.Contains(t, body(page), "ALTER TABLE app.books ADD COLUMN extra TEXT")
}

func TestMigrationExplainShowsPlanWithoutChangingData(t *testing.T) {
	e := newEnvWith(t, false, true)
	id := e.createDraft("Hapus buku", "DELETE FROM app.books WHERE price > 0")

	rec := e.post(migPath(e, id, "/explain"), editorID, url.Values{"analyze": {"on"}})
	require.Equal(t, http.StatusOK, rec.Code, body(rec))
	assert.Contains(t, body(rec), "Node Type")
	assert.Contains(t, body(rec), "Actual Total Time")

	var n int
	require.NoError(t, e.target.QueryRow(context.Background(), `SELECT count(*) FROM app.books`).Scan(&n))
	assert.Positive(t, n)
}
