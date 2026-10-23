package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/auth"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/config"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/testutil"
)

type client struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
	csrf   string
}

func (c *client) do(method, path, body string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if c.csrf != "" {
		req.Header.Set(auth.CSRFHeader, c.csrf)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == auth.CookieName {
			c.cookie = ck
		}
	}
	return rec
}

func (c *client) login(username string) {
	c.t.Helper()
	rec := c.do(http.MethodPost, "/api/auth/login", `{"username":"`+username+`","password":"rahasia123"}`)
	require.Equal(c.t, http.StatusOK, rec.Code, rec.Body.String())
	var s apigen.AuthSession
	require.NoError(c.t, json.Unmarshal(rec.Body.Bytes(), &s))
	c.csrf = s.CsrfToken
}

func TestServerWiring(t *testing.T) {
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

	anon := &client{t: t, h: h}
	assert.Equal(t, http.StatusOK, anon.do(http.MethodGet, "/api/healthz", "").Code)
	for _, path := range []string{"/api/auth/me", "/api/databases", "/api/admin/databases"} {
		assert.Equal(t, http.StatusUnauthorized, anon.do(http.MethodGet, path, "").Code, path)
	}

	alice := &client{t: t, h: h}
	alice.login("alice")
	assert.Equal(t, http.StatusOK, alice.do(http.MethodGet, "/api/databases", "").Code)
	assert.Equal(t, http.StatusForbidden, alice.do(http.MethodGet, "/api/admin/databases", "").Code)

	admin := &client{t: t, h: h}
	admin.login("admin")
	assert.Equal(t, http.StatusOK, admin.do(http.MethodGet, "/api/admin/databases", "").Code)

	body := `{"name":"x","kind":"postgres","host":"localhost","port":1,"database":"x","username":"x","password":"x"}`
	csrf := admin.csrf
	admin.csrf = ""
	rec := admin.do(http.MethodPost, "/api/admin/databases", body)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "CSRF_INVALID")
	admin.csrf = csrf
	assert.NotEqual(t, http.StatusForbidden, admin.do(http.MethodPost, "/api/admin/databases", body).Code)
}
