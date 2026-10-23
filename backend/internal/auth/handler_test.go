package auth

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apigen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/httpx"
)

func newTestRouter(svc *Service) http.Handler {
	r := httpx.NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Route("/api", func(api chi.Router) { Routes(api, svc) })
	return r
}

func request(t *testing.T, h http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			return c
		}
	}
	return nil
}

func TestLoginHandlerSetsSecureCookie(t *testing.T) {
	svc, pool, _ := newTestService(t)
	createUser(t, pool, "alice", "rahasia123", true)
	h := newTestRouter(svc)

	rec := request(t, h, http.MethodPost, "/api/auth/login", `{"username":"alice","password":"rahasia123"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	c := sessionCookie(t, rec)
	require.NotNil(t, c)
	assert.True(t, c.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
	assert.Equal(t, "/", c.Path)
	assert.False(t, c.Secure)
	assert.InDelta(t, testSessionCfg.AbsoluteTimeout.Seconds(), float64(c.MaxAge), 1)

	body := rec.Body.String()
	assert.NotContains(t, body, c.Value)
	assert.NotContains(t, body, "rahasia123")
	assert.NotContains(t, body, "argon2id")
	var got apigen.AuthSession
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "alice", got.User.Username)
}

func TestLoginHandlerSecureFlagInProduction(t *testing.T) {
	svc, pool, _ := newTestService(t)
	svc.secure = true
	createUser(t, pool, "alice", "rahasia123", true)

	rec := request(t, newTestRouter(svc), http.MethodPost, "/api/auth/login", `{"username":"alice","password":"rahasia123"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, sessionCookie(t, rec).Secure)
}

func TestLoginHandlerFailures(t *testing.T) {
	svc, pool, _ := newTestService(t)
	createUser(t, pool, "alice", "rahasia123", true)
	h := newTestRouter(svc)

	cases := []struct {
		body   string
		status int
	}{
		{`{"username":"alice","password":"salah"}`, http.StatusUnauthorized},
		{`{"username":"tidakada","password":"x"}`, http.StatusUnauthorized},
		{`{"username":"","password":""}`, http.StatusUnprocessableEntity},
		{`bukan json`, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		rec := request(t, h, http.MethodPost, "/api/auth/login", tc.body, nil)
		assert.Equal(t, tc.status, rec.Code, tc.body)
		assert.Nil(t, sessionCookie(t, rec), tc.body)
	}
}

func TestMeAndLogout(t *testing.T) {
	svc, pool, _ := newTestService(t)
	createUser(t, pool, "alice", "rahasia123", true)
	h := newTestRouter(svc)

	assert.Equal(t, http.StatusUnauthorized, request(t, h, http.MethodGet, "/api/auth/me", "", nil).Code)

	c := sessionCookie(t, request(t, h, http.MethodPost, "/api/auth/login", `{"username":"alice","password":"rahasia123"}`, nil))
	require.NotNil(t, c)

	rec := request(t, h, http.MethodGet, "/api/auth/me", "", c)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"username":"alice"`)

	rec = request(t, h, http.MethodPost, "/api/auth/logout", "", c)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, -1, sessionCookie(t, rec).MaxAge)

	rec = request(t, h, http.MethodGet, "/api/auth/me", "", c)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, -1, sessionCookie(t, rec).MaxAge)
}

func TestMiddlewareRotatesCookie(t *testing.T) {
	svc, pool, clk := newTestService(t)
	createUser(t, pool, "alice", "rahasia123", true)
	h := newTestRouter(svc)
	old := sessionCookie(t, request(t, h, http.MethodPost, "/api/auth/login", `{"username":"alice","password":"rahasia123"}`, nil))

	clk.advance(testSessionCfg.RotationInterval)
	rec := request(t, h, http.MethodGet, "/api/auth/me", "", old)
	require.Equal(t, http.StatusOK, rec.Code)
	fresh := sessionCookie(t, rec)
	require.NotNil(t, fresh)
	assert.Equal(t, CookieName, fresh.Name)
	assert.NotEqual(t, old.Value, fresh.Value)

	assert.Equal(t, http.StatusOK, request(t, h, http.MethodGet, "/api/auth/me", "", old).Code)
	clk.advance(testSessionCfg.GracePeriod + time.Second)
	assert.Equal(t, http.StatusUnauthorized, request(t, h, http.MethodGet, "/api/auth/me", "", old).Code)
	assert.Equal(t, http.StatusOK, request(t, h, http.MethodGet, "/api/auth/me", "", fresh).Code)
}
