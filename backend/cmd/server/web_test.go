package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
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
	spa := fstest.MapFS{"index.html": {Data: []byte("<div id=root></div>")}}
	h, cleanup, err := newHandler(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)), spa)
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
