package web_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionListShowsConnectionsWithoutPassword(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get("/admin/connections", adminID)

	require.Equal(t, http.StatusOK, rec.Code)
	html := body(rec)
	for _, name := range []string{"live", "staging", "inactive", "dead", "sales"} {
		assert.Contains(t, html, "<strong>"+name+"</strong>")
	}
	assert.Contains(t, html, "MySQL")
	assert.Contains(t, html, "Nonaktif")
	assert.NotContains(t, html, "apa-saja")
	assert.NotContains(t, html, "password_ciphertext")
}

func TestConnectionPagesRejectNonAdmin(t *testing.T) {
	e := newEnv(t, false)

	for _, user := range []int64{viewerID, editorID, nobodyID} {
		for _, path := range []string{
			"/admin/connections",
			"/admin/connections/new",
			fmt.Sprintf("/admin/connections/%d", e.liveID),
			fmt.Sprintf("/admin/connections/%d/delete", e.liveID),
		} {
			rec := e.get(path, user)
			assert.Equal(t, http.StatusForbidden, rec.Code, "GET %s user %d", path, user)
		}
		for _, path := range []string{
			"/admin/connections/new",
			"/admin/connections/test",
			fmt.Sprintf("/admin/connections/%d", e.liveID),
			fmt.Sprintf("/admin/connections/%d/delete", e.liveID),
			fmt.Sprintf("/admin/connections/%d/test", e.liveID),
		} {
			rec := e.post(path, user, validConnectionForm("hacker"))
			assert.Equal(t, http.StatusForbidden, rec.Code, "POST %s user %d", path, user)
		}
	}
	assert.Zero(t, e.connectionCount("hacker"))
	assert.Equal(t, 1, e.connectionCount("live"))
}

func TestConnectionNewFormHasCSRFAndEmptyPassword(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get("/admin/connections/new", adminID)

	require.Equal(t, http.StatusOK, rec.Code)
	html := body(rec)
	assert.Contains(t, html, `name="_csrf" value="`+csrfFor(adminID)+`"`)
	assert.Contains(t, html, `name="password" type="password"`)
	assert.Contains(t, html, `action="/admin/connections/new"`)
}

func TestConnectionCreateSuccess(t *testing.T) {
	e := newEnv(t, false)

	rec := e.post("/admin/connections/new", adminID, validConnectionForm("analytics"))

	require.Equal(t, http.StatusSeeOther, rec.Code, body(rec))
	assert.Equal(t, "/admin/connections", rec.Header().Get("Location"))
	list := e.follow(rec, adminID)
	assert.Contains(t, body(list), "analytics")
	assert.Contains(t, body(list), "ditambahkan")
	assert.NotContains(t, body(list), "rahasia-banget")
	assert.Equal(t, 1, e.connectionCount("analytics"))
	assert.NotContains(t, string(e.ciphertext("analytics")), "rahasia-banget")
}

func TestConnectionCreateValidationErrorKeepsValuesButNotPassword(t *testing.T) {
	e := newEnv(t, false)
	form := validConnectionForm("")
	form.Set("host", "")

	rec := e.post("/admin/connections/new", adminID, form)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	html := body(rec)
	assert.Contains(t, html, "wajib diisi")
	assert.Contains(t, html, `value="appdb"`)
	assert.Contains(t, html, `value="reader"`)
	assert.NotContains(t, html, "rahasia-banget")
	assert.Contains(t, html, `class="is-invalid"`)
}

func TestConnectionCreateRejectsNonNumericPort(t *testing.T) {
	e := newEnv(t, false)
	form := validConnectionForm("analytics")
	form.Set("port", "abc")

	rec := e.post("/admin/connections/new", adminID, form)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, body(rec), "harus berupa angka")
	assert.Zero(t, e.connectionCount("analytics"))
}

func TestConnectionCreateDuplicateNameConflicts(t *testing.T) {
	e := newEnv(t, false)

	rec := e.post("/admin/connections/new", adminID, validConnectionForm("live"))

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, body(rec), "sudah dipakai")
	assert.Equal(t, 1, e.connectionCount("live"))
}

func TestConnectionEditFormPrefillsWithoutPassword(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get(fmt.Sprintf("/admin/connections/%d", e.deadID), adminID)

	require.Equal(t, http.StatusOK, rec.Code)
	html := body(rec)
	assert.Contains(t, html, `value="dead"`)
	assert.Contains(t, html, `value="127.0.0.1"`)
	assert.Contains(t, html, "Kosongkan jika tidak ingin mengubah password")
	assert.NotContains(t, html, "apa-saja")
}

func TestConnectionEditUnknownIDNotFound(t *testing.T) {
	e := newEnv(t, false)

	assert.Equal(t, http.StatusNotFound, e.get("/admin/connections/99999", adminID).Code)
	assert.Equal(t, http.StatusNotFound, e.get("/admin/connections/abc", adminID).Code)
}

func TestConnectionUpdateWithoutPasswordKeepsCiphertext(t *testing.T) {
	e := newEnv(t, false)
	before := e.ciphertext("dead")
	form := validConnectionForm("dead-renamed")
	form.Set("password", "")

	rec := e.post(fmt.Sprintf("/admin/connections/%d", e.deadID), adminID, form)

	require.Equal(t, http.StatusSeeOther, rec.Code, body(rec))
	assert.Equal(t, before, e.ciphertext("dead-renamed"))
	assert.Contains(t, body(e.follow(rec, adminID)), "dead-renamed")
}

func TestConnectionUpdateWithPasswordReplacesCiphertext(t *testing.T) {
	e := newEnv(t, false)
	before := e.ciphertext("dead")
	form := validConnectionForm("dead")
	form.Set("password", "password-baru")

	rec := e.post(fmt.Sprintf("/admin/connections/%d", e.deadID), adminID, form)

	require.Equal(t, http.StatusSeeOther, rec.Code, body(rec))
	assert.NotEqual(t, before, e.ciphertext("dead"))
}

func TestConnectionUpdateValidationError(t *testing.T) {
	e := newEnv(t, false)
	form := validConnectionForm("")

	rec := e.post(fmt.Sprintf("/admin/connections/%d", e.deadID), adminID, form)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, body(rec), "Ubah koneksi")
}

func TestConnectionDeleteRequiresConfirmation(t *testing.T) {
	e := newEnv(t, false)
	path := fmt.Sprintf("/admin/connections/%d/delete", e.deadID)

	confirm := e.get(path, adminID)
	require.Equal(t, http.StatusOK, confirm.Code)
	assert.Contains(t, body(confirm), "Hapus koneksi")
	assert.Contains(t, body(confirm), "<strong>dead</strong>")
	assert.Equal(t, 1, e.connectionCount("dead"))

	rec := e.post(path, adminID, url.Values{})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Zero(t, e.connectionCount("dead"))
	assert.NotContains(t, body(e.follow(rec, adminID)), "<strong>dead</strong>")
}

func TestConnectionTestSavedSuccessAndFailure(t *testing.T) {
	e := newEnv(t, false)

	ok := e.post(fmt.Sprintf("/admin/connections/%d/test", e.liveID), adminID, url.Values{})
	require.Equal(t, http.StatusSeeOther, ok.Code)
	assert.Contains(t, body(e.follow(ok, adminID)), "berhasil")

	bad := e.post(fmt.Sprintf("/admin/connections/%d/test", e.deadID), adminID, url.Values{})
	require.Equal(t, http.StatusSeeOther, bad.Code)
	failed := body(e.follow(bad, adminID))
	assert.Contains(t, failed, "flash-error")
	assert.Contains(t, failed, "gagal")
	assert.NotContains(t, failed, "apa-saja")
}

func TestConnectionTestFormUnsavedValues(t *testing.T) {
	e := newEnv(t, false)
	cfg := e.target.Config().ConnConfig
	form := url.Values{
		"id":           {"0"},
		"name":         {"baru"},
		"kind":         {"postgres"},
		"host":         {cfg.Host},
		"port":         {fmt.Sprint(cfg.Port)},
		"databaseName": {cfg.Database},
		"username":     {cfg.User},
		"password":     {cfg.Password},
		"sslMode":      {"disable"},
	}

	ok := e.post("/admin/connections/test", adminID, form)
	require.Equal(t, http.StatusOK, ok.Code, body(ok))
	assert.Contains(t, body(ok), "Koneksi berhasil")
	assert.Contains(t, body(ok), `type="password" autocomplete="new-password" value=""`)
	assert.Zero(t, e.connectionCount("baru"))

	form.Set("host", "127.0.0.1")
	form.Set("port", "1")
	bad := e.post("/admin/connections/test", adminID, form)
	require.Equal(t, http.StatusOK, bad.Code)
	assert.Contains(t, body(bad), "Koneksi gagal")
	assert.Contains(t, body(bad), `type="password" autocomplete="new-password" value=""`)
}

func TestConnectionTestFormEditWithoutPasswordUsesSavedConfig(t *testing.T) {
	e := newEnv(t, false)
	form := validConnectionForm("live")
	form.Set("id", fmt.Sprint(e.liveID))
	form.Set("password", "")

	rec := e.post("/admin/connections/test", adminID, form)

	require.Equal(t, http.StatusOK, rec.Code, body(rec))
	assert.Contains(t, body(rec), "Koneksi berhasil")
}

func TestConnectionTestFormValidatesNewConnection(t *testing.T) {
	e := newEnv(t, false)
	form := validConnectionForm("baru")
	form.Set("password", "")
	form.Set("id", "0")

	rec := e.post("/admin/connections/test", adminID, form)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, body(rec), "wajib diisi")
}

func TestConnectionNameIsEscapedInList(t *testing.T) {
	e := newEnv(t, false)
	rec := e.post("/admin/connections/new", adminID, validConnectionForm(`<script>alert(1)</script>`))
	require.Equal(t, http.StatusSeeOther, rec.Code)

	html := body(e.get("/admin/connections", adminID))

	assert.NotContains(t, html, "<script>alert(1)</script>")
	assert.Contains(t, html, "&lt;script&gt;alert(1)&lt;/script&gt;")
}
