package web_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loginForm(username, password, next string) url.Values {
	return url.Values{"username": {username}, "password": {password}, "next": {next}}
}

func sessionCookieOf(t *testing.T, rec interface{ Result() *http.Response }) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == testSessionCookie {
			return c
		}
	}
	return nil
}

func TestLoginPageIsPublicAndHasNoNavigation(t *testing.T) {
	e := newEnv(t, false)

	rec := e.get("/login", 0)

	require.Equal(t, http.StatusOK, rec.Code)
	html := body(rec)
	assert.Contains(t, html, `name="username"`)
	assert.Contains(t, html, `type="password"`)
	assert.Contains(t, html, `action="/login"`)
	assert.NotContains(t, html, `action="/logout"`)
	assert.NotContains(t, html, `aria-label="Utama"`)
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "default-src 'self'")
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

func TestLoginPageShowsExpiredNotice(t *testing.T) {
	e := newEnv(t, false)

	assert.Contains(t, body(e.get("/login?reason=expired", 0)), "Sesi Anda telah berakhir")
	assert.NotContains(t, body(e.get("/login", 0)), "Sesi Anda telah berakhir")
}

func TestLoginPageRedirectsWhenAlreadyAuthenticated(t *testing.T) {
	e := newEnv(t, false)

	home := e.get("/login", viewerID)
	assert.Equal(t, http.StatusSeeOther, home.Code)
	assert.Equal(t, "/databases", home.Header().Get("Location"))

	next := e.get("/login?next="+url.QueryEscape("/admin/connections"), adminID)
	assert.Equal(t, "/admin/connections", next.Header().Get("Location"))
}

func TestLoginSuccessSetsCookieAndRedirectsToNext(t *testing.T) {
	e := newEnv(t, false)

	home := e.do(http.MethodPost, "/login", 0, loginForm("alice", "rahasia123", ""), nil)
	require.Equal(t, http.StatusSeeOther, home.Code, body(home))
	assert.Equal(t, "/databases", home.Header().Get("Location"))
	cookie := sessionCookieOf(t, home)
	require.NotNil(t, cookie)
	assert.True(t, cookie.HttpOnly)

	target := "/databases/1/schemas/app/tables/books?tab=data&limit=5"
	next := e.do(http.MethodPost, "/login", 0, loginForm("alice", "rahasia123", target), nil)
	assert.Equal(t, target, next.Header().Get("Location"))

	after := e.do(http.MethodGet, "/databases", 0, nil, home.Result().Cookies())
	assert.Equal(t, http.StatusOK, after.Code)
}

func TestLoginRejectsOpenRedirectTargets(t *testing.T) {
	e := newEnv(t, false)

	for _, next := range []string{
		"//evil.example/path",
		"https://evil.example",
		"/\\evil.example",
		"javascript:alert(1)",
		"evil",
		"/login",
		"/login?next=/x",
		"/ok\r\nSet-Cookie: x=1",
	} {
		rec := e.do(http.MethodPost, "/login", 0, loginForm("alice", "rahasia123", next), nil)
		require.Equal(t, http.StatusSeeOther, rec.Code, next)
		assert.Equal(t, "/databases", rec.Header().Get("Location"), next)
	}
}

func TestLoginFailureIsGenericAndDoesNotEchoPassword(t *testing.T) {
	e := newEnv(t, false)

	wrong := e.do(http.MethodPost, "/login", 0, loginForm("alice", "salah-total", "/databases"), nil)
	unknown := e.do(http.MethodPost, "/login", 0, loginForm("hantu", "rahasia123", ""), nil)

	for _, rec := range []interface {
		Result() *http.Response
	}{wrong, unknown} {
		assert.Nil(t, sessionCookieOf(t, rec))
	}
	require.Equal(t, http.StatusUnprocessableEntity, wrong.Code)
	require.Equal(t, http.StatusUnprocessableEntity, unknown.Code)
	assert.Contains(t, body(wrong), "Username atau password salah.")
	assert.Contains(t, body(unknown), "Username atau password salah.")
	assert.Contains(t, body(wrong), `value="alice"`)
	assert.NotContains(t, body(wrong), "salah-total")
	assert.Contains(t, body(wrong), `name="next" value="/databases"`)
	assert.Contains(t, body(wrong), `type="password" autocomplete="current-password" value=""`)
}

func TestLoginValidatesRequiredFields(t *testing.T) {
	e := newEnv(t, false)

	rec := e.do(http.MethodPost, "/login", 0, loginForm("", "", ""), nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, body(rec), "wajib diisi")
	assert.Nil(t, sessionCookieOf(t, rec))
}

func TestLoginEscapesReflectedValues(t *testing.T) {
	e := newEnv(t, false)

	rec := e.do(http.MethodPost, "/login", 0, loginForm(`"><script>alert(1)</script>`, "x", `"><img src=x>`), nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, body(rec), "<script>alert(1)</script>")
	assert.NotContains(t, body(rec), "<img src=x>")
	assert.Contains(t, body(rec), "&lt;script&gt;")
}

func TestLoginDoesNotRequireCSRFToken(t *testing.T) {
	e := newEnv(t, false)

	rec := e.do(http.MethodPost, "/login", 0, loginForm("alice", "rahasia123", ""), nil)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
}
