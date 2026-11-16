package web_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnauthenticatedRequestRedirectsToLogin(t *testing.T) {
	e := newEnv(t, false)

	for _, path := range []string{"/databases", "/admin/connections", dbPath(e.liveID, "")} {
		rec := e.get(path, 0)
		assert.Equal(t, http.StatusSeeOther, rec.Code, path)
		assert.Equal(t, "/login?next="+url.QueryEscape(path), rec.Header().Get("Location"), path)
	}
}

func TestUnauthenticatedPostRedirectsWithoutNext(t *testing.T) {
	e := newEnv(t, false)

	rec := e.do(http.MethodPost, "/logout", 0, url.Values{"_csrf": {"x"}}, nil)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
}

func TestExpiredSessionRedirectsWithReason(t *testing.T) {
	e := newEnv(t, false)
	req, _ := http.NewRequest(http.MethodGet, "/databases", nil)
	req.Header.Set("X-Test-Session", "expired")

	rec := serve(e, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "reason=expired")
}

func TestRootRedirectsToDatabases(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get("/", viewerID)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/databases", rec.Header().Get("Location"))
}

func TestPostRequiresValidCSRFToken(t *testing.T) {
	e := newEnv(t, false)

	missing := e.postNoCSRF("/logout", viewerID, url.Values{})
	assert.Equal(t, http.StatusForbidden, missing.Code)
	assert.Contains(t, body(missing), "Token keamanan")

	wrong := e.postNoCSRF("/logout", viewerID, url.Values{"_csrf": {"salah"}})
	assert.Equal(t, http.StatusForbidden, wrong.Code)

	otherUsers := e.postNoCSRF("/logout", viewerID, url.Values{"_csrf": {csrfFor(editorID)}})
	assert.Equal(t, http.StatusForbidden, otherUsers.Code)

	assert.Empty(t, e.guard.loggedO)
}

func TestLogoutRevokesAndRedirects(t *testing.T) {
	e := newEnv(t, false)

	rec := e.post("/logout", viewerID, url.Values{})

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
	assert.Equal(t, []int64{viewerID}, e.guard.loggedO)
}

func TestAuthenticatedPagesSetSecurityHeaders(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get("/databases", viewerID)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "default-src 'self'")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
}

func TestStaticAssetsServedWithoutLogin(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get("/static/app.css", 0)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/css")
	assert.Contains(t, body(rec), "--primary")
	assert.Equal(t, http.StatusNotFound, e.get("/static/tidak-ada.css", 0).Code)
}

func TestNavigationDependsOnRole(t *testing.T) {
	e := newEnv(t, false)

	admin := body(e.get("/admin/connections", adminID))
	assert.Contains(t, admin, `href="/admin/connections"`)
	assert.Contains(t, admin, "admin</span>")

	viewer := body(e.get("/databases", viewerID))
	assert.Contains(t, viewer, `href="/databases"`)
	assert.NotContains(t, viewer, `href="/admin/connections"`)
	assert.Contains(t, viewer, `action="/logout"`)
	assert.Contains(t, viewer, `name="_csrf" value="`+csrfFor(viewerID)+`"`)
}

func TestFlashMessageShownOnceAndEscaped(t *testing.T) {
	e := newEnv(t, false)
	rec := e.post("/admin/connections/new", adminID, validConnectionForm("<b>x</b>"))
	require.Equal(t, http.StatusSeeOther, rec.Code, body(rec))

	first := e.follow(rec, adminID)
	assert.Contains(t, body(first), `class="flash flash-success"`)
	assert.NotContains(t, body(first), "<b>x</b>")
	assert.Contains(t, body(first), "&lt;b&gt;x&lt;/b&gt;")

	second := e.get("/admin/connections", adminID)
	assert.NotContains(t, body(second), "flash-success")
}
