package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

func (c *client) form(method, path string, values url.Values) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

func TestServerRendersPagesWithRealSession(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)
	hash, err := auth.HashPassword("rahasia123")
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(),
		`INSERT INTO users (username, password_hash, is_platform_admin) VALUES ('admin', $1, TRUE), ('alice', $1, FALSE)`, hash)
	require.NoError(t, err)

	cfg := config.Config{
		SecretKey: "REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8=",
		CSRFKey:   "test-csrf-key",
		Session: config.Session{
			IdleTimeout: 30 * time.Minute, AbsoluteTimeout: 8 * time.Hour,
			RotationInterval: 15 * time.Minute, GracePeriod: 30 * time.Second, TouchInterval: time.Minute,
		},
	}
	h, cleanup, err := newHandler(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	t.Cleanup(cleanup)

	alice := &client{t: t, h: h}
	alice.login("alice")
	rec := alice.do(http.MethodGet, "/databases", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "alice")
	assert.Contains(t, rec.Body.String(), "belum memiliki akses")
	assert.NotContains(t, rec.Body.String(), `href="/admin/connections"`)
	assert.Equal(t, http.StatusForbidden, alice.do(http.MethodGet, "/admin/connections", "").Code)

	admin := &client{t: t, h: h}
	admin.login("admin")
	rec = admin.do(http.MethodGet, "/admin/connections", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Koneksi database")
	assert.Contains(t, rec.Body.String(), `name="_csrf" value="`+admin.csrf+`"`)

	create := url.Values{
		"name": {"inventory"}, "kind": {"postgres"}, "host": {"localhost"}, "port": {"5432"},
		"databaseName": {"inv"}, "username": {"app"}, "password": {"rahasia-banget"}, "sslMode": {"disable"}, "isActive": {"on"},
	}
	rec = admin.form(http.MethodPost, "/admin/connections/new", create)
	assert.Equal(t, http.StatusForbidden, rec.Code, "tanpa token CSRF")

	create.Set("_csrf", "salah")
	assert.Equal(t, http.StatusForbidden, admin.form(http.MethodPost, "/admin/connections/new", create).Code)

	create.Set("_csrf", admin.csrf)
	rec = admin.form(http.MethodPost, "/admin/connections/new", create)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, "/admin/connections", rec.Header().Get("Location"))
	list := admin.do(http.MethodGet, "/admin/connections", "")
	assert.Contains(t, list.Body.String(), "<strong>inventory</strong>")
	assert.NotContains(t, list.Body.String(), "rahasia-banget")

	alice.csrf = admin.csrf
	rec = alice.form(http.MethodPost, "/logout", url.Values{"_csrf": {admin.csrf}})
	assert.Equal(t, http.StatusForbidden, rec.Code, "token CSRF milik user lain tidak berlaku")

	rec = admin.form(http.MethodPost, "/logout", url.Values{"_csrf": {admin.csrf}})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
	after := admin.do(http.MethodGet, "/databases", "")
	assert.Equal(t, http.StatusSeeOther, after.Code)
	assert.Contains(t, after.Header().Get("Location"), "/login")
}

var csrfInput = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

type browser struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
}

func (b *browser) send(method, path string, values url.Values) *httptest.ResponseRecorder {
	b.t.Helper()
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if values != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName {
			if c.MaxAge < 0 {
				b.cookie = nil
			} else {
				b.cookie = c
			}
		}
	}
	return rec
}

func (b *browser) csrf(path string) string {
	b.t.Helper()
	rec := b.send(http.MethodGet, path, nil)
	require.Equal(b.t, http.StatusOK, rec.Code, rec.Body.String())
	m := csrfInput.FindStringSubmatch(rec.Body.String())
	require.Len(b.t, m, 2, "form di %s harus memuat token CSRF", path)
	return m[1]
}

func (b *browser) post(formPage, action string, values url.Values) *httptest.ResponseRecorder {
	b.t.Helper()
	values.Set("_csrf", b.csrf(formPage))
	return b.send(http.MethodPost, action, values)
}

func (b *browser) signIn(username, password string) *httptest.ResponseRecorder {
	b.t.Helper()
	return b.send(http.MethodPost, "/login", url.Values{"username": {username}, "password": {password}})
}

func TestServerAdminFlowThroughPagesWithRealSession(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)
	hash, err := auth.HashPassword("rahasia123")
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(),
		`INSERT INTO users (username, password_hash, is_platform_admin) VALUES ('admin', $1, TRUE)`, hash)
	require.NoError(t, err)
	cfg := config.Config{
		SecretKey: "REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8=",
		CSRFKey:   "test-csrf-key",
		Session: config.Session{
			IdleTimeout: 30 * time.Minute, AbsoluteTimeout: 8 * time.Hour,
			RotationInterval: 15 * time.Minute, GracePeriod: 30 * time.Second, TouchInterval: time.Minute,
		},
	}
	h, cleanup, err := newHandler(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	t.Cleanup(cleanup)

	admin := &browser{t: t, h: h}
	assert.Equal(t, http.StatusSeeOther, admin.send(http.MethodGet, "/admin/users", nil).Code)

	bad := admin.signIn("admin", "salah")
	require.Equal(t, http.StatusUnprocessableEntity, bad.Code)
	assert.Contains(t, bad.Body.String(), "Username atau password salah.")
	assert.Nil(t, admin.cookie)

	ok := admin.signIn("admin", "rahasia123")
	require.Equal(t, http.StatusSeeOther, ok.Code, ok.Body.String())
	assert.Equal(t, "/databases", ok.Header().Get("Location"))
	require.NotNil(t, admin.cookie)
	assert.True(t, admin.cookie.HttpOnly)

	rec := admin.post("/admin/users/new", "/admin/users/new", url.Values{"username": {"carol"}, "displayName": {"Carol"}, "password": {"password-carol-1"}})
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	rec = admin.post("/admin/connections/new", "/admin/connections/new", url.Values{
		"name": {"inventory"}, "kind": {"postgres"}, "host": {"localhost"}, "port": {"5432"},
		"databaseName": {"inv"}, "username": {"app"}, "password": {"rahasia-db"}, "sslMode": {"disable"}, "isActive": {"on"},
	})
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	rec = admin.post("/admin/groups/new", "/admin/groups/new", url.Values{"name": {"analyst"}})
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	groupPage := rec.Header().Get("Location")

	var userID, dbID string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE username = 'carol'`).Scan(&userID))
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT id::text FROM database_connections WHERE name = 'inventory'`).Scan(&dbID))

	rec = admin.post(groupPage, groupPage+"/members", url.Values{"userId": {userID}})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	rec = admin.post(groupPage, groupPage+"/grants", url.Values{"databaseId": {dbID}, "level": {"view"}})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	detail := admin.send(http.MethodGet, groupPage, nil)
	assert.Contains(t, detail.Body.String(), "<strong>carol</strong>")
	assert.Contains(t, detail.Body.String(), "<strong>inventory</strong>")

	carol := &browser{t: t, h: h}
	require.Equal(t, http.StatusSeeOther, carol.signIn("carol", "password-carol-1").Code)
	list := carol.send(http.MethodGet, "/databases", nil)
	require.Equal(t, http.StatusOK, list.Code)
	assert.Contains(t, list.Body.String(), "<strong>inventory</strong>")
	assert.Contains(t, list.Body.String(), "View Only")
	assert.NotContains(t, list.Body.String(), `href="/admin/users"`)
	assert.Equal(t, http.StatusForbidden, carol.send(http.MethodGet, "/admin/users", nil).Code)
	assert.Equal(t, http.StatusForbidden, carol.send(http.MethodGet, "/admin/connections", nil).Code)

	rec = carol.post("/databases", "/logout", url.Values{})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
	assert.Nil(t, carol.cookie)
	assert.Equal(t, http.StatusSeeOther, carol.send(http.MethodGet, "/databases", nil).Code)
}

func TestServerMigrationFlowWithRealSession(t *testing.T) {
	pool := testutil.NewPlatformDB(t)
	t.Cleanup(pool.Close)
	target := testutil.NewPostgresTarget(t)
	t.Cleanup(target.Close)
	hash, err := auth.HashPassword("rahasia123")
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(),
		`INSERT INTO users (username, password_hash, is_platform_admin) VALUES ('admin', $1, TRUE), ('dave', $1, FALSE), ('bob', $1, FALSE)`, hash)
	require.NoError(t, err)
	cfg := config.Config{
		SecretKey: "REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8=",
		CSRFKey:   "test-csrf-key",
		Session: config.Session{
			IdleTimeout: 30 * time.Minute, AbsoluteTimeout: 8 * time.Hour,
			RotationInterval: 15 * time.Minute, GracePeriod: 30 * time.Second, TouchInterval: time.Minute,
		},
	}
	h, cleanup, err := newHandler(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	t.Cleanup(cleanup)

	tc := target.Config().ConnConfig
	admin := &browser{t: t, h: h}
	require.Equal(t, http.StatusSeeOther, admin.signIn("admin", "rahasia123").Code)
	rec := admin.post("/admin/connections/new", "/admin/connections/new", url.Values{
		"name": {"inventory"}, "kind": {"postgres"}, "host": {tc.Host}, "port": {strconv.Itoa(int(tc.Port))},
		"databaseName": {tc.Database}, "username": {tc.User}, "password": {tc.Password}, "sslMode": {"disable"}, "isActive": {"on"},
	})
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	var dbID, daveID, bobID string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT id::text FROM database_connections WHERE name = 'inventory'`).Scan(&dbID))
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE username = 'dave'`).Scan(&daveID))
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE username = 'bob'`).Scan(&bobID))
	for _, g := range []struct{ name, user, level string }{{"backend-team", daveID, "edit"}, {"analyst", bobID, "view"}} {
		rec = admin.post("/admin/groups/new", "/admin/groups/new", url.Values{"name": {g.name}})
		require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
		groupPage := rec.Header().Get("Location")
		require.Equal(t, http.StatusSeeOther, admin.post(groupPage, groupPage+"/members", url.Values{"userId": {g.user}}).Code)
		require.Equal(t, http.StatusSeeOther, admin.post(groupPage, groupPage+"/grants", url.Values{"databaseId": {dbID}, "level": {g.level}}).Code)
	}

	base := "/databases/" + dbID
	dave := &browser{t: t, h: h}
	require.Equal(t, http.StatusSeeOther, dave.signIn("dave", "rahasia123").Code)
	rec = dave.post(base+"/migrations/new", base+"/migrations/new", url.Values{
		"title": {"Tabel kategori"}, "sql": {"CREATE TABLE app.categories (id int PRIMARY KEY); INSERT INTO app.categories VALUES (1);"},
	})
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	migrationPage := rec.Header().Get("Location")

	bob := &browser{t: t, h: h}
	require.Equal(t, http.StatusSeeOther, bob.signIn("bob", "rahasia123").Code)
	assert.NotContains(t, bob.send(http.MethodGet, migrationPage, nil).Body.String(), migrationPage+"/execute")
	assert.Equal(t, http.StatusForbidden, bob.post(migrationPage, migrationPage+"/execute", url.Values{"revision": {"1"}}).Code)

	rec = dave.post(migrationPage+"/execute", migrationPage+"/execute", url.Values{"revision": {"1"}})
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Contains(t, dave.send(http.MethodGet, migrationPage, nil).Body.String(), "applied")

	var rows int
	require.NoError(t, target.QueryRow(context.Background(), `SELECT count(*) FROM app.categories`).Scan(&rows))
	assert.Equal(t, 1, rows)

	history := bob.send(http.MethodGet, base+"/history", nil)
	require.Equal(t, http.StatusOK, history.Code)
	assert.Contains(t, history.Body.String(), "Tabel kategori")
	assert.Contains(t, history.Body.String(), "dave")
	assert.Contains(t, history.Body.String(), "berhasil")
}
