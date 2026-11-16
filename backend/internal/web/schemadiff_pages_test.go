package web_test

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/web"
)

func diffForm(source string, against int64) url.Values {
	return url.Values{"source": {source}, "against": {fmt.Sprint(against)}}
}

func TestDiffFormOffersOnlySameKindDatabases(t *testing.T) {
	e := newEnv(t, true)

	rec := e.get(dbPath(e.liveID, "/diff"), editorID)

	require.Equal(t, http.StatusOK, rec.Code, body(rec))
	html := body(rec)
	assert.Contains(t, html, fmt.Sprintf(`<option value="%d">staging</option>`, e.stagingID))
	assert.NotContains(t, html, ">sales</option>")
	assert.NotContains(t, html, ">live</option>")
	assert.Contains(t, html, "Belum ada snapshot")
}

func TestDiffFormRequiresViewAccess(t *testing.T) {
	e := newEnv(t, true)

	assert.Equal(t, http.StatusForbidden, e.get(dbPath(e.liveID, "/diff"), nobodyID).Code)
	assert.Equal(t, http.StatusForbidden, e.get(dbPath(e.nograntID, "/diff"), viewerID).Code)
	assert.Equal(t, http.StatusSeeOther, e.get(dbPath(e.liveID, "/diff"), 0).Code)
}

func TestDiffRunShowsChangesWarningsAndEditableSQL(t *testing.T) {
	e := newEnv(t, true)

	rec := e.post(dbPath(e.liveID, "/diff"), editorID, diffForm("database", e.stagingID))

	require.Equal(t, http.StatusOK, rec.Code, body(rec))
	html := body(rec)
	assert.Contains(t, html, "Hasil perbandingan dengan staging")
	assert.Contains(t, html, "2 perubahan")
	assert.Contains(t, html, "Hapus kolom tags pada app.books")
	assert.Contains(t, html, "Tambah kolom extra pada app.books")
	assert.Contains(t, html, `class="destructive"`)
	assert.Contains(t, html, "badge-danger")
	assert.Contains(t, html, "Ada perubahan destructive")
	assert.Contains(t, html, "<textarea")
	assert.Contains(t, html, `DROP COLUMN &#34;tags&#34;`)
	assert.Contains(t, html, "Sinkronkan live dengan staging")
	assert.NotContains(t, html, "<script")
}

func TestDiffRunWithIdenticalSchemas(t *testing.T) {
	e := newEnv(t, true)

	rec := e.post(dbPath(e.liveID, "/diff"), editorID, diffForm("database", e.nograntID))

	require.Equal(t, http.StatusForbidden, rec.Code)

	same := e.post(dbPath(e.liveID, "/diff"), viewerID, diffForm("database", e.liveID))
	require.Equal(t, http.StatusOK, same.Code, body(same))
	assert.Contains(t, body(same), "Schema identik")
	assert.NotContains(t, body(same), "<textarea")
}

func TestDiffRunDeniedWithoutViewOnComparedDatabase(t *testing.T) {
	e := newEnv(t, true)

	rec := e.post(dbPath(e.liveID, "/diff"), liveOnlyID, diffForm("database", e.stagingID))

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.NotContains(t, body(rec), "Hapus kolom")
}

func TestDiffRunValidatesSelection(t *testing.T) {
	e := newEnv(t, true)

	for _, form := range []url.Values{
		{"source": {"database"}, "against": {""}},
		{"source": {"lainnya"}, "against": {"1"}},
		{},
	} {
		rec := e.post(dbPath(e.liveID, "/diff"), editorID, form)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, form.Encode())
		assert.Contains(t, body(rec), "Pilih pembanding")
	}
}

func TestDiffRunUnknownSnapshotNotFound(t *testing.T) {
	e := newEnv(t, true)

	rec := e.post(dbPath(e.liveID, "/diff"), editorID, diffForm("snapshot", 99999))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSnapshotCreateThenCompareIsIdentical(t *testing.T) {
	e := newEnv(t, true)

	created := e.post(dbPath(e.liveID, "/snapshots"), editorID, url.Values{})
	require.Equal(t, http.StatusSeeOther, created.Code, body(created))
	assert.Equal(t, dbPath(e.liveID, "/diff"), created.Header().Get("Location"))
	form := e.follow(created, editorID)
	assert.Contains(t, body(form), "Snapshot #1 diambil")
	match := regexp.MustCompile(`<option value="(\d+)">#`).FindStringSubmatch(body(form))
	require.Len(t, match, 2, body(form))

	var id int64
	_, err := fmt.Sscan(match[1], &id)
	require.NoError(t, err)
	result := e.post(dbPath(e.liveID, "/diff"), editorID, diffForm("snapshot", id))

	require.Equal(t, http.StatusOK, result.Code, body(result))
	assert.Contains(t, body(result), "Schema identik")
}

func TestSnapshotCreateRequiresCSRFAndAccess(t *testing.T) {
	e := newEnv(t, true)

	assert.Equal(t, http.StatusForbidden, e.postNoCSRF(dbPath(e.liveID, "/snapshots"), editorID, url.Values{}).Code)
	assert.Equal(t, http.StatusForbidden, e.post(dbPath(e.liveID, "/snapshots"), nobodyID, url.Values{}).Code)
}

func TestDiffSaveCreatesDraftForEditor(t *testing.T) {
	e := newEnv(t, true)
	form := url.Values{"title": {"  Sinkronkan staging  "}, "sql": {"ALTER TABLE app.books ADD COLUMN extra TEXT;"}}

	rec := e.post(dbPath(e.liveID, "/diff/save"), editorID, form)

	require.Equal(t, http.StatusSeeOther, rec.Code, body(rec))
	require.Equal(t, 1, e.saver.count())
	saved := e.saver.saved[0]
	assert.Equal(t, editorID, saved.Principal.UserID)
	assert.Equal(t, e.liveID, saved.DatabaseID)
	assert.Equal(t, "Sinkronkan staging", saved.Draft.Title)
	assert.Equal(t, "ALTER TABLE app.books ADD COLUMN extra TEXT;", saved.Draft.SQL)
	assert.Equal(t, dbPath(e.liveID, "/diff"), rec.Header().Get("Location"))
	assert.Contains(t, body(e.follow(rec, editorID)), "Draft migration #1 tersimpan")
}

func TestDiffSaveRedirectsToMigrationWhenTabRegistered(t *testing.T) {
	e := newEnv(t, true)
	e.srv.AddDatabaseTab(web.DatabaseTab{Label: "Migrations", Suffix: "/migrations"})

	rec := e.post(dbPath(e.liveID, "/diff/save"), editorID, url.Values{"title": {"t"}, "sql": {"SELECT 1"}})

	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, dbPath(e.liveID, "/migrations/1"), rec.Header().Get("Location"))
	assert.Contains(t, body(e.get(dbPath(e.liveID, ""), editorID)), `href="`+dbPath(e.liveID, "/migrations")+`"`)
}

func TestDiffSaveForbiddenForViewOnlyAccess(t *testing.T) {
	e := newEnv(t, true)

	rec := e.post(dbPath(e.liveID, "/diff/save"), diffViewID, url.Values{"title": {"t"}, "sql": {"SELECT 1"}})

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Zero(t, e.saver.count())
}

func TestDiffResultDisablesSaveForViewOnlyAccess(t *testing.T) {
	e := newEnv(t, true)

	rec := e.post(dbPath(e.liveID, "/diff"), diffViewID, diffForm("database", e.stagingID))

	require.Equal(t, http.StatusOK, rec.Code, body(rec))
	html := body(rec)
	assert.Contains(t, html, "Hapus kolom tags")
	assert.Regexp(t, `<button class="btn" type="submit" disabled>Simpan sebagai draft migration</button>`, html)
	assert.Contains(t, html, "butuh akses View &#43; Edit")
}

func TestDiffSaveValidatesTitleAndSQL(t *testing.T) {
	e := newEnv(t, true)

	for _, form := range []url.Values{
		{"title": {""}, "sql": {"SELECT 1"}},
		{"title": {"t"}, "sql": {"   "}},
		{"title": {string(make([]byte, 201))}, "sql": {"SELECT 1"}},
	} {
		rec := e.post(dbPath(e.liveID, "/diff/save"), editorID, form)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	}
	assert.Zero(t, e.saver.count())
}

func TestDiffSaveRequiresCSRF(t *testing.T) {
	e := newEnv(t, true)

	rec := e.postNoCSRF(dbPath(e.liveID, "/diff/save"), editorID, url.Values{"title": {"t"}, "sql": {"SELECT 1"}})

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Zero(t, e.saver.count())
}

func TestDiffWithoutDraftSaverShowsNotice(t *testing.T) {
	e := newEnv(t, false)

	result := e.post(dbPath(e.liveID, "/diff"), editorID, diffForm("database", e.stagingID))
	require.Equal(t, http.StatusOK, result.Code)
	assert.Contains(t, body(result), "belum tersedia")
	assert.Contains(t, body(result), "disabled>Simpan sebagai draft migration")

	save := e.post(dbPath(e.liveID, "/diff/save"), editorID, url.Values{"title": {"t"}, "sql": {"SELECT 1"}})
	require.Equal(t, http.StatusSeeOther, save.Code)
	assert.Contains(t, body(e.follow(save, editorID)), "flash-error")
}
