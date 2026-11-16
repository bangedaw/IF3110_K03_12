package web_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
)

var publicRoutes = map[string]bool{"/login": true, "/static/*": true}

func (e *env) routes(method string) []string {
	e.t.Helper()
	var out []string
	err := chi.Walk(e.router, func(m, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if m == method && !publicRoutes[route] {
			out = append(out, route)
		}
		return nil
	})
	require.NoError(e.t, err)
	return out
}

func concrete(route string) string {
	for strings.Contains(route, "{") {
		start := strings.Index(route, "{")
		end := strings.Index(route, "}")
		route = route[:start] + "1" + route[end+1:]
	}
	return strings.ReplaceAll(route, "*", "x")
}

func TestEveryProtectedRouteRedirectsAnonymousUsers(t *testing.T) {
	e := newEnvWith(t, true, true)
	gets, posts := e.routes(http.MethodGet), e.routes(http.MethodPost)
	require.GreaterOrEqual(t, len(gets), 15)
	require.GreaterOrEqual(t, len(posts), 20)

	for _, route := range gets {
		rec := e.get(concrete(route), 0)
		assert.Equal(t, http.StatusSeeOther, rec.Code, "GET %s", route)
		assert.True(t, strings.HasPrefix(rec.Header().Get("Location"), "/login"), "GET %s -> %s", route, rec.Header().Get("Location"))
	}
	for _, route := range posts {
		rec := e.do(http.MethodPost, concrete(route), 0, url.Values{"_csrf": {"x"}}, nil)
		assert.Equal(t, http.StatusSeeOther, rec.Code, "POST %s", route)
		assert.True(t, strings.HasPrefix(rec.Header().Get("Location"), "/login"), "POST %s", route)
	}
}

func TestEveryPostRouteRejectsMissingOrWrongCSRFToken(t *testing.T) {
	e := newEnvWith(t, true, true)

	for _, route := range e.routes(http.MethodPost) {
		path := concrete(route)
		missing := e.postNoCSRF(path, adminID, url.Values{"name": {"x"}})
		assert.Equal(t, http.StatusForbidden, missing.Code, "POST %s tanpa token", route)
		assert.Contains(t, body(missing), "Token keamanan", "POST %s tanpa token", route)

		wrong := e.postNoCSRF(path, adminID, url.Values{"_csrf": {"bukan-token"}})
		assert.Equal(t, http.StatusForbidden, wrong.Code, "POST %s token salah", route)
		assert.Contains(t, body(wrong), "Token keamanan", "POST %s token salah", route)

		other := e.postNoCSRF(path, adminID, url.Values{"_csrf": {csrfFor(viewerID)}})
		assert.Equal(t, http.StatusForbidden, other.Code, "POST %s token milik user lain", route)
	}
}

func TestEveryPostRouteRejectsCrossSiteRequests(t *testing.T) {
	e := newEnvWith(t, true, true)
	routes := append(e.routes(http.MethodPost), "/login")

	for _, route := range routes {
		form := url.Values{"_csrf": {csrfFor(adminID)}, "username": {"alice"}, "password": {"rahasia123"}}
		req := httptest.NewRequest(http.MethodPost, concrete(route), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Test-User", fmt.Sprint(adminID))
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Origin", "https://evil.example")
		rec := serve(e, req)
		assert.Equal(t, http.StatusForbidden, rec.Code, "POST %s lintas situs", route)
	}
	assert.Equal(t, "0", e.scalar(`SELECT count(*)::text FROM users WHERE username NOT LIKE 'user%'`))
}

func TestEveryPostRouteAcceptsSameOriginRequests(t *testing.T) {
	e := newEnvWith(t, true, true)
	form := url.Values{"_csrf": {csrfFor(adminID)}}
	req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Test-User", fmt.Sprint(adminID))
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	rec := serve(e, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestNoGetRouteChangesState(t *testing.T) {
	e := newEnvWith(t, true, true)
	before := e.scalar(`SELECT (SELECT count(*) FROM users)::text || '/' || (SELECT count(*) FROM groups)::text || '/' ||
		(SELECT count(*) FROM database_connections)::text || '/' || (SELECT count(*) FROM group_database_grants)::text || '/' ||
		(SELECT count(*) FROM schema_snapshots)::text`)

	for _, user := range []int64{adminID, editorID} {
		for _, route := range e.routes(http.MethodGet) {
			e.get(concrete(route), user)
		}
	}

	after := e.scalar(`SELECT (SELECT count(*) FROM users)::text || '/' || (SELECT count(*) FROM groups)::text || '/' ||
		(SELECT count(*) FROM database_connections)::text || '/' || (SELECT count(*) FROM group_database_grants)::text || '/' ||
		(SELECT count(*) FROM schema_snapshots)::text`)
	assert.Equal(t, before, after)
}

func TestFailMapsEveryErrorKindToItsPage(t *testing.T) {
	e := newEnv(t, false)
	secret := "pq: password=apa-saja host=10.0.0.5"

	cases := []struct {
		name     string
		err      error
		status   int
		contains string
		absent   string
		location string
	}{
		{"belum login", apperr.ErrUnauthenticated, http.StatusSeeOther, "", "", "/login?next=%2Fdatabases"},
		{"sesi berakhir", apperr.New(apperr.CodeSessionExpired, "x"), http.StatusSeeOther, "", "", "/login?next=%2Fdatabases&reason=expired"},
		{"forbidden", apperr.ErrForbidden, http.StatusForbidden, "Akses ditolak", "", ""},
		{"csrf", apperr.New(apperr.CodeCSRFInvalid, "x"), http.StatusForbidden, "Token keamanan", "", ""},
		{"not found", apperr.ErrNotFound, http.StatusNotFound, "Tidak ditemukan", "", ""},
		{"conflict", apperr.New(apperr.CodeConflict, "nama sudah dipakai"), http.StatusConflict, "nama sudah dipakai", "", ""},
		{"validation", apperr.New(apperr.CodeValidation, "input salah"), http.StatusUnprocessableEntity, "input salah", "", ""},
		{"migration failed", apperr.New(apperr.CodeMigrationFailed, "statement 3 gagal"), http.StatusUnprocessableEntity, "statement 3 gagal", "", ""},
		{"db unreachable", apperr.Wrap(apperr.CodeDBUnreachable, "detail internal", errors.New(secret)), http.StatusServiceUnavailable, "Database tidak terjangkau", "apa-saja", ""},
		{"error dibungkus", fmt.Errorf("konteks: %w", apperr.ErrNotFound), http.StatusNotFound, "Tidak ditemukan", "", ""},
		{"error tak dikenal", errors.New(secret), http.StatusInternalServerError, "Terjadi kesalahan internal", "apa-saja", ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/databases", nil)
		rec := httptest.NewRecorder()
		e.srv.Fail(rec, req, c.err)

		assert.Equal(t, c.status, rec.Code, c.name)
		if c.location != "" {
			assert.Equal(t, c.location, rec.Header().Get("Location"), c.name)
		}
		if c.contains != "" {
			assert.Contains(t, rec.Body.String(), c.contains, c.name)
		}
		if c.absent != "" {
			assert.NotContains(t, rec.Body.String(), c.absent, c.name)
			assert.NotContains(t, rec.Body.String(), "10.0.0.5", c.name)
		}
	}
}
