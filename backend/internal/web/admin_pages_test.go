package web_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newUserForm(username, password string) url.Values {
	return url.Values{"username": {username}, "displayName": {"Nama " + username}, "password": {password}}
}

func TestAdminPagesRejectNonAdminEverywhere(t *testing.T) {
	e := newEnv(t, false)
	group := e.insertGroup("rahasia-group")
	user := fmt.Sprint(viewerID)
	gid := fmt.Sprint(group)

	gets := []string{
		"/admin/users", "/admin/users/new", "/admin/users/" + user, "/admin/users/" + user + "/delete",
		"/admin/groups", "/admin/groups/new", "/admin/groups/" + gid, "/admin/groups/" + gid + "/delete",
	}
	posts := []string{
		"/admin/users/new", "/admin/users/" + user, "/admin/users/" + user + "/password",
		"/admin/users/" + user + "/revoke-sessions", "/admin/users/" + user + "/delete",
		"/admin/groups/new", "/admin/groups/" + gid, "/admin/groups/" + gid + "/members",
		"/admin/groups/" + gid + "/members/" + user + "/remove", "/admin/groups/" + gid + "/grants",
		fmt.Sprintf("/admin/groups/%s/grants/%d/remove", gid, e.liveID), "/admin/groups/" + gid + "/delete",
	}
	for _, who := range []int64{viewerID, editorID, nobodyID} {
		for _, path := range gets {
			assert.Equal(t, http.StatusForbidden, e.get(path, who).Code, "GET %s user %d", path, who)
		}
		for _, path := range posts {
			form := url.Values{"username": {"x"}, "password": {"abcdefgh"}, "name": {"x"}, "userId": {user}, "databaseId": {fmt.Sprint(e.liveID)}, "level": {"edit"}}
			assert.Equal(t, http.StatusForbidden, e.post(path, who, form).Code, "POST %s user %d", path, who)
		}
	}
	assert.Equal(t, "0", e.scalar(`SELECT count(*)::text FROM users WHERE username = 'x'`))
	assert.Equal(t, "rahasia-group", e.scalar(`SELECT name FROM groups WHERE id = $1`, group))
}

func TestAdminPagesRequireCSRF(t *testing.T) {
	e := newEnv(t, false)

	assert.Equal(t, http.StatusForbidden, e.postNoCSRF("/admin/users/new", adminID, newUserForm("baru", "password-kuat")).Code)
	assert.Equal(t, http.StatusForbidden, e.postNoCSRF("/admin/groups/new", adminID, url.Values{"name": {"g"}}).Code)
	assert.Equal(t, "0", e.scalar(`SELECT count(*)::text FROM users WHERE username = 'baru'`))
}

func TestAdminNavigationShownOnlyToAdmin(t *testing.T) {
	e := newEnv(t, false)

	admin := body(e.get("/admin/users", adminID))
	assert.Contains(t, admin, `href="/admin/users"`)
	assert.Contains(t, admin, `href="/admin/groups"`)
	assert.NotContains(t, body(e.get("/databases", viewerID)), `href="/admin/users"`)
}

func TestUserListShowsUsersAndPaginates(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get("/admin/users?limit=2", adminID)

	require.Equal(t, http.StatusOK, rec.Code, body(rec))
	html := body(rec)
	assert.Contains(t, html, "<strong>user1</strong>")
	assert.Contains(t, html, "Platform Admin")
	assert.Contains(t, html, `rel="next"`)
	assert.Contains(t, html, "offset=2")
	assert.NotContains(t, html, "password_hash")

	later := body(e.get("/admin/users?limit=2&offset=2", adminID))
	assert.Contains(t, later, `rel="prev"`)
}

func TestUserCreateStoresHashedPasswordAndRedirects(t *testing.T) {
	e := newEnv(t, false)

	rec := e.post("/admin/users/new", adminID, newUserForm("budi", "password-kuat"))

	require.Equal(t, http.StatusSeeOther, rec.Code, body(rec))
	id := e.scalar(`SELECT id::text FROM users WHERE username = 'budi'`)
	assert.Equal(t, "/admin/users/"+id, rec.Header().Get("Location"))
	assert.Contains(t, body(e.follow(rec, adminID)), "dibuat")
	hash := e.scalar(`SELECT password_hash FROM users WHERE username = 'budi'`)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$"), hash)
	assert.NotContains(t, hash, "password-kuat")
	assert.Equal(t, "false", e.scalar(`SELECT is_platform_admin::text FROM users WHERE username = 'budi'`))
}

func TestUserCreateCanGrantAdminRole(t *testing.T) {
	e := newEnv(t, false)
	form := newUserForm("bos", "password-kuat")
	form.Set("isAdmin", "on")

	rec := e.post("/admin/users/new", adminID, form)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "true", e.scalar(`SELECT is_platform_admin::text FROM users WHERE username = 'bos'`))
}

func TestUserCreateValidationKeepsInputButNotPassword(t *testing.T) {
	e := newEnv(t, false)

	rec := e.post("/admin/users/new", adminID, newUserForm("a!", "pendek"))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	html := body(rec)
	assert.Contains(t, html, "3-64 karakter")
	assert.Contains(t, html, "8-256 karakter")
	assert.Contains(t, html, `value="a!"`)
	assert.NotContains(t, html, "pendek")
	assert.Equal(t, "0", e.scalar(`SELECT count(*)::text FROM users WHERE username = 'a!'`))
}

func TestUserCreateDuplicateUsernameConflicts(t *testing.T) {
	e := newEnv(t, false)

	rec := e.post("/admin/users/new", adminID, newUserForm("user42", "password-kuat"))

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, body(rec), "sudah dipakai")
}

func TestUserDetailAndUnknownUser(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get(fmt.Sprintf("/admin/users/%d", viewerID), adminID)

	require.Equal(t, http.StatusOK, rec.Code)
	html := body(rec)
	assert.Contains(t, html, "user42")
	assert.Contains(t, html, "Reset password")
	assert.Contains(t, html, "Cabut semua session")
	assert.Contains(t, html, `action="/admin/users/42/password"`)
	assert.Equal(t, http.StatusNotFound, e.get("/admin/users/999999", adminID).Code)
	assert.Equal(t, http.StatusNotFound, e.get("/admin/users/abc", adminID).Code)
}

func TestUserUpdateChangesFlags(t *testing.T) {
	e := newEnv(t, false)
	path := fmt.Sprintf("/admin/users/%d", viewerID)

	rec := e.post(path, adminID, url.Values{"displayName": {"Alice Baru"}, "isActive": {"on"}, "isAdmin": {"on"}})

	require.Equal(t, http.StatusSeeOther, rec.Code, body(rec))
	assert.Equal(t, "Alice Baru", e.scalar(`SELECT display_name FROM users WHERE id = $1`, viewerID))
	assert.Equal(t, "true", e.scalar(`SELECT is_platform_admin::text FROM users WHERE id = $1`, viewerID))

	off := e.post(path, adminID, url.Values{"displayName": {"Alice Baru"}})
	require.Equal(t, http.StatusSeeOther, off.Code)
	assert.Equal(t, "false", e.scalar(`SELECT is_active::text FROM users WHERE id = $1`, viewerID))
	assert.Equal(t, "false", e.scalar(`SELECT is_platform_admin::text FROM users WHERE id = $1`, viewerID))
}

func TestUserUpdateProtectsOwnAccount(t *testing.T) {
	e := newEnv(t, false)
	path := fmt.Sprintf("/admin/users/%d", adminID)

	rec := e.post(path, adminID, url.Values{"displayName": {"x"}, "isActive": {"on"}})

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, body(rec), "tidak dapat menonaktifkan atau mencabut status admin akun sendiri")
	assert.Equal(t, "true", e.scalar(`SELECT is_platform_admin::text FROM users WHERE id = $1`, adminID))
}

func TestUserUpdateValidatesDisplayName(t *testing.T) {
	e := newEnv(t, false)

	rec := e.post(fmt.Sprintf("/admin/users/%d", viewerID), adminID, url.Values{"displayName": {strings.Repeat("a", 200)}, "isActive": {"on"}})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, body(rec), "maksimal 128 karakter")
}

func TestUserResetPassword(t *testing.T) {
	e := newEnv(t, false)
	path := fmt.Sprintf("/admin/users/%d/password", viewerID)
	before := e.scalar(`SELECT password_hash FROM users WHERE id = $1`, viewerID)

	mismatch := e.post(path, adminID, url.Values{"password": {"password-baru-1"}, "passwordConfirm": {"beda"}})
	require.Equal(t, http.StatusUnprocessableEntity, mismatch.Code)
	assert.Contains(t, body(mismatch), "konfirmasi password tidak sama")
	assert.NotContains(t, body(mismatch), "password-baru-1")

	short := e.post(path, adminID, url.Values{"password": {"pendek"}, "passwordConfirm": {"pendek"}})
	require.Equal(t, http.StatusUnprocessableEntity, short.Code)
	assert.Contains(t, body(short), "8-256 karakter")
	assert.Equal(t, before, e.scalar(`SELECT password_hash FROM users WHERE id = $1`, viewerID))

	ok := e.post(path, adminID, url.Values{"password": {"password-baru-1"}, "passwordConfirm": {"password-baru-1"}})
	require.Equal(t, http.StatusSeeOther, ok.Code, body(ok))
	assert.NotEqual(t, before, e.scalar(`SELECT password_hash FROM users WHERE id = $1`, viewerID))
	assert.Contains(t, body(e.follow(ok, adminID)), "session pengguna dicabut")
}

func TestUserRevokeSessions(t *testing.T) {
	e := newEnv(t, false)

	ok := e.post(fmt.Sprintf("/admin/users/%d/revoke-sessions", viewerID), adminID, url.Values{})
	require.Equal(t, http.StatusSeeOther, ok.Code)
	assert.Contains(t, body(e.follow(ok, adminID)), "flash-success")

	missing := e.post("/admin/users/999999/revoke-sessions", adminID, url.Values{})
	require.Equal(t, http.StatusSeeOther, missing.Code)
	assert.Contains(t, body(e.follow(missing, adminID)), "flash-error")
}

func TestUserDeleteNeedsConfirmationAndProtectsSelf(t *testing.T) {
	e := newEnv(t, false)
	victim := e.insertUser("korban", true)
	path := fmt.Sprintf("/admin/users/%d/delete", victim)

	confirm := e.get(path, adminID)
	require.Equal(t, http.StatusOK, confirm.Code)
	assert.Contains(t, body(confirm), "<strong>korban</strong>")
	assert.Equal(t, "1", e.scalar(`SELECT count(*)::text FROM users WHERE id = $1`, victim))

	rec := e.post(path, adminID, url.Values{})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/admin/users", rec.Header().Get("Location"))
	assert.Equal(t, "0", e.scalar(`SELECT count(*)::text FROM users WHERE id = $1`, victim))

	self := e.post(fmt.Sprintf("/admin/users/%d/delete", adminID), adminID, url.Values{})
	require.Equal(t, http.StatusSeeOther, self.Code)
	assert.Contains(t, body(e.follow(self, adminID)), "tidak dapat menghapus akun sendiri")
	assert.Equal(t, "1", e.scalar(`SELECT count(*)::text FROM users WHERE id = $1`, adminID))
}

func TestUserDisplayNameIsEscaped(t *testing.T) {
	e := newEnv(t, false)
	e.post("/admin/users/new", adminID, url.Values{"username": {"xssuser"}, "displayName": {`<script>alert(1)</script>`}, "password": {"password-kuat"}})

	html := body(e.get("/admin/users?limit=200", adminID))

	assert.NotContains(t, html, "<script>alert(1)</script>")
	assert.Contains(t, html, "&lt;script&gt;alert(1)&lt;/script&gt;")
}

func TestGroupCreateListAndValidation(t *testing.T) {
	e := newEnv(t, false)

	rec := e.post("/admin/groups/new", adminID, url.Values{"name": {" backend-team "}, "description": {"Tim backend"}})
	require.Equal(t, http.StatusSeeOther, rec.Code, body(rec))
	gid := e.scalar(`SELECT id::text FROM groups WHERE name = 'backend-team'`)
	assert.Equal(t, "/admin/groups/"+gid, rec.Header().Get("Location"))
	assert.Contains(t, body(e.follow(rec, adminID)), "dibuat")

	list := body(e.get("/admin/groups", adminID))
	assert.Contains(t, list, "<strong>backend-team</strong>")
	assert.Contains(t, list, "Tim backend")

	dup := e.post("/admin/groups/new", adminID, url.Values{"name": {"backend-team"}})
	require.Equal(t, http.StatusConflict, dup.Code)
	assert.Contains(t, body(dup), "sudah dipakai")

	empty := e.post("/admin/groups/new", adminID, url.Values{"name": {"  "}})
	require.Equal(t, http.StatusUnprocessableEntity, empty.Code)
	assert.Contains(t, body(empty), "1-64 karakter")
}

func TestGroupDetailShowsMembersGrantsAndCandidates(t *testing.T) {
	e := newEnv(t, false)
	gid := e.insertGroup("analyst")
	_, err := e.platform.Exec(t.Context(), `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, gid, viewerID)
	require.NoError(t, err)
	_, err = e.platform.Exec(t.Context(), `INSERT INTO group_database_grants (group_id, database_id, level) VALUES ($1, $2, 'view')`, gid, e.liveID)
	require.NoError(t, err)

	rec := e.get(fmt.Sprintf("/admin/groups/%d", gid), adminID)

	require.Equal(t, http.StatusOK, rec.Code, body(rec))
	html := body(rec)
	assert.Contains(t, html, "<strong>user42</strong>")
	assert.Contains(t, html, "<strong>live</strong>")
	assert.Contains(t, html, `<option value="view" selected>View Only</option>`)
	assert.Contains(t, html, fmt.Sprintf(`<option value="%d">user44</option>`, editorID))
	assert.NotContains(t, html, fmt.Sprintf(`<option value="%d">user42</option>`, viewerID))
	assert.Contains(t, html, fmt.Sprintf(`<option value="%d">staging</option>`, e.stagingID))
	assert.NotContains(t, html, fmt.Sprintf(`<option value="%d">live</option>`, e.liveID))
	assert.Equal(t, http.StatusNotFound, e.get("/admin/groups/999999", adminID).Code)
}

func TestGroupUpdateRenamesAndRejectsDuplicates(t *testing.T) {
	e := newEnv(t, false)
	gid := e.insertGroup("lama")
	e.insertGroup("terpakai")
	path := fmt.Sprintf("/admin/groups/%d", gid)

	ok := e.post(path, adminID, url.Values{"name": {"baru"}, "description": {"d"}})
	require.Equal(t, http.StatusSeeOther, ok.Code, body(ok))
	assert.Equal(t, "baru", e.scalar(`SELECT name FROM groups WHERE id = $1`, gid))

	dup := e.post(path, adminID, url.Values{"name": {"terpakai"}})
	require.Equal(t, http.StatusConflict, dup.Code)
	assert.Contains(t, body(dup), "sudah dipakai")
	assert.Contains(t, body(dup), "Akses database")
}

func TestGroupMembershipAddAndRemove(t *testing.T) {
	e := newEnv(t, false)
	gid := e.insertGroup("tim")
	base := fmt.Sprintf("/admin/groups/%d", gid)

	add := e.post(base+"/members", adminID, url.Values{"userId": {fmt.Sprint(viewerID)}})
	require.Equal(t, http.StatusSeeOther, add.Code)
	assert.Contains(t, body(e.follow(add, adminID)), "flash-success")
	assert.Equal(t, "1", e.scalar(`SELECT count(*)::text FROM group_members WHERE group_id = $1 AND user_id = $2`, gid, viewerID))

	none := e.post(base+"/members", adminID, url.Values{"userId": {""}})
	require.Equal(t, http.StatusSeeOther, none.Code)
	assert.Contains(t, body(e.follow(none, adminID)), "Pilih pengguna")

	unknown := e.post(base+"/members", adminID, url.Values{"userId": {"999999"}})
	require.Equal(t, http.StatusSeeOther, unknown.Code)
	assert.Contains(t, body(e.follow(unknown, adminID)), "flash-error")

	remove := e.post(fmt.Sprintf("%s/members/%d/remove", base, viewerID), adminID, url.Values{})
	require.Equal(t, http.StatusSeeOther, remove.Code)
	assert.Equal(t, "0", e.scalar(`SELECT count(*)::text FROM group_members WHERE group_id = $1`, gid))
}

func TestGroupGrantsPutChangeAndRemove(t *testing.T) {
	e := newEnv(t, false)
	gid := e.insertGroup("tim")
	base := fmt.Sprintf("/admin/groups/%d", gid)
	grant := func(level string) *struct{ code int } {
		rec := e.post(base+"/grants", adminID, url.Values{"databaseId": {fmt.Sprint(e.liveID)}, "level": {level}})
		return &struct{ code int }{rec.Code}
	}
	level := func() string {
		return e.scalar(`SELECT level::text FROM group_database_grants WHERE group_id = $1 AND database_id = $2`, gid, e.liveID)
	}

	assert.Equal(t, http.StatusSeeOther, grant("view").code)
	assert.Equal(t, "view", level())
	assert.Equal(t, http.StatusSeeOther, grant("edit").code)
	assert.Equal(t, "edit", level())

	invalid := e.post(base+"/grants", adminID, url.Values{"databaseId": {fmt.Sprint(e.liveID)}, "level": {"owner"}})
	require.Equal(t, http.StatusSeeOther, invalid.Code)
	assert.Contains(t, body(e.follow(invalid, adminID)), "flash-error")
	assert.Equal(t, "edit", level())

	missing := e.post(base+"/grants", adminID, url.Values{"databaseId": {""}, "level": {"view"}})
	assert.Contains(t, body(e.follow(missing, adminID)), "Pilih database")

	remove := e.post(fmt.Sprintf("%s/grants/%d/remove", base, e.liveID), adminID, url.Values{})
	require.Equal(t, http.StatusSeeOther, remove.Code)
	assert.Equal(t, "0", e.scalar(`SELECT count(*)::text FROM group_database_grants WHERE group_id = $1`, gid))
}

func TestGroupDeleteNeedsConfirmation(t *testing.T) {
	e := newEnv(t, false)
	gid := e.insertGroup("sementara")
	path := fmt.Sprintf("/admin/groups/%d/delete", gid)

	confirm := e.get(path, adminID)
	require.Equal(t, http.StatusOK, confirm.Code)
	assert.Contains(t, body(confirm), "<strong>sementara</strong>")
	assert.Equal(t, "1", e.scalar(`SELECT count(*)::text FROM groups WHERE id = $1`, gid))

	rec := e.post(path, adminID, url.Values{})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "0", e.scalar(`SELECT count(*)::text FROM groups WHERE id = $1`, gid))
	assert.Equal(t, http.StatusNotFound, e.post(path, adminID, url.Values{}).Code)
}
